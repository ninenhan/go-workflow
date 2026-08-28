package scheduler

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/core/workerproto"
)

type WorkerRegistry interface {
	Register(ctx context.Context, worker workerproto.WorkerDescriptor) error
	Heartbeat(ctx context.Context, workerID string) error
	FindForTask(ctx context.Context, task executor.ExecuteTask) (*workerproto.WorkerDescriptor, error)
	List(ctx context.Context) ([]workerproto.WorkerDescriptor, error)
}

type WorkerLease struct {
	Worker  *workerproto.WorkerDescriptor
	Release func()
}

type WorkerAllocator interface {
	AcquireForTask(ctx context.Context, task executor.ExecuteTask) (*WorkerLease, error)
}

type MemoryWorkerRegistry struct {
	mu           sync.RWMutex
	workers      map[string]workerproto.WorkerDescriptor
	inflight     map[string]int
	nextIndex    int
	HeartbeatTTL time.Duration
	Now          func() time.Time
}

func NewMemoryWorkerRegistry() *MemoryWorkerRegistry {
	return &MemoryWorkerRegistry{
		workers:      make(map[string]workerproto.WorkerDescriptor),
		inflight:     make(map[string]int),
		HeartbeatTTL: 30 * time.Second,
		Now:          time.Now,
	}
}

func (r *MemoryWorkerRegistry) Register(_ context.Context, worker workerproto.WorkerDescriptor) error {
	if strings.TrimSpace(worker.ID) == "" {
		return fmt.Errorf("worker id is required")
	}
	if worker.Transport == "" {
		worker.Transport = workerproto.TransportCallback
	}
	if !workerproto.AcceptsProtocolVersion(worker.ProtocolVersion) {
		return fmt.Errorf("unsupported worker protocol version: %s", worker.ProtocolVersion)
	}
	if worker.Transport != workerproto.TransportCallback && worker.Transport != workerproto.TransportPull {
		return fmt.Errorf("unsupported worker transport: %s", worker.Transport)
	}
	if worker.Transport == workerproto.TransportPull && !workerproto.SupportsPullTransport(worker.ProtocolVersion) {
		return fmt.Errorf("pull worker requires protocol version %s", workerproto.ProtocolVersion)
	}
	if worker.Transport == workerproto.TransportCallback && strings.TrimSpace(worker.Endpoint) == "" {
		return fmt.Errorf("worker endpoint is required")
	}
	if worker.Status == "" {
		worker.Status = workerproto.StatusOnline
	}
	if worker.Weight <= 0 {
		worker.Weight = 1
	}
	worker.LastHeartbeatAt = r.now()
	r.mu.Lock()
	r.workers[worker.ID] = worker
	r.mu.Unlock()
	return nil
}

func (r *MemoryWorkerRegistry) Heartbeat(_ context.Context, workerID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	worker, ok := r.workers[workerID]
	if !ok {
		return fmt.Errorf("worker not found: %s", workerID)
	}
	worker.LastHeartbeatAt = r.now()
	if worker.Status == workerproto.StatusOffline {
		worker.Status = workerproto.StatusOnline
	}
	r.workers[workerID] = worker
	return nil
}

func (r *MemoryWorkerRegistry) FindForTask(_ context.Context, task executor.ExecuteTask) (*workerproto.WorkerDescriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	candidates := r.matchingWorkersLocked(task, false)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no worker available for executor type=%s ref=%s", task.ExecutorType, task.ExecutorRef)
	}
	selected := chooseWorker(candidates, r.inflight, r.nextIndex, parseWorkerSelector(task.Params))
	if selected == nil {
		return nil, fmt.Errorf("no worker available for executor type=%s ref=%s", task.ExecutorType, task.ExecutorRef)
	}
	cp := *selected
	cp.CurrentTasks = r.inflight[cp.ID]
	return &cp, nil
}

func (r *MemoryWorkerRegistry) AcquireForTask(_ context.Context, task executor.ExecuteTask) (*WorkerLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	selector := parseWorkerSelector(task.Params)
	candidates := r.matchingWorkersLocked(task, true)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no worker slot available for executor type=%s ref=%s", task.ExecutorType, task.ExecutorRef)
	}

	selected := chooseWorker(candidates, r.inflight, r.nextIndex, selector)
	if selected == nil {
		return nil, fmt.Errorf("no worker slot available for executor type=%s ref=%s", task.ExecutorType, task.ExecutorRef)
	}
	r.inflight[selected.ID]++
	r.nextIndex++

	workerCopy := *selected
	workerCopy.CurrentTasks = r.inflight[selected.ID]
	return &WorkerLease{
		Worker: &workerCopy,
		Release: func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.inflight[selected.ID] > 0 {
				r.inflight[selected.ID]--
			}
		},
	}, nil
}

func (r *MemoryWorkerRegistry) List(_ context.Context) ([]workerproto.WorkerDescriptor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]workerproto.WorkerDescriptor, 0, len(r.workers))
	for _, worker := range r.workers {
		worker = r.normalizeWorkerLocked(worker)
		worker.CurrentTasks = r.inflight[worker.ID]
		out = append(out, worker)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *MemoryWorkerRegistry) matchingWorkersLocked(task executor.ExecuteTask, enforceCapacity bool) []workerproto.WorkerDescriptor {
	selector := parseWorkerSelector(task.Params)
	workers := make([]workerproto.WorkerDescriptor, 0, len(r.workers))
	for _, worker := range r.workers {
		worker = r.normalizeWorkerLocked(worker)
		if worker.Status == workerproto.StatusOffline {
			continue
		}
		if selector.WorkerID != "" && worker.ID != selector.WorkerID {
			continue
		}
		if selector.Tenant != "" && worker.Tenant != selector.Tenant {
			continue
		}
		if !matchesLabels(worker, selector.Labels) {
			continue
		}
		if task.ExecutorRef != "" && !supportsRef(worker, task.ExecutorRef) {
			continue
		}
		if task.ExecutorRef == "" && !supportsType(worker, task.ExecutorType) {
			continue
		}
		if enforceCapacity && worker.MaxConcurrent > 0 && r.inflight[worker.ID] >= worker.MaxConcurrent {
			continue
		}
		workers = append(workers, worker)
	}
	return workers
}

type workerSelector struct {
	WorkerID        string
	Tenant          string
	Labels          map[string]string
	PreferredLabels map[string]string
}

func parseWorkerSelector(params map[string]any) workerSelector {
	if len(params) == 0 {
		return workerSelector{}
	}
	out := workerSelector{}
	if workerID, ok := params["worker_id"].(string); ok {
		out.WorkerID = strings.TrimSpace(workerID)
	}
	if tenant, ok := params["tenant"].(string); ok {
		out.Tenant = strings.TrimSpace(tenant)
	}
	out.Labels = toStringMap(params["worker_labels"])
	out.PreferredLabels = toStringMap(params["preferred_worker_labels"])
	return out
}

func toStringMap(value any) map[string]string {
	switch raw := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(raw))
		for key, val := range raw {
			out[key] = val
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(raw))
		for key, val := range raw {
			if text, ok := val.(string); ok && text != "" {
				out[key] = text
			}
		}
		return out
	default:
		return nil
	}
}

func matchesLabels(worker workerproto.WorkerDescriptor, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	for key, expected := range required {
		if worker.Labels[key] != expected {
			return false
		}
	}
	return true
}

func chooseWorker(candidates []workerproto.WorkerDescriptor, inflight map[string]int, nextIndex int, selector workerSelector) *workerproto.WorkerDescriptor {
	if len(candidates) == 0 {
		return nil
	}
	bestIdx := -1
	bestScore := 0
	bestLoad := 0
	bestTie := 0
	for idx := range candidates {
		score := preferenceScore(candidates[idx], selector.PreferredLabels)
		load := inflight[candidates[idx].ID]
		tie := (idx - (nextIndex % len(candidates)) + len(candidates)) % len(candidates)
		if bestIdx == -1 || score > bestScore || (score == bestScore && load < bestLoad) || (score == bestScore && load == bestLoad && tie < bestTie) {
			bestIdx = idx
			bestScore = score
			bestLoad = load
			bestTie = tie
		}
	}
	if bestIdx < 0 {
		return nil
	}
	return &candidates[bestIdx]
}

func preferenceScore(worker workerproto.WorkerDescriptor, preferredLabels map[string]string) int {
	score := worker.Weight * 100
	if len(preferredLabels) == 0 {
		return score
	}
	for key, expected := range preferredLabels {
		if worker.Labels[key] == expected {
			score += 10
		}
	}
	return score
}

func (r *MemoryWorkerRegistry) now() time.Time {
	if r != nil && r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *MemoryWorkerRegistry) normalizeWorkerLocked(worker workerproto.WorkerDescriptor) workerproto.WorkerDescriptor {
	if worker.Weight <= 0 {
		worker.Weight = 1
	}
	if r == nil || r.HeartbeatTTL <= 0 {
		return worker
	}
	if !worker.LastHeartbeatAt.IsZero() && r.now().Sub(worker.LastHeartbeatAt) > r.HeartbeatTTL {
		worker.Status = workerproto.StatusOffline
	}
	return worker
}

func supportsType(worker workerproto.WorkerDescriptor, executorType string) bool {
	for _, v := range worker.SupportedExecutorTypes {
		if v == executorType {
			return true
		}
	}
	return false
}

func supportsRef(worker workerproto.WorkerDescriptor, executorRef string) bool {
	for _, v := range worker.SupportedExecutorRefs {
		if v == executorRef {
			return true
		}
	}
	if executorRef == "" {
		return false
	}
	for _, v := range worker.SupportedExecutorTypes {
		if v == string(executor.TypeUnit) {
			return true
		}
	}
	return false
}
