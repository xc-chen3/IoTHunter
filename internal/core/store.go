package core

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	mu    sync.RWMutex
	path  string
	state State
	db    *sql.DB
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, state: State{Version: 1}}
	if path == "" {
		return s, nil
	}
	if isSQLitePath(path) {
		if path != ":memory:" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}
		if _, err := db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure sqlite store: %w", err)
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS iothunter_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			version INTEGER NOT NULL,
			payload BLOB NOT NULL,
			updated_at TEXT NOT NULL
		)`); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("initialize sqlite store: %w", err)
		}
		s.db = db
		var payload []byte
		err = db.QueryRow(`SELECT payload FROM iothunter_state WHERE id = 1`).Scan(&payload)
		if err == nil && len(payload) > 0 {
			if err := json.Unmarshal(payload, &s.state); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("decode sqlite state: %w", err)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			_ = db.Close()
			return nil, fmt.Errorf("read sqlite state: %w", err)
		}
		if s.state.Version == 0 {
			s.state.Version = 1
		}
		return s, nil
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &s.state); err != nil {
			return nil, fmt.Errorf("decode state: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) && err != nil {
		return nil, err
	}
	if s.state.Version == 0 {
		s.state.Version = 1
	}
	return s, nil
}

func isSQLitePath(path string) bool {
	if path == ":memory:" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".db" || ext == ".sqlite" || ext == ".sqlite3"
}

// Close releases the SQLite handle. JSON-backed stores do not hold resources.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Store) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return cloneState(s.state) }

func (s *Store) mutate(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := cloneState(s.state)
	if err := fn(&s.state); err != nil {
		s.state = before
		return err
	}
	if err := s.persistLocked(); err != nil {
		s.state = before
		return err
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if s.db != nil {
		data, err := json.Marshal(s.state)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT INTO iothunter_state (id, version, payload, updated_at)
			VALUES (1, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET version = excluded.version,
			payload = excluded.payload, updated_at = excluded.updated_at`,
			s.state.Version, data, now().Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("persist sqlite state: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cloneState(in State) State {
	b, _ := json.Marshal(in)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}

func NewID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(b)
}

func now() time.Time { return time.Now().UTC() }

func (s *Store) CreateWorkspace(w Workspace) error {
	return s.mutate(func(st *State) error { st.Workspaces = append(st.Workspaces, w); return nil })
}
func (s *Store) CreateTarget(t Target) error {
	return s.mutate(func(st *State) error { st.Targets = append(st.Targets, t); return nil })
}
func (s *Store) CreateDevice(d Device) error {
	return s.mutate(func(st *State) error { st.Devices = append(st.Devices, d); return nil })
}
func (s *Store) CreatePeripheral(p Peripheral) error {
	return s.mutate(func(st *State) error { st.Peripherals = append(st.Peripherals, p); return nil })
}
func (s *Store) CreateAttachment(a DeviceAttachment) error {
	return s.mutate(func(st *State) error { st.Attachments = append(st.Attachments, a); return nil })
}
func (s *Store) UpdatePeripheral(id string, fn func(*Peripheral) error) error {
	return s.mutate(func(st *State) error {
		for i := range st.Peripherals {
			if st.Peripherals[i].ID == id {
				if err := fn(&st.Peripherals[i]); err != nil {
					return err
				}
				st.Peripherals[i].UpdatedAt = now()
				return nil
			}
		}
		return fmt.Errorf("peripheral %s not found", id)
	})
}
func (s *Store) CreateConversation(c Conversation) error {
	return s.mutate(func(st *State) error { st.Conversations = append(st.Conversations, c); return nil })
}
func (s *Store) UpdateConversation(id string, fn func(*Conversation) error) error {
	return s.mutate(func(st *State) error {
		for i := range st.Conversations {
			if st.Conversations[i].ID == id {
				if err := fn(&st.Conversations[i]); err != nil {
					return err
				}
				st.Conversations[i].UpdatedAt = now()
				return nil
			}
		}
		return fmt.Errorf("conversation %s not found", id)
	})
}
func (s *Store) AddCapture(c ProtocolCapture) error {
	return s.mutate(func(st *State) error { st.Captures = append(st.Captures, c); return nil })
}
func (s *Store) AddTelemetry(v TelemetryRecord) error {
	return s.mutate(func(st *State) error {
		st.Telemetry = append(st.Telemetry, v)
		if len(st.Telemetry) > 100000 {
			st.Telemetry = st.Telemetry[len(st.Telemetry)-100000:]
		}
		return nil
	})
}
func (s *Store) CreateTask(t Task) error {
	return s.mutate(func(st *State) error { st.Tasks = append(st.Tasks, t); return nil })
}
func (s *Store) AddAgent(a Agent) error {
	return s.mutate(func(st *State) error { st.Agents = append(st.Agents, a); return nil })
}
func (s *Store) AddModel(v ModelConfig) error {
	return s.mutate(func(st *State) error {
		for i := range st.Models {
			if st.Models[i].ID == v.ID || (v.Name != "" && st.Models[i].Name == v.Name) {
				st.Models[i] = v
				return nil
			}
		}
		st.Models = append(st.Models, v)
		return nil
	})
}
func (s *Store) AddPrompt(v PromptVersion) error {
	return s.mutate(func(st *State) error {
		for i := range st.Prompts {
			if st.Prompts[i].ID == v.ID || (v.Name != "" && st.Prompts[i].Name == v.Name && st.Prompts[i].Version == v.Version) {
				st.Prompts[i] = v
				return nil
			}
		}
		st.Prompts = append(st.Prompts, v)
		return nil
	})
}
func (s *Store) AddSkill(v Skill) error {
	return s.mutate(func(st *State) error { st.Skills = append(st.Skills, v); return nil })
}
func (s *Store) AddKnowledge(v KnowledgeItem) error {
	return s.mutate(func(st *State) error { st.Knowledge = append(st.Knowledge, v); return nil })
}
func (s *Store) CreateFinding(f Finding) error {
	return s.mutate(func(st *State) error { st.Findings = append(st.Findings, f); return nil })
}
func (s *Store) AddEvidence(e Evidence) error {
	return s.mutate(func(st *State) error {
		for _, existing := range st.Evidence {
			if e.ID != "" && existing.ID == e.ID && existing.FindingID == e.FindingID {
				return nil
			}
		}
		st.Evidence = append(st.Evidence, e)
		for i := range st.Findings {
			if st.Findings[i].ID == e.FindingID {
				if !containsString(st.Findings[i].EvidenceIDs, e.ID) {
					st.Findings[i].EvidenceIDs = append(st.Findings[i].EvidenceIDs, e.ID)
				}
				st.Findings[i].UpdatedAt = now()
			}
		}
		return nil
	})
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, value string) []string {
	if value == "" || containsString(values, value) {
		return values
	}
	return append(values, value)
}
func (s *Store) AddArtifact(a Artifact) error {
	return s.mutate(func(st *State) error {
		for i := range st.Artifacts {
			if (a.ID != "" && st.Artifacts[i].ID == a.ID) || (a.SHA256 != "" && st.Artifacts[i].SHA256 == a.SHA256 && st.Artifacts[i].Path == a.Path) {
				if st.Artifacts[i].Metadata == nil {
					st.Artifacts[i].Metadata = map[string]any{}
				}
				for key, value := range a.Metadata {
					st.Artifacts[i].Metadata[key] = value
				}
				return nil
			}
		}
		st.Artifacts = append(st.Artifacts, a)
		return nil
	})
}
func (s *Store) AddAgentRun(v AgentRun) error {
	return s.mutate(func(st *State) error { st.AgentRuns = append(st.AgentRuns, v); return nil })
}
func (s *Store) AddCapabilityRun(v CapabilityRun) error {
	return s.mutate(func(st *State) error { st.CapabilityRuns = append(st.CapabilityRuns, v); return nil })
}
func (s *Store) AddToolRun(v ToolRun) error {
	return s.mutate(func(st *State) error { st.ToolRuns = append(st.ToolRuns, v); return nil })
}
func (s *Store) AddGate(v GateDecision) error {
	return s.mutate(func(st *State) error { st.Gates = append(st.Gates, v); return nil })
}
func (s *Store) AddApproval(a Approval) error {
	return s.mutate(func(st *State) error { st.Approvals = append(st.Approvals, a); return nil })
}
func (s *Store) AddEvent(e Event) error {
	return s.mutate(func(st *State) error {
		st.Events = append(st.Events, e)
		if len(st.Events) > 10000 {
			st.Events = st.Events[len(st.Events)-10000:]
		}
		return nil
	})
}
func (s *Store) AddAudit(a AuditLog) error {
	return s.mutate(func(st *State) error { st.Audit = append(st.Audit, a); return nil })
}

func (s *Store) UpdateTask(id string, fn func(*Task) error) error {
	return s.mutate(func(st *State) error {
		for i := range st.Tasks {
			if st.Tasks[i].ID == id {
				if err := fn(&st.Tasks[i]); err != nil {
					return err
				}
				st.Tasks[i].UpdatedAt = now()
				return nil
			}
		}
		return fmt.Errorf("task %s not found", id)
	})
}
func (s *Store) UpdateFinding(id string, fn func(*Finding) error) error {
	return s.mutate(func(st *State) error {
		for i := range st.Findings {
			if st.Findings[i].ID == id {
				if err := fn(&st.Findings[i]); err != nil {
					return err
				}
				st.Findings[i].UpdatedAt = now()
				return nil
			}
		}
		return fmt.Errorf("finding %s not found", id)
	})
}
func (s *Store) UpdateApproval(id string, fn func(*Approval) error) error {
	return s.mutate(func(st *State) error {
		for i := range st.Approvals {
			if st.Approvals[i].ID == id {
				return fn(&st.Approvals[i])
			}
		}
		return fmt.Errorf("approval %s not found", id)
	})
}

func (s *Store) Workspace(id string) (Workspace, bool) {
	st := s.Snapshot()
	for _, v := range st.Workspaces {
		if v.ID == id {
			return v, true
		}
	}
	return Workspace{}, false
}
func (s *Store) Target(id string) (Target, bool) {
	st := s.Snapshot()
	for _, v := range st.Targets {
		if v.ID == id {
			return v, true
		}
	}
	return Target{}, false
}
func (s *Store) Task(id string) (Task, bool) {
	st := s.Snapshot()
	for _, v := range st.Tasks {
		if v.ID == id {
			return v, true
		}
	}
	return Task{}, false
}
func (s *Store) Finding(id string) (Finding, bool) {
	st := s.Snapshot()
	for _, v := range st.Findings {
		if v.ID == id {
			return v, true
		}
	}
	return Finding{}, false
}
