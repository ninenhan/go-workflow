package planning

import (
	"strings"
	"time"
)

const loopGroupVariablePrefix = "__loop_group."

func LoopGroupItemVariable(groupID string) string {
	return loopGroupVariablePrefix + groupID + ".item"
}

func LoopGroupIndexVariable(groupID string) string {
	return loopGroupVariablePrefix + groupID + ".index"
}

func ParseLoopGroupVariable(key string) (groupID string, kind string, ok bool) {
	if !strings.HasPrefix(key, loopGroupVariablePrefix) {
		return "", "", false
	}
	value := strings.TrimPrefix(key, loopGroupVariablePrefix)
	for _, suffix := range []string{".item", ".index"} {
		if strings.HasSuffix(value, suffix) {
			groupID = strings.TrimSuffix(value, suffix)
			if groupID == "" {
				return "", "", false
			}
			return groupID, strings.TrimPrefix(suffix, "."), true
		}
	}
	return "", "", false
}

type ExecutionPlan struct {
	PlanID            string                `json:"plan_id"`
	WorkflowID        string                `json:"workflow_id"`
	WorkflowVersionID string                `json:"workflow_version_id"`
	MaxConcurrency    int                   `json:"max_concurrency"`
	FailFast          bool                  `json:"fail_fast,omitempty"`
	ConcurrencyGroups map[string]int        `json:"concurrency_groups,omitempty"`
	ResourcePools     map[string]int        `json:"resource_pools,omitempty"`
	EntryNodes        []string              `json:"entry_nodes"`
	ExitNodes         []string              `json:"exit_nodes"`
	Adjacency         map[string][]string   `json:"adjacency"`
	Dependencies      map[string][]string   `json:"dependencies"`
	TopologicalOrder  []string              `json:"topological_order"`
	Nodes             map[string]PlanNode   `json:"nodes"`
	Branches          map[string]BranchMeta `json:"branches,omitempty"`
	BackEdges         map[string]BranchMeta `json:"back_edges,omitempty"`
	LoopGroups        map[string]LoopGroup  `json:"loop_groups,omitempty"`
	CreatedAt         time.Time             `json:"created_at"`
}

type LoopGroup struct {
	ID            string        `json:"id"`
	Start         string        `json:"start"`
	End           string        `json:"end"`
	Mode          string        `json:"mode"`
	MaxIterations int           `json:"max_iterations"`
	CountBinding  *InputBinding `json:"count_binding,omitempty"`
	Scope         []string      `json:"scope"`
	Parent        string        `json:"parent,omitempty"`
	Depth         int           `json:"depth,omitempty"`
}

type PlanNode struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	Type             string                   `json:"type,omitempty"`
	ExecutorType     string                   `json:"executor_type"`
	ExecutorRef      string                   `json:"executor_ref,omitempty"`
	ExecutorConf     map[string]any           `json:"executor_conf,omitempty"`
	Input            any                      `json:"input,omitempty"`
	InputSpec        *InputSpec               `json:"input_spec,omitempty"`
	Params           map[string]any           `json:"params,omitempty"`
	ParamBindings    map[string]InputBinding  `json:"param_bindings,omitempty"`
	ParamTemplates   map[string]ParamTemplate `json:"param_templates,omitempty"`
	ConcurrencyGroup string                   `json:"concurrency_group,omitempty"`
	ResourcePool     string                   `json:"resource_pool,omitempty"`
	Retry            RetryPolicy              `json:"retry,omitempty"`
	Loop             *LoopPolicy              `json:"loop,omitempty"`
	Timeout          time.Duration            `json:"timeout,omitempty"`
	ContinueOnError  bool                     `json:"continue_on_error,omitempty"`
}

type InputSpec struct {
	Mode     string         `json:"mode,omitempty"`
	Bindings []InputBinding `json:"bindings,omitempty"`
}

type InputBinding struct {
	Source    string `json:"source,omitempty"`
	From      string `json:"from,omitempty"`
	Path      string `json:"path,omitempty"`
	Label     string `json:"label,omitempty"`
	As        string `json:"as,omitempty"`
	Required  bool   `json:"required,omitempty"`
	Default   any    `json:"default,omitempty"`
	Transform string `json:"transform,omitempty"`
}

type ParamTemplate struct {
	Segments []ParamTemplateSegment `json:"segments"`
}

type ParamTemplateSegment struct {
	Type    string        `json:"type"`
	Value   string        `json:"value,omitempty"`
	Binding *InputBinding `json:"binding,omitempty"`
}

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
