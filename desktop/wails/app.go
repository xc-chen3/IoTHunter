package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	core "github.com/iothunter/iothunter/internal/core"
)

// App is the Wails application service boundary. The React frontend can use
// these typed bindings directly; the HTTP API remains available for remote
// workers and compatibility with the Electron shell during migration.
type App struct {
	ctx    context.Context
	engine *core.Engine
	server *http.Server
	apiURL string
	mu     sync.RWMutex
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	root, err := os.UserConfigDir()
	if err != nil {
		root, _ = filepath.Abs(".")
	}
	// A packaged Wails binary normally lives beside a capability-workers
	// resource directory. Set the worker root before constructing the Engine so
	// Python capabilities work regardless of the process working directory.
	for _, candidate := range []string{filepath.Dir(os.Args[0]), filepath.Join(filepath.Dir(os.Args[0]), ".."), filepath.Join(root, "IoTHunter")} {
		if _, err := os.Stat(filepath.Join(candidate, "capability-workers", "knowledge", "worker.py")); err == nil {
			_ = os.Setenv("IOTHUNTER_WORKER_ROOT", candidate)
			break
		}
	}
	store, err := core.NewStore(filepath.Join(root, "IoTHunter", "state.db"))
	if err != nil {
		return
	}
	a.engine = core.NewEngine(store, 4)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	a.mu.Lock()
	a.apiURL = "http://" + listener.Addr().String()
	a.server = &http.Server{Handler: core.NewAPIServer(a.engine).Handler()}
	server := a.server
	a.mu.Unlock()
	go func() { _ = server.Serve(listener) }()
}

func (a *App) Health() map[string]string {
	return map[string]string{"status": "ok", "service": "iothunter", "transport": "wails-binding"}
}

func (a *App) APIBase() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.apiURL
}

func (a *App) Workspaces() []core.Workspace {
	if a.engine == nil {
		return []core.Workspace{}
	}
	return a.engine.Store.Snapshot().Workspaces
}

func (a *App) CreateWorkspace(name, description string) (core.Workspace, error) {
	if a.engine == nil {
		return core.Workspace{}, fmt.Errorf("application is not initialized")
	}
	return a.engine.CreateWorkspace(name, "local", description)
}

func (a *App) Snapshot() core.State {
	if a.engine == nil {
		return core.State{Version: 1}
	}
	return a.engine.Store.Snapshot()
}

func (a *App) CreateTarget(workspaceID string, target core.Target) (core.Target, error) {
	if a.engine == nil {
		return core.Target{}, fmt.Errorf("application is not initialized")
	}
	return a.engine.CreateTarget(workspaceID, target)
}

func (a *App) CreateConversation(workspaceID, title, content string) (core.Conversation, error) {
	if a.engine == nil {
		return core.Conversation{}, fmt.Errorf("application is not initialized")
	}
	if _, ok := a.engine.Store.Workspace(workspaceID); !ok {
		return core.Conversation{}, fmt.Errorf("workspace not found")
	}
	if strings.TrimSpace(title) == "" {
		title = "New IoT research conversation"
	}
	created := time.Now().UTC()
	conversation := core.Conversation{ID: core.NewID("CONV"), WorkspaceID: workspaceID, Title: title, Status: "active", CreatedAt: created, UpdatedAt: created}
	if strings.TrimSpace(content) != "" {
		conversation.Messages = []core.ConversationMessage{{ID: core.NewID("MSG"), Role: "user", Content: content, CreatedAt: created}}
	}
	return conversation, a.engine.Store.CreateConversation(conversation)
}

func (a *App) SubmitCapabilityTask(workspaceID, targetID, capabilityID, objective string, inputs map[string]any) (core.Task, error) {
	if a.engine == nil {
		return core.Task{}, fmt.Errorf("application is not initialized")
	}
	return a.engine.SubmitCapabilityTaskWithInputs(context.Background(), workspaceID, targetID, capabilityID, objective, core.PermissionSet{Filesystem: "workspace-readonly"}, core.Budget{MaxRuntimeSeconds: 300, MaxToolCalls: 10}, inputs)
}

func (a *App) ControlTask(taskID, action string) (core.Task, error) {
	if a.engine == nil {
		return core.Task{}, fmt.Errorf("application is not initialized")
	}
	return a.engine.ControlTask(taskID, action, "wails-ui")
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	server := a.server
	a.server = nil
	a.mu.Unlock()
	if server != nil {
		_ = server.Close()
	}
	if a.engine != nil {
		_ = a.engine.Store.Close()
	}
}
