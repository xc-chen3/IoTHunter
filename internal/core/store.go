package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, state: State{Version: 1}}
	if path == "" {
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

func (s *Store) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return cloneState(s.state) }

func (s *Store) mutate(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.state); err != nil {
		return err
	}
	return s.persistLocked()
}

func (s *Store) persistLocked() error {
	if s.path == "" {
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
func (s *Store) CreateTask(t Task) error {
	return s.mutate(func(st *State) error { st.Tasks = append(st.Tasks, t); return nil })
}
func (s *Store) AddAgent(a Agent) error {
	return s.mutate(func(st *State) error { st.Agents = append(st.Agents, a); return nil })
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
		st.Evidence = append(st.Evidence, e)
		for i := range st.Findings {
			if st.Findings[i].ID == e.FindingID {
				st.Findings[i].EvidenceIDs = append(st.Findings[i].EvidenceIDs, e.ID)
				st.Findings[i].UpdatedAt = now()
			}
		}
		return nil
	})
}
func (s *Store) AddArtifact(a Artifact) error {
	return s.mutate(func(st *State) error { st.Artifacts = append(st.Artifacts, a); return nil })
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
