package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Duration supports JSON input as "1s"/"500ms" or a number (milliseconds).
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		d.Duration = parsed
		return nil
	}
	var ms float64
	if err := json.Unmarshal(b, &ms); err != nil {
		return err
	}
	d.Duration = time.Duration(ms) * time.Millisecond
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

type JoinPolicy string

const (
	JoinAny JoinPolicy = "any"
	JoinAll JoinPolicy = "all"
)

type NodeRole string

const (
	RoleExec    NodeRole = "exec"
	RoleConfig  NodeRole = "config"
	RoleTrigger NodeRole = "trigger"
)

type RunMode string

const (
	RunModeLazy   RunMode = "lazy"
	RunModeEager  RunMode = "eager"
	RunModeManual RunMode = "manual"
)

type BranchMode string

const (
	BranchAll   BranchMode = "all"
	BranchFirst BranchMode = "first"
)

type NodeOptions struct {
	Timeout         Duration   `json:"timeout,omitempty"`
	Retries         int        `json:"retries,omitempty"`
	RetryBackoff    Duration   `json:"retry_backoff,omitempty"`
	RetryBackoffMax Duration   `json:"retry_backoff_max,omitempty"`
	Join            JoinPolicy `json:"join,omitempty"`
	ContinueOnError bool       `json:"continue_on_error,omitempty"`
	BranchMode      BranchMode `json:"branch_mode,omitempty"`
}

type NodeSpec struct {
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Unit         string         `json:"unit,omitempty"`
	UnitID       string         `json:"unit_id,omitempty"`
	Input        *Input         `json:"input,omitempty"`
	Params       map[string]any `json:"params,omitempty"`
	ExportFields []string       `json:"export_fields,omitempty"`
	Options      *NodeOptions   `json:"options,omitempty"`
	Role         NodeRole       `json:"role,omitempty"`
	RunMode      RunMode        `json:"run_mode,omitempty"`
	DependsOn    []string       `json:"depends_on,omitempty"`
}

type EdgeSpec struct {
	From  string `json:"from"`
	To    string `json:"to"`
	When  string `json:"when,omitempty"`
	Label string `json:"label,omitempty"`
	Order int    `json:"order,omitempty"`
}

type WorkflowDefinition struct {
	ID    string               `json:"id,omitempty"`
	Name  string               `json:"name,omitempty"`
	Start []string             `json:"start,omitempty"`
	Nodes map[string]*NodeSpec `json:"nodes,omitempty"`
	Edges []EdgeSpec           `json:"edges,omitempty"`
}

func (w *WorkflowDefinition) UnmarshalJSON(data []byte) error {
	type Alias WorkflowDefinition
	aux := &struct {
		EdgesRaw json.RawMessage `json:"edges"`
		*Alias
	}{
		Alias: (*Alias)(w),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(aux.EdgesRaw) == 0 {
		return nil
	}
	raw := strings.TrimSpace(string(aux.EdgesRaw))
	if raw == "" || raw == "null" {
		return nil
	}
	switch raw[0] {
	case '{':
		var m map[string][]string
		if err := json.Unmarshal(aux.EdgesRaw, &m); err != nil {
			return err
		}
		for from, tos := range m {
			for _, to := range tos {
				w.Edges = append(w.Edges, EdgeSpec{From: from, To: to})
			}
		}
	case '[':
		var edges []EdgeSpec
		if err := json.Unmarshal(aux.EdgesRaw, &edges); err != nil {
			return err
		}
		w.Edges = append(w.Edges, edges...)
	default:
		return errors.New("invalid edges format: must be object or array")
	}
	return nil
}

func ParseWorkflowJSON(data []byte) (*WorkflowDefinition, error) {
	var def WorkflowDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

func (w *WorkflowDefinition) ValidateBasic() error {
	if w == nil {
		return errors.New("workflow definition is nil")
	}
	if len(w.Nodes) == 0 {
		return errors.New("workflow nodes is empty")
	}
	for id, node := range w.Nodes {
		if node == nil {
			return fmt.Errorf("node %s is nil", id)
		}
		unit := node.Unit
		if unit == "" {
			unit = node.UnitID
		}
		if unit == "" {
			return fmt.Errorf("node %s missing unit/unit_id", id)
		}
	}
	return nil
}
