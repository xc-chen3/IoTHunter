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
	TaskCancelled TaskStatus = "cancelled"
)

type Workspace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Description string `json:"description,omitempty"`
	// Root is the workspace-owned directory used for imported artifacts and
	// read-only capability inputs. It is optional for legacy state files.
	Root      string    `json:"root,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	ID             string        `json:"id"`
	Role           string        `json:"role"`
	ModelProvider  string        `json:"model_provider,omitempty"`
	Model          string        `json:"model,omitempty"`
	RuntimeID      string        `json:"runtime_id,omitempty"`
	Enabled        bool          `json:"enabled"`
	Status         string        `json:"status"`
	MaxConcurrency int           `json:"max_concurrency"`
	Permissions    PermissionSet `json:"permissions"`
}

// LocalRuntime describes a locally installed AI CLI without exposing secrets
// or starting an interactive/model session.
type LocalRuntime struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Provider     string    `json:"provider"`
	Command      string    `json:"command"`
	Path         string    `json:"path,omitempty"`
	Available    bool      `json:"available"`
	Status       string    `json:"status"`
	Version      string    `json:"version,omitempty"`
	AuthState    string    `json:"auth_state,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	LastChecked  time.Time `json:"last_checked"`
	Error        string    `json:"error,omitempty"`
}

// ModelConfig is a user-owned model endpoint. Credentials are referenced by
// environment variable name and are never persisted in the state payload.
type ModelConfig struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	Endpoint      string    `json:"endpoint,omitempty"`
	CredentialEnv string    `json:"credential_env,omitempty"`
	Enabled       bool      `json:"enabled"`
	Capabilities  []string  `json:"capabilities,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PromptVersion keeps the exact prompt used by an Agent auditable and
// reproducible. Content is intentionally stored as plain text, while its
// digest is calculated by the control plane.
type PromptVersion struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Version   string    `json:"version"`
	Content   string    `json:"content"`
	SHA256    string    `json:"sha256"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// RuntimeResult is the bounded result of one non-interactive local CLI
// invocation. Interactive terminals are intentionally outside this API.
type RuntimeResult struct {
	RuntimeID   string     `json:"runtime_id"`
	Status      string     `json:"status"`
	Output      string     `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type Skill struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Version     string        `json:"version"`
	Description string        `json:"description,omitempty"`
	Enabled     bool          `json:"enabled"`
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
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Vendor      string         `json:"vendor,omitempty"`
	Model       string         `json:"model,omitempty"`
	Serial      string         `json:"serial,omitempty"`
	Transport   string         `json:"transport,omitempty"`
	Status      string         `json:"status"`
	Authorized  bool           `json:"authorized"`
	Owner       string         `json:"owner,omitempty"`
	LockOwner   string         `json:"lock_owner,omitempty"`
	Config      map[string]any `json:"config,omitempty"`
}

// Peripheral is a lab instrument or adapter used to study a target Device.
// It is intentionally separate from Device so target research records do not
// get mixed with UART, power, scope, JTAG, or Bluetooth hardware.
type Peripheral struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Driver       string         `json:"driver,omitempty"`
	Transport    string         `json:"transport,omitempty"`
	Address      string         `json:"address,omitempty"`
	Port         string         `json:"port,omitempty"`
	Status       string         `json:"status"`
	OccupiedBy   string         `json:"occupied_by,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Config       map[string]any `json:"config,omitempty"`
	ActiveConfig map[string]any `json:"active_config,omitempty"`
	SafetyLimits map[string]any `json:"safety_limits,omitempty"`
	ConnectedAt  *time.Time     `json:"connected_at,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type DeviceAttachment struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	TargetDeviceID string `json:"target_device_id"`
	PeripheralID   string `json:"peripheral_id"`
	Role           string `json:"role"`
	Channel        string `json:"channel,omitempty"`
}

type ConversationMessage struct {
	ID         string              `json:"id"`
	Role       string              `json:"role"`
	Content    string              `json:"content"`
	References map[string][]string `json:"references,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
}

type Conversation struct {
	ID          string                `json:"id"`
	WorkspaceID string                `json:"workspace_id"`
	Title       string                `json:"title"`
	Status      string                `json:"status"`
	Messages    []ConversationMessage `json:"messages,omitempty"`
	TaskIDs     []string              `json:"task_ids,omitempty"`
	ArchivedAt  *time.Time            `json:"archived_at,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
	UpdatedAt   time.Time             `json:"updated_at"`
}

type ProtocolCapture struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	PeripheralID string         `json:"peripheral_id,omitempty"`
	Source       string         `json:"source"`
	RawPath      string         `json:"raw_path,omitempty"`
	Parsed       map[string]any `json:"parsed,omitempty"`
	Judgement    string         `json:"judgement,omitempty"`
	Status       string         `json:"status"`
	StartedAt    time.Time      `json:"started_at"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

// TelemetryRecord is the persisted, queryable projection of a peripheral
// telemetry sample. Large payloads should be represented by ArtifactID.
type TelemetryRecord struct {
	ID           string         `json:"id"`
	WorkspaceID  string         `json:"workspace_id"`
	PeripheralID string         `json:"peripheral_id"`
	SessionID    string         `json:"session_id"`
	Topic        string         `json:"topic"`
	Values       map[string]any `json:"values,omitempty"`
	BytesBase64  string         `json:"bytes_base64,omitempty"`
	ArtifactID   string         `json:"artifact_id,omitempty"`
	At           time.Time      `json:"at"`
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

type TaskNode struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	Summary     string         `json:"summary,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

type Task struct {
	ID                   string         `json:"id"`
	WorkspaceID          string         `json:"workspace_id"`
	TargetID             string         `json:"target_id,omitempty"`
	FindingID            string         `json:"finding_id,omitempty"`
	ConversationID       string         `json:"conversation_id,omitempty"`
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
	Summary              string         `json:"summary,omitempty"`
	Output               map[string]any `json:"output,omitempty"`
	Nodes                []TaskNode     `json:"nodes,omitempty"`
	StartedAt            *time.Time     `json:"started_at,omitempty"`
	CompletedAt          *time.Time     `json:"completed_at,omitempty"`
	RetryCount           int            `json:"retry_count,omitempty"`
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
	Isolation      string        `json:"isolation,omitempty"`
	Image          string        `json:"image,omitempty"`
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
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	WorkspaceID    string         `json:"workspace_id,omitempty"`
	ConversationID string         `json:"conversation_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
	FindingID      string         `json:"finding_id,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
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
	Version                int                `json:"version"`
	Workspaces             []Workspace        `json:"workspaces"`
	Targets                []Target           `json:"targets"`
	Devices                []Device           `json:"devices"`
	Peripherals            []Peripheral       `json:"peripherals"`
	Attachments            []DeviceAttachment `json:"attachments"`
	Conversations          []Conversation     `json:"conversations"`
	Captures               []ProtocolCapture  `json:"captures"`
	Telemetry              []TelemetryRecord  `json:"telemetry,omitempty"`
	Agents                 []Agent            `json:"agents"`
	Models                 []ModelConfig      `json:"models,omitempty"`
	Prompts                []PromptVersion    `json:"prompts,omitempty"`
	Skills                 []Skill            `json:"skills"`
	Knowledge              []KnowledgeItem    `json:"knowledge"`
	RegisteredCapabilities []Capability       `json:"registered_capabilities,omitempty"`
	RegisteredTools        []Tool             `json:"registered_tools,omitempty"`
	Tasks                  []Task             `json:"tasks"`
	Findings               []Finding          `json:"findings"`
	Evidence               []Evidence         `json:"evidence"`
	Artifacts              []Artifact         `json:"artifacts"`
	AgentRuns              []AgentRun         `json:"agent_runs"`
	CapabilityRuns         []CapabilityRun    `json:"capability_runs"`
	ToolRuns               []ToolRun          `json:"tool_runs"`
	Gates                  []GateDecision     `json:"gate_decisions"`
	Approvals              []Approval         `json:"approvals"`
	Events                 []Event            `json:"events"`
	Audit                  []AuditLog         `json:"audit"`
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
		TaskAssigned: {TaskRunning, TaskQueued, TaskPaused, TaskCancelled},
		TaskRunning:  {TaskCompleted, TaskFailed, TaskBlocked, TaskPaused, TaskCancelled},
		TaskFailed:   {TaskQueued, TaskCancelled}, TaskBlocked: {TaskQueued, TaskCancelled}, TaskPaused: {TaskRunning, TaskQueued, TaskCancelled},
		TaskCancelled: {TaskQueued},
	}
	for _, candidate := range allowed[from] {
		if candidate == to {
			return true
		}
	}
	return false
}
