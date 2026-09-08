package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelAndPromptRegistryIsPersistentThroughAPI(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewAPIServer(NewEngine(store, 1)).Handler())
	defer server.Close()
	post := func(path string, payload map[string]any) map[string]any {
		body, _ := json.Marshal(payload)
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			t.Fatalf("POST %s status=%d", path, response.StatusCode)
		}
		var value map[string]any
		if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	model := post("/api/v1/models", map[string]any{"name": "local-test", "provider": "local", "model": "fixture"})
	if model["id"] == "" {
		t.Fatal("model id was not generated")
	}
	prompt := post("/api/v1/prompts", map[string]any{"name": "analysis", "role": "analysis", "version": "1.0.0", "content": "inspect {target}"})
	if prompt["sha256"] == "" {
		t.Fatal("prompt digest was not generated")
	}
	activate := post("/api/v1/prompts/"+prompt["id"].(string)+"/activate", map[string]any{})
	if active, _ := activate["active"].(bool); !active {
		t.Fatal("prompt was not activated")
	}
	response, err := http.Get(server.URL + "/api/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listed struct {
		Models []ModelConfig `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Models) != 1 || listed.Models[0].Model != "fixture" {
		t.Fatalf("models = %+v", listed.Models)
	}
}
