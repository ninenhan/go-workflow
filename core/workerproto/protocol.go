package workerproto

import (
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
)

const (
	DefaultRegisterPath  = "/v1/workers/register"
	DefaultHeartbeatPath = "/v1/workers/heartbeat"
	DefaultPullPath      = "/v1/workers/pull"
	DefaultCompletePath  = "/v1/workers/complete"
	DefaultExecutePath   = "/v1/worker/execute"
	DefaultPollPath      = "/v1/worker/poll"
	DefaultCancelPath    = "/v1/worker/cancel"
	ProtocolHeader       = "X-Workflow-Protocol-Version"
	ProtocolVersion      = "1"
	DurationUnit         = "nanosecond"
	TimestampFormat      = "RFC3339Nano"
)

// AcceptsProtocolVersion reports whether the current runtime can decode a
// protocol version. Empty identifies the versionless callback compatibility
// bridge; it must not be interpreted as support for v1 behavior.
func AcceptsProtocolVersion(version string) bool {
	return version == "" || version == ProtocolVersion
}

// SupportsExecuteReplay reports whether the protocol guarantees that replaying
// Execute with the same dispatch_id does not invoke user code twice.
func SupportsExecuteReplay(version string) bool {
	return version == ProtocolVersion
}

// SupportsPullTransport reports whether the protocol implements leased command
// delivery and idempotent completion.
func SupportsPullTransport(version string) bool {
	return version == ProtocolVersion
}

type Transport string

const (
	TransportCallback Transport = "callback"
	TransportPull     Transport = "pull"
)

type Operation string

const (
	OperationExecute Operation = "execute"
	OperationPoll    Operation = "poll"
	OperationCancel  Operation = "cancel"
)

type ErrorCode string

const (
	ErrorInvalidJSON          ErrorCode = "invalid_json"
	ErrorInvalidRequest       ErrorCode = "invalid_request"
	ErrorWorkerUnavailable    ErrorCode = "worker_unavailable"
	ErrorExecutorNotFound     ErrorCode = "executor_not_found"
	ErrorUnsupportedOperation ErrorCode = "unsupported_operation"
	ErrorProtocolVersion      ErrorCode = "unsupported_protocol_version"
	ErrorInternal             ErrorCode = "internal_error"
)

// ErrorResponse is reserved for protocol and transport failures. Executor
// failures are represented by ExecuteResult with an HTTP 200 response.
type ErrorResponse struct {
	Error     string    `json:"error"`
	Code      ErrorCode `json:"code"`
	Retryable bool      `json:"retryable,omitempty"`
}

type ProtocolInfo struct {
	Version         string      `json:"version"`
	DurationUnit    string      `json:"duration_unit"`
	TimestampFormat string      `json:"timestamp_format"`
	Transports      []Transport `json:"transports"`
	Operations      []Operation `json:"operations"`
}

func CurrentProtocolInfo() ProtocolInfo {
	return ProtocolInfo{
		Version:         ProtocolVersion,
		DurationUnit:    DurationUnit,
		TimestampFormat: TimestampFormat,
		Transports:      []Transport{TransportCallback, TransportPull},
		Operations:      []Operation{OperationExecute, OperationPoll, OperationCancel},
	}
}

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
	Transport              Transport         `json:"transport,omitempty"`
	ProtocolVersion        string            `json:"protocol_version,omitempty"`
	Status                 Status            `json:"status"`
	Weight                 int               `json:"weight,omitempty"`
	MaxConcurrent          int               `json:"max_concurrent,omitempty"`
	CurrentTasks           int               `json:"current_tasks,omitempty"`
	SupportedExecutorTypes []string          `json:"supported_executor_types,omitempty"`
	SupportedExecutorRefs  []string          `json:"supported_executor_refs,omitempty"`
	Labels                 map[string]string `json:"labels,omitempty"`
	LastHeartbeatAt        time.Time         `json:"last_heartbeat_at,omitempty,omitzero"`
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

// Command is a leased scheduler-to-worker operation. CommandID identifies the
// delivery, while Task.DispatchID identifies the underlying execution attempt.
type Command struct {
	CommandID      string               `json:"command_id"`
	Operation      Operation            `json:"operation"`
	Task           executor.ExecuteTask `json:"task"`
	ExternalTaskID string               `json:"external_task_id,omitempty"`
	Delivery       int                  `json:"delivery"`
}

type PullRequest struct {
	WorkerID string `json:"worker_id"`
}

type PullResponse struct {
	Command *Command `json:"command,omitempty"`
}

type CompleteRequest struct {
	WorkerID  string                  `json:"worker_id"`
	CommandID string                  `json:"command_id"`
	Result    *executor.ExecuteResult `json:"result,omitempty"`
	Cancelled bool                    `json:"cancelled,omitempty"`
	Error     string                  `json:"error,omitempty"`
}

type CompleteResponse struct {
	Accepted bool `json:"accepted"`
}
