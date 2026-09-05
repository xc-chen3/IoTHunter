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

func TestAPIServesEmbeddedUI(t *testing.T) {
	store, _ := NewStore("")
	server := NewAPIServer(NewEngine(store, 1)).Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("UI status = %d", res.Code)
	}
	if got := res.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("UI content type = %q", got)
	}
	if !bytes.Contains(res.Body.Bytes(), []byte("<title>IoTHunter</title>")) {
		t.Fatal("embedded UI title missing")
	}
}
