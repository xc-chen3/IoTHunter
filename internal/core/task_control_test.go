package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPauseCancelsBoundRuntimeAndRetryRestartsTask(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nsleep 5\nprintf 'done'\n"), 0o755); err != nil {
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
	workspace, _ := engine.CreateWorkspace("control", "test", "")
	target, _ := engine.CreateTarget(workspace.ID, Target{Name: "router", Authorized: true})
	task, err := engine.SubmitResearch(context.Background(), workspace.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	paused, err := engine.ControlTask(task.ID, "pause", "test")
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != TaskPaused {
		t.Fatalf("pause status = %s", paused.Status)
	}
	time.Sleep(100 * time.Millisecond)
	current, _ := store.Task(task.ID)
	if current.Status != TaskPaused {
		t.Fatalf("cancelled runtime changed paused task to %s", current.Status)
	}
	// A retry is a fresh run and is allowed to continue from the task's
	// declared capability after the previous context has been cancelled.
	queued, err := engine.ControlTask(task.ID, "retry", "test")
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != TaskQueued && queued.Status != TaskRunning {
		t.Fatalf("retry status = %s", queued.Status)
	}
	engine.ControlTask(task.ID, "cancel", "test")
}
