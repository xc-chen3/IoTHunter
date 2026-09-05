package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type APIServer struct{ Engine *Engine }

func NewAPIServer(engine *Engine) *APIServer { return &APIServer{Engine: engine} }

func (a *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", a.health)
	mux.HandleFunc("/api/v1/capabilities", a.capabilities)
	mux.HandleFunc("/api/v1/tools", a.tools)
	mux.HandleFunc("/api/v1/workspaces", a.workspaces)
	mux.HandleFunc("/api/v1/workspaces/", a.workspaceRoutes)
	mux.HandleFunc("/api/v1/findings/", a.findingRoutes)
	mux.HandleFunc("/api/v1/approvals", a.approvalRoutes)
	mux.HandleFunc("/api/v1/approvals/", a.approvalRoutes)
	mux.HandleFunc("/api/v1/tasks/", a.taskRoutes)
	return loggingMiddleware(corsMiddleware(mux))
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

func (a *APIServer) tools(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": a.Engine.Tools()})
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
		writeJSON(w, 200, map[string]any{"workspace": mustWorkspace(a.Engine.Store, id), "targets": filterTargets(st.Targets, id), "tasks": filterTasks(st.Tasks, id), "findings": filterFindings(st.Findings, id)})
		return
	}
	if len(parts) == 2 && parts[1] == "targets" {
		a.targets(w, r, id)
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
	if len(parts) == 2 && parts[1] == "report" && r.Method == http.MethodGet {
		path, err := a.Engine.GenerateReport(id)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"path": path})
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

func (a *APIServer) findingRoutes(w http.ResponseWriter, r *http.Request) {
	parts := pathParts(r.URL.Path, "/api/v1/findings/")
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
