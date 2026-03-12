package wfruntime

import (
	"context"
	"sync"
	"time"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusRetry     Status = "retry"
	StatusPaused    Status = "paused"
	StatusCancelled Status = "cancelled"
	StatusTimeout   Status = "timeout"
)

type WorkflowRun struct {
	ID                string              `json:"id"`
	WorkflowID        string              `json:"workflow_id"`
	WorkflowVersionID string              `json:"workflow_version_id"`
	PlanID            string              `json:"plan_id"`
	Status            Status              `json:"status"`
	CurrentNodes      []string            `json:"current_nodes,omitempty"`
	NodeRuns          map[string]*NodeRun `json:"node_runs,omitempty"`
	Context           RunContext          `json:"context"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	StartedAt         time.Time           `json:"started_at,omitempty"`
	FinishedAt        time.Time           `json:"finished_at,omitempty"`
	mu                sync.RWMutex
}

type NodeRun struct {
	NodeID      string         `json:"node_id"`
	Status      Status         `json:"status"`
	Attempt     int            `json:"attempt"`
	MaxAttempts int            `json:"max_attempts,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	FinishedAt  time.Time      `json:"finished_at,omitempty"`
	Result      any            `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type RunContext struct {
	Variables   map[string]any `json:"variables,omitempty"`
	NodeResults map[string]any `json:"node_results,omitempty"`
}

type RunSnapshot struct {
	RunID       string              `json:"run_id"`
	Status      Status              `json:"status"`
	NodeRuns    map[string]*NodeRun `json:"node_runs"`
	Context     RunContext          `json:"context"`
	At          time.Time           `json:"at"`
	Description string              `json:"description,omitempty"`
}

type EventType string

const (
	EventRunStarted  EventType = "run_started"
	EventRunFinished EventType = "run_finished"
	EventRunPaused   EventType = "run_paused"
	EventRunResumed  EventType = "run_resumed"
	EventNodeReady   EventType = "node_ready"
	EventNodeRunning EventType = "node_running"
	EventNodeLoop    EventType = "node_loop"
	EventNodeRetry   EventType = "node_retry"
	EventNodeDone    EventType = "node_done"
	EventNodeFailed  EventType = "node_failed"
)

type RunEvent struct {
	RunID      string         `json:"run_id"`
	Type       EventType      `json:"type"`
	NodeID     string         `json:"node_id,omitempty"`
	Status     Status         `json:"status,omitempty"`
	Time       time.Time      `json:"time"`
	Message    string         `json:"message,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	WorkflowID string         `json:"workflow_id,omitempty"`
}

type Store interface {
	SaveRun(ctx context.Context, run *WorkflowRun) error
	LoadRun(ctx context.Context, runID string) (*WorkflowRun, error)
	ListRuns(ctx context.Context) ([]*WorkflowRun, error)
	SaveSnapshot(ctx context.Context, snapshot *RunSnapshot) error
	Snapshots(ctx context.Context, runID string) ([]*RunSnapshot, error)
	AppendEvent(ctx context.Context, event RunEvent) error
	Events(ctx context.Context, runID string) ([]RunEvent, error)
}

func NewWorkflowRun(runID, workflowID, versionID, planID string) *WorkflowRun {
	now := time.Now()
	return &WorkflowRun{
		ID:                runID,
		WorkflowID:        workflowID,
		WorkflowVersionID: versionID,
		PlanID:            planID,
		Status:            StatusPending,
		NodeRuns:          map[string]*NodeRun{},
		Context: RunContext{
			Variables:   map[string]any{},
			NodeResults: map[string]any{},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (r *WorkflowRun) Clone() *WorkflowRun {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	cp := *r
	cp.CurrentNodes = append([]string{}, r.CurrentNodes...)
	cp.NodeRuns = make(map[string]*NodeRun, len(r.NodeRuns))
	for id, nr := range r.NodeRuns {
		if nr == nil {
			cp.NodeRuns[id] = nil
			continue
		}
		nc := *nr
		nc.Metadata = cloneAnyMap(nr.Metadata)
		cp.NodeRuns[id] = &nc
	}
	cp.Context = RunContext{
		Variables:   cloneAnyMap(r.Context.Variables),
		NodeResults: cloneAnyMap(r.Context.NodeResults),
	}
	return &cp
}

func cloneAnyMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneNodeRuns(src map[string]*NodeRun) map[string]*NodeRun {
	if len(src) == 0 {
		return map[string]*NodeRun{}
	}
	dst := make(map[string]*NodeRun, len(src))
	for key, value := range src {
		if value == nil {
			dst[key] = nil
			continue
		}
		cp := *value
		cp.Metadata = cloneAnyMap(value.Metadata)
		dst[key] = &cp
	}
	return dst
}

func cloneSnapshot(snapshot *RunSnapshot) *RunSnapshot {
	if snapshot == nil {
		return nil
	}
	cp := *snapshot
	cp.NodeRuns = cloneNodeRuns(snapshot.NodeRuns)
	cp.Context = RunContext{
		Variables:   cloneAnyMap(snapshot.Context.Variables),
		NodeResults: cloneAnyMap(snapshot.Context.NodeResults),
	}
	return &cp
}
