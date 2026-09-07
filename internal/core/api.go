package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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
	mux.HandleFunc("/api/v1/agents", a.agents)
	mux.HandleFunc("/api/v1/agents/", a.agentRoutes)
	mux.HandleFunc("/api/v1/runtimes", a.runtimes)
	mux.HandleFunc("/api/v1/runtimes/", a.runtimeRoutes)
	mux.HandleFunc("/api/v1/skills", a.skills)
	mux.HandleFunc("/api/v1/knowledge", a.knowledge)
	mux.HandleFunc("/api/v1/conversations", a.conversations)
	mux.HandleFunc("/api/v1/conversations/", a.conversationRoutes)
	mux.HandleFunc("/api/v1/peripherals", a.peripherals)
	mux.HandleFunc("/api/v1/peripherals/", a.peripheralRoutes)
	mux.HandleFunc("/api/v1/events", a.events)
	mux.HandleFunc("/api/v1/audit", a.audit)
	mux.HandleFunc("/api/v1/workspaces", a.workspaces)
	mux.HandleFunc("/api/v1/workspaces/", a.workspaceRoutes)
	mux.HandleFunc("/api/v1/iot/", a.iotRoutes)
	mux.HandleFunc("/api/v1/findings/", a.findingRoutes)
	mux.HandleFunc("/api/v1/approvals", a.approvalRoutes)
	mux.HandleFunc("/api/v1/approvals/", a.approvalRoutes)
	mux.HandleFunc("/api/v1/tasks/", a.taskRoutes)
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
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
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "iothunter", "version": "0.1.0"})
}
func (a *APIServer) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, 405, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, 200, map[string]any{"capabilities": a.Engine.Capabilities()})
}

func (a *APIServer) capabilityRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/capabilities/")
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
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": a.Engine.Tools()})
}

func (a *APIServer) agents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": a.Engine.Store.Snapshot().Agents})
}

func (a *APIServer) agentRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/agents/")
	if len(parts) != 1 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, fmt.Errorf("agent route not found"))
		return
	}
	var patch struct {
		ModelProvider  string  `json:"model_provider"`
		Model          string  `json:"model"`
		RuntimeID      *string `json:"runtime_id"`
		Enabled        *bool   `json:"enabled"`
		MaxConcurrency int     `json:"max_concurrency"`
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
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": a.Engine.Store.Snapshot().Skills})
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
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, fmt.Errorf("conversation route not found"))
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
	updated := a.Engine.Store.Snapshot().Conversations
	for _, c := range updated {
		if c.ID == current.ID {
			writeJSON(w, http.StatusOK, c)
			return
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

func (a *APIServer) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": a.Engine.Store.Snapshot().Events})
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
			"tasks": filterTasks(st.Tasks, id), "findings": filterFindings(st.Findings, id),
			"evidence": filterEvidence(st.Evidence, filterFindings(st.Findings, id)), "artifacts": st.Artifacts,
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
			TargetID      string        `json:"target_id"`
			Objective     string        `json:"objective"`
			CapabilityIDs []string      `json:"capabilities"`
			Permissions   PermissionSet `json:"permissions"`
			Budget        Budget        `json:"budget"`
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
		tasks := []Task{}
		for _, capabilityID := range in.CapabilityIDs {
			task, err := a.Engine.SubmitCapabilityTask(r.Context(), id, in.TargetID, capabilityID, in.Objective, in.Permissions, in.Budget)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			tasks = append(tasks, task)
		}
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
	if len(parts) == 2 && r.Method == http.MethodPost {
		task, ok := a.Engine.Store.Task(parts[0])
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("task not found"))
			return
		}
		action := parts[1]
		var targetStatus TaskStatus
		switch action {
		case "pause":
			targetStatus = TaskPaused
		case "resume":
			targetStatus = TaskRunning
		case "retry":
			targetStatus = TaskQueued
		case "cancel":
			targetStatus = TaskCancelled
		default:
			writeError(w, http.StatusNotFound, fmt.Errorf("unknown task action"))
			return
		}
		if !CanTransitionTask(task.Status, targetStatus) {
			writeError(w, http.StatusConflict, fmt.Errorf("invalid task transition %s -> %s", task.Status, targetStatus))
			return
		}
		if err := a.Engine.Store.UpdateTask(task.ID, func(v *Task) error {
			v.Status = targetStatus
			if targetStatus == TaskQueued {
				v.Error = ""
			}
			return nil
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		_ = a.Engine.Store.AddEvent(Event{ID: NewID("EVT"), Type: "task." + action, WorkspaceID: task.WorkspaceID, TaskID: task.ID, CreatedAt: now()})
		_ = a.Engine.audit("task."+action, "api", "task", task.ID, nil)
		updated, _ := a.Engine.Store.Task(task.ID)
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
func filterKnowledge(in []KnowledgeItem, id string) []KnowledgeItem {
	out := []KnowledgeItem{}
	for _, v := range in {
		if v.WorkspaceID == "" || v.WorkspaceID == id {
			out = append(out, v)
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
