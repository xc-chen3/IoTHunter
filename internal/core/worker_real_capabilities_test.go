package core

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerExtractsFirmwareAndResolvesTaint(t *testing.T) {
	root := t.TempDir()
	image := filepath.Join(root, "fixture.zip")
	file, err := os.Create(image)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("etc/config")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("admin_password=review\n"))
	_ = archive.Close()
	_ = file.Close()
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	task := Task{ID: NewID("TASK"), WorkspaceID: "W-test", AssignedAgent: "analysis-default", Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 20, MaxToolCalls: 3}}
	result, err := engine.Request(context.Background(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "firmware.extract", Inputs: map[string]any{"path": image, "workspace_root": root}, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil || result.Status != "completed" {
		t.Fatalf("extract result=%+v err=%v", result, err)
	}
	if len(result.Artifacts) != 2 || result.Evidence[0].Content["bytes_written"] == nil {
		t.Fatalf("extraction did not persist manifest artifact: %+v", result)
	}
	task.Budget.MaxToolCalls = 1
	taint, err := engine.Request(context.Background(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "taint.trace", Inputs: map[string]any{"source": "http", "sink": "sprintf", "edges": []any{map[string]any{"from": "http", "to": "config_set"}, map[string]any{"from": "config_set", "to": "sprintf"}}}, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil || taint.Status != "completed" || taint.Evidence[0].Content["traceable"] != true {
		t.Fatalf("taint result=%+v err=%v", taint, err)
	}
}
