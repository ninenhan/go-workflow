package flow

type JobStatus string

const (
	TaskPending   JobStatus = "PENDING"
	TaskRunning   JobStatus = "RUNNING"
	TaskCompleted JobStatus = "COMPLETED"
	TaskFailed    JobStatus = "FAILED"
)
