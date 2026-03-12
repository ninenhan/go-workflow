package planning

import "time"

type ExecutionPlan struct {
	PlanID            string                `json:"plan_id"`
	WorkflowID        string                `json:"workflow_id"`
	WorkflowVersionID string                `json:"workflow_version_id"`
	EntryNodes        []string              `json:"entry_nodes"`
	ExitNodes         []string              `json:"exit_nodes"`
	Adjacency         map[string][]string   `json:"adjacency"`
	Dependencies      map[string][]string   `json:"dependencies"`
	TopologicalOrder  []string              `json:"topological_order"`
	Nodes             map[string]PlanNode   `json:"nodes"`
	Branches          map[string]BranchMeta `json:"branches,omitempty"`
	BackEdges         map[string]BranchMeta `json:"back_edges,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
}

type PlanNode struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Type            string         `json:"type,omitempty"`
	ExecutorType    string         `json:"executor_type"`
	ExecutorRef     string         `json:"executor_ref,omitempty"`
	ExecutorConf    map[string]any `json:"executor_conf,omitempty"`
	Input           any            `json:"input,omitempty"`
	InputSpec       *InputSpec     `json:"input_spec,omitempty"`
	Params          map[string]any `json:"params,omitempty"`
	Retry           RetryPolicy    `json:"retry,omitempty"`
	Loop            *LoopPolicy    `json:"loop,omitempty"`
	Timeout         time.Duration  `json:"timeout,omitempty"`
	ContinueOnError bool           `json:"continue_on_error,omitempty"`
}

type InputSpec struct {
	Mode     string         `json:"mode,omitempty"`
	Bindings []InputBinding `json:"bindings,omitempty"`
}

type InputBinding struct {
	Source    string `json:"source,omitempty"`
	From      string `json:"from,omitempty"`
	Path      string `json:"path,omitempty"`
	As        string `json:"as,omitempty"`
	Required  bool   `json:"required,omitempty"`
	Default   any    `json:"default,omitempty"`
	Transform string `json:"transform,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts,omitempty"`
	Backoff     time.Duration `json:"backoff,omitempty"`
	MaxBackoff  time.Duration `json:"max_backoff,omitempty"`
}

type LoopPolicy struct {
	MaxIterations int    `json:"max_iterations,omitempty"`
	Condition     string `json:"condition,omitempty"`
}

type BranchMeta struct {
	From  string       `json:"from"`
	Mode  string       `json:"mode,omitempty"`
	Edges []BranchEdge `json:"edges"`
}

type BranchEdge struct {
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
	Priority  int    `json:"priority,omitempty"`
	Label     string `json:"label,omitempty"`
}
