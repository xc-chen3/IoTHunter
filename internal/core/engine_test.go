package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestResearchLoopCreatesEvidenceAndFinding(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	w, err := engine.CreateWorkspace("test", "tester", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(w.ID, Target{Name: "router", Vendor: "Acme", Model: "R1", Transport: "http", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitResearch(context.Background(), w.ID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, _ := store.Task(task.ID)
		if current.Status == TaskCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	current, _ := store.Task(task.ID)
	if current.Status != TaskCompleted {
		t.Fatalf("task status = %s", current.Status)
	}
	state := store.Snapshot()
	if len(state.Findings) != 1 || len(state.Evidence) != 1 {
		t.Fatalf("expected one finding and evidence, got %d and %d", len(state.Findings), len(state.Evidence))
	}
	if state.Findings[0].State != FindingCandidate {
		t.Fatalf("finding state = %s", state.Findings[0].State)
	}
	if len(state.Audit) == 0 || len(state.Events) == 0 {
		t.Fatal("expected audit and event records")
	}
}

func TestFindingTransitionsRejectInvalidState(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	w, _ := engine.CreateWorkspace("test", "tester", "")
	f := Finding{ID: NewID("F"), WorkspaceID: w.ID, State: FindingCandidate, CreatedAt: now(), UpdatedAt: now()}
	if err := store.CreateFinding(f); err != nil {
		t.Fatal(err)
	}
	if err := engine.TransitionFinding(f.ID, FindingReported, "test"); err == nil {
		t.Fatal("expected invalid transition error")
	}
	if err := engine.TransitionFinding(f.ID, FindingAnalyzing, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureRegistriesAndFindingGate(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 2)
	if len(engine.Capabilities()) < 20 {
		t.Fatalf("capability catalog too small: %d", len(engine.Capabilities()))
	}
	state := store.Snapshot()
	if len(state.Agents) < 4 || len(state.Skills) < 3 {
		t.Fatalf("default control-plane registries are incomplete")
	}
	w, _ := engine.CreateWorkspace("gate", "tester", "")
	f := Finding{ID: NewID("F"), WorkspaceID: w.ID, State: FindingCandidate, Confidence: .8, Score: 60, AttackSurface: AttackSurface{Type: "web"}, Validation: Validation{State: "not_started"}, CreatedAt: now(), UpdatedAt: now()}
	if err := store.CreateFinding(f); err != nil {
		t.Fatal(err)
	}
	if err := store.AddEvidence(Evidence{ID: NewID("E"), FindingID: f.ID, Type: "manual_review", Confidence: .8, Content: map[string]any{"ok": true}, CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	decision, err := engine.EvaluateGate(f.ID, "finding")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Decision != "PASS" {
		t.Fatalf("gate decision = %s", decision.Decision)
	}
}
