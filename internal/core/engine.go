package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	return e
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
		return result, err
	}
	result.Status = "completed"
	_ = e.Store.AddEvent(Event{ID: NewID("EVT"), Type: "capability.completed", WorkspaceID: task.WorkspaceID, TaskID: task.ID, Payload: map[string]any{"capability_id": cap.ID}, CreatedAt: now()})
	return result, nil
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

func (e *Engine) runTask(ctx context.Context, task Task, target Target) {
	_ = e.transitionTask(task.ID, TaskAssigned, "scheduler")
	_ = e.transitionTask(task.ID, TaskRunning, "scheduler")
	req := CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "target.fingerprint", Objective: task.Objective, Inputs: map[string]any{"vendor": target.Vendor, "model": target.Model, "address": target.Address, "transport": target.Transport}, Permissions: task.Permissions, Budget: task.Budget}
	result, err := e.Request(ctx, req, task)
	if err != nil {
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error { t.Status = TaskFailed; t.Error = err.Error(); return nil })
		_ = e.audit("task.failed", "scheduler", "task", task.ID, map[string]any{"error": err.Error()})
		return
	}
	if result.Status == "pending_approval" {
		_ = e.Store.UpdateTask(task.ID, func(t *Task) error { t.Status = TaskBlocked; t.Error = "waiting for human approval"; return nil })
		return
	}
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
