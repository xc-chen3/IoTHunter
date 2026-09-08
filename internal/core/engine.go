package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	peripheralspkg "github.com/iothunter/iothunter/internal/peripherals"
	toolspkg "github.com/iothunter/iothunter/internal/tools"
	workerpkg "github.com/iothunter/iothunter/internal/worker"
)

type CapabilityExecutor func(context.Context, CapabilityRequest) (CapabilityResult, error)

type Engine struct {
	Store          *Store
	mu             sync.RWMutex
	runMu          sync.Mutex
	capabilities   map[string]Capability
	executors      map[string]CapabilityExecutor
	tools          map[string]Tool
	workers        chan struct{}
	worker         workerpkg.Runner
	Peripherals    *peripheralspkg.Manager
	ToolGateway    *toolspkg.Gateway
	taskRuns       map[string]context.CancelFunc
	runGenerations map[string]uint64
	agentMu        sync.Mutex
	agentSlots     map[string]chan struct{}
	agentActive    map[string]int
	toolUsageMu    sync.Mutex
	taskToolUsage  map[string]int
}

func NewEngine(store *Store, maxWorkers int) *Engine {
	if maxWorkers < 1 {
		maxWorkers = 4
	}
	e := &Engine{Store: store, capabilities: map[string]Capability{}, executors: map[string]CapabilityExecutor{}, tools: map[string]Tool{}, workers: make(chan struct{}, maxWorkers), worker: workerpkg.NewRunner(workerpkg.KnowledgeSpec()), Peripherals: peripheralspkg.NewManager(), ToolGateway: toolspkg.NewGateway(), taskRuns: map[string]context.CancelFunc{}, runGenerations: map[string]uint64{}, agentSlots: map[string]chan struct{}{}, agentActive: map[string]int{}, taskToolUsage: map[string]int{}}
	e.registerHostTools()
	e.RegisterBuiltinCapabilities()
	e.ensureDefaults()
	e.loadConfiguredRegistry()
	e.Peripherals.SetObserver(func(session peripheralspkg.Session, event string) {
		if session.PeripheralID == "" {
			return
		}
		if event == "expired" || event == "disconnected" {
			_ = e.Store.UpdatePeripheral(session.PeripheralID, func(value *Peripheral) error {
				value.Status, value.OccupiedBy, value.ConnectedAt = "offline", "", nil
				return nil
			})
		}
		workspaceID := session.WorkspaceID
		if workspaceID == "" {
			if peripheral, ok := findPeripheral(e.Store.Snapshot().Peripherals, session.PeripheralID); ok {
				workspaceID = peripheral.WorkspaceID
			}
		}
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.session." + event, WorkspaceID: workspaceID, Payload: map[string]any{"peripheral_id": session.PeripheralID, "session_id": session.ID, "owner": session.OwnerID}, CreatedAt: now()})
		_ = e.audit("peripheral.session."+event, session.OwnerID, "peripheral", session.PeripheralID, map[string]any{"session_id": session.ID})
	})
	e.Peripherals.SetTelemetryObserver(e.persistPeripheralTelemetry)
	e.recoverPeripherals()
	e.recoverTasks()
	return e
}

// persistPeripheralTelemetry projects live driver samples into the durable
// workspace state. The manager invokes this asynchronously, so artifact and
// SQLite writes never hold a device command lock.
func (e *Engine) persistPeripheralTelemetry(session peripheralspkg.Session, event peripheralspkg.Telemetry) {
	if e.Store == nil || session.PeripheralID == "" {
		return
	}
	workspaceID := session.WorkspaceID
	peripheral, ok := findPeripheral(e.Store.Snapshot().Peripherals, session.PeripheralID)
	if !ok {
		return
	}
	if workspaceID == "" {
		workspaceID = peripheral.WorkspaceID
	}
	values := cloneMap(event.Data)
	if values == nil {
		values = map[string]any{}
	}
	if event.Command != "" {
		values["command"] = event.Command
	}
	if event.Status != "" {
		values["status"] = event.Status
	}
	if event.Error != "" {
		values["error"] = event.Error
	}
	record := TelemetryRecord{
		ID:           NewID("TEL"),
		WorkspaceID:  workspaceID,
		PeripheralID: session.PeripheralID,
		SessionID:    session.ID,
		Topic:        peripheralspkg.TelemetryTopic(event),
		Values:       values,
		At:           event.At,
	}
	if record.At.IsZero() {
		record.At = now()
	}
	if len(event.Bytes) > 0 {
		const inlineLimit = 16 * 1024
		if len(event.Bytes) <= inlineLimit {
			record.BytesBase64 = base64.StdEncoding.EncodeToString(event.Bytes)
		} else if workspaceID != "" {
			artifact, err := e.StoreArtifactBytes(workspaceID, fmt.Sprintf("telemetry-%s.bin", record.ID), "peripheral-telemetry", event.Bytes, map[string]any{
				"session_id":    session.ID,
				"peripheral_id": session.PeripheralID,
				"topic":         record.Topic,
			})
			if err == nil {
				record.ArtifactID = artifact.ID
			} else {
				values["artifact_error"] = err.Error()
			}
		} else {
			values["bytes_dropped"] = len(event.Bytes)
		}
	}
	if err := e.Store.AddTelemetry(record); err != nil {
		return
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.telemetry", WorkspaceID: workspaceID, Payload: map[string]any{
		"telemetry_id": record.ID, "peripheral_id": record.PeripheralID, "session_id": record.SessionID,
		"topic": record.Topic, "artifact_id": record.ArtifactID, "status": event.Status,
	}, CreatedAt: record.At})
}

func (e *Engine) recoverPeripherals() {
	for _, peripheral := range e.Store.Snapshot().Peripherals {
		if peripheral.Status != "connected" {
			continue
		}
		_ = e.Store.UpdatePeripheral(peripheral.ID, func(value *Peripheral) error {
			value.Status, value.OccupiedBy, value.ConnectedAt = "offline", "", nil
			return nil
		})
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.recovered_offline", WorkspaceID: peripheral.WorkspaceID, Payload: map[string]any{"peripheral_id": peripheral.ID}, CreatedAt: now()})
		_ = e.audit("peripheral.recovered_offline", "startup", "peripheral", peripheral.ID, nil)
	}
}

func (e *Engine) loadConfiguredRegistry() {
	state := e.Store.Snapshot()
	for _, capability := range state.RegisteredCapabilities {
		if strings.TrimSpace(capability.ID) == "" {
			continue
		}
		e.mu.RLock()
		_, exists := e.capabilities[capability.ID]
		e.mu.RUnlock()
		if exists {
			continue
		}
		executor := CapabilityExecutor(e.catalogCapability)
		if capability.Category == "peripheral" {
			executor = e.peripheralCapability
		}
		e.RegisterCapability(capability, executor)
	}
	for _, configured := range state.RegisteredTools {
		path := configured.Runtime
		if path == "" && len(configured.Command) > 0 {
			path = configured.Command[0]
		}
		if path == "" || e.ToolGateway == nil {
			continue
		}
		_ = e.ToolGateway.Register(toolspkg.Definition{Name: configured.Name, Path: path, Isolation: configured.Isolation, Image: configured.Image, Permissions: toolspkg.PermissionSet{Network: configured.Permissions.Network, Filesystem: configured.Permissions.Filesystem, Destructive: configured.Permissions.Destructive}, Timeout: time.Duration(configured.TimeoutSeconds) * time.Second})
	}
}

// RegisterConfiguredCapability adds a user-defined capability to the
// persistent registry. Implementations are still selected by the control
// plane; a definition cannot inject a shell command into an Agent request.
func (e *Engine) RegisterConfiguredCapability(capability Capability) error {
	capability.ID = strings.TrimSpace(capability.ID)
	if capability.ID == "" || capability.Version == "" {
		return errors.New("capability id and version are required")
	}
	if capability.TimeoutSeconds <= 0 {
		capability.TimeoutSeconds = 60
	}
	if capability.Permissions.Filesystem == "" {
		capability.Permissions.Filesystem = "workspace-readonly"
	}
	executor := CapabilityExecutor(e.catalogCapability)
	if capability.Category == "peripheral" {
		executor = e.peripheralCapability
	}
	e.RegisterCapability(capability, executor)
	return e.Store.mutate(func(state *State) error {
		for i := range state.RegisteredCapabilities {
			if state.RegisteredCapabilities[i].ID == capability.ID {
				state.RegisteredCapabilities[i] = capability
				return nil
			}
		}
		state.RegisteredCapabilities = append(state.RegisteredCapabilities, capability)
		return nil
	})
}

func (e *Engine) RegisterConfiguredTool(definition toolspkg.Definition) error {
	if e.ToolGateway == nil {
		return errors.New("tool gateway is unavailable")
	}
	if err := e.ToolGateway.Register(definition); err != nil {
		return err
	}
	tool := Tool{Name: definition.Name, Category: "external", Execution: "process", Isolation: definition.Isolation, Image: definition.Image, Permissions: PermissionSet{Network: definition.Permissions.Network, Filesystem: definition.Permissions.Filesystem, Destructive: definition.Permissions.Destructive}, Runtime: definition.Path, TimeoutSeconds: int(definition.Timeout / time.Second)}
	return e.Store.mutate(func(state *State) error {
		for i := range state.RegisteredTools {
			if state.RegisteredTools[i].Name == tool.Name {
				state.RegisteredTools[i] = tool
				return nil
			}
		}
		state.RegisteredTools = append(state.RegisteredTools, tool)
		return nil
	})
}

func (e *Engine) ensureDefaults() {
	_ = e.Store.mutate(func(state *State) error {
		for i := range state.Workspaces {
			if strings.TrimSpace(state.Workspaces[i].Root) == "" {
				state.Workspaces[i].Root = e.defaultWorkspaceRoot(state.Workspaces[i].ID)
			}
		}
		if len(state.Agents) == 0 {
			base := PermissionSet{Filesystem: "workspace-readonly"}
			state.Agents = []Agent{
				{ID: "commander-default", Role: "commander", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 1, Permissions: base},
				{ID: "recon-default", Role: "recon", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 4, Permissions: base},
				{ID: "analysis-default", Role: "analysis", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 4, Permissions: base},
				{ID: "validation-default", Role: "validation", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 2, Permissions: PermissionSet{Filesystem: "workspace-readonly", Device: true}},
			}
		} else {
			for i := range state.Agents {
				if state.Agents[i].Permissions.Filesystem == "" {
					state.Agents[i].Permissions.Filesystem = "workspace-readonly"
				}
				if state.Agents[i].Role == "validation" {
					state.Agents[i].Permissions.Device = true
				}
			}
		}
		if len(state.Skills) == 0 {
			state.Skills = []Skill{
				{ID: "skill.hidden-interface-discovery", Name: "hidden_interface_discovery", Version: "1.0.0", Enabled: true, Roles: []string{"recon", "analysis"}, Steps: []string{"firmware.inventory", "web.route_discovery", "binary.search_string", "protocol.hidden_interface", "taint.trace"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
				{ID: "skill.passive-target-recon", Name: "passive_target_recon", Version: "1.0.0", Enabled: true, Roles: []string{"commander", "recon"}, Steps: []string{"target.fingerprint", "finding.gate"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
				{ID: "skill.validation-review", Name: "validation_review", Version: "1.0.0", Enabled: true, Roles: []string{"validation"}, Steps: []string{"fuzz.constraint", "emulation.run", "cvss.score"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
			}
		}
		return nil
	})
}

// recoverTasks converts work that was interrupted by a process restart back to
// queued work and starts it through the same scheduler path as new tasks.
// Blocked tasks remain blocked because they require a human decision first.
func (e *Engine) recoverTasks() {
	for _, task := range e.Store.Snapshot().Tasks {
		if task.Status != TaskQueued && task.Status != TaskAssigned && task.Status != TaskRunning {
			continue
		}
		target, ok := e.Store.Target(task.TargetID)
		if !ok {
			_ = e.Store.UpdateTask(task.ID, func(value *Task) error {
				value.Status, value.Error = TaskFailed, "target for task recovery was not found"
				value.CompletedAt = timePtr(now())
				return nil
			})
			continue
		}
		if task.Status != TaskQueued {
			_ = e.Store.UpdateTask(task.ID, func(value *Task) error {
				value.Status = TaskQueued
				value.Error = ""
				return nil
			})
			fresh, _ := e.Store.Task(task.ID)
			task = fresh
		}
		e.startTask(context.Background(), task, target)
	}
}

func (e *Engine) RegisterCapability(c Capability, executor CapabilityExecutor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.capabilities[c.ID] = c
	e.tools["builtin:"+c.ID] = Tool{Name: "builtin:" + c.ID, Category: c.Category, Execution: c.Runtime, Permissions: c.Permissions, Runtime: c.Implementation, TimeoutSeconds: c.TimeoutSeconds}
	if executor != nil {
		e.executors[c.ID] = executor
	}
}

// SetWorkerRunner replaces the process runner used by worker-backed
// capabilities. It is primarily useful for embedding and integration tests.
func (e *Engine) SetWorkerRunner(r workerpkg.Runner) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.worker = r
}

func (e *Engine) Tools() []Tool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Tool, 0, len(e.tools))
	for _, tool := range e.tools {
		out = append(out, tool)
	}
	if e.ToolGateway != nil {
		for _, definition := range e.ToolGateway.List() {
			out = append(out, Tool{Name: definition.Name, Category: "external", Execution: "process", Isolation: definition.Isolation, Image: definition.Image, Permissions: PermissionSet{Network: definition.Permissions.Network, Filesystem: definition.Permissions.Filesystem, Destructive: definition.Permissions.Destructive}, Runtime: definition.Path, TimeoutSeconds: int(definition.Timeout / time.Second)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunTool is the only control-plane entry point for host tool execution. It
// applies the task permission set and, for workspace tasks, prevents path
// arguments and working directories from escaping the workspace root.
func (e *Engine) RunTool(ctx context.Context, taskID, name string, args []string, workingDir string, requested toolspkg.PermissionSet, timeout time.Duration) (toolspkg.Result, error) {
	if e.ToolGateway == nil {
		return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: "tool gateway is unavailable"}, errors.New("tool gateway is unavailable")
	}
	workspaceRoot := ""
	if taskID != "" {
		task, ok := e.Store.Task(taskID)
		if !ok {
			return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: "task not found"}, errors.New("task not found")
		}
		if isTerminalTask(task.Status) {
			return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: "task is not active"}, errors.New("task is not active")
		}
		requested = toolspkg.PermissionSet{Network: task.Permissions.Network, Filesystem: task.Permissions.Filesystem, Destructive: task.Permissions.Destructive}
		workspace, ok := e.Store.Workspace(task.WorkspaceID)
		if !ok {
			return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: "workspace not found"}, errors.New("workspace not found")
		}
		root := workspace.Root
		if root == "" {
			root = e.defaultWorkspaceRoot(workspace.ID)
		}
		workspaceRoot = root
		if workingDir == "" {
			workingDir = root
		}
		if err := ensureInsideRoot(root, workingDir); err != nil {
			return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: err.Error()}, err
		}
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") || strings.TrimSpace(arg) == "" {
				continue
			}
			candidate := arg
			if !filepath.IsAbs(candidate) {
				candidate = filepath.Join(workingDir, candidate)
			}
			if filepath.IsAbs(arg) || strings.ContainsAny(arg, `/\\`) || func() bool { _, statErr := os.Stat(candidate); return statErr == nil }() {
				if err := ensureInsideRoot(root, candidate); err != nil {
					return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: err.Error()}, err
				}
			}
		}
		if err := e.consumeToolBudget(task.ID, task.Budget.MaxToolCalls); err != nil {
			return toolspkg.Result{Tool: name, Status: "failed", ExitCode: -1, StartedAt: now(), Error: err.Error()}, err
		}
	}
	if workingDir == "" {
		workingDir = "."
	}
	result, err := e.ToolGateway.Run(ctx, toolspkg.Request{Tool: name, Args: append([]string(nil), args...), WorkingDir: workingDir, WorkspaceRoot: workspaceRoot, Permissions: requested, Timeout: timeout})
	if taskID != "" {
		status := result.Status
		if err != nil {
			status = "failed"
		}
		_ = e.Store.AddToolRun(ToolRun{ID: NewID("TOOLRUN"), TaskID: taskID, ToolName: name, Status: status, Command: append([]string{name}, args...), ExitCode: result.ExitCode, Output: map[string]any{"output": result.Output, "error": result.Error}, StartedAt: result.StartedAt, CompletedAt: timePtr(result.StartedAt.Add(result.Duration))})
		if task, ok := e.Store.Task(taskID); ok {
			_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "tool." + status, WorkspaceID: task.WorkspaceID, TaskID: taskID, Payload: map[string]any{"tool": name, "command": append([]string{name}, args...)}, CreatedAt: now()})
		}
	}
	return result, err
}

func ensureInsideRoot(root, candidate string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("invalid workspace root: %w", err)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve workspace root: %w", err)
		}
		resolvedRoot = filepath.Clean(rootAbs)
	}
	resolvedCandidate, err := filepath.EvalSymlinks(candidateAbs)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("resolve path: %w", err)
		}
		resolvedCandidate = filepath.Clean(candidateAbs)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path is outside the workspace")
	}
	return nil
}

func (e *Engine) registerHostTools() {
	for _, name := range []string{"file", "strings", "objdump"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		_ = e.ToolGateway.Register(toolspkg.Definition{Name: name, Path: path, Permissions: toolspkg.PermissionSet{Filesystem: "workspace-readonly"}, Timeout: 2 * time.Minute})
	}
}

func (e *Engine) Capabilities() []Capability {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Capability, 0, len(e.capabilities))
	for _, c := range e.capabilities {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) RegisterBuiltinCapabilities() {
	base := PermissionSet{Filesystem: "workspace-readonly"}
	e.RegisterCapability(Capability{ID: "target.fingerprint", Version: "1.0.0", Category: "recon", Description: "Build a passive target fingerprint from declared metadata.", Permissions: base, Runtime: "builtin", Implementation: "go", TimeoutSeconds: 30}, e.fingerprint)
	e.RegisterCapability(Capability{ID: "finding.gate", Version: "1.0.0", Category: "control", Description: "Evaluate whether a finding has enough evidence to continue.", Permissions: base, Runtime: "builtin", Implementation: "go", TimeoutSeconds: 10}, e.findingGate)
	e.RegisterCapability(Capability{ID: "report.generate", Version: "1.0.0", Category: "report", Description: "Render a workspace SITREP and Markdown report.", Permissions: base, Runtime: "builtin", Implementation: "go", TimeoutSeconds: 30}, e.reportCapability)
	for _, item := range capabilityCatalog() {
		if _, exists := e.capabilities[item.ID]; exists {
			continue
		}
		executor := CapabilityExecutor(e.catalogCapability)
		if item.Category == "peripheral" {
			executor = e.peripheralCapability
		}
		if item.ID == "binary.decompile" {
			executor = e.binaryDecompile
		}
		e.RegisterCapability(item, executor)
	}
}

func capabilityCatalog() []Capability {
	base := PermissionSet{Filesystem: "workspace-readonly"}
	items := []Capability{
		{ID: "firmware.extract", Category: "firmware", Description: "Describe an offline firmware extraction plan and inputs.", Runtime: "builtin", Implementation: "go"},
		{ID: "firmware.inventory", Category: "firmware", Description: "Inventory offline firmware/filesystem entries supplied by the caller.", Runtime: "builtin", Implementation: "go"},
		{ID: "firmware.config_scan", Category: "firmware", Description: "Scan supplied configuration entries for risky settings.", Runtime: "builtin", Implementation: "go"},
		{ID: "binary.identify", Category: "binary", Description: "Identify a binary from its declared name, type, or magic metadata.", Runtime: "builtin", Implementation: "go"},
		{ID: "binary.decompile", Category: "binary", Description: "Disassemble an offline binary through the allow-listed objdump tool.", Runtime: "builtin", Implementation: "objdump"},
		{ID: "binary.search_string", Category: "binary", Description: "Search strings supplied in an offline binary index.", Runtime: "builtin", Implementation: "go"},
		{ID: "binary.callgraph", Category: "binary", Description: "Normalize a supplied call graph for analysis.", Runtime: "builtin", Implementation: "go"},
		{ID: "binary.xref", Category: "binary", Description: "Resolve supplied cross references.", Runtime: "builtin", Implementation: "go"},
		{ID: "taint.trace", Category: "taint", Description: "Record a source-to-sink trace supplied by an analysis worker.", Runtime: "builtin", Implementation: "go"},
		{ID: "taint.storage_trace", Category: "taint", Description: "Record a source-to-storage-to-sink trace.", Runtime: "builtin", Implementation: "go"},
		{ID: "protocol.parse", Category: "protocol", Description: "Parse key-value protocol text without sending packets.", Runtime: "builtin", Implementation: "go"},
		{ID: "protocol.attack_surface", Category: "protocol", Description: "Summarize declared protocol entry points.", Runtime: "builtin", Implementation: "go"},
		{ID: "protocol.hidden_interface", Category: "protocol", Description: "Extract route-like strings from supplied HTML or text.", Runtime: "builtin", Implementation: "go"},
		{ID: "web.route_discovery", Category: "web", Description: "Discover routes from saved HTML or route text.", Runtime: "builtin", Implementation: "go"},
		{ID: "web.auth_analysis", Category: "web", Description: "Summarize declared authentication controls.", Runtime: "builtin", Implementation: "go"},
		{ID: "config.audit", Category: "analysis", Description: "Audit supplied configuration keys for risky values.", Runtime: "builtin", Implementation: "go"},
		{ID: "fuzz.constraint", Category: "validation", Description: "Generate a bounded, non-executing fuzz constraint plan.", Runtime: "builtin", Implementation: "go"},
		{ID: "fuzz.seed_generate", Category: "validation", Description: "Generate structured seed candidates without sending them.", Runtime: "builtin", Implementation: "go"},
		{ID: "emulation.run", Category: "validation", Description: "Create an emulation run plan for an isolated worker.", Runtime: "builtin", Implementation: "go"},
		{ID: "packet.generate", Category: "validation", Description: "Generate a packet description without replaying it.", Runtime: "builtin", Implementation: "go"},
		{ID: "packet.replay", Category: "validation", Description: "Create a replay approval request; no network is performed by the builtin.", Runtime: "builtin", Implementation: "go"},
		{ID: "device.inspect", Category: "device", Description: "Inspect declared device metadata without connecting to it.", Runtime: "builtin", Implementation: "go"},
		{ID: "device.validate", Category: "device", Description: "Create a device validation plan requiring an external approved worker.", Runtime: "builtin", Implementation: "go"},
		{ID: "poc.verify", Category: "validation", Description: "Record a safe, supplied PoC verification result.", Runtime: "builtin", Implementation: "go"},
		{ID: "cvss.score", Category: "analysis", Description: "Calculate a transparent approximate score from supplied impact fields.", Runtime: "builtin", Implementation: "go"},
		{ID: "knowledge.search", Category: "knowledge", Description: "Search stored knowledge items by title and content.", Runtime: "builtin", Implementation: "go"},
		{ID: "knowledge.pattern_match", Category: "knowledge", Description: "Match supplied terms against stored knowledge patterns.", Runtime: "builtin", Implementation: "go"},
		{ID: "serial.open", Category: "peripheral", Description: "Open a leased serial peripheral session for a task.", Runtime: "builtin", Implementation: "go"},
		{ID: "serial.configure", Category: "peripheral", Description: "Apply validated serial parameters to an acquired session.", Runtime: "builtin", Implementation: "go"},
		{ID: "serial.read", Category: "peripheral", Description: "Read bytes from an acquired serial peripheral session.", Runtime: "builtin", Implementation: "go"},
		{ID: "serial.write", Category: "peripheral", Description: "Write caller-supplied bytes to an acquired serial peripheral session.", Runtime: "builtin", Implementation: "go"},
		{ID: "serial.identity", Category: "peripheral", Description: "Read identity and current configuration from a serial peripheral session.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.read", Category: "peripheral", Description: "Read power supply telemetry through an acquired session.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.measure", Category: "peripheral", Description: "Query a power supply measurement through an acquired session.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.set_voltage", Category: "peripheral", Description: "Set a bounded power supply voltage after approval and safety checks.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.set_current", Category: "peripheral", Description: "Set a bounded power supply current after approval and safety checks.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.output", Category: "peripheral", Description: "Enable or disable power output after approval and safety checks.", Runtime: "builtin", Implementation: "go"},
		{ID: "power.cycle", Category: "peripheral", Description: "Power-cycle a target through a bounded, approved supply session.", Runtime: "builtin", Implementation: "go"},
		{ID: "scope.capture", Category: "peripheral", Description: "Capture a bounded waveform through an acquired oscilloscope session.", Runtime: "builtin", Implementation: "go"},
		{ID: "scope.configure", Category: "peripheral", Description: "Apply validated oscilloscope acquisition parameters.", Runtime: "builtin", Implementation: "go"},
		{ID: "scope.measure", Category: "peripheral", Description: "Read a bounded measurement from an oscilloscope session.", Runtime: "builtin", Implementation: "go"},
		{ID: "jlink.attach", Category: "peripheral", Description: "Attach to a debug probe through the configured driver host.", Runtime: "builtin", Implementation: "go"},
		{ID: "jlink.reset", Category: "peripheral", Description: "Reset a target through an approved debug-probe session.", Runtime: "builtin", Implementation: "go"},
		{ID: "jlink.halt", Category: "peripheral", Description: "Halt a target through an approved debug-probe session.", Runtime: "builtin", Implementation: "go"},
		{ID: "jlink.read_memory", Category: "peripheral", Description: "Read a bounded memory range through a debug-probe session.", Runtime: "builtin", Implementation: "go"},
		{ID: "bluetooth.capture", Category: "peripheral", Description: "Start a bounded capture on a supported Bluetooth analyzer.", Runtime: "builtin", Implementation: "go"},
		{ID: "bluetooth.scan", Category: "peripheral", Description: "Scan for Bluetooth advertisements through an analyzer session.", Runtime: "builtin", Implementation: "go"},
		{ID: "packet.capture", Category: "peripheral", Description: "Capture bounded packets through a configured capture session.", Runtime: "builtin", Implementation: "go"},
	}
	for i := range items {
		items[i].Version = "1.0.0"
		items[i].Permissions = base
		items[i].TimeoutSeconds = 60
		items[i].InputSchema = capabilityInputSchema(items[i].ID)
		if items[i].Category == "peripheral" {
			items[i].Permissions.Device = true
			switch items[i].ID {
			case "power.set_voltage", "power.set_current", "power.output", "power.cycle", "jlink.reset", "jlink.halt":
				items[i].Permissions.Destructive = true
			}
		}
		switch items[i].ID {
		case "packet.replay":
			items[i].Permissions.Network = true
			items[i].Permissions.Destructive = true
		case "device.validate":
			items[i].Permissions.Device = true
			items[i].Permissions.Destructive = true
		}
		switch items[i].ID {
		case "firmware.extract", "firmware.inventory", "firmware.config_scan", "binary.identify", "binary.search_string", "binary.callgraph", "binary.xref", "protocol.parse", "protocol.attack_surface", "protocol.hidden_interface", "web.route_discovery", "taint.trace", "taint.storage_trace", "fuzz.seed_generate", "packet.generate", "packet.replay", "device.validate", "poc.verify", "cvss.score", "knowledge.search", "knowledge.pattern_match":
			items[i].Runtime = "python"
			items[i].Implementation = "capability-workers/knowledge/worker.py"
		}
	}
	return items
}

func capabilityInputSchema(id string) map[string]any {
	properties := map[string]any{}
	switch id {
	case "firmware.extract", "firmware.inventory", "firmware.config_scan", "binary.identify", "binary.decompile", "binary.search_string":
		properties["path"] = map[string]any{"type": "string", "description": "Path to an offline artifact in the workspace"}
		if id == "binary.search_string" {
			properties["query"] = map[string]any{"type": "string"}
		}
	case "protocol.parse", "protocol.attack_surface", "protocol.hidden_interface", "web.route_discovery":
		properties["text"] = map[string]any{"type": "string"}
	case "config.audit":
		properties["values"] = map[string]any{"type": "object"}
	case "knowledge.search", "knowledge.pattern_match":
		properties["query"] = map[string]any{"type": "string"}
		properties["corpus"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case "serial.open":
		properties["peripheral_id"] = map[string]any{"type": "string"}
		properties["workspace_id"] = map[string]any{"type": "string"}
		properties["args"] = map[string]any{"type": "object"}
	case "serial.read", "serial.write", "serial.identity", "serial.configure":
		properties["session_id"] = map[string]any{"type": "string"}
		properties["args"] = map[string]any{"type": "object"}
	case "power.measure", "power.read", "power.cycle", "scope.capture", "scope.configure", "scope.measure", "jlink.attach", "jlink.reset", "jlink.halt", "jlink.read_memory", "bluetooth.capture", "bluetooth.scan", "packet.capture", "power.set_voltage", "power.set_current", "power.output":
		properties["session_id"] = map[string]any{"type": "string"}
		properties["args"] = map[string]any{"type": "object"}
	default:
		properties["inputs"] = map[string]any{"type": "object"}
	}
	return map[string]any{"type": "object", "properties": properties}
}

func (e *Engine) authorize(task Task, cap Capability) error {
	if cap.Permissions.Network && !task.Permissions.Network {
		return errors.New("task does not grant network access required by capability")
	}
	if cap.Permissions.Device && !task.Permissions.Device {
		return errors.New("task does not grant device access required by capability")
	}
	if cap.Permissions.Destructive && !task.Permissions.Destructive {
		return errors.New("task does not grant destructive access required by capability")
	}
	if cap.Permissions.Filesystem == "workspace-write" && task.Permissions.Filesystem != "workspace-write" {
		return errors.New("task does not grant workspace writes required by capability")
	}
	// Task permissions are an upper bound. A task may grant a broad scope so
	// that a plan can contain both read-only and high-risk nodes; each node is
	// still checked against its own capability requirements below.
	agentFound := false
	for _, agent := range e.Store.Snapshot().Agents {
		if agent.ID != task.AssignedAgent {
			continue
		}
		agentFound = true
		if !agent.Enabled {
			return errors.New("assigned agent is disabled")
		}
		permissions := agent.Permissions
		if permissions.Filesystem == "" {
			permissions.Filesystem = "workspace-readonly"
		}
		if task.Permissions.Network && !permissions.Network {
			return errors.New("assigned agent does not allow network access")
		}
		if task.Permissions.Device && !permissions.Device {
			return errors.New("assigned agent does not allow device access")
		}
		if task.Permissions.Destructive && !permissions.Destructive {
			return errors.New("assigned agent does not allow destructive access")
		}
		if task.Permissions.Filesystem == "workspace-write" && permissions.Filesystem != "workspace-write" {
			return errors.New("assigned agent does not allow workspace writes")
		}
		if cap.Permissions.Network && !permissions.Network {
			return errors.New("assigned agent does not allow capability network access")
		}
		if cap.Permissions.Device && !permissions.Device {
			return errors.New("assigned agent does not allow capability device access")
		}
		if cap.Permissions.Destructive && !permissions.Destructive {
			return errors.New("assigned agent does not allow capability destructive access")
		}
		break
	}
	if strings.TrimSpace(task.AssignedAgent) != "" && !agentFound {
		return errors.New("assigned agent was not found")
	}
	return nil
}

func (e *Engine) Request(ctx context.Context, req CapabilityRequest, task Task) (CapabilityResult, error) {
	e.mu.RLock()
	cap, ok := e.capabilities[req.CapabilityID]
	executor := e.executors[req.CapabilityID]
	e.mu.RUnlock()
	if !ok {
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "failed", Error: "capability not found"}, fmt.Errorf("capability %s not found", req.CapabilityID)
	}
	if err := e.authorize(task, cap); err != nil {
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "denied", Error: err.Error()}, err
	}
	// Read-only device access is L0. Only capabilities explicitly marked
	// destructive (or a task that requests destructive scope) enter approval.
	if cap.Permissions.Destructive && !e.hasApprovedRequest(task.ID, cap.ID) {
		if e.hasPendingRequest(task.ID, cap.ID) {
			return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "pending_approval", Error: "human approval required"}, nil
		}
		a := Approval{ID: NewID("APR"), TaskID: task.ID, CapabilityID: cap.ID, Reason: "high-risk permission requires explicit human approval", Status: "pending", RequestedAt: now()}
		_ = e.Store.AddApproval(a)
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "human.approval.required", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"approval_id": a.ID}, CreatedAt: now()})
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "pending_approval", Error: "human approval required"}, nil
	}
	if executor == nil {
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "failed", Error: "capability has no executor"}, errors.New("capability has no executor")
	}
	capCtx := ctx
	capCancel := func() {}
	if cap.TimeoutSeconds > 0 {
		capCtx, capCancel = context.WithTimeout(ctx, time.Duration(cap.TimeoutSeconds)*time.Second)
	}
	defer capCancel()
	if err := e.preflightCapabilityTools(capCtx, req, task, cap); err != nil {
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "failed", Error: err.Error()}, err
	}
	select {
	case e.workers <- struct{}{}:
	case <-capCtx.Done():
		return CapabilityResult{}, capCtx.Err()
	}
	defer func() { <-e.workers }()
	started := time.Now()
	toolName := "builtin:" + cap.ID
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.started", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID}, CreatedAt: now()})
	var result CapabilityResult
	var err error
	if cap.Runtime == "python" {
		result, err = e.requestWorker(capCtx, req)
	} else {
		result, err = executor(capCtx, req)
	}
	if result.RequestID == "" {
		result.RequestID = req.RequestID
	}
	if result.CapabilityID == "" {
		result.CapabilityID = cap.ID
	}
	if result.Metrics == nil {
		result.Metrics = map[string]any{}
	}
	result.Metrics["runtime_ms"] = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.failed", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID, "error": err.Error()}, CreatedAt: now()})
		_ = e.Store.AddCapabilityRun(CapabilityRun{ID: NewID("CAPRUN"), RequestID: req.RequestID, TaskID: task.ID, CapabilityID: cap.ID, Status: "failed", Result: map[string]any{"error": err.Error()}, StartedAt: started, CompletedAt: timePtr(now())})
		_ = e.Store.AddToolRun(ToolRun{ID: NewID("TOOLRUN"), TaskID: task.ID, ToolName: toolName, Status: "failed", Output: map[string]any{"error": err.Error()}, StartedAt: started, CompletedAt: timePtr(now())})
		return result, err
	}
	if result.Status == "pending_approval" {
		_ = e.Store.AddCapabilityRun(CapabilityRun{ID: NewID("CAPRUN"), RequestID: req.RequestID, TaskID: task.ID, CapabilityID: cap.ID, Status: "pending_approval", Result: map[string]any{"error": result.Error}, StartedAt: started, CompletedAt: timePtr(now())})
		return result, nil
	}
	if result.Status == "" {
		// Builtin executors may omit the transport status; a returned result is
		// considered successful unless it explicitly reports failure.
		result.Status = "completed"
	}
	if result.Status != "completed" || strings.TrimSpace(result.Error) != "" {
		if strings.TrimSpace(result.Error) == "" {
			result.Error = "capability returned a non-completed status"
		}
		result.Status = "failed"
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.failed", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID, "error": result.Error}, CreatedAt: now()})
		_ = e.Store.AddCapabilityRun(CapabilityRun{ID: NewID("CAPRUN"), RequestID: req.RequestID, TaskID: task.ID, CapabilityID: cap.ID, Status: "failed", Result: map[string]any{"error": result.Error}, StartedAt: started, CompletedAt: timePtr(now())})
		return result, errors.New(result.Error)
	}
	result.Status = "completed"
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.completed", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID}, CreatedAt: now()})
	_ = e.Store.AddCapabilityRun(CapabilityRun{ID: NewID("CAPRUN"), RequestID: req.RequestID, TaskID: task.ID, CapabilityID: cap.ID, Status: "completed", Result: map[string]any{"summary": result.Summary, "confidence": result.Confidence, "metrics": result.Metrics}, StartedAt: started, CompletedAt: timePtr(now())})
	_ = e.Store.AddToolRun(ToolRun{ID: NewID("TOOLRUN"), TaskID: task.ID, ToolName: toolName, Status: "completed", Output: map[string]any{"summary": result.Summary}, StartedAt: started, CompletedAt: timePtr(now())})
	return result, nil
}

func (e *Engine) hasApprovedRequest(taskID, capabilityID string) bool {
	for _, approval := range e.Store.Snapshot().Approvals {
		if approval.TaskID == taskID && approval.CapabilityID == capabilityID && approval.Status == "approved" {
			return true
		}
	}
	return false
}

func (e *Engine) hasPendingRequest(taskID, capabilityID string) bool {
	for _, approval := range e.Store.Snapshot().Approvals {
		if approval.TaskID == taskID && approval.CapabilityID == capabilityID && approval.Status == "pending" {
			return true
		}
	}
	return false
}

// preflightCapabilityTools records the concrete Tool Gateway invocation that
// accompanies file-backed capabilities. The Python worker performs the
// structured analysis; the gateway supplies bounded host-tool observations and
// keeps the Agent -> Capability -> Tool boundary auditable.
func (e *Engine) preflightCapabilityTools(ctx context.Context, req CapabilityRequest, task Task, cap Capability) error {
	if e.ToolGateway == nil || cap.Runtime != "python" {
		return nil
	}
	pathValue, _ := req.Inputs["path"].(string)
	if pathValue == "" {
		pathValue, _ = req.Inputs["file_path"].(string)
	}
	if pathValue == "" {
		return nil
	}
	if root, _ := req.Inputs["workspace_root"].(string); strings.TrimSpace(root) != "" {
		resolvedPath, pathErr := filepath.EvalSymlinks(pathValue)
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if pathErr != nil || rootErr != nil {
			return fmt.Errorf("cannot resolve capability input path")
		}
		relative, relErr := filepath.Rel(resolvedRoot, resolvedPath)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("capability input path is outside the workspace")
		}
	}
	toolNames := []string{}
	switch cap.ID {
	case "binary.identify", "firmware.extract", "firmware.inventory", "firmware.config_scan":
		toolNames = []string{"file"}
	case "binary.search_string":
		toolNames = []string{"file", "strings"}
	default:
		return nil
	}
	for _, name := range toolNames {
		if err := e.consumeToolBudget(task.ID, task.Budget.MaxToolCalls); err != nil {
			return err
		}
		if !e.ToolGateway.Has(name) {
			// A missing optional host utility is still counted as an attempted
			// call so a task cannot bypass its declared budget by probing tools.
			continue
		}
		args := []string{"--brief", pathValue}
		if name == "strings" {
			args = []string{"-n", "4", pathValue}
		}
		result, err := e.ToolGateway.Run(ctx, toolspkg.Request{Tool: name, Args: args, WorkingDir: filepath.Dir(pathValue), WorkspaceRoot: stringInput(req.Inputs, "workspace_root", ""), Permissions: toolspkg.PermissionSet{Filesystem: task.Permissions.Filesystem}, Timeout: time.Duration(cap.TimeoutSeconds) * time.Second})
		status := result.Status
		if err != nil {
			status = "failed"
		}
		_ = e.Store.AddToolRun(ToolRun{ID: NewID("TOOLRUN"), TaskID: task.ID, ToolName: name, Status: status, Command: append([]string{name}, args...), ExitCode: result.ExitCode, Output: map[string]any{"output": result.Output, "error": result.Error, "capability_id": cap.ID}, StartedAt: result.StartedAt, CompletedAt: timePtr(result.StartedAt.Add(result.Duration))})
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "tool." + status, WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"tool": name, "capability_id": cap.ID}, CreatedAt: now()})
		// The structured worker remains authoritative for the capability. A
		// missing optional host utility is recorded for diagnostics but does not
		// turn an otherwise valid offline analysis into a platform failure.
	}
	return nil
}

func (e *Engine) consumeToolBudget(taskID string, maximum int) error {
	if strings.TrimSpace(taskID) == "" || maximum <= 0 {
		return nil
	}
	e.toolUsageMu.Lock()
	defer e.toolUsageMu.Unlock()
	used := e.taskToolUsage[taskID]
	if used >= maximum {
		return fmt.Errorf("task tool-call budget exhausted (%d)", maximum)
	}
	e.taskToolUsage[taskID] = used + 1
	return nil
}

func (e *Engine) resetToolBudget(taskID string) {
	e.toolUsageMu.Lock()
	delete(e.taskToolUsage, taskID)
	e.toolUsageMu.Unlock()
}

func (e *Engine) requestWorker(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	e.mu.RLock()
	runner := e.worker
	e.mu.RUnlock()
	permissions := map[string]any{
		"network": req.Permissions.Network, "filesystem": req.Permissions.Filesystem,
		"device": req.Permissions.Device, "destructive": req.Permissions.Destructive,
	}
	budget := map[string]any{
		"max_runtime_seconds": req.Budget.MaxRuntimeSeconds, "max_tool_calls": req.Budget.MaxToolCalls,
		"max_tokens": req.Budget.MaxTokens,
	}
	result, err := runner.Run(ctx, workerpkg.Request{
		RequestID: req.RequestID, TaskID: req.TaskID, AgentID: req.AgentID,
		CapabilityID: req.CapabilityID, Objective: req.Objective, Inputs: req.Inputs,
		Permissions: permissions, Budget: budget,
	})
	if err != nil {
		return CapabilityResult{}, err
	}
	out := CapabilityResult{RequestID: result.RequestID, CapabilityID: result.CapabilityID, Status: result.Status, Summary: result.Summary, Confidence: result.Confidence, Metrics: result.Metrics, Error: result.Error}
	for _, raw := range result.Evidence {
		encoded, marshalErr := json.Marshal(raw)
		if marshalErr != nil {
			return CapabilityResult{}, fmt.Errorf("encode worker evidence: %w", marshalErr)
		}
		var evidence Evidence
		if err := json.Unmarshal(encoded, &evidence); err != nil {
			return CapabilityResult{}, fmt.Errorf("decode worker evidence: %w", err)
		}
		out.Evidence = append(out.Evidence, evidence)
	}
	for _, raw := range result.Artifacts {
		encoded, marshalErr := json.Marshal(raw)
		if marshalErr != nil {
			return CapabilityResult{}, fmt.Errorf("encode worker artifact: %w", marshalErr)
		}
		var artifact Artifact
		if err := json.Unmarshal(encoded, &artifact); err != nil {
			return CapabilityResult{}, fmt.Errorf("decode worker artifact: %w", err)
		}
		out.Artifacts = append(out.Artifacts, artifact)
	}
	return out, nil
}

func timePtr(t time.Time) *time.Time { return &t }

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func (e *Engine) binaryDecompile(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	path := stringInput(req.Inputs, "path", "")
	if path == "" {
		return CapabilityResult{}, errors.New("binary.decompile requires inputs.path")
	}
	if root := stringInput(req.Inputs, "workspace_root", ""); root != "" {
		if err := ensureInsideRoot(root, path); err != nil {
			return CapabilityResult{}, err
		}
	}
	if e.ToolGateway == nil || !e.ToolGateway.Has("objdump") {
		return CapabilityResult{}, errors.New("objdump is not available in the Tool Gateway")
	}
	workingDir := filepath.Dir(path)
	permissions := toolspkg.PermissionSet{Filesystem: req.Permissions.Filesystem}
	var run toolspkg.Result
	var err error
	if _, ok := e.Store.Task(req.TaskID); ok {
		run, err = e.RunTool(ctx, req.TaskID, "objdump", []string{"-d", path}, workingDir, permissions, 2*time.Minute)
	} else {
		run, err = e.ToolGateway.Run(ctx, toolspkg.Request{Tool: "objdump", Args: []string{"-d", path}, WorkingDir: workingDir, Permissions: permissions, Timeout: 2 * time.Minute})
	}
	if err != nil {
		return CapabilityResult{}, fmt.Errorf("objdump failed: %w", err)
	}
	evidence := Evidence{ID: NewID("E"), Type: "binary_disassembly", Confidence: 0.9, Content: map[string]any{"path": path, "tool": "objdump", "output": run.Output, "exit_code": run.ExitCode}, Source: map[string]string{"agent_id": req.AgentID, "task_id": req.TaskID, "capability_id": req.CapabilityID}}
	artifacts := []Artifact{}
	if task, ok := e.Store.Task(req.TaskID); ok {
		artifact, storeErr := e.StoreArtifactBytes(task.WorkspaceID, filepath.Base(path)+".objdump.txt", "binary-disassembly", []byte(run.Output), map[string]any{"source_path": path, "tool": "objdump", "task_id": req.TaskID})
		if storeErr != nil {
			return CapabilityResult{}, storeErr
		}
		artifacts = append(artifacts, artifact)
	}
	return CapabilityResult{Summary: fmt.Sprintf("Disassembled %s with objdump", filepath.Base(path)), Confidence: 0.9, Evidence: []Evidence{evidence}, Artifacts: artifacts, Metrics: map[string]any{"output_bytes": len(run.Output)}}, nil
}

func (e *Engine) fingerprint(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	select {
	case <-ctx.Done():
		return CapabilityResult{}, ctx.Err()
	default:
	}
	vendor, _ := req.Inputs["vendor"].(string)
	model, _ := req.Inputs["model"].(string)
	address, _ := req.Inputs["address"].(string)
	transport, _ := req.Inputs["transport"].(string)
	fp := map[string]any{"vendor": vendor, "model": model, "address": address, "transport": transport, "passive": true}
	confidence := 0.35
	if vendor != "" {
		confidence += 0.2
	}
	if model != "" {
		confidence += 0.2
	}
	if transport != "" {
		confidence += 0.15
	}
	return CapabilityResult{Summary: "Passive target fingerprint collected", Confidence: confidence, Evidence: []Evidence{{ID: NewID("E"), Type: "attack_surface", Confidence: confidence, Content: fp, Source: map[string]string{"agent_id": "recon-default", "task_id": req.TaskID, "capability_id": req.CapabilityID}}}}, nil
}

func (e *Engine) findingGate(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	gate := stringInput(req.Inputs, "gate", "finding")
	findingID := stringInput(req.Inputs, "finding_id", "")
	if findingID == "" && req.TaskID != "" {
		if task, ok := e.Store.Task(req.TaskID); ok {
			findingID = task.FindingID
		}
	}
	if findingID == "" {
		return CapabilityResult{Summary: "Finding gate is waiting for a finding", Confidence: 0.2, Evidence: []Evidence{{ID: NewID("E"), Type: "gate_decision", Confidence: 0.2, Content: map[string]any{"gate": gate, "decision": "HOLD", "reason": "finding has not been created yet"}, Source: map[string]string{"capability_id": req.CapabilityID}}}}, nil
	}
	decision, err := e.EvaluateGate(findingID, gate)
	if err != nil {
		return CapabilityResult{}, err
	}
	return CapabilityResult{Summary: fmt.Sprintf("%s gate decision: %s", gate, decision.Decision), Confidence: 1, Evidence: []Evidence{{ID: NewID("E"), Type: "gate_decision", Confidence: 1, Content: map[string]any{"gate": gate, "decision": decision.Decision, "reasons": decision.Reasons, "finding_id": findingID}, Source: map[string]string{"capability_id": req.CapabilityID, "task_id": req.TaskID}}}}, nil
}

func (e *Engine) reportCapability(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	workspaceID, _ := req.Inputs["workspace_id"].(string)
	path, err := e.GenerateReport(workspaceID)
	if err != nil {
		return CapabilityResult{}, err
	}
	return CapabilityResult{Summary: "Report generated", Confidence: 1, Artifacts: []Artifact{{ID: NewID("ART"), Name: filepath.Base(path), Type: "report", Path: path, SHA256: fileHash(path), CreatedAt: now()}}}, nil
}

func (e *Engine) peripheralCapability(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	if e.Peripherals == nil {
		return CapabilityResult{}, errors.New("peripheral manager is unavailable")
	}
	sessionID, _ := req.Inputs["session_id"].(string)
	if req.CapabilityID == "serial.open" {
		return e.openPeripheralSession(ctx, req)
	}
	if strings.TrimSpace(sessionID) == "" {
		return CapabilityResult{}, errors.New("session_id is required")
	}
	if task, ok := e.Store.Task(req.TaskID); ok {
		if session, err := e.Peripherals.Session(sessionID); err == nil && session.WorkspaceID != "" && task.WorkspaceID != "" && session.WorkspaceID != task.WorkspaceID {
			return CapabilityResult{}, errors.New("peripheral session belongs to another workspace")
		}
	}
	command, _ := req.Inputs["command"].(string)
	if command == "" {
		switch req.CapabilityID {
		case "serial.configure", "scope.configure":
			command = "configure"
		case "serial.read":
			command = "read"
		case "serial.write":
			command = "write"
		case "serial.identity":
			command = "identity"
		case "power.measure", "power.read":
			command = "measure"
		case "power.cycle":
			command = "cycle"
		case "scope.capture":
			command = "capture"
		case "scope.measure":
			command = "measure"
		case "power.set_voltage":
			command = "set_voltage"
		case "power.set_current":
			command = "set_current"
		case "power.output":
			command = "output"
		case "jlink.attach":
			command = "attach"
		case "jlink.reset":
			command = "reset"
		case "jlink.halt":
			command = "halt"
		case "jlink.read_memory":
			command = "read_memory"
		case "bluetooth.capture":
			command = "bluetooth_capture"
		case "bluetooth.scan":
			command = "scan"
		case "packet.capture":
			command = "capture"
		default:
			return CapabilityResult{}, fmt.Errorf("capability %s requires a registered driver", req.CapabilityID)
		}
	}
	args, _ := req.Inputs["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	if command == "configure" {
		config, _ := args["config"].(map[string]any)
		if config == nil {
			config = args
		}
		if err := e.validatePeripheralConfig(sessionID, config); err != nil {
			return CapabilityResult{}, err
		}
		if err := e.Peripherals.ApplyConfig(ctx, sessionID, config); err != nil {
			return CapabilityResult{}, err
		}
		return CapabilityResult{Summary: "Peripheral configuration applied", Confidence: 1, Evidence: []Evidence{{ID: NewID("E"), Type: "peripheral_configuration", Confidence: 1, Content: map[string]any{"session_id": sessionID, "config": config}, Source: map[string]string{"agent_id": req.AgentID, "task_id": req.TaskID, "capability_id": req.CapabilityID}}}}, nil
	}
	if strings.HasPrefix(req.CapabilityID, "power.") {
		if err := e.validatePowerCommand(sessionID, req.CapabilityID, args); err != nil {
			return CapabilityResult{}, err
		}
	}
	invoked, err := e.Peripherals.Invoke(ctx, peripheralspkg.InvokeRequest{SessionID: sessionID, Command: command, Args: args})
	if err != nil {
		return CapabilityResult{}, err
	}
	content := map[string]any{"session_id": sessionID, "command": command, "data": invoked.Data, "bytes_base64": ""}
	if len(invoked.Bytes) > 0 {
		content["bytes_base64"] = base64.StdEncoding.EncodeToString(invoked.Bytes)
	}
	return CapabilityResult{Summary: "Peripheral command completed", Confidence: 1, Evidence: []Evidence{{ID: NewID("E"), Type: "peripheral_observation", Confidence: 1, Content: content, Source: map[string]string{"agent_id": req.AgentID, "task_id": req.TaskID, "capability_id": req.CapabilityID}}}}, nil
}

func (e *Engine) validatePeripheralConfig(sessionID string, config map[string]any) error {
	session, err := e.Peripherals.Session(sessionID)
	if err != nil {
		return err
	}
	peripheral, ok := findPeripheral(e.Store.Snapshot().Peripherals, session.PeripheralID)
	if !ok {
		return errors.New("peripheral record not found for session")
	}
	return validatePeripheralConfigRecord(peripheral, config)
}

func validatePeripheralConfigRecord(peripheral Peripheral, config map[string]any) error {
	if !strings.EqualFold(peripheral.Kind, "power") && !strings.EqualFold(peripheral.Kind, "scpi") {
		return nil
	}
	for _, pair := range []struct{ value, limit string }{{"voltage", "max_voltage"}, {"current", "max_current"}} {
		requested, present := config[pair.value]
		if !present {
			continue
		}
		value, valid := numericValue(requested)
		if !valid || value < 0 {
			return fmt.Errorf("power config %s must be a non-negative number", pair.value)
		}
		if limitValue, hasLimit := numericValue(peripheral.SafetyLimits[pair.limit]); hasLimit && value > limitValue {
			return fmt.Errorf("requested %s %.4g exceeds safety limit %.4g", pair.value, value, limitValue)
		}
	}
	return nil
}

func (e *Engine) validatePowerCommand(sessionID, capabilityID string, args map[string]any) error {
	session, err := e.Peripherals.Session(sessionID)
	if err != nil {
		return err
	}
	peripheral, ok := findPeripheral(e.Store.Snapshot().Peripherals, session.PeripheralID)
	if !ok {
		return errors.New("peripheral record not found for session")
	}
	if capabilityID == "power.measure" || capabilityID == "power.read" {
		return nil
	}
	if capabilityID == "power.output" || capabilityID == "power.cycle" {
		if capabilityID == "power.cycle" {
			return validatePowerEnable(peripheral)
		}
		enabled, ok := boolInput(args, "enabled")
		if !ok {
			return errors.New("power.output requires args.enabled")
		}
		if !enabled {
			return nil
		}
		if maxVoltage, ok := numericValue(peripheral.SafetyLimits["max_voltage"]); !ok || maxVoltage <= 0 {
			return errors.New("configure peripheral safety_limits.max_voltage before enabling output")
		}
		if maxCurrent, ok := numericValue(peripheral.SafetyLimits["max_current"]); !ok || maxCurrent <= 0 {
			return errors.New("configure peripheral safety_limits.max_current before enabling output")
		}
		return nil
	}
	key := "voltage"
	limitKey := "max_voltage"
	if capabilityID == "power.set_current" {
		key, limitKey = "current", "max_current"
	}
	value, ok := numericValue(args[key])
	if !ok || value < 0 {
		return fmt.Errorf("power.%s requires a non-negative numeric %s", strings.TrimPrefix(capabilityID, "power."), key)
	}
	limit, ok := numericValue(peripheral.SafetyLimits[limitKey])
	if !ok || limit <= 0 {
		return fmt.Errorf("configure peripheral safety_limits.%s before changing power", limitKey)
	}
	if value > limit {
		return fmt.Errorf("requested %s %.4g exceeds safety limit %.4g", key, value, limit)
	}
	return nil
}

func validatePowerEnable(peripheral Peripheral) error {
	if maxVoltage, ok := numericValue(peripheral.SafetyLimits["max_voltage"]); !ok || maxVoltage <= 0 {
		return errors.New("configure peripheral safety_limits.max_voltage before enabling output")
	}
	if maxCurrent, ok := numericValue(peripheral.SafetyLimits["max_current"]); !ok || maxCurrent <= 0 {
		return errors.New("configure peripheral safety_limits.max_current before enabling output")
	}
	return nil
}

func (e *Engine) openPeripheralSession(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	peripheralID := stringInput(req.Inputs, "peripheral_id", "")
	if peripheralID == "" {
		return CapabilityResult{}, errors.New("serial.open requires peripheral_id")
	}
	peripheral, ok := findPeripheral(e.Store.Snapshot().Peripherals, peripheralID)
	if !ok {
		return CapabilityResult{}, errors.New("peripheral not found")
	}
	args, _ := req.Inputs["args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	endpoint := peripheralspkg.Endpoint{Address: peripheral.Address, Port: peripheral.Port}
	if address := stringInput(args, "address", ""); address != "" {
		endpoint.Address = address
	}
	if port := stringInput(args, "port", ""); port != "" {
		endpoint.Port = port
	}
	config, _ := args["config"].(map[string]any)
	workspaceID := stringInput(req.Inputs, "workspace_id", "")
	if task, taskOK := e.Store.Task(req.TaskID); taskOK {
		workspaceID = task.WorkspaceID
	}
	session, err := e.Peripherals.Connect(ctx, peripheralspkg.OpenRequest{WorkspaceID: workspaceID, PeripheralID: peripheralID, Kind: peripheral.Kind, Endpoint: endpoint, Config: config, OwnerType: "task", OwnerID: req.TaskID, Mode: "exclusive"})
	if err != nil {
		return CapabilityResult{}, err
	}
	_ = e.Store.UpdatePeripheral(peripheralID, func(value *Peripheral) error {
		value.Status, value.OccupiedBy, value.ConnectedAt = "connected", session.OwnerType+"/"+session.OwnerID, timePtr(now())
		value.ActiveConfig = cloneMap(config)
		return nil
	})
	return CapabilityResult{Summary: "Peripheral session opened", Confidence: 1, Evidence: []Evidence{{ID: NewID("E"), Type: "peripheral_session", Confidence: 1, Content: map[string]any{"session": session}, Source: map[string]string{"agent_id": req.AgentID, "task_id": req.TaskID, "capability_id": req.CapabilityID}}}}, nil
}

func numericValue(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolInput(args map[string]any, key string) (bool, bool) {
	if args == nil {
		return false, false
	}
	switch value := args[key].(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return false, false
	}
}

func stringInput(inputs map[string]any, key, fallback string) string {
	if inputs == nil {
		return fallback
	}
	if value, ok := inputs[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func (e *Engine) catalogCapability(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	if err := ctx.Err(); err != nil {
		return CapabilityResult{}, err
	}
	content := map[string]any{"capability": req.CapabilityID, "inputs": req.Inputs, "offline": true}
	summary := "Structured capability result recorded"
	switch req.CapabilityID {
	case "firmware.inventory":
		entries, _ := req.Inputs["entries"].([]any)
		content["entry_count"] = len(entries)
		summary = fmt.Sprintf("Inventoried %d offline firmware entries", len(entries))
	case "binary.identify":
		name, _ := req.Inputs["name"].(string)
		content["format"] = identifyFormat(name)
		summary = "Binary metadata identified"
	case "protocol.parse":
		text, _ := req.Inputs["text"].(string)
		parsed := map[string]string{}
		for _, line := range strings.Split(text, "\n") {
			pair := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(pair) == 2 {
				parsed[strings.TrimSpace(pair[0])] = strings.TrimSpace(pair[1])
			}
		}
		content["fields"] = parsed
		summary = fmt.Sprintf("Parsed %d protocol fields", len(parsed))
	case "web.route_discovery", "protocol.hidden_interface":
		text, _ := req.Inputs["text"].(string)
		routes := discoverRoutes(text)
		content["routes"] = routes
		summary = fmt.Sprintf("Discovered %d route candidates", len(routes))
	case "config.audit":
		values, _ := req.Inputs["values"].(map[string]any)
		risky := []string{}
		for key := range values {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
				risky = append(risky, key)
			}
		}
		content["risky_keys"] = risky
		summary = fmt.Sprintf("Audited %d configuration keys", len(values))
	case "knowledge.search", "knowledge.pattern_match":
		query, _ := req.Inputs["query"].(string)
		matches := []map[string]any{}
		for _, item := range e.Store.Snapshot().Knowledge {
			blob, _ := json.Marshal(item.Content)
			if strings.Contains(strings.ToLower(item.Title+" "+string(blob)), strings.ToLower(query)) {
				matches = append(matches, map[string]any{"id": item.ID, "title": item.Title, "kind": item.Kind})
			}
		}
		content["matches"] = matches
		summary = fmt.Sprintf("Matched %d knowledge items", len(matches))
	case "taint.trace", "taint.storage_trace":
		source, _ := req.Inputs["source"].(string)
		sink, _ := req.Inputs["sink"].(string)
		content["source"] = source
		content["sink"] = sink
		content["traceable"] = source != "" && sink != ""
		summary = "Taint trace normalized"
	case "cvss.score":
		impact, _ := req.Inputs["impact"].(float64)
		exploitability, _ := req.Inputs["exploitability"].(float64)
		score := impact*0.6 + exploitability*0.4
		if score > 10 {
			score = 10
		}
		content["score"] = score
		summary = fmt.Sprintf("Approximate CVSS score %.1f", score)
	case "fuzz.constraint", "fuzz.seed_generate", "emulation.run", "packet.generate", "packet.replay", "device.validate", "poc.verify":
		content["execution"] = "plan_only"
		summary = "Execution plan created; external worker or approval is required"
	case "device.inspect":
		content["execution"] = "metadata_only"
		summary = "Device metadata inspected without connection"
	}
	return CapabilityResult{Summary: summary, Confidence: 0.7, Evidence: []Evidence{{ID: NewID("E"), Type: "capability_result", Confidence: 0.7, Content: content, Source: map[string]string{"agent_id": req.AgentID, "task_id": req.TaskID, "capability_id": req.CapabilityID}}}}, nil
}

func identifyFormat(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".elf"):
		return "ELF"
	case strings.HasSuffix(lower, ".bin"), strings.HasSuffix(lower, ".img"):
		return "firmware-image"
	case strings.HasSuffix(lower, ".so"):
		return "ELF-shared-object"
	default:
		return "unknown"
	}
}
func discoverRoutes(text string) []string {
	re := regexp.MustCompile(`(?i)(?:href|action|url)\s*=\s*["']([^"']+)["']|(?:^|\s)(/[A-Za-z0-9_./-]{2,})`)
	seen := map[string]bool{}
	out := []string{}
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		route := match[1]
		if route == "" {
			route = match[2]
		}
		if route != "" && !seen[route] {
			seen[route] = true
			out = append(out, route)
		}
	}
	return out
}

func (e *Engine) CreateWorkspace(name, owner, description string) (Workspace, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, errors.New("workspace name is required")
	}
	if strings.TrimSpace(owner) == "" {
		owner = "local"
	}
	w := Workspace{ID: NewID("W"), Name: name, Owner: owner, Description: strings.TrimSpace(description), Root: e.defaultWorkspaceRoot("pending"), CreatedAt: now(), UpdatedAt: now()}
	w.Root = e.defaultWorkspaceRoot(w.ID)
	if err := initializeWorkspace(w); err != nil {
		return Workspace{}, err
	}
	return w, e.Store.CreateWorkspace(w)
}

func initializeWorkspace(workspace Workspace) error {
	if strings.TrimSpace(workspace.Root) == "" {
		return errors.New("workspace root is required")
	}
	directories := []string{
		"targets/firmware", "targets/filesystem", "targets/metadata",
		"findings", "evidence/static", "evidence/dynamic", "evidence/packet", "evidence/manual",
		"artifacts", "artifacts/binary", "artifacts/poc", "artifacts/fuzz", "artifacts/harness", "artifacts/reports",
		"tasks", "capabilities", "knowledge", "skills", "approvals", "peripherals/profiles", "peripherals/presets", "peripherals/sessions", "peripherals/telemetry", "logs", "sitrep",
	}
	for _, directory := range directories {
		if err := os.MkdirAll(filepath.Join(workspace.Root, directory), 0o700); err != nil {
			return fmt.Errorf("initialize workspace directory %s: %w", directory, err)
		}
	}
	// Keep the manifest human-readable and dependency-free.
	manifest := fmt.Sprintf("version: 1\nid: %s\nname: %q\nowner: %q\ncreated_at: %s\n", workspace.ID, workspace.Name, workspace.Owner, workspace.CreatedAt.Format(time.RFC3339Nano))
	if strings.TrimSpace(workspace.Description) != "" {
		manifest += fmt.Sprintf("description: %q\n", workspace.Description)
	}
	if err := os.WriteFile(filepath.Join(workspace.Root, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		return fmt.Errorf("write workspace manifest: %w", err)
	}
	return nil
}

func (e *Engine) defaultWorkspaceRoot(workspaceID string) string {
	base := filepath.Dir(e.Store.path)
	if base == "." || base == "" {
		base = ".iothunter"
	}
	return filepath.Join(base, "workspaces", workspaceID)
}

// ImportArtifact copies a user-selected file into the workspace artifact
// store, computes its digest while copying, and returns the persisted record.
// The source may be outside the workspace because import is an explicit user
// action; subsequent worker reads use the copied workspace path.
func (e *Engine) ImportArtifact(workspaceID, sourcePath, name, artifactType string) (Artifact, error) {
	workspace, ok := e.Store.Workspace(workspaceID)
	if !ok {
		return Artifact{}, errors.New("workspace not found")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return Artifact{}, errors.New("source path is required")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return Artifact{}, fmt.Errorf("open artifact: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return Artifact{}, fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Artifact{}, errors.New("artifact source must be a regular file")
	}
	if info.Size() > 512<<20 {
		return Artifact{}, errors.New("artifact exceeds 512 MiB limit")
	}
	root := workspace.Root
	if root == "" {
		root = e.defaultWorkspaceRoot(workspace.ID)
	}
	dir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create artifact store: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".import-*")
	if err != nil {
		return Artifact{}, fmt.Errorf("create artifact staging file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), source)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("copy artifact: %w", err)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(sourcePath)
	}
	name = filepath.Base(name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return Artifact{}, errors.New("artifact name is invalid")
	}
	destination := filepath.Join(dir, digest+"-"+name)
	if err := os.Rename(tmpPath, destination); err != nil {
		return Artifact{}, fmt.Errorf("store artifact: %w", err)
	}
	if strings.TrimSpace(artifactType) == "" {
		artifactType = "input"
	}
	artifact := Artifact{ID: NewID("ART"), Name: name, Type: artifactType, Path: destination, SHA256: digest, Size: size, Metadata: map[string]any{"source_path": sourcePath, "workspace_id": workspaceID}, CreatedAt: now()}
	if err := e.Store.AddArtifact(artifact); err != nil {
		return Artifact{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "artifact.imported", WorkspaceID: workspaceID, Payload: map[string]any{"artifact_id": artifact.ID, "sha256": artifact.SHA256}, CreatedAt: now()})
	_ = e.audit("artifact.imported", "user", "artifact", artifact.ID, map[string]any{"workspace_id": workspaceID, "size": artifact.Size})
	return artifact, nil
}

// StoreArtifactBytes persists bounded telemetry or generated output in the
// workspace artifact store and records the same provenance as file imports.
func (e *Engine) StoreArtifactBytes(workspaceID, name, artifactType string, data []byte, metadata map[string]any) (Artifact, error) {
	workspace, ok := e.Store.Workspace(workspaceID)
	if !ok {
		return Artifact{}, errors.New("workspace not found")
	}
	if len(data) > 64<<20 {
		return Artifact{}, errors.New("artifact exceeds 64 MiB limit")
	}
	if strings.TrimSpace(name) == "" {
		name = "capture.bin"
	}
	name = filepath.Base(name)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return Artifact{}, errors.New("artifact name is invalid")
	}
	root := workspace.Root
	if root == "" {
		root = e.defaultWorkspaceRoot(workspace.ID)
	}
	dir := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create artifact store: %w", err)
	}
	digest := sha256.Sum256(data)
	destination := filepath.Join(dir, hex.EncodeToString(digest[:])+"-"+name)
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return Artifact{}, fmt.Errorf("write artifact: %w", err)
	}
	if strings.TrimSpace(artifactType) == "" {
		artifactType = "output"
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata = cloneMap(metadata)
	metadata["workspace_id"] = workspaceID
	artifact := Artifact{ID: NewID("ART"), Name: name, Type: artifactType, Path: destination, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(data)), Metadata: metadata, CreatedAt: now()}
	if err := e.Store.AddArtifact(artifact); err != nil {
		return Artifact{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "artifact.created", WorkspaceID: workspaceID, Payload: map[string]any{"artifact_id": artifact.ID, "type": artifact.Type}, CreatedAt: now()})
	return artifact, nil
}
func (e *Engine) CreateTarget(workspaceID string, t Target) (Target, error) {
	if _, ok := e.Store.Workspace(workspaceID); !ok {
		return Target{}, errors.New("workspace not found")
	}
	t.ID = NewID("T")
	t.WorkspaceID = workspaceID
	t.CreatedAt = now()
	if err := e.Store.CreateTarget(t); err != nil {
		return Target{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "target.created", WorkspaceID: workspaceID, Payload: map[string]any{"target_id": t.ID}, CreatedAt: now()})
	_ = e.audit("target.created", "user", "target", t.ID, map[string]any{"workspace_id": workspaceID})
	return t, nil
}

func (e *Engine) SubmitResearch(ctx context.Context, workspaceID, targetID string) (Task, error) {
	if _, ok := e.Store.Workspace(workspaceID); !ok {
		return Task{}, errors.New("workspace not found")
	}
	target, ok := e.Store.Target(targetID)
	if !ok || target.WorkspaceID != workspaceID {
		return Task{}, errors.New("target not found in workspace")
	}
	if !target.Authorized {
		return Task{}, errors.New("target is not marked as authorized")
	}
	t := Task{ID: NewID("TASK"), WorkspaceID: workspaceID, TargetID: targetID, Type: "recon.fingerprint", Objective: "Collect a passive target fingerprint and create a candidate finding", Priority: 80, Status: TaskQueued, AssignedAgent: "recon-default", RequiredCapabilities: []string{"target.fingerprint"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 120, MaxToolCalls: 5}, CreatedAt: now(), UpdatedAt: now()}
	if err := e.Store.CreateTask(t); err != nil {
		return Task{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task.created", WorkspaceID: workspaceID, TaskID: t.ID, Payload: map[string]any{"target_id": targetID}, CreatedAt: now()})
	e.startTask(context.Background(), t, target)
	return t, nil
}

func (e *Engine) SubmitCapabilityTask(ctx context.Context, workspaceID, targetID, capabilityID, objective string, permissions PermissionSet, budget Budget) (Task, error) {
	return e.SubmitCapabilityTaskWithInputs(ctx, workspaceID, targetID, capabilityID, objective, permissions, budget, nil)
}

func (e *Engine) SubmitCapabilityTaskWithInputs(ctx context.Context, workspaceID, targetID, capabilityID, objective string, permissions PermissionSet, budget Budget, inputs map[string]any) (Task, error) {
	return e.submitTaskWithCapabilities(ctx, workspaceID, targetID, []string{capabilityID}, objective, permissions, budget, inputs)
}

// SubmitCapabilityPlan creates one traceable task for an ordered capability
// plan. Each capability becomes a node in the same task, so dependencies,
// evidence, approvals, retry and final summary remain visible as one unit.
func (e *Engine) SubmitCapabilityPlan(ctx context.Context, workspaceID, targetID string, capabilityIDs []string, objective string, permissions PermissionSet, budget Budget, inputs map[string]any) (Task, error) {
	return e.submitTaskWithCapabilities(ctx, workspaceID, targetID, capabilityIDs, objective, permissions, budget, inputs)
}

func (e *Engine) submitTaskWithCapabilities(ctx context.Context, workspaceID, targetID string, capabilityIDs []string, objective string, permissions PermissionSet, budget Budget, inputs map[string]any) (Task, error) {
	if len(capabilityIDs) == 0 {
		return Task{}, errors.New("at least one capability is required")
	}
	if strings.TrimSpace(permissions.Filesystem) == "" {
		permissions.Filesystem = "workspace-readonly"
	}
	e.mu.RLock()
	for _, capabilityID := range capabilityIDs {
		if _, ok := e.capabilities[capabilityID]; !ok {
			e.mu.RUnlock()
			return Task{}, fmt.Errorf("capability %s not found", capabilityID)
		}
	}
	e.mu.RUnlock()
	workspace, ok := e.Store.Workspace(workspaceID)
	if !ok {
		return Task{}, errors.New("workspace not found")
	}
	target, ok := e.Store.Target(targetID)
	if !ok || target.WorkspaceID != workspaceID {
		return Task{}, errors.New("target not found in workspace")
	}
	if !target.Authorized && (permissions.Network || permissions.Device || permissions.Destructive) {
		return Task{}, errors.New("target is not marked as authorized")
	}
	if objective == "" {
		objective = "Run capability plan"
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	inputs = cloneMap(inputs)
	if err := e.resolveArtifactInputs(workspaceID, inputs); err != nil {
		return Task{}, err
	}
	if workspace.Root != "" {
		inputs["workspace_root"] = workspace.Root
	}
	assignedAgent := "analysis-default"
	e.mu.RLock()
	if cap, ok := e.capabilities[capabilityIDs[0]]; ok {
		switch cap.Category {
		case "recon":
			assignedAgent = "recon-default"
		case "validation", "peripheral":
			assignedAgent = "validation-default"
		}
	}
	e.mu.RUnlock()
	task := Task{ID: NewID("TASK"), WorkspaceID: workspaceID, TargetID: targetID, Type: "capability.plan", Objective: objective, Priority: 60, Status: TaskQueued, AssignedAgent: assignedAgent, RequiredCapabilities: append([]string(nil), capabilityIDs...), Context: inputs, Permissions: permissions, Budget: budget, CreatedAt: now(), UpdatedAt: now()}
	if task.Budget.MaxRuntimeSeconds == 0 {
		task.Budget.MaxRuntimeSeconds = 300
	}
	if task.Budget.MaxToolCalls == 0 {
		task.Budget.MaxToolCalls = 10
	}
	if err := e.Store.CreateTask(task); err != nil {
		return Task{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task.created", WorkspaceID: workspaceID, TaskID: task.ID, Payload: map[string]any{"capabilities": capabilityIDs}, CreatedAt: now()})
	e.setTaskNode(task.ID, "commander", "planning", "completed", "Task plan accepted", map[string]any{"capabilities": capabilityIDs})
	e.startTask(context.Background(), task, target)
	return task, nil
}

// resolveArtifactInputs turns stable artifact references from the UI/API into
// the workspace path expected by file-backed capabilities. Callers may still
// provide path directly for integrations, but an artifact_id is preferred so
// the task remains portable and auditable.
func (e *Engine) resolveArtifactInputs(workspaceID string, inputs map[string]any) error {
	if inputs == nil {
		return nil
	}
	state := e.Store.Snapshot()
	findArtifact := func(id string) (Artifact, bool) {
		for _, artifact := range state.Artifacts {
			if artifact.ID != id {
				continue
			}
			if owner, ok := artifact.Metadata["workspace_id"].(string); ok && owner != "" && owner != workspaceID {
				return Artifact{}, false
			}
			return artifact, true
		}
		return Artifact{}, false
	}
	artifactID := stringInput(inputs, "artifact_id", "")
	if artifactID == "" {
		if value, ok := inputs["artifact"].(map[string]any); ok {
			artifactID = stringInput(value, "artifact_id", "")
		}
	}
	if artifactID == "" {
		if values, ok := inputs["artifact_ids"].([]any); ok && len(values) > 0 {
			artifactID, _ = values[0].(string)
		}
	}
	pathValue := stringInput(inputs, "path", "")
	if pathValue == "" {
		pathValue = stringInput(inputs, "file_path", "")
	}
	if artifactID == "" && pathValue != "" {
		if _, ok := findArtifact(pathValue); ok {
			artifactID = pathValue
		}
	}
	if artifactID == "" {
		return nil
	}
	artifact, ok := findArtifact(artifactID)
	if !ok || strings.TrimSpace(artifact.Path) == "" {
		return fmt.Errorf("artifact %s not found in workspace", artifactID)
	}
	// An explicit artifact reference is authoritative. This prevents a task
	// from claiming one artifact while reading a different path.
	inputs["path"] = artifact.Path
	delete(inputs, "file_path")
	inputs["artifact_id"] = artifact.ID
	return nil
}

// startTask owns the cancellable execution context for a task. Every entry
// point (HTTP, gRPC, approval recovery, and the desktop client) uses this
// function so cancellation cannot leave a detached worker running.
func (e *Engine) startTask(parent context.Context, task Task, target Target) {
	ctx, cancel := context.WithCancel(parent)
	e.runMu.Lock()
	if previous, exists := e.taskRuns[task.ID]; exists {
		previous()
	}
	e.runGenerations[task.ID]++
	generation := e.runGenerations[task.ID]
	e.taskRuns[task.ID] = cancel
	e.runMu.Unlock()
	go func() {
		defer func() {
			e.runMu.Lock()
			if _, ok := e.taskRuns[task.ID]; ok && e.runGenerations[task.ID] == generation {
				delete(e.taskRuns, task.ID)
			}
			e.runMu.Unlock()
		}()
		if len(task.RequiredCapabilities) == 0 || (len(task.RequiredCapabilities) == 1 && task.RequiredCapabilities[0] == "target.fingerprint") {
			e.runTask(ctx, task, target)
			return
		}
		e.runCapabilityTask(ctx, task, target)
	}()
}

func (e *Engine) stopTask(taskID string) {
	e.runMu.Lock()
	cancel := e.taskRuns[taskID]
	e.runMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) taskStopped(taskID string) bool {
	task, ok := e.Store.Task(taskID)
	return ok && (task.Status == TaskPaused || task.Status == TaskCancelled)
}

// acquireAgentSlot enforces the Agent's declared MaxConcurrency across the
// complete task execution, including runtime, capability and result storage.
// Waiting tasks remain cancellable through their context.
func (e *Engine) acquireAgentSlot(ctx context.Context, agentID string) (func(), error) {
	if strings.TrimSpace(agentID) == "" {
		return func() {}, nil
	}
	max := 1
	for _, agent := range e.Store.Snapshot().Agents {
		if agent.ID == agentID {
			if agent.MaxConcurrency > 0 {
				max = agent.MaxConcurrency
			}
			break
		}
	}
	e.agentMu.Lock()
	slots := e.agentSlots[agentID]
	if slots == nil {
		slots = make(chan struct{}, max)
		e.agentSlots[agentID] = slots
	}
	e.agentMu.Unlock()
	select {
	case slots <- struct{}{}:
		e.agentMu.Lock()
		e.agentActive[agentID]++
		e.agentMu.Unlock()
		_ = e.Store.mutate(func(state *State) error {
			for i := range state.Agents {
				if state.Agents[i].ID == agentID {
					state.Agents[i].Status = "running"
				}
			}
			return nil
		})
		var once sync.Once
		return func() {
			once.Do(func() {
				<-slots
				e.agentMu.Lock()
				e.agentActive[agentID]--
				active := e.agentActive[agentID]
				e.agentMu.Unlock()
				if active == 0 {
					_ = e.Store.mutate(func(state *State) error {
						for i := range state.Agents {
							if state.Agents[i].ID == agentID {
								state.Agents[i].Status = "idle"
							}
						}
						return nil
					})
				}
			})
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ControlTask changes task state and starts or stops execution as needed.
// Retry always uses the task's declared capability, never a hard-coded flow.
func (e *Engine) ControlTask(taskID, action, actor string) (Task, error) {
	task, ok := e.Store.Task(taskID)
	if !ok {
		return Task{}, errors.New("task not found")
	}
	var next TaskStatus
	switch action {
	case "pause":
		next = TaskPaused
	case "resume":
		if task.Status != TaskPaused {
			return Task{}, fmt.Errorf("task is not paused")
		}
		next = TaskQueued
	case "retry":
		next = TaskQueued
	case "cancel":
		next = TaskCancelled
	default:
		return Task{}, fmt.Errorf("unknown task action %q", action)
	}
	if !CanTransitionTask(task.Status, next) {
		return Task{}, fmt.Errorf("invalid task transition %s -> %s", task.Status, next)
	}
	if action == "pause" || action == "cancel" || action == "retry" {
		e.stopTask(taskID)
	}
	if err := e.Store.UpdateTask(taskID, func(value *Task) error {
		value.Status = next
		if action == "retry" {
			value.RetryCount++
			e.resetToolBudget(taskID)
		}
		if next == TaskQueued {
			value.Error = ""
			value.CompletedAt = nil
		}
		if next == TaskCancelled || next == TaskPaused {
			value.CompletedAt = timePtr(now())
		}
		return nil
	}); err != nil {
		return Task{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task." + action, WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"from": task.Status, "to": next, "actor": actor}, CreatedAt: now()})
	_ = e.audit("task."+action, actor, "task", task.ID, map[string]any{"from": task.Status, "to": next})
	if next == TaskQueued {
		fresh, _ := e.Store.Task(taskID)
		if target, targetOK := e.Store.Target(fresh.TargetID); targetOK {
			e.startTask(context.Background(), fresh, target)
		}
	}
	updated, _ := e.Store.Task(taskID)
	return updated, nil
}

func (e *Engine) setTaskNode(taskID, name, kind, status, summary string, output map[string]any) {
	started := now()
	completed := started
	_ = e.Store.UpdateTask(taskID, func(task *Task) error {
		for i := range task.Nodes {
			if task.Nodes[i].Name != name || task.Nodes[i].Kind != kind {
				continue
			}
			task.Nodes[i].Status, task.Nodes[i].Summary, task.Nodes[i].Output = status, summary, output
			if status == "running" {
				task.Nodes[i].CompletedAt = nil
			}
			if status == "completed" || status == "failed" || status == "blocked" {
				task.Nodes[i].CompletedAt = &completed
			}
			return nil
		}
		var done *time.Time
		if status != "running" {
			done = &completed
		}
		task.Nodes = append(task.Nodes, TaskNode{ID: NewID("NODE"), Name: name, Kind: kind, Status: status, Summary: summary, Output: output, StartedAt: started, CompletedAt: done})
		return nil
	})
	if task, ok := e.Store.Task(taskID); ok {
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task.node." + status, WorkspaceID: task.WorkspaceID, TaskID: taskID, Payload: map[string]any{"node": name, "kind": kind, "summary": summary, "output": output}, CreatedAt: completed})
	}
}

func (e *Engine) appendTaskConversation(taskID, role, content string) {
	task, ok := e.Store.Task(taskID)
	if !ok || task.ConversationID == "" || strings.TrimSpace(content) == "" {
		return
	}
	_ = e.Store.UpdateConversation(task.ConversationID, func(c *Conversation) error {
		c.Messages = append(c.Messages, ConversationMessage{ID: NewID("MSG"), Role: role, Content: content, References: map[string][]string{"tasks": {task.ID}}, CreatedAt: now()})
		return nil
	})
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "conversation.message.created", WorkspaceID: task.WorkspaceID, ConversationID: task.ConversationID, TaskID: task.ID, Payload: map[string]any{"role": role, "content_bytes": len(content)}, CreatedAt: now()})
}

func (e *Engine) runBoundAgent(ctx context.Context, task Task) (string, string, error) {
	var agent Agent
	for _, candidate := range e.Store.Snapshot().Agents {
		if candidate.ID == task.AssignedAgent {
			agent = candidate
			break
		}
	}
	if agent.RuntimeID == "" {
		return "", "", nil
	}
	e.setTaskNode(task.ID, agent.ID, "agent", "running", "Running the configured local agent", map[string]any{"runtime_id": agent.RuntimeID})
	result := RunLocalRuntime(ctx, agent.RuntimeID, task.Objective)
	if result.Status != "completed" {
		e.setTaskNode(task.ID, agent.ID, "agent", "failed", result.Error, map[string]any{"runtime_id": agent.RuntimeID, "output": result.Output})
		return agent.RuntimeID, result.Output, errors.New(result.Error)
	}
	e.setTaskNode(task.ID, agent.ID, "agent", "completed", "Local agent returned a plan", map[string]any{"runtime_id": agent.RuntimeID, "output": result.Output})
	return agent.RuntimeID, result.Output, nil
}

func (e *Engine) runCapabilityTask(ctx context.Context, task Task, target Target) {
	if e.taskStopped(task.ID) {
		return
	}
	releaseAgent, err := e.acquireAgentSlot(ctx, task.AssignedAgent)
	if err != nil {
		return
	}
	defer releaseAgent()
	if fresh, ok := e.Store.Task(task.ID); ok {
		task = fresh
	}
	if task.Budget.MaxRuntimeSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(task.Budget.MaxRuntimeSeconds)*time.Second)
		defer cancel()
	}
	e.setTaskNode(task.ID, "scheduler", "scheduler", "running", "Preparing capability execution", nil)
	_ = e.transitionTask(task.ID, TaskAssigned, "commander")
	_ = e.transitionTask(task.ID, TaskRunning, "scheduler")
	runtimeID, agentOutput, agentErr := e.runBoundAgent(ctx, task)
	if agentErr != nil {
		if e.taskStopped(task.ID) {
			return
		}
		_ = e.Store.UpdateTask(task.ID, func(v *Task) error {
			v.Status = TaskFailed
			v.Error = agentErr.Error()
			v.Summary = "Agent execution failed"
			v.Output = map[string]any{"runtime_id": runtimeID, "output": agentOutput}
			v.CompletedAt = timePtr(now())
			return nil
		})
		e.setTaskNode(task.ID, "scheduler", "scheduler", "failed", "Scheduler stopped after agent failure", map[string]any{"error": agentErr.Error()})
		e.setTaskNode(task.ID, "summary", "summary", "failed", "Agent execution failed", map[string]any{"error": agentErr.Error()})
		e.appendTaskConversation(task.ID, "assistant", "Task failed while running the configured agent: "+agentErr.Error())
		_ = e.audit("task.failed", "agent-runtime", "task", task.ID, map[string]any{"error": agentErr.Error()})
		return
	}
	agentRun := AgentRun{ID: NewID("AGENTRUN"), AgentID: task.AssignedAgent, TaskID: task.ID, Status: "completed", Model: "configured-by-user", Input: map[string]any{"objective": task.Objective, "capabilities": task.RequiredCapabilities, "runtime_id": runtimeID}, Output: map[string]any{"runtime_output": agentOutput}, StartedAt: task.CreatedAt, CompletedAt: timePtr(now())}
	_ = e.Store.AddAgentRun(agentRun)
	outputs := make([]map[string]any, 0, len(task.RequiredCapabilities))
	var findingID string
	startIndex := 0
	for index, capabilityID := range task.RequiredCapabilities {
		if node, ok := taskCapabilityNode(task, capabilityID); ok && node.Status == "completed" {
			outputs = append(outputs, cloneMap(node.Output))
			startIndex = index + 1
			if task.FindingID != "" {
				findingID = task.FindingID
			}
			continue
		}
		break
	}
	for index := startIndex; index < len(task.RequiredCapabilities); index++ {
		capabilityID := task.RequiredCapabilities[index]
		if e.taskStopped(task.ID) {
			return
		}
		e.setTaskNode(task.ID, capabilityID, "capability", "running", fmt.Sprintf("Capability step %d/%d started", index+1, len(task.RequiredCapabilities)), nil)
		inputs := cloneMap(task.Context)
		inputs["vendor"], inputs["model"] = target.Vendor, target.Model
		inputs["address"], inputs["transport"] = target.Address, target.Transport
		if runtimeID != "" {
			inputs["agent_runtime"], inputs["agent_output"] = runtimeID, agentOutput
		}
		if len(outputs) > 0 {
			inputs["previous_results"] = outputs
		}
		req := CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: capabilityID, Objective: task.Objective, Inputs: inputs, Permissions: task.Permissions, Budget: task.Budget}
		result, err := e.Request(ctx, req, task)
		if e.taskStopped(task.ID) {
			return
		}
		if err != nil {
			e.failCapabilityTask(task.ID, capabilityID, err)
			return
		}
		if result.Status == "pending_approval" {
			_ = e.Store.UpdateTask(task.ID, func(v *Task) error {
				v.Status, v.Error, v.Summary = TaskBlocked, "waiting for human approval", "Waiting for human approval"
				return nil
			})
			e.setTaskNode(task.ID, capabilityID, "capability", "blocked", "Waiting for human approval", map[string]any{"reason": "human approval required"})
			e.setTaskNode(task.ID, "scheduler", "scheduler", "blocked", "Scheduler paused for approval", nil)
			e.appendTaskConversation(task.ID, "assistant", "Task is waiting for human approval before continuing.")
			return
		}
		if findingID == "" {
			finding := Finding{ID: NewID("F"), WorkspaceID: task.WorkspaceID, TargetID: target.ID, Title: "Capability result: " + capabilityID, State: FindingCandidate, Priority: "P2", Score: 40, Confidence: result.Confidence, Location: Location{Component: target.Model}, Validation: Validation{State: "not_started"}, CreatedAt: now(), UpdatedAt: now()}
			if err := e.Store.CreateFinding(finding); err != nil {
				e.failCapabilityTask(task.ID, capabilityID, err)
				return
			}
			findingID = finding.ID
			_ = e.Store.UpdateTask(task.ID, func(v *Task) error { v.FindingID = findingID; return nil })
		}
		artifactIDs := make([]string, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			if artifact.ID == "" {
				artifact.ID = NewID("ART")
			}
			if artifact.CreatedAt.IsZero() {
				artifact.CreatedAt = now()
			}
			artifact.ID = e.canonicalArtifactID(artifact)
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]any{}
			}
			artifact.Metadata["workspace_id"] = task.WorkspaceID
			if err := e.Store.AddArtifact(artifact); err != nil {
				e.failCapabilityTask(task.ID, capabilityID, err)
				return
			}
			artifactIDs = appendUniqueString(artifactIDs, artifact.ID)
			_ = e.Store.UpdateFinding(findingID, func(value *Finding) error {
				value.ArtifactIDs = appendUniqueString(value.ArtifactIDs, artifact.ID)
				return nil
			})
		}
		for _, evidence := range result.Evidence {
			evidence.FindingID = findingID
			if evidence.ID == "" {
				evidence.ID = NewID("E")
			}
			if evidence.CreatedAt.IsZero() {
				evidence.CreatedAt = now()
			}
			if len(artifactIDs) > 0 {
				for _, artifactID := range artifactIDs {
					evidence.ArtifactRefs = appendUniqueString(evidence.ArtifactRefs, artifactID)
				}
			}
			if err := e.Store.AddEvidence(evidence); err != nil {
				e.failCapabilityTask(task.ID, capabilityID, err)
				return
			}
		}
		e.applyCapabilityFindingResult(findingID, capabilityID, result)
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "evidence.added", WorkspaceID: task.WorkspaceID, TaskID: task.ID, FindingID: findingID, Payload: map[string]any{"capability_id": capabilityID}, CreatedAt: now()})
		stepOutput := map[string]any{"capability_id": capabilityID, "summary": result.Summary, "confidence": result.Confidence, "evidence_count": len(result.Evidence), "artifact_count": len(result.Artifacts), "metrics": result.Metrics}
		outputs = append(outputs, stepOutput)
		e.setTaskNode(task.ID, capabilityID, "capability", "completed", result.Summary, stepOutput)
	}
	e.setTaskNode(task.ID, "scheduler", "scheduler", "completed", "Capability plan finished", map[string]any{"steps": len(outputs)})
	_ = e.Store.UpdateTask(task.ID, func(v *Task) error {
		v.Summary = fmt.Sprintf("Completed %d capability steps", len(outputs))
		v.Output = map[string]any{"finding_id": findingID, "steps": outputs}
		return nil
	})
	e.setTaskNode(task.ID, "summary", "summary", "completed", "Task completed", map[string]any{"finding_id": findingID, "steps": outputs})
	e.appendTaskConversation(task.ID, "assistant", fmt.Sprintf("Task completed: %d capability steps finished.", len(outputs)))
	_ = e.transitionTask(task.ID, TaskCompleted, "scheduler")
	_ = e.audit("task.completed", "scheduler", "task", task.ID, map[string]any{"finding_id": findingID, "steps": len(outputs)})
}

func taskCapabilityNode(task Task, capabilityID string) (TaskNode, bool) {
	for _, node := range task.Nodes {
		if node.Kind == "capability" && node.Name == capabilityID {
			return node, true
		}
	}
	return TaskNode{}, false
}

// applyCapabilityFindingResult turns structured capability output into the
// Finding state machine. The transitions are deliberately conservative: a
// result can enrich a finding, but only an explicit successful validation can
// mark it validated or reportable.
func (e *Engine) applyCapabilityFindingResult(findingID, capabilityID string, result CapabilityResult) {
	if strings.TrimSpace(findingID) == "" {
		return
	}
	var content map[string]any
	if len(result.Evidence) > 0 {
		content = result.Evidence[0].Content
	}
	finding, ok := e.Store.Finding(findingID)
	if !ok {
		return
	}
	switch capabilityID {
	case "taint.trace", "taint.storage_trace":
		traceable, _ := content["traceable"].(bool)
		if !traceable {
			return
		}
		_ = e.Store.UpdateFinding(findingID, func(value *Finding) error {
			if source, ok := content["source"].(string); ok && source != "" {
				value.Source = appendUniqueString(value.Source, source)
			}
			if sink, ok := content["sink"].(string); ok && sink != "" {
				value.Sink = appendUniqueString(value.Sink, sink)
			}
			if path, ok := content["path"].([]any); ok {
				for _, item := range path {
					if itemValue, ok := item.(string); ok {
						value.CallChain = appendUniqueString(value.CallChain, itemValue)
					}
				}
			}
			return nil
		})
		if finding.State == FindingCandidate {
			_ = e.TransitionFinding(findingID, FindingAnalyzing, "analysis-agent")
			finding, _ = e.Store.Finding(findingID)
		}
		if finding.State == FindingAnalyzing {
			_ = e.TransitionFinding(findingID, FindingReadyForValidation, "analysis-agent")
		}
	case "cvss.score":
		if score, ok := numericValue(content["score"]); ok {
			_ = e.Store.UpdateFinding(findingID, func(value *Finding) error {
				value.CVSS = score
				value.Score = score * 10
				return nil
			})
		}
	case "poc.verify":
		passed, _ := content["passed"].(bool)
		if !passed {
			return
		}
		if finding.State == FindingCandidate {
			_ = e.TransitionFinding(findingID, FindingAnalyzing, "validation-agent")
			finding, _ = e.Store.Finding(findingID)
		}
		if finding.State == FindingAnalyzing {
			_ = e.TransitionFinding(findingID, FindingReadyForValidation, "validation-agent")
			finding, _ = e.Store.Finding(findingID)
		}
		if finding.State == FindingReadyForValidation {
			_ = e.TransitionFinding(findingID, FindingValidating, "validation-agent")
			finding, _ = e.Store.Finding(findingID)
		}
		if finding.State == FindingValidating {
			_ = e.Store.UpdateFinding(findingID, func(value *Finding) error {
				value.Validation = Validation{State: "validated", Method: stringInput(content, "method", "poc"), Reproducible: true, Result: "passed"}
				return nil
			})
			_ = e.TransitionFinding(findingID, FindingValidated, "validation-agent")
		}
	case "report.generate":
		if finding.State == FindingValidated {
			_ = e.TransitionFinding(findingID, FindingReportable, "reporter")
			finding, _ = e.Store.Finding(findingID)
		}
		if finding.State == FindingReportable {
			_ = e.TransitionFinding(findingID, FindingReported, "reporter")
		}
	}
}

func (e *Engine) failCapabilityTask(taskID, capabilityID string, err error) {
	if err == nil {
		err = errors.New("capability failed")
	}
	_ = e.Store.UpdateTask(taskID, func(v *Task) error {
		v.Status, v.Error, v.Summary = TaskFailed, err.Error(), "Task execution failed"
		v.Output = map[string]any{"error": err.Error(), "capability_id": capabilityID}
		v.CompletedAt = timePtr(now())
		return nil
	})
	e.setTaskNode(taskID, capabilityID, "capability", "failed", err.Error(), map[string]any{"error": err.Error()})
	e.setTaskNode(taskID, "scheduler", "scheduler", "failed", "Scheduler stopped after capability failure", map[string]any{"error": err.Error()})
	e.setTaskNode(taskID, "summary", "summary", "failed", "Task execution failed", map[string]any{"error": err.Error()})
	e.appendTaskConversation(taskID, "assistant", "Task failed: "+err.Error())
}

func (e *Engine) runTask(ctx context.Context, task Task, target Target) {
	if e.taskStopped(task.ID) {
		return
	}
	releaseAgent, err := e.acquireAgentSlot(ctx, task.AssignedAgent)
	if err != nil {
		return
	}
	defer releaseAgent()
	e.setTaskNode(task.ID, "scheduler", "scheduler", "running", "Preparing passive fingerprint", nil)
	_ = e.transitionTask(task.ID, TaskAssigned, "scheduler")
	_ = e.transitionTask(task.ID, TaskRunning, "scheduler")
	runtimeID, agentOutput, agentErr := e.runBoundAgent(ctx, task)
	if agentErr != nil {
		if e.taskStopped(task.ID) {
			return
		}
		_ = e.Store.UpdateTask(task.ID, func(v *Task) error {
			v.Status = TaskFailed
			v.Error = agentErr.Error()
			v.Summary = "Agent execution failed"
			v.Output = map[string]any{"runtime_id": runtimeID, "output": agentOutput}
			v.CompletedAt = timePtr(now())
			return nil
		})
		e.setTaskNode(task.ID, "scheduler", "scheduler", "failed", "Scheduler stopped after agent failure", map[string]any{"error": agentErr.Error()})
		e.setTaskNode(task.ID, "summary", "summary", "failed", "Agent execution failed", map[string]any{"error": agentErr.Error()})
		e.appendTaskConversation(task.ID, "assistant", "Task failed while running the configured agent: "+agentErr.Error())
		_ = e.audit("task.failed", "agent-runtime", "task", task.ID, map[string]any{"error": agentErr.Error()})
		return
	}
	e.setTaskNode(task.ID, "target.fingerprint", "capability", "running", "Collecting declared target metadata", nil)
	if task.Budget.MaxRuntimeSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(task.Budget.MaxRuntimeSeconds)*time.Second)
		defer cancel()
	}
	inputs := map[string]any{"vendor": target.Vendor, "model": target.Model, "address": target.Address, "transport": target.Transport}
	if runtimeID != "" {
		inputs["agent_runtime"] = runtimeID
		inputs["agent_output"] = agentOutput
	}
	req := CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "target.fingerprint", Objective: task.Objective, Inputs: inputs, Permissions: task.Permissions, Budget: task.Budget}
	result, err := e.Request(ctx, req, task)
	if e.taskStopped(task.ID) {
		return
	}
	agentRun := AgentRun{ID: NewID("AGENTRUN"), AgentID: task.AssignedAgent, TaskID: task.ID, Status: "completed", Model: "configured-by-user", Input: map[string]any{"objective": task.Objective, "capabilities": task.RequiredCapabilities, "runtime_id": runtimeID}, Output: map[string]any{"runtime_output": agentOutput}, StartedAt: task.CreatedAt, CompletedAt: timePtr(now())}
	if err != nil {
		if e.taskStopped(task.ID) {
			return
		}
		agentRun.Status = "failed"
		agentRun.Output = map[string]any{"error": err.Error()}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error {
			t.Status = TaskFailed
			t.Error = err.Error()
			t.Summary = "Task execution failed"
			t.Output = map[string]any{"error": err.Error()}
			t.CompletedAt = timePtr(now())
			return nil
		})
		_ = e.audit("task.failed", "scheduler", "task", task.ID, map[string]any{"error": err.Error()})
		e.setTaskNode(task.ID, "target.fingerprint", "capability", "failed", err.Error(), map[string]any{"error": err.Error()})
		e.setTaskNode(task.ID, "scheduler", "scheduler", "failed", "Scheduler stopped after capability failure", map[string]any{"error": err.Error()})
		e.setTaskNode(task.ID, "summary", "summary", "failed", "Task execution failed", map[string]any{"error": err.Error()})
		e.appendTaskConversation(task.ID, "assistant", "Task failed: "+err.Error())
		return
	}
	if result.Status == "pending_approval" {
		agentRun.Status = "blocked"
		agentRun.Output = map[string]any{"reason": "human approval required"}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error {
			t.Status = TaskBlocked
			t.Error = "waiting for human approval"
			t.Summary = "Waiting for human approval"
			return nil
		})
		e.setTaskNode(task.ID, "target.fingerprint", "capability", "blocked", "Waiting for human approval", map[string]any{"reason": "human approval required"})
		e.setTaskNode(task.ID, "scheduler", "scheduler", "blocked", "Scheduler paused for approval", nil)
		e.appendTaskConversation(task.ID, "assistant", "Task is waiting for human approval before continuing.")
		return
	}
	agentRun.Output["summary"] = result.Summary
	agentRun.Output["confidence"] = result.Confidence
	_ = e.Store.AddAgentRun(agentRun)
	finding := Finding{ID: NewID("F"), WorkspaceID: task.WorkspaceID, TargetID: target.ID, Title: fmt.Sprintf("Potential attack surface on %s", displayTarget(target)), State: FindingHypothesis, Priority: "P2", Score: 46, Confidence: result.Confidence, AttackSurface: AttackSurface{Type: "device-management", Protocol: target.Transport, Entrypoint: target.Address}, Location: Location{Component: target.Model}, Validation: Validation{State: "not_started"}, CreatedAt: now(), UpdatedAt: now()}
	if len(result.Evidence) > 0 {
		finding.State = FindingCandidate
	}
	if err := e.Store.CreateFinding(finding); err != nil {
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error {
			t.Status = TaskFailed
			t.Error = err.Error()
			t.CompletedAt = timePtr(now())
			return nil
		})
		return
	}
	for _, evidence := range result.Evidence {
		evidence.FindingID = finding.ID
		if evidence.ID == "" {
			evidence.ID = NewID("E")
		}
		if evidence.CreatedAt.IsZero() {
			evidence.CreatedAt = now()
		}
		_ = e.Store.AddEvidence(evidence)
	}
	for _, artifact := range result.Artifacts {
		if artifact.ID == "" {
			artifact.ID = NewID("ART")
		}
		if artifact.CreatedAt.IsZero() {
			artifact.CreatedAt = now()
		}
		artifact.ID = e.canonicalArtifactID(artifact)
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]any{}
		}
		artifact.Metadata["workspace_id"] = task.WorkspaceID
		_ = e.Store.AddArtifact(artifact)
		_ = e.Store.UpdateFinding(finding.ID, func(value *Finding) error {
			value.ArtifactIDs = appendUniqueString(value.ArtifactIDs, artifact.ID)
			return nil
		})
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "finding.created", WorkspaceID: task.WorkspaceID, TaskID: task.ID, FindingID: finding.ID, Payload: map[string]any{"state": finding.State}, CreatedAt: now()})
	e.setTaskNode(task.ID, "target.fingerprint", "capability", "completed", result.Summary, map[string]any{"confidence": result.Confidence, "evidence_count": len(result.Evidence), "metrics": result.Metrics})
	e.setTaskNode(task.ID, "scheduler", "scheduler", "completed", "Capability execution finished", nil)
	_ = e.Store.UpdateTask(task.ID, func(v *Task) error {
		v.Summary = result.Summary
		v.Output = map[string]any{"confidence": result.Confidence, "evidence_count": len(result.Evidence), "artifact_count": len(result.Artifacts), "metrics": result.Metrics}
		return nil
	})
	e.setTaskNode(task.ID, "summary", "summary", "completed", "Task completed", map[string]any{"finding_id": finding.ID, "summary": result.Summary})
	e.appendTaskConversation(task.ID, "assistant", "Task completed: "+result.Summary)
	_ = e.transitionTask(task.ID, TaskCompleted, "scheduler")
	_ = e.audit("task.completed", "scheduler", "task", task.ID, map[string]any{"finding_id": finding.ID})
}

func (e *Engine) canonicalArtifactID(artifact Artifact) string {
	if artifact.ID != "" {
		for _, existing := range e.Store.Snapshot().Artifacts {
			if existing.ID == artifact.ID {
				return existing.ID
			}
			if artifact.SHA256 != "" && existing.SHA256 == artifact.SHA256 && existing.Path == artifact.Path {
				return existing.ID
			}
		}
	}
	return artifact.ID
}

func displayTarget(t Target) string {
	if t.Name != "" {
		return t.Name
	}
	if t.Model != "" {
		return t.Model
	}
	return t.ID
}
func (e *Engine) transitionTask(id string, to TaskStatus, actor string) error {
	task, ok := e.Store.Task(id)
	if !ok {
		return errors.New("task not found")
	}
	if err := e.Store.UpdateTask(id, func(t *Task) error {
		if !CanTransitionTask(t.Status, to) {
			return fmt.Errorf("invalid task transition %s -> %s", t.Status, to)
		}
		t.Status = to
		if to == TaskRunning && t.StartedAt == nil {
			t.StartedAt = timePtr(now())
		}
		if isTerminalTask(to) {
			t.CompletedAt = timePtr(now())
		}
		return nil
	}); err != nil {
		return err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task.status_changed", WorkspaceID: task.WorkspaceID, TaskID: id, Payload: map[string]any{"from": task.Status, "to": to, "actor": actor}, CreatedAt: now()})
	_ = e.audit("task.status_changed", actor, "task", id, map[string]any{"from": task.Status, "to": to})
	if isTerminalTask(to) {
		e.resetToolBudget(id)
	}
	return nil
}
func (e *Engine) TransitionFinding(id string, to FindingState, actor string) error {
	f, ok := e.Store.Finding(id)
	if !ok {
		return errors.New("finding not found")
	}
	if !CanTransitionFinding(f.State, to) {
		return fmt.Errorf("invalid finding transition %s -> %s", f.State, to)
	}
	if err := e.Store.UpdateFinding(id, func(v *Finding) error { v.State = to; return nil }); err != nil {
		return err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "finding.updated", WorkspaceID: f.WorkspaceID, FindingID: id, Payload: map[string]any{"state": to, "actor": actor}, CreatedAt: now()})
	return e.audit("finding.transition", actor, "finding", id, map[string]any{"from": f.State, "to": to})
}

func (e *Engine) advanceFindingToValidated(id string) error {
	for {
		finding, ok := e.Store.Finding(id)
		if !ok {
			return errors.New("finding not found")
		}
		switch finding.State {
		case FindingValidated, FindingReportable, FindingReported, FindingKnowledgeCaptured:
			return nil
		case FindingHypothesis:
			if err := e.TransitionFinding(id, FindingCandidate, "validation"); err != nil {
				return err
			}
		case FindingCandidate:
			if err := e.TransitionFinding(id, FindingAnalyzing, "validation"); err != nil {
				return err
			}
		case FindingAnalyzing:
			if err := e.TransitionFinding(id, FindingReadyForValidation, "validation"); err != nil {
				return err
			}
		case FindingReadyForValidation:
			if err := e.TransitionFinding(id, FindingValidating, "validation"); err != nil {
				return err
			}
		case FindingValidating:
			if err := e.TransitionFinding(id, FindingValidated, "validation"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("cannot advance finding state %s to validated", finding.State)
		}
	}
}

func (e *Engine) EvaluateGate(id, gate string) (GateDecision, error) {
	finding, ok := e.Store.Finding(id)
	if !ok {
		return GateDecision{}, errors.New("finding not found")
	}
	decision := GateDecision{ID: NewID("GATE"), FindingID: id, Gate: gate, Decision: "HOLD", CreatedAt: now()}
	switch gate {
	case "finding":
		if len(finding.EvidenceIDs) == 0 {
			decision.Reasons = append(decision.Reasons, "no evidence is attached")
		}
		if finding.AttackSurface.Type == "" && finding.Location.Component == "" {
			decision.Reasons = append(decision.Reasons, "no attack surface or component is defined")
		}
		if finding.Confidence < 0.5 {
			decision.Reasons = append(decision.Reasons, "confidence is below 0.50")
		}
		if len(decision.Reasons) == 0 {
			decision.Decision = "PASS"
		} else if len(finding.EvidenceIDs) == 0 {
			decision.Decision = "DROP"
		}
	case "validation":
		if finding.State != FindingValidated && finding.State != FindingReportable && finding.State != FindingReported {
			decision.Reasons = append(decision.Reasons, "finding is not validated")
		}
		if !finding.Validation.Reproducible {
			decision.Reasons = append(decision.Reasons, "reproduction has not been confirmed")
		}
		if len(finding.CWE) == 0 {
			decision.Reasons = append(decision.Reasons, "CWE is not assigned")
		}
		if len(decision.Reasons) == 0 {
			decision.Decision = "REPORTABLE"
		}
	default:
		return GateDecision{}, fmt.Errorf("unknown gate %q", gate)
	}
	if err := e.Store.AddGate(decision); err != nil {
		return GateDecision{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "gate." + strings.ToLower(decision.Decision), WorkspaceID: finding.WorkspaceID, FindingID: id, Payload: map[string]any{"gate": gate, "reasons": decision.Reasons}, CreatedAt: now()})
	_ = e.audit("gate."+strings.ToLower(decision.Decision), "commander", "finding", id, map[string]any{"gate": gate, "reasons": decision.Reasons})
	return decision, nil
}
func (e *Engine) DecideApproval(id, status, actor string) error {
	if status != "approved" && status != "rejected" {
		return errors.New("status must be approved or rejected")
	}
	t := now()
	err := e.Store.UpdateApproval(id, func(a *Approval) error {
		if a.Status != "pending" {
			return errors.New("approval already decided")
		}
		a.Status = status
		a.DecidedAt = &t
		a.DecidedBy = actor
		return nil
	})
	if err != nil {
		return err
	}
	_ = e.audit("approval."+status, actor, "approval", id, nil)
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "approval." + status, TaskID: approvalTaskID(e.Store.Snapshot().Approvals, id), Payload: map[string]any{"approval_id": id, "actor": actor}, CreatedAt: now()})
	if status == "rejected" {
		approval, ok := findApproval(e.Store.Snapshot().Approvals, id)
		if ok {
			_ = e.Store.UpdateTask(approval.TaskID, func(task *Task) error {
				if task.Status == TaskBlocked {
					task.Status = TaskFailed
					task.Error = "human approval rejected"
					task.Summary = "Execution rejected by human approval"
					task.CompletedAt = timePtr(now())
				}
				return nil
			})
			e.setTaskNode(approval.TaskID, "summary", "summary", "failed", "Execution rejected by human approval", map[string]any{"approval_id": id})
		}
		return nil
	}
	if status == "approved" {
		st := e.Store.Snapshot()
		for _, approval := range st.Approvals {
			if approval.ID != id {
				continue
			}
			task, ok := e.Store.Task(approval.TaskID)
			if !ok {
				return nil
			}
			if task.Status != TaskBlocked {
				return nil
			}
			target, ok := e.Store.Target(task.TargetID)
			if !ok {
				return nil
			}
			if err := e.Store.UpdateTask(task.ID, func(v *Task) error { v.Status = TaskQueued; v.Error = ""; v.CompletedAt = nil; return nil }); err != nil {
				return err
			}
			fresh, _ := e.Store.Task(task.ID)
			e.startTask(context.Background(), fresh, target)
			return nil
		}
	}
	return nil
}

func findApproval(items []Approval, id string) (Approval, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Approval{}, false
}

func approvalTaskID(items []Approval, id string) string {
	item, ok := findApproval(items, id)
	if !ok {
		return ""
	}
	return item.TaskID
}
func (e *Engine) audit(action, actor, resourceType, resourceID string, details map[string]any) error {
	return e.Store.AddAudit(AuditLog{ID: NewID("AUD"), Action: action, Actor: actor, ResourceType: resourceType, ResourceID: resourceID, Details: details, CreatedAt: now()})
}

func (e *Engine) GenerateReport(workspaceID string) (string, error) {
	w, ok := e.Store.Workspace(workspaceID)
	if !ok {
		return "", errors.New("workspace not found")
	}
	st := e.Store.Snapshot()
	root := w.Root
	if root == "" {
		root = e.defaultWorkspaceRoot(workspaceID)
	}
	reportDir := filepath.Join(root, "sitrep")
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(reportDir, "workspace.md")
	var b strings.Builder
	b.WriteString("# IoTHunter SITREP\n\n")
	b.WriteString(fmt.Sprintf("- Workspace: %s (`%s`)\n- Owner: %s\n- Generated: %s\n\n", w.Name, w.ID, w.Owner, now().Format(time.RFC3339)))
	b.WriteString("## Targets\n\n")
	for _, target := range st.Targets {
		if target.WorkspaceID == workspaceID {
			b.WriteString(fmt.Sprintf("- %s (`%s`) — %s %s, `%s`, authorized=%t\n", target.Name, target.ID, target.Vendor, target.Model, target.Transport, target.Authorized))
		}
	}
	b.WriteString("\n## Tasks\n\n")
	for _, task := range st.Tasks {
		if task.WorkspaceID == workspaceID {
			b.WriteString(fmt.Sprintf("- `%s` — %s — %s\n", task.ID, task.Status, task.Objective))
		}
	}
	b.WriteString("\n")
	b.WriteString("## Findings\n\n")
	count := 0
	for _, f := range st.Findings {
		if f.WorkspaceID != workspaceID {
			continue
		}
		count++
		b.WriteString(fmt.Sprintf("### %s\n\n- Finding ID: `%s`\n- State: `%s`\n- Priority: `%s`\n- Confidence: %.2f\n- Score: %.1f\n- Attack surface: `%s` / `%s`\n- Evidence: %d\n- Artifacts: %d\n- Validation: `%s` (reproducible=%t)\n\n", f.Title, f.ID, f.State, f.Priority, f.Confidence, f.Score, f.AttackSurface.Protocol, f.AttackSurface.Entrypoint, len(uniqueStrings(f.EvidenceIDs)), len(uniqueStrings(f.ArtifactIDs)), f.Validation.State, f.Validation.Reproducible))
	}
	if count == 0 {
		b.WriteString("No findings yet. Start a research run to populate this report.\n")
	}
	b.WriteString("\n## Evidence ledger\n\n")
	evidenceCount := 0
	for _, evidence := range st.Evidence {
		finding, ok := e.Store.Finding(evidence.FindingID)
		if !ok || finding.WorkspaceID != workspaceID {
			continue
		}
		evidenceCount++
		b.WriteString(fmt.Sprintf("- `%s` — %s — confidence %.2f — finding `%s`\n", evidence.ID, evidence.Type, evidence.Confidence, evidence.FindingID))
	}
	if evidenceCount == 0 {
		b.WriteString("No evidence recorded.\n")
	}
	b.WriteString("\n## Auditability\n\nAll changes are recorded in the append-only audit log stored with this workspace.\n")
	content := []byte(b.String())
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return "", err
	}
	// Keep the pre-v2 reports directory as a compatibility export while the
	// canonical copy lives under the workspace artifact layout.
	legacyDir := filepath.Join(filepath.Dir(e.Store.path), "reports")
	if err := os.MkdirAll(legacyDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(legacyDir, workspaceID+".md"), content, 0o644)
	}
	digest := sha256.Sum256(content)
	artifact := Artifact{ID: NewID("ART"), Name: "workspace.md", Type: "report", Path: path, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(content)), Metadata: map[string]any{"workspace_id": workspaceID, "report": true}, CreatedAt: now()}
	_ = e.Store.AddArtifact(artifact)
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "report.generated", WorkspaceID: workspaceID, Payload: map[string]any{"path": path, "artifact_id": artifact.ID}, CreatedAt: now()})
	_ = e.audit("report.generated", "reporter", "workspace", workspaceID, map[string]any{"path": path, "artifact_id": artifact.ID})
	return path, nil
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = appendUniqueString(out, value)
	}
	return out
}

func fileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
