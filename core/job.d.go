package core

import (
	"context"
	"github.com/google/uuid"
	"time"
)

// -----------------------------
// JobPolicy & Job
// -----------------------------

type IJobPolicy interface {
	RetryCount() int           // 重试次数
	MaxRetries() int           // 最大重试次数
	RetryDelay() time.Duration // 每次重试之间的延迟
}

type JobPolicy struct {
	retryCount int           // 重试次数
	maxRetries int           // 最大重试次数
	retryDelay time.Duration // 每次重试之间的延迟
}

func NewPolicy(
	maxRetries int,
	retryDelay time.Duration,
) *JobPolicy {
	return &JobPolicy{
		retryCount: 0,
		maxRetries: maxRetries,
		retryDelay: retryDelay,
	}
}
func (j *JobPolicy) RetryCount() int {
	return j.retryCount
}

func (j *JobPolicy) Retry() {
	j.retryCount = j.retryCount + 1
}

func (j *JobPolicy) ReachMaxRetries() bool {
	return j.retryCount >= j.maxRetries
}

func (j *JobPolicy) MaxRetries() int {
	return j.maxRetries
}

func (j *JobPolicy) RetryDelay() time.Duration {
	return j.retryDelay
}

type Job struct {
	id           string                                                       // 任务唯一标识
	Name         string                                                       // 任务名称
	Results      any                                                          // 存储阶段产物，key 为阶段名称
	status       JobStatus                                                    // 任务的整体状态
	Output       any                                                          // 输出可以是任意类型的数据
	Input        any                                                          // 输入也可以是任意类型的数据
	Policy       IJobPolicy                                                   //
	Dependencies []*IJob[any]                                                 // 该任务依赖的其他任务, 用于构建依赖关系
	Executor     func(ctx context.Context, i any, current *Unit) (any, error) // 执行器
}

func NewJob(name string) *Job {
	return &Job{Name: name, id: uuid.NewString()}
}

// IJob  Job接口：每个单元固定绑定一个Job，实现输入->输出的逻辑
type IJob[T any] interface {
	ID() string        // 任务唯一标识
	Status() JobStatus // 任务的整体状态
	GetPolicy() IJobPolicy
	Execute(ctx context.Context, input any, current *Unit) (T, error) //
}

func (j *Job) ID() string {
	return j.id
}

func (j *Job) Status() JobStatus {
	return j.status
}

func (j *Job) GetPolicy() IJobPolicy {
	return j.Policy
}

func (j *Job) Execute(ctx context.Context, input any, current *Unit) (any, error) {
	return j.Executor(ctx, input, current)
}
