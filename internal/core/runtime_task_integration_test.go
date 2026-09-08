package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskUsesBoundLocalRuntimeBeforeCapability(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nprintf 'agent-plan:%s\\n' \"$3\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	if err := store.mutate(func(state *State) error {
		for i := range state.Agents {
			if state.Agents[i].ID == "recon-default" {
				state.Agents[i].RuntimeID = "codex"
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspace, err := engine.CreateWorkspace("runtime-task", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "router", Vendor: "Acme", Model: "R1", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitResearch(context.Background(), workspace.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskCompleted || current.Status == TaskFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, _ := store.Task(task.ID)
	if current.Status != TaskCompleted {
		t.Fatalf("task status = %s, error = %s", current.Status, current.Error)
	}
	if len(store.Snapshot().AgentRuns) == 0 || store.Snapshot().AgentRuns[0].Output["runtime_output"] != "agent-plan:Collect a passive target fingerprint and create a candidate finding" {
		t.Fatalf("runtime output was not recorded: %+v", store.Snapshot().AgentRuns)
	}
}
