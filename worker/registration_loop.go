package worker

import (
	"context"
	"time"

	"github.com/ninenhan/go-workflow/core/workerproto"
)

type RegistrationOptions struct {
	SchedulerEndpoint string
	Descriptor        workerproto.WorkerDescriptor
	Interval          time.Duration
	Client            *RegistrationClient
}

func (s *Service) MaintainRegistration(ctx context.Context, opts RegistrationOptions) error {
	if s == nil || !s.Enabled() {
		return nil
	}
	client := opts.Client
	if client == nil {
		client = NewRegistrationClient(nil)
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if err := client.Register(ctx, opts.SchedulerEndpoint, opts.Descriptor); err != nil {
		return err
	}

	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := client.Heartbeat(ctx, opts.SchedulerEndpoint, opts.Descriptor.ID); err != nil {
				return err
			}
		}
	}
}
