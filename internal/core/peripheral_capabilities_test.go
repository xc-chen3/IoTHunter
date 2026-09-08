package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestArchitecturePeripheralCapabilitiesAreRegistered(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := NewEngine(store, 1)
	wanted := map[string]bool{
		"serial.open": true, "serial.configure": true, "serial.read": true, "serial.write": true,
		"power.read": true, "power.cycle": true, "scope.configure": true, "scope.measure": true,
		"jlink.reset": true, "jlink.halt": true, "jlink.read_memory": true,
		"bluetooth.scan": true, "packet.capture": true,
	}
	for _, capability := range engine.Capabilities() {
		delete(wanted, capability.ID)
	}
	if len(wanted) != 0 {
		t.Fatalf("missing architecture capabilities: %v", wanted)
	}
	for _, capability := range engine.Capabilities() {
		if capability.ID == "power.cycle" || capability.ID == "jlink.reset" || capability.ID == "jlink.halt" {
			if !capability.Permissions.Destructive {
				t.Fatalf("%s must require destructive permission", capability.ID)
			}
		}
	}
}

func TestPowerCycleRequestsApprovalBeforePeripheralInvocation(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("approval", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	task := Task{ID: NewID("TASK"), WorkspaceID: workspace.ID, AssignedAgent: "validation-default", Permissions: PermissionSet{Filesystem: "workspace-readonly", Device: true, Destructive: true}, Budget: Budget{MaxRuntimeSeconds: 10}}
	if err := store.mutate(func(state *State) error {
		for index := range state.Agents {
			if state.Agents[index].ID == task.AssignedAgent {
				state.Agents[index].Permissions.Device = true
				state.Agents[index].Permissions.Destructive = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := engine.Request(context.Background(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "power.cycle", Inputs: map[string]any{"session_id": "missing"}, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil || result.Status != "pending_approval" {
		t.Fatalf("expected approval gate, result=%+v err=%v", result, err)
	}
}

func TestPowerConfigCannotExceedSafetyLimit(t *testing.T) {
	err := validatePeripheralConfigRecord(Peripheral{Kind: "power", SafetyLimits: map[string]any{"max_voltage": 3.3, "max_current": 1}}, map[string]any{"voltage": 5})
	if err == nil {
		t.Fatal("expected voltage safety limit denial")
	}
}

func TestPeripheralPlanUsesValidationAgent(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("routing", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "board", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	task, err := engine.SubmitCapabilityPlan(context.Background(), workspace.ID, target.ID, []string{"power.measure"}, "read power", PermissionSet{Filesystem: "workspace-readonly", Device: true}, Budget{MaxRuntimeSeconds: 1}, map[string]any{"session_id": "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if task.AssignedAgent != "validation-default" {
		t.Fatalf("peripheral task assigned to %s", task.AssignedAgent)
	}
}
