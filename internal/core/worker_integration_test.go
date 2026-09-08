package core

import (
	"context"
	"testing"
)

func TestKnowledgeCapabilityRunsThroughPythonWorker(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	task := Task{ID: NewID("TASK"), WorkspaceID: "W-test", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 10, MaxToolCalls: 1}}
	result, err := engine.Request(context.Background(), CapabilityRequest{
		RequestID: NewID("REQ"), TaskID: task.ID, AgentID: "analysis-default", CapabilityID: "knowledge.search",
		Objective: "search worker", Inputs: map[string]any{"query": "sprintf", "corpus": []any{"strcpy", "sprintf"}}, Permissions: task.Permissions, Budget: task.Budget,
	}, task)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Summary != "Found 1 matching knowledge items" || len(result.Evidence) != 1 {
		t.Fatalf("unexpected worker result: %+v", result)
	}
}

func TestWorkerFailureIsPropagatedAsCapabilityFailure(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	task := Task{ID: NewID("TASK"), WorkspaceID: "W-test", AssignedAgent: "analysis-default", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 10, MaxToolCalls: 1}}
	result, err := engine.Request(context.Background(), CapabilityRequest{
		RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "binary.identify",
		Objective: "invalid input", Inputs: map[string]any{}, Permissions: task.Permissions, Budget: task.Budget,
	}, task)
	if err == nil || result.Status != "failed" || result.Error == "" {
		t.Fatalf("worker failure was not propagated: result=%+v err=%v", result, err)
	}
}
