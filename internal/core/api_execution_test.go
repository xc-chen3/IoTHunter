package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIExposesExecutableTaskAndRegistryLifecycle(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	server := httptest.NewServer(NewAPIServer(engine).Handler())
	defer server.Close()
	post := func(path string, body any) map[string]any {
		encoded, _ := json.Marshal(body)
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			data, _ := io.ReadAll(response.Body)
			t.Fatalf("POST %s returned %d: %s", path, response.StatusCode, data)
		}
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	workspace := post("/api/v1/workspaces", map[string]any{"name": "api execution"})
	workspaceID := workspace["id"].(string)
	target := post("/api/v1/workspaces/"+workspaceID+"/targets", map[string]any{"name": "router", "authorized": true})
	targetID := target["id"].(string)
	post("/api/v1/capabilities", map[string]any{"id": "custom.audit", "version": "1.0.0", "category": "analysis", "description": "custom", "runtime": "builtin", "implementation": "go"})
	capabilitiesResponse, err := http.Get(server.URL + "/api/v1/capabilities/custom.audit")
	if err != nil {
		t.Fatal(err)
	}
	if capabilitiesResponse.StatusCode != http.StatusOK {
		t.Fatalf("capability detail status = %d", capabilitiesResponse.StatusCode)
	}
	_ = capabilitiesResponse.Body.Close()
	plan := post("/api/v1/workspaces/"+workspaceID+"/plan", map[string]any{"target_id": targetID, "capabilities": []string{"custom.audit"}, "objective": "run custom audit", "permissions": map[string]any{"filesystem": "workspace-readonly"}, "budget": map[string]any{"max_runtime_seconds": 10, "max_tool_calls": 2}})
	tasks, ok := plan["tasks"].([]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("plan response = %+v", plan)
	}
	taskID := tasks[0].(map[string]any)["id"].(string)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, getErr := http.Get(server.URL + "/api/v1/tasks/" + taskID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		var task Task
		_ = json.NewDecoder(response.Body).Decode(&task)
		_ = response.Body.Close()
		if task.Status == TaskCompleted || task.Status == TaskFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	listResponse, err := http.Get(server.URL + "/api/v1/tasks?workspace_id=" + workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer listResponse.Body.Close()
	var listed struct {
		Tasks []Task `json:"tasks"`
	}
	if err := json.NewDecoder(listResponse.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != taskID || listed.Tasks[0].Status != TaskCompleted {
		t.Fatalf("listed task = %+v", listed.Tasks)
	}
	detailResponse, err := http.Get(server.URL + "/api/v1/tasks/" + taskID + "/detail")
	if err != nil {
		t.Fatal(err)
	}
	defer detailResponse.Body.Close()
	var detail map[string]any
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail["events"].([]any)) == 0 || len(detail["capability_runs"].([]any)) == 0 {
		t.Fatalf("task detail did not include execution records: %+v", detail)
	}
}
