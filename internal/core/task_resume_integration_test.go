package core

import (
	"context"
	"testing"
	"time"
)

func TestCapabilityPlanResumesAtBlockedNodeAfterApproval(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	if err := store.mutate(func(state *State) error {
		for i := range state.Agents {
			if state.Agents[i].ID == "analysis-default" {
				state.Agents[i].Permissions.Destructive = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	engine.RegisterCapability(Capability{ID: "test.read", Version: "1.0.0", Category: "analysis", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Runtime: "builtin", Implementation: "test", TimeoutSeconds: 5}, func(context.Context, CapabilityRequest) (CapabilityResult, error) {
		return CapabilityResult{Summary: "read node completed", Confidence: 0.8, Evidence: []Evidence{{Type: "observation", Confidence: 0.8, Content: map[string]any{"step": "read"}}}}, nil
	})
	engine.RegisterCapability(Capability{ID: "test.risky", Version: "1.0.0", Category: "validation", Permissions: PermissionSet{Filesystem: "workspace-readonly", Destructive: true}, Runtime: "builtin", Implementation: "test", TimeoutSeconds: 5}, func(context.Context, CapabilityRequest) (CapabilityResult, error) {
		return CapabilityResult{Summary: "risky node completed", Confidence: 0.9, Evidence: []Evidence{{Type: "observation", Confidence: 0.9, Content: map[string]any{"step": "risky"}}}}, nil
	})
	workspace, err := engine.CreateWorkspace("resume", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "lab target", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitCapabilityPlan(context.Background(), workspace.ID, target.ID, []string{"test.read", "test.risky"}, "resume plan", PermissionSet{Filesystem: "workspace-readonly", Destructive: true}, Budget{MaxRuntimeSeconds: 30, MaxToolCalls: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskBlocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	blocked, _ := store.Task(task.ID)
	if blocked.Status != TaskBlocked {
		t.Fatalf("task status = %s, error = %s", blocked.Status, blocked.Error)
	}
	if node, ok := taskCapabilityNode(blocked, "test.read"); !ok || node.Status != "completed" {
		t.Fatalf("read node was not completed before approval: %+v", blocked.Nodes)
	}
	var pending Approval
	for _, candidate := range store.Snapshot().Approvals {
		if candidate.TaskID == task.ID && candidate.Status == "pending" {
			pending = candidate
			break
		}
	}
	if pending.ID == "" {
		t.Fatal("pending approval was not created")
	}
	if err := engine.DecideApproval(pending.ID, "approved", "tester"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskCompleted || current.Status == TaskFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	completed, _ := store.Task(task.ID)
	if completed.Status != TaskCompleted {
		t.Fatalf("task status after approval = %s, error = %s", completed.Status, completed.Error)
	}
	if node, ok := taskCapabilityNode(completed, "test.read"); !ok || node.Status != "completed" {
		t.Fatalf("read node changed after resume: %+v", completed.Nodes)
	}
	if node, ok := taskCapabilityNode(completed, "test.risky"); !ok || node.Status != "completed" {
		t.Fatalf("risky node did not complete after approval: %+v", completed.Nodes)
	}
	if got := len(store.Snapshot().Findings); got != 1 {
		t.Fatalf("expected one finding, got %d", got)
	}
}
