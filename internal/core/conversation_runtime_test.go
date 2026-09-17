package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversationRuntimeTimeoutConfiguration(t *testing.T) {
	t.Setenv("IOTHUNTER_CHAT_TIMEOUT_SECONDS", "45")
	if got := conversationRuntimeTimeout(); got != 45*time.Second {
		t.Fatalf("timeout = %s", got)
	}
	t.Setenv("IOTHUNTER_CHAT_TIMEOUT_SECONDS", "invalid")
	if got := conversationRuntimeTimeout(); got != 2*time.Minute {
		t.Fatalf("invalid timeout fallback = %s", got)
	}
}

func TestConversationUsesLocalRuntimeWithWorkspaceHistoryAndTargetContext(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "prompt.txt")
	script := filepath.Join(binDir, "claude")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$IOTHUNTER_TEST_PROMPT\"\nprintf 'The UART log indicates a normal boot sequence.'\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("IOTHUNTER_TEST_PROMPT", capture)

	store, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(store, 1)
	workspace, err := engine.CreateWorkspace("Router lab", "test", "Boot-log research")
	if err != nil {
		t.Fatal(err)
	}
	target, err := engine.CreateTarget(workspace.ID, Target{Name: "Lab router", Vendor: "Acme", Model: "R1", Transport: "uart", Authorized: true})
	if err != nil {
		t.Fatal(err)
	}
	conversation := Conversation{ID: NewID("CONV"), WorkspaceID: workspace.ID, Title: "Boot review", Status: "active", Messages: []ConversationMessage{{ID: NewID("MSG"), Role: "user", Content: "The bootloader is U-Boot.", CreatedAt: now()}}, CreatedAt: now(), UpdatedAt: now()}
	if err := store.CreateConversation(conversation); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(map[string]any{"content": "What does the UART log suggest?", "runtime_id": "claude", "target_id": target.ID})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+conversation.ID+"/message", bytes.NewReader(payload))
	res := httptest.NewRecorder()
	NewAPIServer(engine).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("message status = %d, body=%s", res.Code, res.Body.String())
	}
	var response struct {
		Conversation Conversation  `json:"conversation"`
		Runtime      RuntimeResult `json:"runtime"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Runtime.Status != "completed" || response.Runtime.RuntimeID != "claude" {
		t.Fatalf("runtime result = %+v", response.Runtime)
	}
	if len(response.Conversation.Messages) != 3 {
		t.Fatalf("messages = %+v", response.Conversation.Messages)
	}
	reply := response.Conversation.Messages[2]
	if reply.Role != "assistant" || reply.Content != "The UART log indicates a normal boot sequence." {
		t.Fatalf("assistant reply = %+v", reply)
	}
	if got := reply.References["runtimes"]; len(got) != 1 || got[0] != "claude" {
		t.Fatalf("runtime reference = %+v", got)
	}
	prompt, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Router lab", "Lab router", "The bootloader is U-Boot.", "What does the UART log suggest?"} {
		if !strings.Contains(string(prompt), expected) {
			t.Fatalf("prompt did not contain %q: %s", expected, prompt)
		}
	}
}

func TestConversationRuntimeFailureIsPersistedAsAssistantReply(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".grok", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "grok"), []byte("#!/bin/sh\necho 'authentication required' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	store, _ := NewStore("")
	engine := NewEngine(store, 1)
	workspace, _ := engine.CreateWorkspace("failure", "test", "")
	conversation := Conversation{ID: NewID("CONV"), WorkspaceID: workspace.ID, Title: "Failure", Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := store.CreateConversation(conversation); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"content": "hello", "runtime_id": "grok"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+conversation.ID+"/message", bytes.NewReader(payload))
	res := httptest.NewRecorder()
	NewAPIServer(engine).Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("message status = %d, body=%s", res.Code, res.Body.String())
	}
	updated := store.Snapshot().Conversations[0]
	if len(updated.Messages) != 2 || !strings.Contains(updated.Messages[1].Content, "authentication required") {
		t.Fatalf("failure reply was not persisted: %+v", updated.Messages)
	}
}
