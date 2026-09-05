package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type CapabilityExecutor func(context.Context, CapabilityRequest) (CapabilityResult, error)

type Engine struct {
	Store        *Store
	mu           sync.RWMutex
	capabilities map[string]Capability
	executors    map[string]CapabilityExecutor
	tools        map[string]Tool
	workers      chan struct{}
}

func NewEngine(store *Store, maxWorkers int) *Engine {
	if maxWorkers < 1 {
		maxWorkers = 4
	}
	e := &Engine{Store: store, capabilities: map[string]Capability{}, executors: map[string]CapabilityExecutor{}, tools: map[string]Tool{}, workers: make(chan struct{}, maxWorkers)}
	e.RegisterBuiltinCapabilities()
	e.ensureDefaults()
	return e
}

func (e *Engine) ensureDefaults() {
	st := e.Store.Snapshot()
	if len(st.Agents) > 0 && len(st.Skills) > 0 {
		return
	}
	_ = e.Store.mutate(func(state *State) error {
		if len(state.Agents) == 0 {
			state.Agents = []Agent{
				{ID: "commander-default", Role: "commander", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 1},
				{ID: "recon-default", Role: "recon", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 4},
				{ID: "analysis-default", Role: "analysis", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 4},
				{ID: "validation-default", Role: "validation", ModelProvider: "local", Model: "configured-by-user", Enabled: true, Status: "idle", MaxConcurrency: 2},
			}
		}
		if len(state.Skills) == 0 {
			state.Skills = []Skill{
				{ID: "skill.hidden-interface-discovery", Name: "hidden_interface_discovery", Version: "1.0.0", Roles: []string{"recon", "analysis"}, Steps: []string{"firmware.inventory", "web.route_discovery", "binary.search_string", "protocol.hidden_interface", "taint.trace"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
				{ID: "skill.passive-target-recon", Name: "passive_target_recon", Version: "1.0.0", Roles: []string{"commander", "recon"}, Steps: []string{"target.fingerprint", "finding.gate"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
				{ID: "skill.validation-review", Name: "validation_review", Version: "1.0.0", Roles: []string{"validation"}, Steps: []string{"fuzz.constraint", "emulation.run", "cvss.score"}, Outputs: []string{"finding", "evidence"}, Permissions: PermissionSet{Filesystem: "workspace-readonly"}},
			}
		}
		return nil
	})
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

func (e *Engine) Tools() []Tool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Tool, 0, len(e.tools))
	for _, tool := range e.tools {
		out = append(out, tool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
		e.RegisterCapability(item, e.catalogCapability)
	}
}

func capabilityCatalog() []Capability {
	base := PermissionSet{Filesystem: "workspace-readonly"}
	items := []Capability{
		{ID: "firmware.extract", Category: "firmware", Description: "Describe an offline firmware extraction plan and inputs.", Runtime: "builtin", Implementation: "go"},
		{ID: "firmware.inventory", Category: "firmware", Description: "Inventory offline firmware/filesystem entries supplied by the caller.", Runtime: "builtin", Implementation: "go"},
		{ID: "firmware.config_scan", Category: "firmware", Description: "Scan supplied configuration entries for risky settings.", Runtime: "builtin", Implementation: "go"},
		{ID: "binary.identify", Category: "binary", Description: "Identify a binary from its declared name, type, or magic metadata.", Runtime: "builtin", Implementation: "go"},
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
	}
	for i := range items {
		items[i].Version = "1.0.0"
		items[i].Permissions = base
		items[i].TimeoutSeconds = 60
	}
	return items
}

func (e *Engine) authorize(task Task, cap Capability) error {
	if task.Permissions.Network && !cap.Permissions.Network {
		return errors.New("task requests network but capability does not allow it")
	}
	if task.Permissions.Device && !cap.Permissions.Device {
		return errors.New("task requests device but capability does not allow it")
	}
	if task.Permissions.Destructive && !cap.Permissions.Destructive {
		return errors.New("task requests destructive access but capability does not allow it")
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
	if task.Permissions.Device || task.Permissions.Destructive {
		a := Approval{ID: NewID("APR"), TaskID: task.ID, CapabilityID: cap.ID, Reason: "high-risk permission requires explicit human approval", Status: "pending", RequestedAt: now()}
		_ = e.Store.AddApproval(a)
		_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "human.approval.required", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"approval_id": a.ID}, CreatedAt: now()})
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "pending_approval", Error: "human approval required"}, nil
	}
	if executor == nil {
		return CapabilityResult{RequestID: req.RequestID, CapabilityID: req.CapabilityID, Status: "failed", Error: "capability has no executor"}, errors.New("capability has no executor")
	}
	select {
	case e.workers <- struct{}{}:
	case <-ctx.Done():
		return CapabilityResult{}, ctx.Err()
	}
	defer func() { <-e.workers }()
	started := time.Now()
	toolName := "builtin:" + cap.ID
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.started", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID}, CreatedAt: now()})
	result, err := executor(ctx, req)
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
	result.Status = "completed"
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.completed", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID}, CreatedAt: now()})
	_ = e.Store.AddCapabilityRun(CapabilityRun{ID: NewID("CAPRUN"), RequestID: req.RequestID, TaskID: task.ID, CapabilityID: cap.ID, Status: "completed", Result: map[string]any{"summary": result.Summary, "confidence": result.Confidence, "metrics": result.Metrics}, StartedAt: started, CompletedAt: timePtr(now())})
	_ = e.Store.AddToolRun(ToolRun{ID: NewID("TOOLRUN"), TaskID: task.ID, ToolName: toolName, Status: "completed", Output: map[string]any{"summary": result.Summary}, StartedAt: started, CompletedAt: timePtr(now())})
	return result, nil
}

func timePtr(t time.Time) *time.Time { return &t }

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
	return CapabilityResult{Summary: "Finding gate evaluation is handled by the stateful engine", Confidence: 1, Evidence: []Evidence{{ID: NewID("E"), Type: "manual_review", Confidence: 1, Content: map[string]any{"decision": "pass", "reason": "structured evidence present"}, Source: map[string]string{"capability_id": req.CapabilityID}}}}, nil
}

func (e *Engine) reportCapability(ctx context.Context, req CapabilityRequest) (CapabilityResult, error) {
	workspaceID, _ := req.Inputs["workspace_id"].(string)
	path, err := e.GenerateReport(workspaceID)
	if err != nil {
		return CapabilityResult{}, err
	}
	return CapabilityResult{Summary: "Report generated", Confidence: 1, Artifacts: []Artifact{{ID: NewID("ART"), Name: filepath.Base(path), Type: "report", Path: path, SHA256: fileHash(path), CreatedAt: now()}}}, nil
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
	w := Workspace{ID: NewID("W"), Name: name, Owner: owner, Description: description, CreatedAt: now(), UpdatedAt: now()}
	return w, e.Store.CreateWorkspace(w)
}
func (e *Engine) CreateTarget(workspaceID string, t Target) (Target, error) {
	if _, ok := e.Store.Workspace(workspaceID); !ok {
		return Target{}, errors.New("workspace not found")
	}
	t.ID = NewID("T")
	t.WorkspaceID = workspaceID
	t.CreatedAt = now()
	return t, e.Store.CreateTarget(t)
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
	go e.runTask(context.Background(), t, target)
	return t, nil
}

func (e *Engine) SubmitCapabilityTask(ctx context.Context, workspaceID, targetID, capabilityID, objective string, permissions PermissionSet, budget Budget) (Task, error) {
	if _, ok := e.Store.Workspace(workspaceID); !ok {
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
		objective = "Run capability " + capabilityID
	}
	task := Task{ID: NewID("TASK"), WorkspaceID: workspaceID, TargetID: targetID, Type: "capability." + capabilityID, Objective: objective, Priority: 60, Status: TaskQueued, AssignedAgent: "analysis-default", RequiredCapabilities: []string{capabilityID}, Permissions: permissions, Budget: budget, CreatedAt: now(), UpdatedAt: now()}
	if task.Budget.MaxRuntimeSeconds == 0 {
		task.Budget.MaxRuntimeSeconds = 300
	}
	if task.Budget.MaxToolCalls == 0 {
		task.Budget.MaxToolCalls = 10
	}
	if err := e.Store.CreateTask(task); err != nil {
		return Task{}, err
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task.created", WorkspaceID: workspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": capabilityID}, CreatedAt: now()})
	go e.runCapabilityTask(context.Background(), task, target)
	return task, nil
}

func (e *Engine) runCapabilityTask(ctx context.Context, task Task, target Target) {
	_ = e.transitionTask(task.ID, TaskAssigned, "commander")
	_ = e.transitionTask(task.ID, TaskRunning, "scheduler")
	req := CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: task.RequiredCapabilities[0], Objective: task.Objective, Inputs: map[string]any{"vendor": target.Vendor, "model": target.Model, "address": target.Address, "transport": target.Transport}, Permissions: task.Permissions, Budget: task.Budget}
	result, err := e.Request(ctx, req, task)
	agentRun := AgentRun{ID: NewID("AGENTRUN"), AgentID: task.AssignedAgent, TaskID: task.ID, Status: "completed", Model: "configured-by-user", Input: map[string]any{"objective": task.Objective, "capability": task.RequiredCapabilities[0]}, Output: map[string]any{"summary": result.Summary, "confidence": result.Confidence}, StartedAt: task.CreatedAt, CompletedAt: timePtr(now())}
	if err != nil {
		agentRun.Status = "failed"
		agentRun.Output = map[string]any{"error": err.Error()}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(v *Task) error { v.Status = TaskFailed; v.Error = err.Error(); return nil })
		return
	}
	if result.Status == "pending_approval" {
		agentRun.Status = "blocked"
		agentRun.Output = map[string]any{"reason": "human approval required"}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(v *Task) error { v.Status = TaskBlocked; v.Error = "waiting for human approval"; return nil })
		return
	}
	_ = e.Store.AddAgentRun(agentRun)
	var finding Finding
	if task.FindingID != "" {
		finding, _ = e.Store.Finding(task.FindingID)
	} else {
		finding = Finding{ID: NewID("F"), WorkspaceID: task.WorkspaceID, TargetID: target.ID, Title: "Capability result: " + task.RequiredCapabilities[0], State: FindingCandidate, Priority: "P2", Score: 40, Confidence: result.Confidence, Location: Location{Component: target.Model}, Validation: Validation{State: "not_started"}, CreatedAt: now(), UpdatedAt: now()}
		_ = e.Store.CreateFinding(finding)
	}
	for _, evidence := range result.Evidence {
		evidence.FindingID = finding.ID
		_ = e.Store.AddEvidence(evidence)
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "evidence.added", WorkspaceID: task.WorkspaceID, TaskID: task.ID, FindingID: finding.ID, Payload: map[string]any{"capability_id": task.RequiredCapabilities[0]}, CreatedAt: now()})
	_ = e.transitionTask(task.ID, TaskCompleted, "scheduler")
}

func (e *Engine) runTask(ctx context.Context, task Task, target Target) {
	_ = e.transitionTask(task.ID, TaskAssigned, "scheduler")
	_ = e.transitionTask(task.ID, TaskRunning, "scheduler")
	req := CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "target.fingerprint", Objective: task.Objective, Inputs: map[string]any{"vendor": target.Vendor, "model": target.Model, "address": target.Address, "transport": target.Transport}, Permissions: task.Permissions, Budget: task.Budget}
	result, err := e.Request(ctx, req, task)
	agentRun := AgentRun{ID: NewID("AGENTRUN"), AgentID: task.AssignedAgent, TaskID: task.ID, Status: "completed", Model: "configured-by-user", Input: map[string]any{"objective": task.Objective, "capabilities": task.RequiredCapabilities}, StartedAt: task.CreatedAt, CompletedAt: timePtr(now())}
	if err != nil {
		agentRun.Status = "failed"
		agentRun.Output = map[string]any{"error": err.Error()}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error { t.Status = TaskFailed; t.Error = err.Error(); return nil })
		_ = e.audit("task.failed", "scheduler", "task", task.ID, map[string]any{"error": err.Error()})
		return
	}
	if result.Status == "pending_approval" {
		agentRun.Status = "blocked"
		agentRun.Output = map[string]any{"reason": "human approval required"}
		_ = e.Store.AddAgentRun(agentRun)
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error { t.Status = TaskBlocked; t.Error = "waiting for human approval"; return nil })
		return
	}
	agentRun.Output = map[string]any{"summary": result.Summary, "confidence": result.Confidence}
	_ = e.Store.AddAgentRun(agentRun)
	finding := Finding{ID: NewID("F"), WorkspaceID: task.WorkspaceID, TargetID: target.ID, Title: fmt.Sprintf("Potential attack surface on %s", displayTarget(target)), State: FindingHypothesis, Priority: "P2", Score: 46, Confidence: result.Confidence, AttackSurface: AttackSurface{Type: "device-management", Protocol: target.Transport, Entrypoint: target.Address}, Location: Location{Component: target.Model}, Validation: Validation{State: "not_started"}, CreatedAt: now(), UpdatedAt: now()}
	if len(result.Evidence) > 0 {
		finding.State = FindingCandidate
	}
	if err := e.Store.CreateFinding(finding); err != nil {
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error { t.Status = TaskFailed; t.Error = err.Error(); return nil })
		return
	}
	for _, evidence := range result.Evidence {
		evidence.FindingID = finding.ID
		_ = e.Store.AddEvidence(evidence)
	}
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "finding.created", WorkspaceID: task.WorkspaceID, TaskID: task.ID, FindingID: finding.ID, Payload: map[string]any{"state": finding.State}, CreatedAt: now()})
	_ = e.transitionTask(task.ID, TaskCompleted, "scheduler")
	_ = e.audit("task.completed", "scheduler", "task", task.ID, map[string]any{"finding_id": finding.ID})
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
	return e.Store.UpdateTask(id, func(t *Task) error {
		if !CanTransitionTask(t.Status, to) {
			return fmt.Errorf("invalid task transition %s -> %s", t.Status, to)
		}
		t.Status = to
		return nil
	})
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
			if err := e.Store.UpdateTask(task.ID, func(v *Task) error { v.Status = TaskQueued; v.Error = ""; return nil }); err != nil {
				return err
			}
			go e.runTask(context.Background(), task, target)
			return nil
		}
	}
	return nil
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
	root := filepath.Join(filepath.Dir(e.Store.path), "reports")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(root, workspaceID+".md")
	var b strings.Builder
	b.WriteString("# IoTHunter SITREP\n\n")
	b.WriteString(fmt.Sprintf("- Workspace: %s (`%s`)\n- Owner: %s\n- Generated: %s\n\n", w.Name, w.ID, w.Owner, now().Format(time.RFC3339)))
	b.WriteString("## Findings\n\n")
	count := 0
	for _, f := range st.Findings {
		if f.WorkspaceID != workspaceID {
			continue
		}
		count++
		b.WriteString(fmt.Sprintf("### %s\n\n- State: `%s`\n- Priority: `%s`\n- Confidence: %.2f\n- Score: %.1f\n- Attack surface: `%s` / `%s`\n- Evidence: %d\n\n", f.Title, f.State, f.Priority, f.Confidence, f.Score, f.AttackSurface.Protocol, f.AttackSurface.Entrypoint, len(f.EvidenceIDs)))
	}
	if count == 0 {
		b.WriteString("No findings yet. Start a research run to populate this report.\n")
	}
	b.WriteString("\n## Auditability\n\nAll changes are recorded in the append-only audit log stored with this workspace.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	_ = e.audit("report.generated", "reporter", "workspace", workspaceID, map[string]any{"path": path})
	return path, nil
}

func fileHash(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
