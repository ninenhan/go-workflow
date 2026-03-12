package workerproto

import (
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

const (
	DefaultRegisterPath  = "/v1/workers/register"
	DefaultHeartbeatPath = "/v1/workers/heartbeat"
	DefaultExecutePath   = "/v1/worker/execute"
	DefaultPollPath      = "/v1/worker/poll"
	DefaultCancelPath    = "/v1/worker/cancel"
)

type Status string

const (
	StatusOnline  Status = "online"
	StatusOffline Status = "offline"
	StatusBusy    Status = "busy"
)

type WorkerDescriptor struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name,omitempty"`
	Tenant                 string            `json:"tenant,omitempty"`
	Endpoint               string            `json:"endpoint"`
	Status                 Status            `json:"status"`
	Weight                 int               `json:"weight,omitempty"`
	MaxConcurrent          int               `json:"max_concurrent,omitempty"`
	CurrentTasks           int               `json:"current_tasks,omitempty"`
	SupportedExecutorTypes []string          `json:"supported_executor_types,omitempty"`
	SupportedExecutorRefs  []string          `json:"supported_executor_refs,omitempty"`
	Labels                 map[string]string `json:"labels,omitempty"`
	LastHeartbeatAt        time.Time         `json:"last_heartbeat_at,omitempty"`
}

type RegisterRequest struct {
	Worker WorkerDescriptor `json:"worker"`
}

type RegisterResponse struct {
	Accepted bool   `json:"accepted"`
	Message  string `json:"message,omitempty"`
}

type HeartbeatRequest struct {
	WorkerID string `json:"worker_id"`
}

type ExecuteRequest struct {
	Task executor.ExecuteTask `json:"task"`
}

type ExecuteResponse struct {
	Result executor.ExecuteResult `json:"result"`
}

type PollRequest struct {
	Task           executor.ExecuteTask `json:"task"`
	ExternalTaskID string               `json:"external_task_id"`
}

type PollResponse struct {
	Result executor.ExecuteResult `json:"result"`
}

type CancelRequest struct {
	Task           executor.ExecuteTask `json:"task"`
	ExternalTaskID string               `json:"external_task_id"`
}

type CancelResponse struct {
	Cancelled bool   `json:"cancelled"`
	Message   string `json:"message,omitempty"`
}
