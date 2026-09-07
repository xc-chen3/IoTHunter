package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIWorkspaceTargetAndRun(t *testing.T) {
	store, _ := NewStore("")
	server := NewAPIServer(NewEngine(store, 1)).Handler()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		r := httptest.NewRequest(method, path, &payload)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodPost, "/api/v1/workspaces", map[string]string{"name": "api-test"})
	if w.Code != http.StatusCreated {
		t.Fatalf("workspace status = %d", w.Code)
	}
	var workspace Workspace
	_ = json.NewDecoder(w.Body).Decode(&workspace)
	w = request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/targets", map[string]any{"name": "target", "vendor": "Acme", "model": "R1", "transport": "http", "authorized": true})
	if w.Code != http.StatusCreated {
		t.Fatalf("target status = %d", w.Code)
	}
	var target Target
	_ = json.NewDecoder(w.Body).Decode(&target)
	w = request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/run", map[string]string{"target_id": target.ID})
	if w.Code != http.StatusAccepted {
		t.Fatalf("run status = %d", w.Code)
	}
}

func TestAPIRootIdentifiesDesktopClient(t *testing.T) {
	store, _ := NewStore("")
	server := NewAPIServer(NewEngine(store, 1)).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("UI status = %d", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("root content type = %q", got)
	}
	if !bytes.Contains(res.Body.Bytes(), []byte(`"client":"desktop"`)) {
		t.Fatal("desktop client marker missing")
	}
}

func TestAPIRuntimeRegistryAndAgentBindingValidation(t *testing.T) {
	store, _ := NewStore("")
	server := NewAPIServer(NewEngine(store, 1)).Handler()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		r := httptest.NewRequest(method, path, &payload)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	w := request(http.MethodGet, "/api/v1/runtimes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("runtime registry status = %d", w.Code)
	}
	var registry struct {
		Runtimes []LocalRuntime `json:"runtimes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&registry); err != nil {
		t.Fatal(err)
	}
	if len(registry.Runtimes) != 4 {
		t.Fatalf("runtime count = %d, want 4", len(registry.Runtimes))
	}
	w = request(http.MethodPost, "/api/v1/agents/commander-default", map[string]string{"runtime_id": "does-not-exist"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime binding status = %d", w.Code)
	}
}

func TestAPIIoTSummaryProjection(t *testing.T) {
	store, _ := NewStore("")
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("iot-summary", "test", "device inventory")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.CreateTarget(workspace.ID, Target{Name: "lab-router", Model: "AX73", Authorized: true}); err != nil {
		t.Fatal(err)
	}
	server := NewAPIServer(engine).Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/iot/summary", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("summary status = %d", res.Code)
	}
	var summary map[string]int
	if err := json.NewDecoder(res.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if summary["targets"] != 1 {
		t.Fatalf("targets = %d, want 1", summary["targets"])
	}
}

func TestAPIDeviceConfigurationUpdate(t *testing.T) {
	store, _ := NewStore("")
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("device-config", "test", "peripheral configuration")
	if err != nil {
		t.Fatal(err)
	}
	server := NewAPIServer(engine).Handler()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(method, path, &payload)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		return res
	}
	res := request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/devices", Device{Vendor: "RIGOL", Model: "DP832", Transport: "USB"})
	if res.Code != http.StatusCreated {
		t.Fatalf("create device status = %d", res.Code)
	}
	var device Device
	if err := json.NewDecoder(res.Body).Decode(&device); err != nil {
		t.Fatal(err)
	}
	res = request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/devices/"+device.ID, map[string]any{"status": "connected", "config": map[string]any{"voltage_limit": "5.00"}})
	if res.Code != http.StatusOK {
		t.Fatalf("update device status = %d", res.Code)
	}
	var updated Device
	_ = json.NewDecoder(res.Body).Decode(&updated)
	if updated.Status != "connected" || updated.Config["voltage_limit"] != "5.00" {
		t.Fatalf("device update not persisted: %+v", updated)
	}
}

func TestAPIConversationAndPeripheralBoundaries(t *testing.T) {
	store, _ := NewStore("")
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("boundaries", "test", "conversation and peripheral records")
	if err != nil {
		t.Fatal(err)
	}
	server := NewAPIServer(engine).Handler()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(method, path, &payload)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		return res
	}
	res := request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/conversations", map[string]string{"title": "UART review", "content": "inspect boot"})
	if res.Code != http.StatusCreated {
		t.Fatalf("conversation status = %d", res.Code)
	}
	res = request(http.MethodPost, "/api/v1/workspaces/"+workspace.ID+"/peripherals", Peripheral{Name: "UART 01", Kind: "serial", Driver: "pyserial", Port: "/dev/ttyUSB0"})
	if res.Code != http.StatusCreated {
		t.Fatalf("peripheral status = %d", res.Code)
	}
	var peripheral Peripheral
	if err := json.NewDecoder(res.Body).Decode(&peripheral); err != nil {
		t.Fatal(err)
	}
	res = request(http.MethodPost, "/api/v1/peripherals/"+peripheral.ID, map[string]any{"status": "connected", "occupied_by": "test"})
	if res.Code != http.StatusOK {
		t.Fatalf("peripheral update status = %d", res.Code)
	}
	var updated Peripheral
	_ = json.NewDecoder(res.Body).Decode(&updated)
	if updated.Status != "connected" || updated.OccupiedBy != "test" {
		t.Fatalf("peripheral update not persisted: %+v", updated)
	}
	res = request(http.MethodGet, "/api/v1/workspaces/"+workspace.ID, nil)
	if res.Code != http.StatusOK || !bytes.Contains(res.Body.Bytes(), []byte(`"conversations"`)) || !bytes.Contains(res.Body.Bytes(), []byte(`"peripherals"`)) {
		t.Fatalf("aggregate boundary fields missing: %s", res.Body.String())
	}
}
