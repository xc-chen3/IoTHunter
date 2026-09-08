package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	peripherals "github.com/iothunter/iothunter/internal/peripherals"
)

func TestPeripheralTelemetryPersistsAndStoresLargePayloadAsArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("telemetry", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	peripheral := Peripheral{ID: NewID("PER"), WorkspaceID: workspace.ID, Name: "uart", Kind: "serial", Status: "offline", UpdatedAt: now()}
	if err := store.CreatePeripheral(peripheral); err != nil {
		t.Fatal(err)
	}
	session := peripherals.Session{ID: "SES-telemetry", WorkspaceID: workspace.ID, PeripheralID: peripheral.ID, Kind: "serial", Status: "connected"}
	large := bytes.Repeat([]byte("A"), 32*1024)
	engine.persistPeripheralTelemetry(session, peripherals.Telemetry{At: time.Now().UTC(), Command: "read", Status: "completed", Bytes: large})
	state := store.Snapshot()
	if len(state.Telemetry) != 1 {
		t.Fatalf("telemetry count = %d", len(state.Telemetry))
	}
	record := state.Telemetry[0]
	if record.Topic != "serial.rx" || record.ArtifactID == "" || record.BytesBase64 != "" {
		t.Fatalf("large telemetry record = %+v", record)
	}
	if len(state.Artifacts) != 1 || state.Artifacts[0].ID != record.ArtifactID {
		t.Fatalf("artifact projection = %+v", state.Artifacts)
	}
	if got := state.Events[len(state.Events)-1].Type; got != "peripheral.telemetry" {
		t.Fatalf("last event type = %q", got)
	}

	server := NewAPIServer(engine).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/telemetry?workspace_id="+workspace.ID, nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), record.ID) {
		t.Fatalf("telemetry API response: status=%d body=%s", res.Code, res.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+workspace.ID, nil)
	res = httptest.NewRecorder()
	server.ServeHTTP(res, req)
	var detail struct {
		Telemetry []TelemetryRecord `json:"telemetry"`
	}
	if err := json.NewDecoder(res.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Telemetry) != 1 {
		t.Fatalf("workspace telemetry count = %d", len(detail.Telemetry))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := len(reopened.Snapshot().Telemetry); got != 1 {
		t.Fatalf("reopened telemetry count = %d", got)
	}
}

func TestPeripheralTelemetrySmallPayloadIsInlineBase64(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("inline", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	peripheral := Peripheral{ID: NewID("PER"), WorkspaceID: workspace.ID, Name: "uart", Kind: "serial", Status: "offline", UpdatedAt: now()}
	if err := store.CreatePeripheral(peripheral); err != nil {
		t.Fatal(err)
	}
	engine.persistPeripheralTelemetry(peripherals.Session{ID: "SES-inline", WorkspaceID: workspace.ID, PeripheralID: peripheral.ID}, peripherals.Telemetry{Command: "write", Status: "completed", Bytes: []byte("hello")})
	record := store.Snapshot().Telemetry[0]
	if record.Topic != "serial.tx" || record.BytesBase64 == "" || record.ArtifactID != "" {
		t.Fatalf("inline telemetry record = %+v", record)
	}
}
