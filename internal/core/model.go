package core

import "time"

type FindingState string

const (
	FindingHypothesis         FindingState = "hypothesis"
	FindingCandidate          FindingState = "candidate"
	FindingAnalyzing          FindingState = "analyzing"
	FindingReadyForValidation FindingState = "ready_for_validation"
	FindingValidating         FindingState = "validating"
	FindingValidated          FindingState = "validated"
	FindingReportable         FindingState = "reportable"
	FindingReported           FindingState = "reported"
	FindingKnowledgeCaptured  FindingState = "knowledge_captured"
	FindingDropped            FindingState = "dropped"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskAssigned  TaskStatus = "assigned"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskBlocked   TaskStatus = "blocked"
	TaskPaused    TaskStatus = "paused"
)

type Workspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Owner       string    `json:"owner"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Target struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Name        string         `json:"name"`
	Vendor      string         `json:"vendor,omitempty"`
	Model       string         `json:"model,omitempty"`
	Address     string         `json:"address,omitempty"`
	Transport   string         `json:"transport,omitempty"`
	Authorized  bool           `json:"authorized"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Agent struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	ModelProvider  string `json:"model_provider,omitempty"`
	Model          string `json:"model,omitempty"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"`
	MaxConcurrency int    `json:"max_concurrency"`
}

type Skill struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Roles       []string      `json:"roles,omitempty"`
	Steps       []string      `json:"steps,omitempty"`
	Outputs     []string      `json:"outputs,omitempty"`
	Permissions PermissionSet `json:"permissions"`
}

type KnowledgeItem struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Content     map[string]any `json:"content"`
	Tags        []string       `json:"tags,omitempty"`
	SourceIDs   []string       `json:"source_ids,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AgentRun struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	TaskID      string         `json:"task_id"`
	Status      string         `json:"status"`
	Model       string         `json:"model,omitempty"`
	Input       map[string]any `json:"input,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

type CapabilityRun struct {
	ID           string         `json:"id"`
	RequestID    string         `json:"request_id"`
	TaskID       string         `json:"task_id"`
	CapabilityID string         `json:"capability_id"`
	Status       string         `json:"status"`
	Result       map[string]any `json:"result,omitempty"`
	StartedAt    time.Time      `json:"started_at"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

type ToolRun struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"task_id"`
	ToolName    string         `json:"tool_name"`
	Status      string         `json:"status"`
	Command     []string       `json:"command,omitempty"`
	ExitCode    int            `json:"exit_code,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

type GateDecision struct {
	ID        string    `json:"id"`
	FindingID string    `json:"finding_id"`
	Gate      string    `json:"gate"`
	Decision  string    `json:"decision"`
	Reasons   []string  `json:"reasons,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Device struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Vendor      string `json:"vendor,omitempty"`
	Model       string `json:"model,omitempty"`
	Serial      string `json:"serial,omitempty"`
	Transport   string `json:"transport,omitempty"`
	Status      string `json:"status"`
	Authorized  bool   `json:"authorized"`
	Owner       string `json:"owner,omitempty"`
	LockOwner   string `json:"lock_owner,omitempty"`
}

type PermissionSet struct {
	Network     bool   `json:"network"`
	Filesystem  string `json:"filesystem"`
	Device      bool   `json:"device"`
	Destructive bool   `json:"destructive"`
}

type Budget struct {
	MaxRuntimeSeconds int `json:"max_runtime_seconds"`
	MaxToolCalls      int `json:"max_tool_calls"`
	MaxTokens         int `json:"max_tokens,omitempty"`
}

type Task struct {
	ID                   string         `json:"id"`
	WorkspaceID          string         `json:"workspace_id"`
	TargetID             string         `json:"target_id,omitempty"`
	FindingID            string         `json:"finding_id,omitempty"`
	Type                 string         `json:"type"`
	Objective            string         `json:"objective"`
	Priority             int            `json:"priority"`
	Status               TaskStatus     `json:"status"`
	AssignedAgent        string         `json:"assigned_agent,omitempty"`
	RequiredCapabilities []string       `json:"required_capabilities,omitempty"`
	Context              map[string]any `json:"context,omitempty"`
	Permissions          PermissionSet  `json:"permissions"`
	Budget               Budget         `json:"budget"`
	Error                string         `json:"error,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type AttackSurface struct {
	Type       string `json:"type,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
}

type Location struct {
	Component string `json:"component,omitempty"`
	Binary    string `json:"binary,omitempty"`
	File      string `json:"file,omitempty"`
	Function  string `json:"function,omitempty"`
	Offset    string `json:"offset,omitempty"`
}

type Validation struct {
	State        string `json:"state"`
	Method       string `json:"method,omitempty"`
	Reproducible bool   `json:"reproducible"`
	Result       string `json:"result,omitempty"`
}

type Finding struct {
	ID            string         `json:"finding_id"`
	WorkspaceID   string         `json:"workspace_id"`
	TargetID      string         `json:"target_id,omitempty"`
	Title         string         `json:"title"`
	State         FindingState   `json:"state"`
	Priority      string         `json:"priority"`
	Score         float64        `json:"score"`
	Confidence    float64        `json:"confidence"`
	AttackSurface AttackSurface  `json:"attack_surface"`
	Location      Location       `json:"location"`
	Source        []string       `json:"source,omitempty"`
	Sink          []string       `json:"sink,omitempty"`
	CallChain     []string       `json:"call_chain,omitempty"`
	Constraints   []string       `json:"constraints,omitempty"`
	CWE           []string       `json:"cwe,omitempty"`
	EvidenceIDs   []string       `json:"evidence_ids,omitempty"`
	ArtifactIDs   []string       `json:"artifact_ids,omitempty"`
	Validation    Validation     `json:"validation"`
	POC           map[string]any `json:"poc,omitempty"`
	CVSS          float64        `json:"cvss,omitempty"`
	Impact        string         `json:"impact,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

type Evidence struct {
	ID           string            `json:"evidence_id"`
	FindingID    string            `json:"finding_id"`
	Type         string            `json:"type"`
	Source       map[string]string `json:"source"`
	Confidence   float64           `json:"confidence"`
	Content      map[string]any    `json:"content"`
	ArtifactRefs []string          `json:"artifact_refs,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
}

type Artifact struct {
	ID        string         `json:"artifact_id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Path      string         `json:"path"`
	SHA256    string         `json:"sha256"`
	Size      int64          `json:"size"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Capability struct {
	ID             string         `json:"id"`
	Version        string         `json:"version"`
	Category       string         `json:"category"`
	Description    string         `json:"description"`
	InputSchema    map[string]any `json:"input_schema,omitempty"`
	OutputSchema   map[string]any `json:"output_schema,omitempty"`
	Permissions    PermissionSet  `json:"permissions"`
	Runtime        string         `json:"runtime"`
	Implementation string         `json:"implementation"`
	TimeoutSeconds int            `json:"timeout_seconds"`
}

// Tool describes the execution implementation behind a Capability. The MVP
// ships builtin tools; container and remote-worker implementations can use
// the same contract without changing agent-facing requests.
type Tool struct {
	Name           string        `json:"name"`
	Category       string        `json:"category"`
	Execution      string        `json:"execution"`
	Permissions    PermissionSet `json:"permissions"`
	Runtime        string        `json:"runtime"`
	TimeoutSeconds int           `json:"timeout_seconds"`
	Command        []string      `json:"command,omitempty"`
}

type CapabilityRequest struct {
	RequestID    string         `json:"request_id"`
	TaskID       string         `json:"task_id"`
	AgentID      string         `json:"agent_id"`
	CapabilityID string         `json:"capability_id"`
	Objective    string         `json:"objective"`
	Inputs       map[string]any `json:"inputs"`
	Permissions  PermissionSet  `json:"permissions"`
	Budget       Budget         `json:"budget"`
}

type CapabilityResult struct {
	RequestID    string         `json:"request_id"`
	CapabilityID string         `json:"capability_id"`
	Status       string         `json:"status"`
	Summary      string         `json:"summary"`
	Evidence     []Evidence     `json:"evidence,omitempty"`
	Artifacts    []Artifact     `json:"artifacts,omitempty"`
	Confidence   float64        `json:"confidence"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	Error        string         `json:"error,omitempty"`
}

type Approval struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	CapabilityID string     `json:"capability_id"`
	Reason       string     `json:"reason"`
	Status       string     `json:"status"`
	RequestedAt  time.Time  `json:"requested_at"`
	DecidedAt    *time.Time `json:"decided_at,omitempty"`
	DecidedBy    string     `json:"decided_by,omitempty"`
}

type Event struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	WorkspaceID string         `json:"workspace_id,omitempty"`
	TaskID      string         `json:"task_id,omitempty"`
	FindingID   string         `json:"finding_id,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AuditLog struct {
	ID           string         `json:"id"`
	Action       string         `json:"action"`
	Actor        string         `json:"actor"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Details      map[string]any `json:"details,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type State struct {
	Version        int             `json:"version"`
	Workspaces     []Workspace     `json:"workspaces"`
	Targets        []Target        `json:"targets"`
	Devices        []Device        `json:"devices"`
	Agents         []Agent         `json:"agents"`
	Skills         []Skill         `json:"skills"`
	Knowledge      []KnowledgeItem `json:"knowledge"`
	Tasks          []Task          `json:"tasks"`
	Findings       []Finding       `json:"findings"`
	Evidence       []Evidence      `json:"evidence"`
	Artifacts      []Artifact      `json:"artifacts"`
	AgentRuns      []AgentRun      `json:"agent_runs"`
	CapabilityRuns []CapabilityRun `json:"capability_runs"`
	ToolRuns       []ToolRun       `json:"tool_runs"`
	Gates          []GateDecision  `json:"gate_decisions"`
	Approvals      []Approval      `json:"approvals"`
	Events         []Event         `json:"events"`
	Audit          []AuditLog      `json:"audit"`
}

func CanTransitionFinding(from, to FindingState) bool {
	if from == to {
		return true
	}
	allowed := map[FindingState][]FindingState{
		FindingHypothesis:         {FindingCandidate, FindingDropped},
		FindingCandidate:          {FindingAnalyzing, FindingDropped},
		FindingAnalyzing:          {FindingCandidate, FindingReadyForValidation, FindingDropped},
		FindingReadyForValidation: {FindingValidating, FindingAnalyzing},
		FindingValidating:         {FindingAnalyzing, FindingCandidate, FindingValidated, FindingDropped},
		FindingValidated:          {FindingReportable, FindingAnalyzing},
		FindingReportable:         {FindingReported, FindingValidated},
		FindingReported:           {FindingKnowledgeCaptured},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func CanTransitionTask(from, to TaskStatus) bool {
	if from == to {
		return true
	}
	allowed := map[TaskStatus][]TaskStatus{
		TaskQueued:   {TaskAssigned, TaskPaused, TaskCancelled},
		TaskAssigned: {TaskRunning, TaskQueued, TaskPaused},
		TaskRunning:  {TaskCompleted, TaskFailed, TaskBlocked, TaskPaused},
		TaskFailed:   {TaskQueued}, TaskBlocked: {TaskQueued}, TaskPaused: {TaskRunning, TaskQueued},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

const TaskCancelled TaskStatus = "cancelled"
