package definition

import "time"

// Workflow is the immutable business entity that owns multiple versions.
type Workflow struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	ActiveVersion string         `json:"active_version,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// WorkflowVersion captures a frozen definition snapshot.
type WorkflowVersion struct {
	ID         string              `json:"id"`
	WorkflowID string              `json:"workflow_id"`
	Version    int                 `json:"version"`
	Status     VersionStatus       `json:"status"`
	Definition *WorkflowDefinition `json:"definition"`
	CreatedAt  time.Time           `json:"created_at"`
}

type VersionStatus string

const (
	VersionDraft     VersionStatus = "draft"
	VersionPublished VersionStatus = "published"
	VersionArchived  VersionStatus = "archived"
)

// WorkflowDefinition only contains static and executable-agnostic fields.
type WorkflowDefinition struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	EntryNodes    []string       `json:"entry_nodes,omitempty"`
	Nodes         []Node         `json:"nodes"`
	Edges         []Edge         `json:"edges,omitempty"`
	LoopGroups    []LoopGroup    `json:"loop_groups,omitempty"`
	Triggers      []Trigger      `json:"triggers,omitempty"`
	PublishConfig *PublishConfig `json:"publish_config,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// LoopGroup repeats a structured single-entry, single-exit subgraph. The
// start node receives either the original input or the current list item, and
// the end node publishes the collected outputs after the final iteration.
type LoopGroup struct {
	ID            string        `json:"id"`
	Start         string        `json:"start"`
	End           string        `json:"end"`
	Mode          string        `json:"mode,omitempty"`
	MaxIterations int           `json:"max_iterations"`
	CountBinding  *InputBinding `json:"count_binding,omitempty"`
}

type Node struct {
	ID             string                   `json:"id"`
	Name           string                   `json:"name"`
	Description    string                   `json:"description,omitempty"`
	Type           string                   `json:"type,omitempty"`
	Executor       ExecutorSpec             `json:"executor"`
	Input          any                      `json:"input,omitempty"`
	InputSpec      *InputSpec               `json:"input_spec,omitempty"`
	Params         map[string]any           `json:"params,omitempty"`
	ParamBindings  map[string]InputBinding  `json:"param_bindings,omitempty"`
	ParamTemplates map[string]ParamTemplate `json:"param_templates,omitempty"`
	DependsOn      []string                 `json:"depends_on,omitempty"`
	Retry          RetryPolicy              `json:"retry,omitempty"`
	Loop           *LoopPolicy              `json:"loop,omitempty"`
	Timeout        time.Duration            `json:"timeout,omitempty"`
	Branch         *BranchPolicy            `json:"branch,omitempty"`
	Disabled       bool                     `json:"disabled,omitempty"`
	UI             map[string]any           `json:"ui,omitempty"` // editor-only data, removed by compiler
}

type Edge struct {
	ID        string         `json:"id,omitempty"`
	From      string         `json:"from"`
	To        string         `json:"to"`
	Kind      EdgeKind       `json:"kind,omitempty"`
	Condition string         `json:"condition,omitempty"`
	Priority  int            `json:"priority,omitempty"`
	Label     string         `json:"label,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type EdgeKind string

const (
	EdgeKindNormal EdgeKind = "normal"
	EdgeKindBack   EdgeKind = "back"
)

type Trigger struct {
	ID      string         `json:"id"`
	Type    TriggerType    `json:"type"`
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config,omitempty"`
}

type TriggerType string

const (
	TriggerManual TriggerType = "manual"
	TriggerHTTP   TriggerType = "http"
	TriggerCron   TriggerType = "cron"
)

type PublishConfig struct {
	Enabled      bool              `json:"enabled"`
	Route        string            `json:"route,omitempty"`
	Method       string            `json:"method,omitempty"`
	InputMode    string            `json:"input_mode,omitempty"`
	ResponseMode string            `json:"response_mode,omitempty"`
	AuthRequired bool              `json:"auth_required,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	TimeoutMS    int64             `json:"timeout,omitempty"`
}

type ExecutorSpec struct {
	Type    string         `json:"type"`
	Ref     string         `json:"ref,omitempty"`
	Version string         `json:"version,omitempty"`
	Config  map[string]any `json:"config,omitempty"`
}

type InputSpec struct {
	Mode     InputMode      `json:"mode,omitempty"`
	Bindings []InputBinding `json:"bindings,omitempty"`
}

type InputBinding struct {
	Source    InputSource `json:"source,omitempty"`
	From      string      `json:"from,omitempty"`
	Path      string      `json:"path,omitempty"`
	Label     string      `json:"label,omitempty"`
	As        string      `json:"as,omitempty"`
	Required  bool        `json:"required,omitempty"`
	Default   any         `json:"default,omitempty"`
	Transform string      `json:"transform,omitempty"`
}

type ParamTemplate struct {
	Segments []ParamTemplateSegment `json:"segments"`
}

type ParamTemplateSegment struct {
	Type    string        `json:"type"`
	Value   string        `json:"value,omitempty"`
	Binding *InputBinding `json:"binding,omitempty"`
}

type InputMode string

const (
	InputModeReplace InputMode = "replace"
	InputModeObject  InputMode = "object"
	InputModeArray   InputMode = "array"
)

type InputSource string

const (
	InputSourceNode    InputSource = "node"
	InputSourceVar     InputSource = "var"
	InputSourceRequest InputSource = "request"
	InputSourceRun     InputSource = "run"
)

type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts,omitempty"`
	Backoff     time.Duration `json:"backoff,omitempty"`
	MaxBackoff  time.Duration `json:"max_backoff,omitempty"`
}

type LoopPolicy struct {
	MaxIterations int           `json:"max_iterations,omitempty"`
	Condition     string        `json:"condition,omitempty"`
	Mode          string        `json:"mode,omitempty"`
	CountBinding  *InputBinding `json:"count_binding,omitempty"`
}

type BranchPolicy struct {
	Mode BranchMode `json:"mode,omitempty"`
}

type BranchMode string

const (
	BranchAll   BranchMode = "all"
	BranchFirst BranchMode = "first"
)
