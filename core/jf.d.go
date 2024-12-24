package core

type JobStatus string

const (
	TaskPending   JobStatus = "PENDING"
	TaskRunning   JobStatus = "RUNNING"
	TaskCompleted JobStatus = "COMPLETED"
	TaskFailed    JobStatus = "FAILED"
)

type StageStatus string

const (
	StagePending       StageStatus = "PENDING"
	StageRunning       StageStatus = "RUNNING"
	StageCompleted     StageStatus = "COMPLETED"
	StageFailed        StageStatus = "FAILED"
	StageNeedTerminate StageStatus = "BREAK"
)
