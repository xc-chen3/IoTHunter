package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	peripheralspkg "github.com/iothunter/iothunter/internal/peripherals"
	toolspkg "github.com/iothunter/iothunter/internal/tools"
)

type APIServer struct{ Engine *Engine }

func NewAPIServer(engine *Engine) *APIServer { return &APIServer{Engine: engine} }

func (a *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.root)
	mux.HandleFunc("/healthz", a.health)
	mux.HandleFunc("/api/v1/capabilities", a.capabilities)
	mux.HandleFunc("/api/v1/capabilities/", a.capabilityRoutes)
	mux.HandleFunc("/api/v1/tools", a.tools)
	mux.HandleFunc("/api/v1/tools/run", a.toolRun)
	mux.HandleFunc("/api/v1/agents", a.agents)
	mux.HandleFunc("/api/v1/agents/", a.agentRoutes)
	mux.HandleFunc("/api/v1/models", a.models)
	mux.HandleFunc("/api/v1/models/", a.modelRoutes)
	mux.HandleFunc("/api/v1/prompts", a.prompts)
	mux.HandleFunc("/api/v1/prompts/", a.promptRoutes)
	mux.HandleFunc("/api/v1/runtimes", a.runtimes)
	mux.HandleFunc("/api/v1/runtimes/", a.runtimeRoutes)
	mux.HandleFunc("/api/v1/skills", a.skills)
	mux.HandleFunc("/api/v1/skills/", a.skillRoutes)
	mux.HandleFunc("/api/v1/knowledge", a.knowledge)
	mux.HandleFunc("/api/v1/conversations", a.conversations)
	mux.HandleFunc("/api/v1/conversations/", a.conversationRoutes)
	mux.HandleFunc("/api/v1/peripherals", a.peripherals)
	mux.HandleFunc("/api/v1/peripherals/", a.peripheralRoutes)
	mux.HandleFunc("/api/v1/peripheral-sessions", a.peripheralSessionRoutes)
	mux.HandleFunc("/api/v1/peripheral-sessions/", a.peripheralSessionRoutes)
	mux.HandleFunc("/api/v1/telemetry", a.telemetry)
	mux.HandleFunc("/api/v1/events", a.events)
	mux.HandleFunc("/api/v1/audit", a.audit)
	mux.HandleFunc("/api/v1/workspaces", a.workspaces)
	mux.HandleFunc("/api/v1/workspaces/", a.workspaceRoutes)
	mux.HandleFunc("/api/v1/iot/", a.iotRoutes)
	mux.HandleFunc("/api/v1/findings/", a.findingRoutes)
	mux.HandleFunc("/api/v1/approvals", a.approvalRoutes)
	mux.HandleFunc("/api/v1/approvals/", a.approvalRoutes)
	mux.HandleFunc("/api/v1/tasks", a.tasks)
	mux.HandleFunc("/api/v1/tasks/", a.taskRoutes)
	mux.HandleFunc("/api/v1/findings", a.findings)
	return loggingMiddleware(corsMiddleware(mux))
}

func (a *APIServer) root(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, fmt.Errorf("route not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"service": "iothunter", "api": "/api/v1", "client": "desktop"})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}
func decode(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

func (a *APIServer) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "iothunter", "version": "0.4.0"})
}
func (a *APIServer) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var capability Capability
		if err := decode(r, &capability); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Engine.RegisterConfiguredCapability(capability); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		_ = a.Engine.audit("capability.registered", "api", "capability", capability.ID, map[string]any{"runtime": capability.Runtime})
		writeJSON(w, http.StatusCreated, capability)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, 200, map[string]any{"capabilities": a.Engine.Capabilities()})
}

func (a *APIServer) capabilityRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/capabilities/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		for _, capability := range a.Engine.Capabilities() {
			if capability.ID == parts[0] {
				writeJSON(w, http.StatusOK, capability)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Errorf("capability %q not found", parts[0]))
		return
	}
	if len(parts) != 2 || parts[1] != "test" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, fmt.Errorf("capability route not found"))
		return
	}
	var in struct {
		Inputs    map[string]any `json:"inputs"`
		Objective string         `json:"objective"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	task := Task{ID: NewID("TEST"), Type: "capability.test", Objective: in.Objective, Permissions: PermissionSet{Filesystem: "workspace-readonly"}, Budget: Budget{MaxRuntimeSeconds: 60, MaxToolCalls: 1}}
	result, err := a.Engine.Request(r.Context(), CapabilityRequest{RequestID: NewID("REQ"), TaskID: task.ID, AgentID: "commander-default", CapabilityID: parts[0], Objective: in.Objective, Inputs: in.Inputs, Permissions: task.Permissions, Budget: task.Budget}, task)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *APIServer) tools(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var in struct {
			Name        string        `json:"name"`
			Path        string        `json:"path"`
			Isolation   string        `json:"isolation"`
			Image       string        `json:"image"`
			Permissions PermissionSet `json:"permissions"`
			TimeoutSecs int           `json:"timeout_seconds"`
			MaxOutput   int           `json:"max_output"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		definition := toolspkg.Definition{Name: in.Name, Path: in.Path, Isolation: in.Isolation, Image: in.Image, Permissions: toolspkg.PermissionSet{Network: in.Permissions.Network, Filesystem: in.Permissions.Filesystem, Destructive: in.Permissions.Destructive}, Timeout: time.Duration(in.TimeoutSecs) * time.Second, MaxOutput: in.MaxOutput}
		if err := a.Engine.RegisterConfiguredTool(definition); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		_ = a.Engine.audit("tool.registered", "api", "tool", in.Name, nil)
		writeJSON(w, http.StatusCreated, map[string]any{"name": in.Name, "path": in.Path})
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": a.Engine.Tools()})
}

func (a *APIServer) toolRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var in struct {
		TaskID      string        `json:"task_id"`
		Tool        string        `json:"tool"`
		Args        []string      `json:"args"`
		WorkingDir  string        `json:"working_dir"`
		TimeoutSecs int           `json:"timeout_seconds"`
		Permissions PermissionSet `json:"permissions"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Tool) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("tool is required"))
		return
	}
	if in.WorkingDir == "" {
		if in.TaskID != "" {
			if task, ok := a.Engine.Store.Task(in.TaskID); ok {
				if workspace, workspaceOK := a.Engine.Store.Workspace(task.WorkspaceID); workspaceOK {
					in.WorkingDir = workspace.Root
				}
			}
		}
		if in.WorkingDir == "" {
			in.WorkingDir = filepath.Dir(a.Engine.Store.path)
		}
	}
	if a.Engine.ToolGateway == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("tool gateway is unavailable"))
		return
	}
	result, err := a.Engine.RunTool(r.Context(), in.TaskID, in.Tool, in.Args, in.WorkingDir, toolspkg.PermissionSet{Network: in.Permissions.Network, Filesystem: in.Permissions.Filesystem, Destructive: in.Permissions.Destructive}, time.Duration(in.TimeoutSecs)*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *APIServer) agents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": a.Engine.Store.Snapshot().Agents})
}

func (a *APIServer) models(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		models := a.Engine.Store.Snapshot().Models
		if models == nil {
			models = []ModelConfig{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var model ModelConfig
	if err := decode(r, &model); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	model.Name, model.Provider, model.Model = strings.TrimSpace(model.Name), strings.TrimSpace(model.Provider), strings.TrimSpace(model.Model)
	if model.Name == "" || model.Provider == "" || model.Model == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name, provider, and model are required"))
		return
	}
	if model.ID == "" {
		model.ID = NewID("MODEL")
	}
	if model.CreatedAt.IsZero() {
		model.CreatedAt = now()
	}
	model.UpdatedAt = now()
	if err := a.Engine.Store.AddModel(model); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.audit("model.registered", "api", "model", model.ID, map[string]any{"provider": model.Provider, "model": model.Model})
	writeJSON(w, http.StatusCreated, model)
}

func (a *APIServer) modelRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/models/")
	if len(parts) != 1 || r.Method != http.MethodGet {
		writeError(w, http.StatusNotFound, fmt.Errorf("model route not found"))
		return
	}
	for _, model := range a.Engine.Store.Snapshot().Models {
		if model.ID == parts[0] {
			writeJSON(w, http.StatusOK, model)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("model not found"))
}

func (a *APIServer) prompts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		prompts := a.Engine.Store.Snapshot().Prompts
		if prompts == nil {
			prompts = []PromptVersion{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"prompts": prompts})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var prompt PromptVersion
	if err := decode(r, &prompt); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	prompt.Name, prompt.Role, prompt.Version, prompt.Content = strings.TrimSpace(prompt.Name), strings.TrimSpace(prompt.Role), strings.TrimSpace(prompt.Version), strings.TrimSpace(prompt.Content)
	if prompt.Name == "" || prompt.Role == "" || prompt.Content == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("name, role, and content are required"))
		return
	}
	if prompt.Version == "" {
		prompt.Version = "1.0.0"
	}
	if prompt.ID == "" {
		prompt.ID = NewID("PROMPT")
	}
	if prompt.SHA256 == "" {
		digest := sha256.Sum256([]byte(prompt.Content))
		prompt.SHA256 = hex.EncodeToString(digest[:])
	}
	if prompt.CreatedAt.IsZero() {
		prompt.CreatedAt = now()
	}
	if err := a.Engine.Store.AddPrompt(prompt); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.audit("prompt.registered", "api", "prompt", prompt.ID, map[string]any{"role": prompt.Role, "version": prompt.Version})
	writeJSON(w, http.StatusCreated, prompt)
}

func (a *APIServer) promptRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/prompts/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		for _, prompt := range a.Engine.Store.Snapshot().Prompts {
			if prompt.ID == parts[0] {
				writeJSON(w, http.StatusOK, prompt)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Errorf("prompt not found"))
		return
	}
	if len(parts) != 2 || parts[1] != "activate" || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, fmt.Errorf("prompt route not found"))
		return
	}
	id := parts[0]
	var selected *PromptVersion
	if err := a.Engine.Store.mutate(func(state *State) error {
		for i := range state.Prompts {
			if state.Prompts[i].ID == id {
				copy := state.Prompts[i]
				selected = &copy
				for j := range state.Prompts {
					if state.Prompts[j].Name == copy.Name {
						state.Prompts[j].Active = state.Prompts[j].ID == id
					}
				}
				return nil
			}
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if selected == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("prompt not found"))
		return
	}
	selected.Active = true
	_ = a.Engine.audit("prompt.activated", "api", "prompt", id, map[string]any{"name": selected.Name})
	writeJSON(w, http.StatusOK, selected)
}

func (a *APIServer) agentRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/agents/")
	if len(parts) != 1 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, fmt.Errorf("agent route not found"))
		return
	}
	var patch struct {
		ModelProvider  string         `json:"model_provider"`
		Model          string         `json:"model"`
		RuntimeID      *string        `json:"runtime_id"`
		Enabled        *bool          `json:"enabled"`
		MaxConcurrency int            `json:"max_concurrency"`
		Permissions    *PermissionSet `json:"permissions"`
	}
	if err := decode(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if patch.RuntimeID != nil && strings.TrimSpace(*patch.RuntimeID) != "" {
		runtimeID := strings.TrimSpace(*patch.RuntimeID)
		spec, ok := localRuntimeSpec(runtimeID)
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown runtime %q", runtimeID))
			return
		}
		if runtimePath(spec) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("runtime %q is not installed", runtimeID))
			return
		}
		patch.ModelProvider = spec.provider
	}
	found := false
	if err := a.Engine.Store.mutate(func(state *State) error {
		for i := range state.Agents {
			if state.Agents[i].ID != parts[0] {
				continue
			}
			found = true
			if patch.ModelProvider != "" {
				state.Agents[i].ModelProvider = patch.ModelProvider
			}
			if patch.Model != "" {
				state.Agents[i].Model = patch.Model
			}
			if patch.RuntimeID != nil {
				state.Agents[i].RuntimeID = strings.TrimSpace(*patch.RuntimeID)
			}
			if patch.Enabled != nil {
				state.Agents[i].Enabled = *patch.Enabled
			}
			if patch.MaxConcurrency > 0 {
				state.Agents[i].MaxConcurrency = patch.MaxConcurrency
			}
			if patch.Permissions != nil {
				state.Agents[i].Permissions = *patch.Permissions
			}
			return nil
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Errorf("agent not found"))
		return
	}
	updated := a.Engine.Store.Snapshot()
	for _, agent := range updated.Agents {
		if agent.ID == parts[0] {
			writeJSON(w, http.StatusOK, agent)
			return
		}
	}
}

func (a *APIServer) runtimes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": discoverLocalRuntimes(r.Context())})
}

func (a *APIServer) runtimeRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/runtimes/")
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, fmt.Errorf("runtime route not found"))
		return
	}
	runtimeID := parts[0]
	spec, ok := localRuntimeSpec(runtimeID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("runtime %q not found", runtimeID))
		return
	}
	switch parts[1] {
	case "run":
		var in struct {
			Prompt string `json:"prompt"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Prompt) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("prompt is required"))
			return
		}
		result := RunLocalRuntime(r.Context(), runtimeID, in.Prompt)
		if result.Status != "completed" {
			writeJSON(w, http.StatusBadGateway, result)
			return
		}
		_ = a.Engine.audit("runtime.invoked", "api", "runtime", runtimeID, map[string]any{"output_bytes": len(result.Output)})
		writeJSON(w, http.StatusOK, result)
	case "check":
		var found *LocalRuntime
		for _, item := range discoverLocalRuntimes(r.Context()) {
			if item.ID == runtimeID {
				copy := item
				found = &copy
				break
			}
		}
		if found == nil {
			writeError(w, http.StatusNotFound, fmt.Errorf("runtime %q not found", runtimeID))
			return
		}
		writeJSON(w, http.StatusOK, found)
	case "probe":
		runtime, output, err := probeLocalRuntime(r.Context(), runtimeID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"runtime": runtime, "error": safeRuntimeError(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runtime": runtime, "output": output, "note": "help output only; no model session was started"})
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unsupported runtime action %q for %s", parts[1], spec.name))
	}
}

func (a *APIServer) skills(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var skill Skill
		if err := decode(r, &skill); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(skill.Name) == "" || len(skill.Steps) == 0 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("skill name and at least one step are required"))
			return
		}
		if skill.ID == "" {
			skill.ID = NewID("SKILL")
		}
		if skill.Version == "" {
			skill.Version = "1.0.0"
		}
		if skill.Permissions.Filesystem == "" {
			skill.Permissions.Filesystem = "workspace-readonly"
		}
		skill.Enabled = true
		if err := a.Engine.Store.mutate(func(state *State) error {
			for i := range state.Skills {
				if state.Skills[i].ID == skill.ID {
					state.Skills[i] = skill
					return nil
				}
			}
			state.Skills = append(state.Skills, skill)
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.audit("skill.registered", "api", "skill", skill.ID, nil)
		writeJSON(w, http.StatusCreated, skill)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": a.Engine.Store.Snapshot().Skills})
}

func (a *APIServer) skillRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/skills/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		for _, skill := range a.Engine.Store.Snapshot().Skills {
			if skill.ID == parts[0] {
				writeJSON(w, http.StatusOK, skill)
				return
			}
		}
		writeError(w, http.StatusNotFound, fmt.Errorf("skill not found"))
		return
	}
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		var in struct {
			WorkspaceID string         `json:"workspace_id"`
			TargetID    string         `json:"target_id"`
			Objective   string         `json:"objective"`
			Inputs      map[string]any `json:"inputs"`
			Permissions PermissionSet  `json:"permissions"`
			Budget      Budget         `json:"budget"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var skill *Skill
		for _, candidate := range a.Engine.Store.Snapshot().Skills {
			if candidate.ID == parts[0] {
				copy := candidate
				skill = &copy
				break
			}
		}
		if skill == nil {
			writeError(w, http.StatusNotFound, fmt.Errorf("skill not found"))
			return
		}
		if !skill.Enabled {
			writeError(w, http.StatusConflict, fmt.Errorf("skill is disabled"))
			return
		}
		if strings.TrimSpace(in.WorkspaceID) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("workspace_id is required"))
			return
		}
		if in.Permissions.Filesystem == "" {
			in.Permissions = skill.Permissions
		}
		task, err := a.Engine.SubmitCapabilityPlan(r.Context(), in.WorkspaceID, in.TargetID, skill.Steps, in.Objective, in.Permissions, in.Budget, in.Inputs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"task": task, "skill_id": skill.ID})
		return
	}
	writeError(w, http.StatusNotFound, fmt.Errorf("skill route not found"))
}

func (a *APIServer) knowledge(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"knowledge": a.Engine.Store.Snapshot().Knowledge})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var item KnowledgeItem
	if err := decode(r, &item); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(item.Title) == "" || strings.TrimSpace(item.Kind) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("kind and title are required"))
		return
	}
	item.ID, item.CreatedAt = NewID("KNOW"), now()
	if item.Content == nil {
		item.Content = map[string]any{}
	}
	if err := a.Engine.Store.AddKnowledge(item); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *APIServer) conversations(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		workspaceID := r.URL.Query().Get("workspace_id")
		items := a.Engine.Store.Snapshot().Conversations
		if workspaceID != "" {
			filtered := make([]Conversation, 0, len(items))
			for _, item := range items {
				if item.WorkspaceID == workspaceID {
					filtered = append(filtered, item)
				}
			}
			items = filtered
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversations": items})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var in struct {
		WorkspaceID string `json:"workspace_id"`
		Title       string `json:"title"`
		Content     string `json:"content"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.WorkspaceID) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workspace_id is required"))
		return
	}
	if _, ok := a.Engine.Store.Workspace(in.WorkspaceID); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("workspace not found"))
		return
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "New IoT research conversation"
	}
	c := Conversation{ID: NewID("CONV"), WorkspaceID: in.WorkspaceID, Title: title, Status: "active", CreatedAt: now(), UpdatedAt: now()}
	if strings.TrimSpace(in.Content) != "" {
		c.Messages = append(c.Messages, ConversationMessage{ID: NewID("MSG"), Role: "user", Content: in.Content, CreatedAt: now()})
	}
	if err := a.Engine.Store.CreateConversation(c); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.audit("conversation.created", "api", "conversation", c.ID, map[string]any{"workspace_id": c.WorkspaceID})
	writeJSON(w, http.StatusCreated, c)
}

func (a *APIServer) conversationRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/conversations/")
	if len(parts) < 1 || len(parts) > 2 {
		writeError(w, http.StatusNotFound, fmt.Errorf("conversation route not found"))
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		a.streamConversationEvents(w, r, parts[0])
		return
	}
	items := a.Engine.Store.Snapshot().Conversations
	var current *Conversation
	for i := range items {
		if items[i].ID == parts[0] {
			copy := items[i]
			current = &copy
			break
		}
	}
	if current == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("conversation not found"))
		return
	}
	if len(parts) == 2 && parts[1] == "message" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			Content      string              `json:"content"`
			CreateTask   bool                `json:"create_task"`
			RunRuntime   bool                `json:"run_runtime"`
			RuntimeID    string              `json:"runtime_id"`
			TargetID     string              `json:"target_id"`
			CapabilityID string              `json:"capability_id"`
			References   map[string][]string `json:"references"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.Content) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("content is required"))
			return
		}
		if in.CapabilityID == "" {
			in.CapabilityID = "target.fingerprint"
		}
		var runtimeResult *RuntimeResult
		if in.RunRuntime && !in.CreateTask {
			runtimeID := strings.TrimSpace(in.RuntimeID)
			if runtimeID == "" {
				for _, agent := range a.Engine.Store.Snapshot().Agents {
					if agent.ID == "commander-default" {
						runtimeID = agent.RuntimeID
						break
					}
				}
			}
			if runtimeID == "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("runtime_id is required when run_runtime is enabled"))
				return
			}
			result := RunLocalRuntime(r.Context(), runtimeID, in.Content)
			runtimeResult = &result
		}
		var createdTask *Task
		if in.CreateTask {
			if in.TargetID == "" {
				for _, target := range a.Engine.Store.Snapshot().Targets {
					if target.WorkspaceID == current.WorkspaceID {
						in.TargetID = target.ID
						break
					}
				}
			}
			if in.TargetID == "" {
				writeError(w, http.StatusBadRequest, fmt.Errorf("target_id is required to create a task"))
				return
			}
			task, err := a.Engine.SubmitCapabilityTask(r.Context(), current.WorkspaceID, in.TargetID, in.CapabilityID, in.Content, PermissionSet{Filesystem: "workspace-readonly"}, Budget{MaxRuntimeSeconds: 300, MaxToolCalls: 10})
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			createdTask = &task
			_ = a.Engine.Store.UpdateTask(task.ID, func(v *Task) error { v.ConversationID = current.ID; return nil })
			if linked, ok := a.Engine.Store.Task(task.ID); ok {
				createdTask = &linked
			}
		}
		if err := a.Engine.Store.UpdateConversation(current.ID, func(c *Conversation) error {
			c.Messages = append(c.Messages, ConversationMessage{ID: NewID("MSG"), Role: "user", Content: in.Content, References: in.References, CreatedAt: now()})
			if createdTask != nil {
				c.TaskIDs = append(c.TaskIDs, createdTask.ID)
			}
			reply := "Commander received the request."
			if runtimeResult != nil {
				if runtimeResult.Status == "completed" {
					reply = runtimeResult.Output
				} else {
					reply = "Runtime execution failed: " + runtimeResult.Error
				}
			}
			if createdTask != nil {
				reply = fmt.Sprintf("Task %s was created and queued. I will report node output and the final summary here.", createdTask.ID)
			}
			c.Messages = append(c.Messages, ConversationMessage{ID: NewID("MSG"), Role: "assistant", Content: reply, CreatedAt: now()})
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "conversation.message.created", WorkspaceID: current.WorkspaceID, ConversationID: current.ID, TaskID: func() string {
			if createdTask != nil {
				return createdTask.ID
			}
			return ""
		}(), Payload: map[string]any{"role": "user", "content_bytes": len(in.Content)}, CreatedAt: now()})
		updated := a.Engine.Store.Snapshot().Conversations
		for _, c := range updated {
			if c.ID == current.ID {
				writeJSON(w, http.StatusOK, map[string]any{"conversation": c, "task": createdTask, "runtime": runtimeResult})
				return
			}
		}
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, fmt.Errorf("conversation route not found"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, current)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var in struct {
		Title      *string             `json:"title"`
		Status     *string             `json:"status"`
		Content    string              `json:"content"`
		Role       string              `json:"role"`
		References map[string][]string `json:"references"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := a.Engine.Store.UpdateConversation(current.ID, func(c *Conversation) error {
		if in.Title != nil && strings.TrimSpace(*in.Title) != "" {
			c.Title = strings.TrimSpace(*in.Title)
		}
		if in.Status != nil {
			c.Status = strings.TrimSpace(*in.Status)
		}
		if strings.TrimSpace(in.Content) != "" {
			role := in.Role
			if role == "" {
				role = "user"
			}
			c.Messages = append(c.Messages, ConversationMessage{ID: NewID("MSG"), Role: role, Content: in.Content, References: in.References, CreatedAt: now()})
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "conversation.updated", WorkspaceID: current.WorkspaceID, ConversationID: current.ID, Payload: map[string]any{"content_bytes": len(in.Content)}, CreatedAt: now()})
	updated := a.Engine.Store.Snapshot().Conversations
	for _, c := range updated {
		if c.ID == current.ID {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
}

func (a *APIServer) streamConversationEvents(w http.ResponseWriter, r *http.Request, conversationID string) {
	var current *Conversation
	for _, conversation := range a.Engine.Store.Snapshot().Conversations {
		if conversation.ID == conversationID {
			copy := conversation
			current = &copy
			break
		}
	}
	if current == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("conversation not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is unsupported by this server"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	last := 0
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		events := a.Engine.Store.Snapshot().Events
		matching := make([]Event, 0)
		for _, event := range events {
			if event.ConversationID == conversationID {
				matching = append(matching, event)
			}
		}
		for last < len(matching) {
			payload, _ := json.Marshal(matching[last])
			_, _ = fmt.Fprintf(w, "id: %s\ndata: %s\n\n", matching[last].ID, payload)
			flusher.Flush()
			last++
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (a *APIServer) peripherals(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		workspaceID := r.URL.Query().Get("workspace_id")
		items := a.Engine.Store.Snapshot().Peripherals
		if workspaceID != "" {
			items = filterPeripherals(items, workspaceID)
		}
		writeJSON(w, http.StatusOK, map[string]any{"peripherals": items})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var p Peripheral
	if err := decode(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(p.WorkspaceID) == "" || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Kind) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("workspace_id, name, and kind are required"))
		return
	}
	if _, ok := a.Engine.Store.Workspace(p.WorkspaceID); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("workspace not found"))
		return
	}
	if err := peripheralspkg.ValidateConfig(p.Kind, p.Config); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := peripheralspkg.ValidateSafetyLimits(p.SafetyLimits); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	p.ID, p.Status, p.UpdatedAt = NewID("PER"), "offline", now()
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	if p.SafetyLimits == nil {
		p.SafetyLimits = map[string]any{}
	}
	if err := a.Engine.Store.CreatePeripheral(p); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.audit("peripheral.created", "api", "peripheral", p.ID, map[string]any{"workspace_id": p.WorkspaceID})
	writeJSON(w, http.StatusCreated, p)
}

func (a *APIServer) peripheralRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/peripherals/")
	if len(parts) == 1 && parts[0] == "discover" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		kind := r.URL.Query().Get("kind")
		if a.Engine.Peripherals == nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
			return
		}
		items, err := a.Engine.Peripherals.Discover(r.Context(), kind)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"peripherals": items})
		return
	}
	if len(parts) == 2 && parts[1] == "connect" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		p, ok := findPeripheral(a.Engine.Store.Snapshot().Peripherals, parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("peripheral not found"))
			return
		}
		if a.Engine.Peripherals == nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
			return
		}
		var in struct {
			OwnerType string `json:"owner_type"`
			OwnerID   string `json:"owner_id"`
			Mode      string `json:"mode"`
			LeaseTTL  int    `json:"lease_ttl_seconds"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		session, err := a.Engine.Peripherals.Connect(r.Context(), peripheralspkg.OpenRequest{
			WorkspaceID: p.WorkspaceID, PeripheralID: p.ID, Kind: p.Kind, Endpoint: peripheralspkg.Endpoint{Address: p.Address, Port: p.Port}, Config: p.Config,
			OwnerType: in.OwnerType, OwnerID: in.OwnerID, Mode: in.Mode, LeaseTTL: time.Duration(in.LeaseTTL) * time.Second,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		occupied := session.OwnerType + "/" + session.OwnerID
		_ = a.Engine.Store.UpdatePeripheral(p.ID, func(value *Peripheral) error {
			connectedAt := now()
			value.Status, value.OccupiedBy, value.ConnectedAt = "connected", occupied, &connectedAt
			if value.ActiveConfig == nil {
				value.ActiveConfig = cloneMap(value.Config)
			}
			return nil
		})
		_ = a.Engine.audit("peripheral.connected", session.OwnerID, "peripheral", p.ID, map[string]any{"session_id": session.ID, "mode": session.Mode})
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.connected", WorkspaceID: p.WorkspaceID, Payload: map[string]any{"peripheral_id": p.ID, "session_id": session.ID}, CreatedAt: now()})
		updated, _ := findPeripheral(a.Engine.Store.Snapshot().Peripherals, p.ID)
		writeJSON(w, http.StatusOK, map[string]any{"session": session, "peripheral": updated})
		return
	}
	if len(parts) == 2 && parts[1] == "schema" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		p, ok := findPeripheral(a.Engine.Store.Snapshot().Peripherals, parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("peripheral not found"))
			return
		}
		writeJSON(w, http.StatusOK, peripheralConfigSchema(p.Kind))
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, fmt.Errorf("peripheral route not found"))
		return
	}
	p, ok := findPeripheral(a.Engine.Store.Snapshot().Peripherals, parts[0])
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("peripheral not found"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, p)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var patch struct {
		Status       *string        `json:"status"`
		OccupiedBy   *string        `json:"occupied_by"`
		Config       map[string]any `json:"config"`
		SafetyLimits map[string]any `json:"safety_limits"`
	}
	if err := decode(r, &patch); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if patch.Config != nil {
		if err := peripheralspkg.ValidateConfig(p.Kind, patch.Config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := validatePeripheralConfigRecord(p, patch.Config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if patch.SafetyLimits != nil {
		if err := peripheralspkg.ValidateSafetyLimits(patch.SafetyLimits); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := a.Engine.Store.UpdatePeripheral(p.ID, func(v *Peripheral) error {
		if patch.Status != nil {
			v.Status = strings.TrimSpace(*patch.Status)
			if v.Status == "connected" {
				v.ConnectedAt = timePtr(now())
			}
		}
		if patch.OccupiedBy != nil {
			v.OccupiedBy = strings.TrimSpace(*patch.OccupiedBy)
		}
		if patch.Config != nil {
			v.Config = patch.Config
		}
		if patch.SafetyLimits != nil {
			v.SafetyLimits = patch.SafetyLimits
		}
		return nil
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	updated, _ := findPeripheral(a.Engine.Store.Snapshot().Peripherals, p.ID)
	_ = a.Engine.audit("peripheral.updated", "api", "peripheral", p.ID, nil)
	writeJSON(w, http.StatusOK, updated)
}

func peripheralConfigSchema(kind string) map[string]any {
	switch strings.ToLower(kind) {
	case "serial", "uart":
		return map[string]any{"kind": "object", "properties": map[string]any{
			"baud_rate":       map[string]any{"type": "integer", "minimum": 50, "maximum": 4000000, "default": 115200},
			"data_bits":       map[string]any{"type": "integer", "enum": []int{5, 6, 7, 8}, "default": 8},
			"stop_bits":       map[string]any{"type": "integer", "enum": []int{1, 2, 3}, "default": 1},
			"parity":          map[string]any{"type": "string", "enum": []string{"none", "odd", "even"}, "default": "none"},
			"read_timeout_ms": map[string]any{"type": "integer", "minimum": 0, "maximum": 60000, "default": 1000},
		}}
	case "tcp", "scpi", "power", "scope", "jlink", "bluetooth", "packet":
		properties := map[string]any{
			"port":               map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"connect_timeout_ms": map[string]any{"type": "integer", "minimum": 100, "maximum": 60000, "default": 5000},
			"read_timeout_ms":    map[string]any{"type": "integer", "minimum": 0, "maximum": 60000, "default": 1000},
		}
		if strings.EqualFold(kind, "power") || strings.EqualFold(kind, "scpi") {
			properties["channel"] = map[string]any{"type": "string", "default": "CH1"}
			properties["voltage"] = map[string]any{"type": "number", "minimum": 0, "maximum": 1000}
			properties["current"] = map[string]any{"type": "number", "minimum": 0, "maximum": 100}
		}
		if strings.EqualFold(kind, "scope") {
			properties["sample_rate"] = map[string]any{"type": "number", "minimum": 1, "maximum": 10000000000}
			properties["timebase"] = map[string]any{"type": "string"}
			properties["trigger"] = map[string]any{"type": "string"}
		}
		if strings.EqualFold(kind, "jlink") {
			properties["interface"] = map[string]any{"type": "string", "enum": []string{"SWD", "JTAG"}, "default": "SWD"}
			properties["clock_khz"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100000}
			properties["target"] = map[string]any{"type": "string"}
		}
		if strings.EqualFold(kind, "bluetooth") {
			properties["channel"] = map[string]any{"type": "integer", "minimum": 0, "maximum": 39}
			properties["phy"] = map[string]any{"type": "string"}
			properties["filter"] = map[string]any{"type": "string"}
		}
		return map[string]any{"kind": "object", "properties": properties}
	default:
		return map[string]any{"kind": "object", "properties": map[string]any{}}
	}
}

func (a *APIServer) peripheralSessionRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/peripheral-sessions")
	rest = strings.Trim(rest, "/")
	var parts []string
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	if len(parts) == 0 && r.Method == http.MethodGet {
		if a.Engine.Peripherals == nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessions": a.Engine.Peripherals.Sessions()})
		return
	}
	if len(parts) == 3 && parts[1] == "telemetry" && parts[2] == "stream" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		a.streamPeripheralTelemetry(w, r, parts[0])
		return
	}
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, fmt.Errorf("peripheral session route not found"))
		return
	}
	if a.Engine.Peripherals == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
		return
	}
	var session *peripheralspkg.Session
	for _, item := range a.Engine.Peripherals.Sessions() {
		if item.ID == parts[0] {
			copy := item
			session = &copy
			break
		}
	}
	if session == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("peripheral session not found"))
		return
	}
	p, _ := findPeripheral(a.Engine.Store.Snapshot().Peripherals, session.PeripheralID)
	switch parts[1] {
	case "disconnect":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		if err := a.Engine.Peripherals.Disconnect(r.Context(), session.ID); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		_ = a.Engine.Store.UpdatePeripheral(p.ID, func(value *Peripheral) error {
			value.Status, value.OccupiedBy, value.ConnectedAt = "offline", "", nil
			return nil
		})
		_ = a.Engine.audit("peripheral.disconnected", session.OwnerID, "peripheral", p.ID, map[string]any{"session_id": session.ID})
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.disconnected", WorkspaceID: p.WorkspaceID, Payload: map[string]any{"peripheral_id": p.ID, "session_id": session.ID}, CreatedAt: now()})
		writeJSON(w, http.StatusOK, map[string]any{"status": "disconnected", "peripheral_id": p.ID})
	case "config":
		if r.Method == http.MethodGet {
			config := p.Config
			if config == nil {
				config = map[string]any{}
			}
			if live, err := a.Engine.Peripherals.GetConfig(r.Context(), session.ID); err == nil && live != nil {
				config = live
			}
			writeJSON(w, http.StatusOK, map[string]any{"session": *session, "config": config})
			return
		}
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var config map[string]any
		if err := decode(r, &config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Engine.validatePeripheralConfig(session.ID, config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Engine.Peripherals.ApplyConfig(r.Context(), session.ID, config); err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		_ = a.Engine.Store.UpdatePeripheral(p.ID, func(value *Peripheral) error {
			value.Config = config
			value.ActiveConfig = cloneMap(config)
			return nil
		})
		_ = a.Engine.audit("peripheral.config.applied", session.OwnerID, "peripheral", p.ID, map[string]any{"session_id": session.ID})
		writeJSON(w, http.StatusOK, map[string]any{"session": *session, "config": config})
	case "invoke":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		command := strings.ToLower(strings.TrimSpace(in.Command))
		if command == "set_voltage" || command == "set_current" || command == "output" || command == "cycle" {
			capabilityID := "power." + command
			if err := a.Engine.validatePowerCommand(session.ID, capabilityID, in.Args); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
		}
		result, err := a.Engine.Peripherals.Invoke(r.Context(), peripheralspkg.InvokeRequest{SessionID: session.ID, Command: command, Args: in.Args})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		_ = a.Engine.audit("peripheral.command.completed", session.OwnerID, "peripheral", p.ID, map[string]any{"session_id": session.ID, "command": command})
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "peripheral.command.completed", WorkspaceID: p.WorkspaceID, Payload: map[string]any{"peripheral_id": p.ID, "session_id": session.ID, "command": command}, CreatedAt: now()})
		writeJSON(w, http.StatusOK, result)
	case "telemetry":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		events, err := a.Engine.Peripherals.Telemetry(session.ID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": *session, "events": events, "status": "ok"})
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("unsupported peripheral session action %q", parts[1]))
	}
}

func (a *APIServer) streamPeripheralTelemetry(w http.ResponseWriter, r *http.Request, sessionID string) {
	if a.Engine.Peripherals == nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
		return
	}
	session, err := a.Engine.Peripherals.Session(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is unsupported by this server"))
		return
	}
	topics := r.URL.Query()["topic"]
	channel, err := a.Engine.Peripherals.Subscribe(r.Context(), sessionID, topics)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	write := func(value peripheralspkg.Telemetry) {
		payload, _ := json.Marshal(map[string]any{"session": session, "telemetry": value})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
	if history, historyErr := a.Engine.Peripherals.Telemetry(sessionID); historyErr == nil {
		for _, value := range history {
			write(value)
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case value, open := <-channel:
			if !open {
				return
			}
			write(value)
		}
	}
}

func (a *APIServer) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	items := a.Engine.Store.Snapshot().Events
	if taskID := strings.TrimSpace(r.URL.Query().Get("task_id")); taskID != "" {
		filtered := make([]Event, 0)
		for _, event := range items {
			if event.TaskID == taskID {
				filtered = append(filtered, event)
			}
		}
		items = filtered
	}
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id")); workspaceID != "" {
		items = filterEvents(items, workspaceID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": items})
}

// telemetry returns the durable projection of peripheral samples. Live
// session streams remain available under /peripheral-sessions/{id}/telemetry.
func (a *APIServer) telemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	items := a.Engine.Store.Snapshot().Telemetry
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id")); workspaceID != "" {
		items = filterTelemetry(items, workspaceID)
	}
	if peripheralID := strings.TrimSpace(r.URL.Query().Get("peripheral_id")); peripheralID != "" {
		filtered := make([]TelemetryRecord, 0, len(items))
		for _, item := range items {
			if item.PeripheralID == peripheralID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if sessionID := strings.TrimSpace(r.URL.Query().Get("session_id")); sessionID != "" {
		filtered := make([]TelemetryRecord, 0, len(items))
		for _, item := range items {
			if item.SessionID == sessionID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if topic := strings.TrimSpace(r.URL.Query().Get("topic")); topic != "" {
		filtered := make([]TelemetryRecord, 0, len(items))
		for _, item := range items {
			if item.Topic == topic {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 1000 {
		limit = 1000
	}
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"telemetry": items, "count": len(items)})
}

func (a *APIServer) audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": a.Engine.Store.Snapshot().Audit})
}

// iotRoutes exposes device-centric projections for the desktop client and
// integrations. The canonical records remain in the workspace state model.
func (a *APIServer) iotRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	st := a.Engine.Store.Snapshot()
	switch r.URL.Path {
	case "/api/v1/iot/summary":
		activeTasks := 0
		for _, task := range st.Tasks {
			if task.Status == TaskQueued || task.Status == TaskAssigned || task.Status == TaskRunning {
				activeTasks++
			}
		}
		connectedPeripherals := 0
		for _, peripheral := range st.Peripherals {
			if peripheral.Status == "connected" {
				connectedPeripherals++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"targets": len(st.Targets), "devices": len(st.Targets), "peripherals": len(st.Peripherals), "connected_devices": connectedPeripherals, "connected_peripherals": connectedPeripherals, "active_tasks": activeTasks, "findings": len(st.Findings), "artifacts": len(st.Artifacts), "evidence": len(st.Evidence)})
	case "/api/v1/iot/devices":
		writeJSON(w, http.StatusOK, map[string]any{"devices": st.Targets})
	case "/api/v1/iot/peripherals":
		writeJSON(w, http.StatusOK, map[string]any{"peripherals": st.Peripherals})
	case "/api/v1/iot/vulnerabilities":
		writeJSON(w, http.StatusOK, map[string]any{"vulnerabilities": st.Findings})
	case "/api/v1/iot/artifacts":
		writeJSON(w, http.StatusOK, map[string]any{"artifacts": st.Artifacts})
	default:
		writeError(w, http.StatusNotFound, fmt.Errorf("iot route not found"))
	}
}

func (a *APIServer) workspaces(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/workspaces" {
		writeError(w, 404, fmt.Errorf("not found"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"workspaces": a.Engine.Store.Snapshot().Workspaces})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	var in struct {
		Name        string `json:"name"`
		Owner       string `json:"owner"`
		Description string `json:"description"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, fmt.Errorf("name is required"))
		return
	}
	if in.Owner == "" {
		in.Owner = "local"
	}
	workspace, err := a.Engine.CreateWorkspace(in.Name, in.Owner, in.Description)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	_ = a.Engine.Store.AddAudit(AuditLog{ID: NewID("AUD"), Action: "workspace.created", Actor: in.Owner, ResourceType: "workspace", ResourceID: workspace.ID, CreatedAt: now()})
	writeJSON(w, 201, workspace)
}

func (a *APIServer) workspaceRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/workspaces/")
	if len(parts) == 0 {
		writeError(w, 404, fmt.Errorf("workspace id required"))
		return
	}
	id := parts[0]
	if _, ok := a.Engine.Store.Workspace(id); !ok {
		writeError(w, 404, fmt.Errorf("workspace not found"))
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		st := a.Engine.Store.Snapshot()
		writeJSON(w, 200, map[string]any{
			"workspace": mustWorkspace(a.Engine.Store, id),
			"targets":   filterTargets(st.Targets, id), "devices": filterDevices(st.Devices, id),
			"peripherals": filterPeripherals(st.Peripherals, id), "attachments": filterAttachments(st.Attachments, id),
			"conversations": filterConversations(st.Conversations, id), "captures": filterCaptures(st.Captures, id),
			"telemetry": filterTelemetry(st.Telemetry, id),
			"tasks":     filterTasks(st.Tasks, id), "findings": filterFindings(st.Findings, id),
			"evidence": filterEvidence(st.Evidence, filterFindings(st.Findings, id)), "artifacts": filterArtifacts(st.Artifacts, id),
			"approvals": filterApprovals(st.Approvals, filterTasks(st.Tasks, id)),
			"events":    filterEvents(st.Events, id), "audit": st.Audit,
			"agent_runs": st.AgentRuns, "capability_runs": st.CapabilityRuns, "tool_runs": st.ToolRuns, "gate_decisions": st.Gates,
			"agents": st.Agents, "skills": st.Skills, "knowledge": filterKnowledge(st.Knowledge, id),
		})
		return
	}
	if len(parts) == 2 && parts[1] == "targets" {
		a.targets(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "devices" {
		a.devices(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "peripherals" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"peripherals": filterPeripherals(a.Engine.Store.Snapshot().Peripherals, id)})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var p Peripheral
		if err := decode(r, &p); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.WorkspaceID = id
		if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Kind) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("name and kind are required"))
			return
		}
		if err := peripheralspkg.ValidateConfig(p.Kind, p.Config); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := peripheralspkg.ValidateSafetyLimits(p.SafetyLimits); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.ID, p.Status, p.UpdatedAt = NewID("PER"), "offline", now()
		if p.Config == nil {
			p.Config = map[string]any{}
		}
		if p.SafetyLimits == nil {
			p.SafetyLimits = map[string]any{}
		}
		if err := a.Engine.Store.CreatePeripheral(p); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.audit("peripheral.created", "api", "peripheral", p.ID, map[string]any{"workspace_id": id})
		writeJSON(w, http.StatusCreated, p)
		return
	}
	if len(parts) == 2 && parts[1] == "artifacts" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"artifacts": filterArtifacts(a.Engine.Store.Snapshot().Artifacts, id)})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			Path string `json:"path"`
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		artifact, err := a.Engine.ImportArtifact(id, in.Path, in.Name, in.Type)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, artifact)
		return
	}
	if len(parts) == 2 && parts[1] == "captures" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"captures": filterCaptures(a.Engine.Store.Snapshot().Captures, id)})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			SessionID string `json:"session_id"`
			Source    string `json:"source"`
			MaxBytes  int    `json:"max_bytes"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.SessionID) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("session_id is required"))
			return
		}
		if a.Engine.Peripherals == nil {
			writeError(w, http.StatusServiceUnavailable, fmt.Errorf("peripheral manager is unavailable"))
			return
		}
		session, err := a.Engine.Peripherals.Session(in.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, err)
			return
		}
		peripheral, ok := findPeripheral(a.Engine.Store.Snapshot().Peripherals, session.PeripheralID)
		if !ok || peripheral.WorkspaceID != id {
			writeError(w, http.StatusBadRequest, fmt.Errorf("session is not attached to this workspace"))
			return
		}
		if in.MaxBytes <= 0 {
			in.MaxBytes = 1 << 20
		}
		result, err := a.Engine.Peripherals.Invoke(r.Context(), peripheralspkg.InvokeRequest{SessionID: in.SessionID, Command: "read", Args: map[string]any{"max_bytes": in.MaxBytes}})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		artifact, err := a.Engine.StoreArtifactBytes(id, fmt.Sprintf("capture-%s.bin", time.Now().UTC().Format("20060102T150405.000000000Z")), "protocol-capture", result.Bytes, map[string]any{"session_id": in.SessionID, "peripheral_id": peripheral.ID, "source": in.Source})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if strings.TrimSpace(in.Source) == "" {
			in.Source = "peripheral.read"
		}
		capture := ProtocolCapture{ID: NewID("CAP"), WorkspaceID: id, PeripheralID: peripheral.ID, Source: in.Source, RawPath: artifact.Path, Parsed: parseProtocolCapture(string(result.Bytes)), Status: "completed", StartedAt: artifact.CreatedAt, CompletedAt: timePtr(now())}
		if err := a.Engine.Store.AddCapture(capture); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "protocol.capture.completed", WorkspaceID: id, Payload: map[string]any{"capture_id": capture.ID, "artifact_id": artifact.ID, "session_id": in.SessionID}, CreatedAt: now()})
		_ = a.Engine.audit("protocol.capture.completed", session.OwnerID, "capture", capture.ID, map[string]any{"workspace_id": id, "artifact_id": artifact.ID})
		writeJSON(w, http.StatusCreated, map[string]any{"capture": capture, "artifact": artifact})
		return
	}
	if len(parts) == 2 && parts[1] == "conversations" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"conversations": filterConversations(a.Engine.Store.Snapshot().Conversations, id)})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = "New IoT research conversation"
		}
		c := Conversation{ID: NewID("CONV"), WorkspaceID: id, Title: title, Status: "active", CreatedAt: now(), UpdatedAt: now()}
		if strings.TrimSpace(in.Content) != "" {
			c.Messages = []ConversationMessage{{ID: NewID("MSG"), Role: "user", Content: in.Content, CreatedAt: now()}}
		}
		if err := a.Engine.Store.CreateConversation(c); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
		return
	}
	if len(parts) == 2 && parts[1] == "attachments" {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"attachments": filterAttachments(a.Engine.Store.Snapshot().Attachments, id)})
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
			return
		}
		var in DeviceAttachment
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if strings.TrimSpace(in.TargetDeviceID) == "" || strings.TrimSpace(in.PeripheralID) == "" || strings.TrimSpace(in.Role) == "" {
			writeError(w, http.StatusBadRequest, fmt.Errorf("target_device_id, peripheral_id, and role are required"))
			return
		}
		st := a.Engine.Store.Snapshot()
		target, targetOK := findTarget(st.Targets, in.TargetDeviceID)
		peripheral, peripheralOK := findPeripheral(st.Peripherals, in.PeripheralID)
		if !targetOK || target.WorkspaceID != id {
			writeError(w, http.StatusBadRequest, fmt.Errorf("target device not found in workspace"))
			return
		}
		if !peripheralOK || peripheral.WorkspaceID != id {
			writeError(w, http.StatusBadRequest, fmt.Errorf("peripheral not found in workspace"))
			return
		}
		in.ID, in.WorkspaceID = NewID("ATT"), id
		if err := a.Engine.Store.CreateAttachment(in); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.audit("device.attachment.created", "api", "attachment", in.ID, map[string]any{"target_device_id": target.ID, "peripheral_id": peripheral.ID})
		writeJSON(w, http.StatusCreated, in)
		return
	}
	if len(parts) == 3 && parts[1] == "devices" && r.Method == http.MethodPost {
		deviceID := parts[2]
		var patch struct {
			Status    string         `json:"status"`
			Transport string         `json:"transport"`
			Serial    string         `json:"serial"`
			Config    map[string]any `json:"config"`
		}
		if err := decode(r, &patch); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var updated Device
		found := false
		if err := a.Engine.Store.mutate(func(st *State) error {
			for i := range st.Devices {
				if st.Devices[i].ID != deviceID || st.Devices[i].WorkspaceID != id {
					continue
				}
				if patch.Status != "" {
					st.Devices[i].Status = patch.Status
				}
				if patch.Transport != "" {
					st.Devices[i].Transport = patch.Transport
				}
				if patch.Serial != "" {
					st.Devices[i].Serial = patch.Serial
				}
				if patch.Config != nil {
					st.Devices[i].Config = patch.Config
				}
				updated = st.Devices[i]
				found = true
				return nil
			}
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Errorf("device not found"))
			return
		}
		_ = a.Engine.audit("device.updated", "api", "device", deviceID, map[string]any{"workspace_id": id})
		writeJSON(w, http.StatusOK, updated)
		return
	}
	if len(parts) == 2 && parts[1] == "run" && r.Method == http.MethodPost {
		var in struct {
			TargetID string `json:"target_id"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, 400, err)
			return
		}
		if in.TargetID == "" {
			for _, t := range a.Engine.Store.Snapshot().Targets {
				if t.WorkspaceID == id {
					in.TargetID = t.ID
					break
				}
			}
		}
		if in.TargetID == "" {
			writeError(w, 400, fmt.Errorf("target_id is required"))
			return
		}
		task, err := a.Engine.SubmitResearch(r.Context(), id, in.TargetID)
		if err != nil {
			writeError(w, 400, err)
			return
		}
		writeJSON(w, 202, map[string]any{"task": task, "message": "research submitted"})
		return
	}
	if len(parts) == 2 && parts[1] == "plan" && r.Method == http.MethodPost {
		var in struct {
			TargetID      string         `json:"target_id"`
			Objective     string         `json:"objective"`
			CapabilityIDs []string       `json:"capabilities"`
			Inputs        map[string]any `json:"inputs"`
			Permissions   PermissionSet  `json:"permissions"`
			Budget        Budget         `json:"budget"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if in.TargetID == "" {
			for _, t := range a.Engine.Store.Snapshot().Targets {
				if t.WorkspaceID == id {
					in.TargetID = t.ID
					break
				}
			}
		}
		if len(in.CapabilityIDs) == 0 {
			in.CapabilityIDs = []string{"target.fingerprint", "finding.gate"}
		}
		task, err := a.Engine.SubmitCapabilityPlan(r.Context(), id, in.TargetID, in.CapabilityIDs, in.Objective, in.Permissions, in.Budget, in.Inputs)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tasks := []Task{task}
		writeJSON(w, http.StatusAccepted, map[string]any{"tasks": tasks, "planner": "commander-default"})
		return
	}
	if len(parts) == 2 && parts[1] == "report" && r.Method == http.MethodGet {
		path, err := a.Engine.GenerateReport(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		content, err := os.ReadFile(path)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"path": path, "content": string(content)})
		return
	}
	writeError(w, 404, fmt.Errorf("route not found"))
}

func (a *APIServer) targets(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"targets": filterTargets(a.Engine.Store.Snapshot().Targets, workspaceID)})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	var t Target
	if err := decode(r, &t); err != nil {
		writeError(w, 400, err)
		return
	}
	if strings.TrimSpace(t.Name) == "" {
		writeError(w, 400, fmt.Errorf("name is required"))
		return
	}
	created, err := a.Engine.CreateTarget(workspaceID, t)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	writeJSON(w, 201, created)
}

func (a *APIServer) devices(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"devices": filterDevices(a.Engine.Store.Snapshot().Devices, workspaceID)})
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	var device Device
	if err := decode(r, &device); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(device.Model) == "" && strings.TrimSpace(device.Vendor) == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("vendor or model is required"))
		return
	}
	device.ID, device.WorkspaceID, device.Status = NewID("DEV"), workspaceID, "available"
	if err := a.Engine.Store.CreateDevice(device); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	_ = a.Engine.audit("device.created", "api", "device", device.ID, map[string]any{"workspace_id": workspaceID})
	writeJSON(w, http.StatusCreated, device)
}

func (a *APIServer) findingRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/findings/")
	if len(parts) == 2 && parts[1] == "gate" && r.Method == http.MethodPost {
		gate, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		gateName := strings.TrimSpace(string(gate))
		if gateName == "" {
			gateName = "finding"
		}
		if strings.HasPrefix(gateName, "{") {
			var in struct {
				Gate string `json:"gate"`
			}
			if err := json.Unmarshal(gate, &in); err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			gateName = in.Gate
		}
		decision, err := a.Engine.EvaluateGate(parts[0], gateName)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, decision)
		return
	}
	if len(parts) == 2 && parts[1] == "evidence" && r.Method == http.MethodPost {
		if _, ok := a.Engine.Store.Finding(parts[0]); !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("finding not found"))
			return
		}
		var evidence Evidence
		if err := decode(r, &evidence); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		evidence.ID, evidence.FindingID, evidence.CreatedAt = NewID("E"), parts[0], now()
		if evidence.Content == nil {
			evidence.Content = map[string]any{}
		}
		if evidence.Source == nil {
			evidence.Source = map[string]string{"agent_id": "manual", "capability_id": "manual.review"}
		}
		if err := a.Engine.Store.AddEvidence(evidence); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, evidence)
		return
	}
	if len(parts) == 2 && parts[1] == "validate" && r.Method == http.MethodPost {
		var in struct {
			Method       string   `json:"method"`
			Reproducible bool     `json:"reproducible"`
			Result       string   `json:"result"`
			CWE          []string `json:"cwe"`
			CVSS         float64  `json:"cvss"`
			Impact       string   `json:"impact"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		finding, ok := a.Engine.Store.Finding(parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("finding not found"))
			return
		}
		if in.CVSS < 0 || in.CVSS > 10 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("cvss must be between 0 and 10"))
			return
		}
		if err := a.Engine.Store.UpdateFinding(parts[0], func(v *Finding) error {
			v.Validation = Validation{State: "completed", Method: in.Method, Reproducible: in.Reproducible, Result: in.Result}
			v.CWE = append([]string(nil), in.CWE...)
			v.CVSS = in.CVSS
			v.Impact = in.Impact
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		validationEvidence := Evidence{ID: NewID("E"), FindingID: finding.ID, Type: "validation_result", Source: map[string]string{"agent_id": "manual", "capability_id": "poc.verify"}, Confidence: 1, Content: map[string]any{"method": in.Method, "reproducible": in.Reproducible, "result": in.Result, "impact": in.Impact, "cwe": in.CWE, "cvss": in.CVSS}, CreatedAt: now()}
		if err := a.Engine.Store.AddEvidence(validationEvidence); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		if in.Reproducible {
			if err := a.Engine.advanceFindingToValidated(finding.ID); err != nil {
				writeError(w, http.StatusConflict, err)
				return
			}
		}
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "validation.completed", WorkspaceID: finding.WorkspaceID, FindingID: finding.ID, Payload: map[string]any{"reproducible": in.Reproducible}, CreatedAt: now()})
		_ = a.Engine.audit("validation.completed", "api", "finding", finding.ID, nil)
		updated, _ := a.Engine.Store.Finding(finding.ID)
		writeJSON(w, http.StatusOK, updated)
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, fmt.Errorf("finding route not found"))
		return
	}
	f, ok := a.Engine.Store.Finding(parts[0])
	if !ok {
		writeError(w, 404, fmt.Errorf("finding not found"))
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, f)
		return
	}
	if r.Method == http.MethodPost {
		var in struct {
			State FindingState `json:"state"`
		}
		if err := decode(r, &in); err != nil {
			writeError(w, 400, err)
			return
		}
		if err := a.Engine.TransitionFinding(f.ID, in.State, "api"); err != nil {
			writeError(w, 409, err)
			return
		}
		updated, _ := a.Engine.Store.Finding(f.ID)
		writeJSON(w, 200, updated)
		return
	}
	writeError(w, 405, fmt.Errorf("method not allowed"))
}
func (a *APIServer) approvalRoutes(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/approvals" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"approvals": a.Engine.Store.Snapshot().Approvals})
		return
	}
	parts := pathParts(r.URL.Path, "/api/v1/approvals/")
	if len(parts) != 1 || r.Method != http.MethodPost {
		writeError(w, 404, fmt.Errorf("approval route not found"))
		return
	}
	var in struct {
		Status string `json:"status"`
		Actor  string `json:"actor"`
	}
	if err := decode(r, &in); err != nil {
		writeError(w, 400, err)
		return
	}
	if in.Actor == "" {
		in.Actor = "api"
	}
	if err := a.Engine.DecideApproval(parts[0], in.Status, in.Actor); err != nil {
		writeError(w, 409, err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": in.Status, "approval_id": parts[0]})
}
func (a *APIServer) taskRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/tasks/")
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		a.streamTaskEvents(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "detail" && r.Method == http.MethodGet {
		task, ok := a.Engine.Store.Task(parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("task not found"))
			return
		}
		st := a.Engine.Store.Snapshot()
		events := make([]Event, 0)
		for _, event := range st.Events {
			if event.TaskID == task.ID {
				events = append(events, event)
			}
		}
		agentRuns := make([]AgentRun, 0)
		capabilityRuns := make([]CapabilityRun, 0)
		toolRuns := make([]ToolRun, 0)
		for _, run := range st.AgentRuns {
			if run.TaskID == task.ID {
				agentRuns = append(agentRuns, run)
			}
		}
		for _, run := range st.CapabilityRuns {
			if run.TaskID == task.ID {
				capabilityRuns = append(capabilityRuns, run)
			}
		}
		for _, run := range st.ToolRuns {
			if run.TaskID == task.ID {
				toolRuns = append(toolRuns, run)
			}
		}
		var finding *Finding
		if task.FindingID != "" {
			if value, ok := a.Engine.Store.Finding(task.FindingID); ok {
				finding = &value
			}
		}
		findings := make([]Finding, 0, 1)
		if task.FindingID != "" {
			if value, findingOK := a.Engine.Store.Finding(task.FindingID); findingOK {
				findings = append(findings, value)
			}
		}
		artifacts := filterArtifacts(st.Artifacts, task.WorkspaceID)
		if len(findings) > 0 {
			allowed := map[string]bool{}
			for _, finding := range findings {
				for _, artifactID := range finding.ArtifactIDs {
					allowed[artifactID] = true
				}
			}
			filteredArtifacts := make([]Artifact, 0, len(artifacts))
			for _, artifact := range artifacts {
				if allowed[artifact.ID] {
					filteredArtifacts = append(filteredArtifacts, artifact)
				}
			}
			artifacts = filteredArtifacts
		}
		evidence := filterEvidence(st.Evidence, findings)
		writeJSON(w, http.StatusOK, map[string]any{"task": task, "events": events, "agent_runs": agentRuns, "capability_runs": capabilityRuns, "tool_runs": toolRuns, "finding": finding, "evidence": evidence, "artifacts": artifacts})
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		task, ok := a.Engine.Store.Task(parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("task not found"))
			return
		}
		action := parts[1]
		updated, err := a.Engine.ControlTask(task.ID, action, "api")
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodGet {
		writeError(w, 404, fmt.Errorf("task route not found"))
		return
	}
	t, ok := a.Engine.Store.Task(parts[0])
	if !ok {
		writeError(w, 404, fmt.Errorf("task not found"))
		return
	}
	writeJSON(w, 200, t)
}

func (a *APIServer) tasks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/tasks" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	items := a.Engine.Store.Snapshot().Tasks
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id")); workspaceID != "" {
		items = filterTasks(items, workspaceID)
	}
	if status := strings.TrimSpace(r.URL.Query().Get("status")); status != "" {
		filtered := make([]Task, 0, len(items))
		for _, item := range items {
			if string(item.Status) == status {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": items})
}

func (a *APIServer) findings(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/v1/findings" || r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	items := a.Engine.Store.Snapshot().Findings
	if workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id")); workspaceID != "" {
		items = filterFindings(items, workspaceID)
	}
	if state := strings.TrimSpace(r.URL.Query().Get("state")); state != "" {
		filtered := make([]Finding, 0, len(items))
		for _, item := range items {
			if string(item.State) == state {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"findings": items})
}

func (a *APIServer) streamTaskEvents(w http.ResponseWriter, r *http.Request, taskID string) {
	if _, ok := a.Engine.Store.Task(taskID); !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("task not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming is unsupported by this server"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	last := 0
	if requestedID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); requestedID != "" {
		for index, event := range matchingEvents(a.Engine.Store.Snapshot().Events, taskID) {
			if event.ID == requestedID {
				last = index + 1
				break
			}
		}
	}
	write := func(event Event) {
		payload, _ := json.Marshal(event)
		// Keep the stream consumable by the browser EventSource onmessage API;
		// the event type remains part of the structured JSON payload.
		fmt.Fprintf(w, "id: %s\ndata: %s\n\n", event.ID, payload)
		flusher.Flush()
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		state := a.Engine.Store.Snapshot()
		matching := make([]Event, 0)
		for _, event := range state.Events {
			if event.TaskID == taskID {
				matching = append(matching, event)
			}
		}
		for last < len(matching) {
			write(matching[last])
			last++
		}
		if task, ok := a.Engine.Store.Task(taskID); ok && isTerminalTask(task.Status) && last >= len(matching) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func matchingEvents(events []Event, taskID string) []Event {
	matching := make([]Event, 0)
	for _, event := range events {
		if event.TaskID == taskID {
			matching = append(matching, event)
		}
	}
	return matching
}

func isTerminalTask(status TaskStatus) bool {
	return status == TaskCompleted || status == TaskFailed || status == TaskBlocked || status == TaskCancelled
}

func pathParts(path, prefix string) []string {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}
func mustWorkspace(s *Store, id string) Workspace { w, _ := s.Workspace(id); return w }
func filterTargets(in []Target, id string) []Target {
	out := []Target{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterTasks(in []Task, id string) []Task {
	out := []Task{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterFindings(in []Finding, id string) []Finding {
	out := []Finding{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterDevices(in []Device, id string) []Device {
	out := []Device{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterPeripherals(in []Peripheral, id string) []Peripheral {
	out := []Peripheral{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterAttachments(in []DeviceAttachment, id string) []DeviceAttachment {
	out := []DeviceAttachment{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterConversations(in []Conversation, id string) []Conversation {
	out := []Conversation{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterCaptures(in []ProtocolCapture, id string) []ProtocolCapture {
	out := []ProtocolCapture{}
	for _, v := range in {
		if v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func findPeripheral(in []Peripheral, id string) (Peripheral, bool) {
	for _, v := range in {
		if v.ID == id {
			return v, true
		}
	}
	return Peripheral{}, false
}
func findTarget(in []Target, id string) (Target, bool) {
	for _, v := range in {
		if v.ID == id {
			return v, true
		}
	}
	return Target{}, false
}
func filterKnowledge(in []KnowledgeItem, id string) []KnowledgeItem {
	out := []KnowledgeItem{}
	for _, v := range in {
		if v.WorkspaceID == "" || v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}

func parseProtocolCapture(raw string) map[string]any {
	fields := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
			fields[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return map[string]any{"raw_bytes": len([]byte(raw)), "fields": fields}
}

func filterArtifacts(in []Artifact, workspaceID string) []Artifact {
	out := []Artifact{}
	for _, artifact := range in {
		if artifact.Metadata != nil {
			if owner, ok := artifact.Metadata["workspace_id"].(string); ok && owner != "" {
				if owner == workspaceID {
					out = append(out, artifact)
				}
				continue
			}
		}
		// Legacy worker artifacts did not carry workspace metadata. Keep them
		// visible until they are migrated, preserving backward compatibility.
		out = append(out, artifact)
	}
	return out
}

func filterTelemetry(in []TelemetryRecord, workspaceID string) []TelemetryRecord {
	out := make([]TelemetryRecord, 0, len(in))
	if workspaceID == "" {
		return append(out, in...)
	}
	for _, item := range in {
		if item.WorkspaceID == workspaceID {
			out = append(out, item)
		}
	}
	return out
}
func filterEvents(in []Event, id string) []Event {
	out := []Event{}
	for _, v := range in {
		if v.WorkspaceID == "" || v.WorkspaceID == id {
			out = append(out, v)
		}
	}
	return out
}
func filterApprovals(in []Approval, tasks []Task) []Approval {
	ids := map[string]bool{}
	for _, task := range tasks {
		ids[task.ID] = true
	}
	out := []Approval{}
	for _, v := range in {
		if ids[v.TaskID] {
			out = append(out, v)
		}
	}
	return out
}
func filterEvidence(in []Evidence, findings []Finding) []Evidence {
	ids := map[string]bool{}
	for _, finding := range findings {
		ids[finding.ID] = true
	}
	out := []Evidence{}
	for _, v := range in {
		if ids[v.FindingID] {
			out = append(out, v)
		}
	}
	return out
}
