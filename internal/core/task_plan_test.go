package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapabilityPlanRunsOrderedWorkerStepsAndPersistsArtifactChain(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := NewEngine(store, 2)
	workspace, err := engine.CreateWorkspace("plan", "test", "multi-step")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "fixture", Model: "R1", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "firmware.bin")
	if err := os.WriteFile(source, []byte("\x7fELF\x00admin_password=review\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := engine.ImportArtifact(workspace.ID, source, "fixture.bin", "firmware")
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitCapabilityPlan(context.Background(), workspace.ID, target.ID, []string{"binary.identify", "binary.search_string"}, "inspect firmware", PermissionSet{Filesystem: "workspace-readonly"}, Budget{MaxRuntimeSeconds: 30, MaxToolCalls: 5}, map[string]any{"artifact_id": artifact.ID, "query": "password"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskCompleted || current.Status == TaskFailed || current.Status == TaskBlocked {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	current, _ := store.Task(task.ID)
	if current.Status != TaskCompleted {
		t.Fatalf("task status=%s error=%s", current.Status, current.Error)
	}
	if len(current.Nodes) < 5 {
		t.Fatalf("expected planning, scheduler, two capability and summary nodes: %+v", current.Nodes)
	}
	if len(current.RequiredCapabilities) != 2 || current.FindingID == "" {
		t.Fatalf("task chain not persisted: %+v", current)
	}
	finding, ok := store.Finding(current.FindingID)
	if !ok || len(finding.EvidenceIDs) < 2 || len(finding.ArtifactIDs) != 1 {
		t.Fatalf("finding chain incomplete: %+v", finding)
	}
	if len(store.Snapshot().CapabilityRuns) != 2 || len(store.Snapshot().ToolRuns) < 4 {
		t.Fatalf("runs not recorded: capabilities=%d tools=%d", len(store.Snapshot().CapabilityRuns), len(store.Snapshot().ToolRuns))
	}
}
