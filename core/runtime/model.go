package wfruntime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

var ErrRunNotFound = errors.New("run not found")

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"
	StatusSuccess   Status = "success"
	StatusFailed    Status = "failed"
	StatusRetry     Status = "retry"
	StatusPaused    Status = "paused"
	StatusCancelled Status = "cancelled"
	StatusTimeout   Status = "timeout"
)

type WorkflowRun struct {
	ID                 string              `json:"id"`
	WorkflowID         string              `json:"workflow_id"`
	WorkflowVersionID  string              `json:"workflow_version_id"`
	PlanID             string              `json:"plan_id"`
	RequestFingerprint string              `json:"request_fingerprint,omitempty"`
	CredentialScope    string              `json:"credential_scope,omitempty"`
	Status             Status              `json:"status"`
	CurrentNodes       []string            `json:"current_nodes,omitempty"`
	NodeRuns           map[string]*NodeRun `json:"node_runs,omitempty"`
	Context            RunContext          `json:"context"`
	CreatedAt          time.Time           `json:"created_at"`
	UpdatedAt          time.Time           `json:"updated_at"`
	StartedAt          time.Time           `json:"started_at,omitempty"`
	FinishedAt         time.Time           `json:"finished_at,omitempty"`
	mu                 sync.RWMutex
}

type NodeRun struct {
	NodeID      string    `json:"node_id"`
	DispatchID  string    `json:"dispatch_id,omitempty"`
	Status      Status    `json:"status"`
	Attempt     int       `json:"attempt"`
	MaxAttempts int       `json:"max_attempts,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	// Input keeps the latest materialized task input so the UI can explain what
	// a node actually received during execution instead of only showing config.
	Input    any            `json:"input,omitempty"`
	Result   any            `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
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
	EventNodeWaiting EventType = "node_waiting"
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

type AsyncTaskState string

const (
	AsyncTaskWaiting   AsyncTaskState = "waiting"
	AsyncTaskCompleted AsyncTaskState = "completed"
	AsyncTaskClaimed   AsyncTaskState = "claimed"
	AsyncTaskDone      AsyncTaskState = "done"
	AsyncTaskCancelled AsyncTaskState = "cancelled"
)

type AsyncTask struct {
	DispatchID     string                 `json:"dispatch_id"`
	RunID          string                 `json:"run_id"`
	NodeID         string                 `json:"node_id"`
	ExternalTaskID string                 `json:"external_task_id"`
	Task           executor.ExecuteTask   `json:"task"`
	Result         *executor.ExecuteResult `json:"result,omitempty"`
	ResultHash     string                 `json:"-"`
	State          AsyncTaskState         `json:"state"`
	ClaimToken     string                 `json:"-"`
	ClaimUntil     time.Time              `json:"-"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
}

type AsyncTaskStore interface {
	SaveAsyncTask(ctx context.Context, task *AsyncTask) error
	SubmitAsyncResult(ctx context.Context, dispatchID string, result executor.ExecuteResult) (bool, error)
	ClaimCompletedAsyncTasks(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]*AsyncTask, error)
	RenewAsyncTask(ctx context.Context, dispatchID, claimToken string, claimUntil time.Time) (bool, error)
	AcknowledgeAsyncTask(ctx context.Context, dispatchID, claimToken string) error
	ReleaseAsyncTask(ctx context.Context, dispatchID, claimToken string) error
	CancelAsyncTasks(ctx context.Context, runID string) error
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

	cp := &WorkflowRun{
		ID:                 r.ID,
		WorkflowID:         r.WorkflowID,
		WorkflowVersionID:  r.WorkflowVersionID,
		PlanID:             r.PlanID,
		RequestFingerprint: r.RequestFingerprint,
		CredentialScope:    r.CredentialScope,
		Status:             r.Status,
		CurrentNodes:       append([]string{}, r.CurrentNodes...),
		NodeRuns:           make(map[string]*NodeRun, len(r.NodeRuns)),
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		StartedAt:          r.StartedAt,
		FinishedAt:         r.FinishedAt,
	}
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
	return cp
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
