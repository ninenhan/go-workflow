package core

import (
	"context"
	"github.com/google/uuid"
	"log"
	"time"
)

// -----------------------------
// JobUnit
// -----------------------------

type JobUnit struct {
	UnitId string    // Unit id
	Job    IJob[any] // 绑定的任务
}

func (u *JobUnit) ID() string {
	return u.UnitId
}

func NewJobUnit(job IJob[any]) *JobUnit {
	return &JobUnit{
		UnitId: uuid.NewString(),
		Job:    job,
	}
}

// Run Run执行自身绑定的Job逻辑
func (u *JobUnit) Run(ctx context.Context, input any, current *Unit) (any, error) {
	policy := u.Job.GetPolicy()
	r, e := u.Job.Execute(ctx, input, current)
	if policy != nil && e != nil {
		p, ok := policy.(*JobPolicy)
		if ok {
			for !p.ReachMaxRetries() {
				p.Retry()
				log.Printf("重试第%d次", policy.RetryCount())
				time.Sleep(policy.RetryDelay())
				r, e = u.Job.Execute(ctx, input, current)
				if e != nil {
					continue
				}
				break
			}
		}
	}
	return r, e
}
