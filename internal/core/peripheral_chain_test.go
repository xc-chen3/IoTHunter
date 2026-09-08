package core

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	peripherals "github.com/iothunter/iothunter/internal/peripherals"
)

func TestPowerMeasureCapabilityUsesLeasedTCPPeripheral(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), "MEAS:ALL?") {
				_, _ = fmt.Fprintln(conn, "5.01,0.12,0.60")
			}
		}
	}()
	store, err := NewStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	engine := NewEngine(store, 1)
	if err := store.mutate(func(state *State) error {
		for index := range state.Agents {
			if state.Agents[index].ID == "validation-default" {
				state.Agents[index].Permissions.Device = true
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	workspace, _ := engine.CreateWorkspace("peripheral", "test", "")
	target, _ := engine.CreateTarget(workspace.ID, Target{Name: "board", Authorized: true})
	peripheral := Peripheral{ID: NewID("PER"), WorkspaceID: workspace.ID, Name: "supply", Kind: "power", Address: listener.Addr().String(), SafetyLimits: map[string]any{"max_voltage": 5, "max_current": 1}}
	if err := store.CreatePeripheral(peripheral); err != nil {
		t.Fatal(err)
	}
	session, err := engine.Peripherals.Connect(context.Background(), peripheralsOpen(peripheral))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Peripherals.Disconnect(context.Background(), session.ID)
	task := Task{ID: NewID("TASK"), WorkspaceID: workspace.ID, TargetID: target.ID, AssignedAgent: "validation-default", Permissions: PermissionSet{Filesystem: "workspace-readonly", Device: true}, Budget: Budget{MaxRuntimeSeconds: 10}}
	result, err := engine.Request(context.Background(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: task.AssignedAgent, CapabilityID: "power.measure", Inputs: map[string]any{"session_id": session.ID}, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := result.Evidence[0].Content["bytes_base64"].(string)
	if result.Status != "completed" || len(result.Evidence) != 1 || encoded == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(store.Snapshot().Telemetry) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if telemetry := store.Snapshot().Telemetry; len(telemetry) == 0 || telemetry[0].Topic != "power.measurement" {
		t.Fatalf("peripheral telemetry was not persisted: %+v", telemetry)
	}
}

func peripheralsOpen(p Peripheral) peripherals.OpenRequest {
	return peripherals.OpenRequest{PeripheralID: p.ID, Kind: p.Kind, Endpoint: peripherals.Endpoint{Address: p.Address}, Config: p.Config, Mode: "exclusive", LeaseTTL: time.Minute}
}
