package core

import (
	"context"
	"testing"
	"time"
)

func TestCapabilityResultsAdvanceFindingThroughValidation(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("finding-chain", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "fixture", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitCapabilityPlan(context.Background(), workspace.ID, target.ID, []string{"taint.trace", "poc.verify"}, "validate path", PermissionSet{Filesystem: "workspace-readonly"}, Budget{MaxRuntimeSeconds: 20, MaxToolCalls: 4}, map[string]any{"source": "http", "sink": "sprintf", "edges": []any{map[string]any{"from": "http", "to": "sprintf"}}, "expected": "ok", "actual": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskCompleted || current.Status == TaskFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := store.Task(task.ID)
	if current.Status != TaskCompleted {
		t.Fatalf("task status=%s error=%s", current.Status, current.Error)
	}
	if current.FindingID == "" {
		t.Fatal("task did not create a finding")
	}
	finding, ok := store.Finding(current.FindingID)
	if !ok || finding.State != FindingValidated || !finding.Validation.Reproducible {
		t.Fatalf("finding=%+v", finding)
	}
}
