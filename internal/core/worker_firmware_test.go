package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFirmwareCapabilityReadsImageThroughPythonWorker(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "firmware.bin")
	if err := os.WriteFile(image, []byte("\x7fELF\x00boot\x00admin_password=review\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	task := Task{ID: NewID("TASK"), WorkspaceID: "W-test", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 10, MaxToolCalls: 1}}
	result, err := engine.Request(context.Background(), CapabilityRequest{
		RequestID: NewID("REQ"), TaskID: task.ID, AgentID: "analysis-default", CapabilityID: "binary.identify",
		Objective: "identify firmware", Inputs: map[string]any{"path": image, "workspace_root": root}, Permissions: task.Permissions, Budget: task.Budget,
	}, task)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || len(result.Artifacts) != 1 || result.Artifacts[0].SHA256 == "" {
		t.Fatalf("unexpected worker result: %+v", result)
	}
	if result.Evidence[0].Content["file"].(map[string]any)["format"] != "elf" {
		t.Fatalf("format was not detected: %+v", result.Evidence[0])
	}
}
