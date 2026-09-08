package core

import (
	"context"
	"os/exec"
	"testing"
)

func TestBinaryDecompileRunsObjdumpThroughGateway(t *testing.T) {
	if _, err := exec.LookPath("objdump"); err != nil {
		t.Skip("objdump is not installed")
	}
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	task := Task{ID: NewID("TASK"), WorkspaceID: "W-test", AssignedAgent: "analysis-default", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 10, MaxToolCalls: 2}}
	result, err := engine.Request(context.Background(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "binary.decompile", Inputs: map[string]any{"path": "/bin/true"}, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || len(result.Evidence) != 1 || result.Metrics["output_bytes"].(int) == 0 {
		t.Fatalf("unexpected decompile result: %+v", result)
	}
}
