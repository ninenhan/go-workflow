package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/ninenhan/go-workflow/fn"
)

type NodeStatus string

const (
	NodePending   NodeStatus = "pending"
	NodeQueued    NodeStatus = "queued"
	NodeRunning   NodeStatus = "running"
	NodeSucceeded NodeStatus = "succeeded"
	NodeFailed    NodeStatus = "failed"
	NodeSkipped   NodeStatus = "skipped"
)

type RunStatus string

const (
	RunRunning             RunStatus = "running"
	RunSucceeded           RunStatus = "succeeded"
	RunFailed              RunStatus = "failed"
	RunCancelled           RunStatus = "cancelled"
	RunCompletedWithErrors RunStatus = "completed_with_errors"
)

type NodeState struct {
	ID         string           `json:"id,omitempty"`
	Name       string           `json:"name,omitempty"`
	Status     NodeStatus       `json:"status,omitempty"`
	Attempts   int              `json:"attempts,omitempty"`
	StartedAt  time.Time        `json:"started_at,omitempty"`
	FinishedAt time.Time        `json:"finished_at,omitempty"`
	Result     *ExecutionResult `json:"result,omitempty"`
	Error      string           `json:"error,omitempty"`
}

type ExecutionState struct {
	WorkflowID string                `json:"workflow_id,omitempty"`
	RunID      string                `json:"run_id,omitempty"`
	Status     RunStatus             `json:"status,omitempty"`
	StartedAt  time.Time             `json:"started_at,omitempty"`
	UpdatedAt  time.Time             `json:"updated_at,omitempty"`
	Nodes      map[string]*NodeState `json:"nodes,omitempty"`
}

func (s *ExecutionState) Clone() *ExecutionState {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Nodes = make(map[string]*NodeState, len(s.Nodes))
	for k, v := range s.Nodes {
		if v == nil {
			cp.Nodes[k] = nil
			continue
		}
		nc := *v
		cp.Nodes[k] = &nc
	}
	return &cp
}

type StateStore interface {
	Save(ctx context.Context, state *ExecutionState) error
	Load(ctx context.Context, workflowID, runID string) (*ExecutionState, error)
}

type EventType string

const (
	EventRunStarted   EventType = "run_started"
	EventRunFinished  EventType = "run_finished"
	EventNodeStarted  EventType = "node_started"
	EventNodeFinished EventType = "node_finished"
	EventNodeFailed   EventType = "node_failed"
	EventNodeSkipped  EventType = "node_skipped"
	EventEdgeError    EventType = "edge_error"
	EventOutput       EventType = "output"
)

type Event struct {
	Type       EventType `json:"type,omitempty"`
	WorkflowID string    `json:"workflow_id,omitempty"`
	RunID      string    `json:"run_id,omitempty"`
	NodeID     string    `json:"node_id,omitempty"`
	Time       time.Time `json:"time,omitempty"`
	Data       any       `json:"data,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type EventSink interface {
	Emit(ctx context.Context, e Event)
}

type ChannelSink struct {
	ch   chan Event
	once sync.Once
}

func NewChannelSink(buf int) *ChannelSink {
	if buf <= 0 {
		buf = 1
	}
	return &ChannelSink{ch: make(chan Event, buf)}
}

func (s *ChannelSink) Emit(ctx context.Context, e Event) {
	if s == nil {
		return
	}
	select {
	case s.ch <- e:
	case <-ctx.Done():
	default:
	}
}

func (s *ChannelSink) Channel() <-chan Event {
	if s == nil {
		return nil
	}
	return s.ch
}

func (s *ChannelSink) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.ch)
	})
}

type Engine struct {
	Registry    *Registry
	Store       StateStore
	Concurrency int
	Logger      *slog.Logger
	FailFast    bool
}

func NewEngine() *Engine {
	return &Engine{
		Registry:    DefaultRegistry,
		Concurrency: runtime.GOMAXPROCS(0),
		Logger:      slog.Default(),
		FailFast:    true,
	}
}

func (e *Engine) Resume(ctx context.Context, def *WorkflowDefinition, runID string, opts *RunOptions) (*ExecutionState, error) {
	if runID == "" {
		return nil, errors.New("runID is required")
	}
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	store := e.Store
	if opts != nil && opts.Store != nil {
		store = opts.Store
	}
	if store == nil {
		return nil, errors.New("state store is not configured")
	}
	if def.ID == "" {
		return nil, errors.New("workflow id is required for resume")
	}
	prev, err := store.Load(ctx, def.ID, runID)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &RunOptions{}
	}
	opts.RunID = runID
	opts.ResumeState = prev
	return e.Run(ctx, def, opts)
}

type RunOptions struct {
	RunID         string
	Start         []string
	Concurrency   int
	Store         StateStore
	Sink          EventSink
	FailFast      *bool
	AllowCycles   bool
	StopOnControl bool
	SeedState     ContextMap
	SeedExports   map[string][]string
	ResumeState   *ExecutionState
}

type EdgeEnv struct {
	State  map[string]*ExecutionResult
	Result *ExecutionResult
	Node   string
}

type edgeRuntime struct {
	EdgeSpec
	prog *vm.Program
	seq  int
}

type runtimeGraph struct {
	nodes    map[string]*NodeSpec
	outEdges map[string][]*edgeRuntime
	inCount  map[string]int
	start    []string
	active   map[string]struct{}
	inactive map[string]struct{}
}

type nodeTracker struct {
	status   NodeStatus
	inbound  int
	resolved int
	arrived  int
	join     JoinPolicy
	forced   bool
}

type scheduler struct {
	mu        sync.Mutex
	trackers  map[string]*nodeTracker
	pending   int
	ready     chan string
	closeOnce sync.Once
	closed    bool
}

func newScheduler(ready chan string, trackers map[string]*nodeTracker, pending int) *scheduler {
	return &scheduler{
		trackers: trackers,
		pending:  pending,
		ready:    ready,
	}
}

func (s *scheduler) closeReady() {
	s.closeOnce.Do(func() {
		s.closed = true
		close(s.ready)
	})
}

func (s *scheduler) scheduleNodes(ids []string) {
	if s == nil {
		return
	}
	for _, id := range ids {
		if s.isClosed() {
			return
		}
		func() {
			defer func() { _ = recover() }()
			s.ready <- id
		}()
	}
}

func (s *scheduler) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *scheduler) start(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[id]
	if !ok {
		return false
	}
	if t.status != NodeQueued {
		return false
	}
	t.status = NodeRunning
	return true
}

func (s *scheduler) markDone(id string, status NodeStatus) (finished bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[id]
	if !ok {
		return false
	}
	if t.status == NodeSucceeded || t.status == NodeFailed || t.status == NodeSkipped {
		return false
	}
	t.status = status
	s.pending--
	if s.pending <= 0 && !s.closed {
		s.closed = true
		s.closeOnce.Do(func() { close(s.ready) })
		return true
	}
	return false
}

func (s *scheduler) resolveEdge(to string, fired bool) (ready bool, skipped bool) {
	t, ok := s.trackers[to]
	if !ok {
		return false, false
	}
	if t.status != NodePending {
		return false, false
	}
	t.resolved++
	if fired {
		t.arrived++
	}
	switch t.join {
	case JoinAll:
		if t.arrived == t.inbound {
			t.status = NodeQueued
			return true, false
		}
	default:
		if t.arrived >= 1 || t.inbound == 0 {
			t.status = NodeQueued
			return true, false
		}
	}
	if t.resolved >= t.inbound {
		t.status = NodeSkipped
		s.pending--
		return false, true
	}
	return false, false
}

func (e *Engine) Run(ctx context.Context, def *WorkflowDefinition, opts *RunOptions) (*ExecutionState, error) {
	if e == nil {
		e = NewEngine()
	}
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	var resumeState *ExecutionState
	if opts != nil && opts.ResumeState != nil {
		resumeState = opts.ResumeState
		if def.ID == "" && resumeState.WorkflowID != "" {
			def.ID = resumeState.WorkflowID
		}
	}
	if err := def.ValidateBasic(); err != nil {
		return nil, err
	}
	reg := e.Registry
	if reg == nil {
		reg = DefaultRegistry
	}
	rt, err := buildRuntime(def, opts)
	if err != nil {
		return nil, err
	}
	if opts == nil {
		opts = &RunOptions{}
	}
	if !opts.AllowCycles {
		if err := detectCycles(rt); err != nil {
			return nil, err
		}
	}

	runID := opts.RunID
	if runID == "" && resumeState != nil {
		runID = resumeState.RunID
	}
	if runID == "" {
		if id, e := fn.GenerateShortID(); e == nil {
			runID = id
		} else {
			runID = fmt.Sprintf("run-%d", time.Now().UnixNano())
		}
	} else if resumeState != nil && resumeState.RunID != "" && resumeState.RunID != runID {
		return nil, fmt.Errorf("resume run_id mismatch: %s != %s", resumeState.RunID, runID)
	}
	store := e.Store
	if opts.Store != nil {
		store = opts.Store
	}
	sink := opts.Sink
	logger := e.Logger
	if logger == nil {
		logger = slog.Default()
	}
	failFast := e.FailFast
	if opts.FailFast != nil {
		failFast = *opts.FailFast
	}

	concurrency := e.Concurrency
	if opts.Concurrency > 0 {
		concurrency = opts.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 1
	}

	execState := &ExecutionState{
		WorkflowID: def.ID,
		RunID:      runID,
		Status:     RunRunning,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Nodes:      make(map[string]*NodeState, len(rt.nodes)),
	}
	if resumeState != nil {
		execState = resumeState.Clone()
		if execState == nil {
			execState = &ExecutionState{}
		}
		if execState.Nodes == nil {
			execState.Nodes = make(map[string]*NodeState, len(rt.nodes))
		}
		if execState.WorkflowID == "" {
			execState.WorkflowID = def.ID
		}
		if execState.RunID == "" {
			execState.RunID = runID
		}
		if execState.StartedAt.IsZero() {
			execState.StartedAt = time.Now()
		}
		execState.UpdatedAt = time.Now()
	}

	stateMu := sync.RWMutex{}
	stateResults := make(map[string]*ExecutionResult, len(rt.nodes))
	seedExports := map[string][]string{}
	stopOnControl := false
	if opts != nil {
		if opts.SeedExports != nil {
			seedExports = opts.SeedExports
		}
		stopOnControl = opts.StopOnControl
	}
	completedNodes := map[string]NodeStatus{}
	resumeHadErrors := false
	if resumeState != nil {
		completedNodes, resumeHadErrors = applyResumeState(execState, rt, stateResults)
	}

	for id, spec := range rt.nodes {
		if execState.Nodes[id] == nil {
			execState.Nodes[id] = &NodeState{
				ID:     id,
				Name:   spec.Name,
				Status: NodePending,
			}
			continue
		}
		if execState.Nodes[id].ID == "" {
			execState.Nodes[id].ID = id
		}
		if execState.Nodes[id].Name == "" {
			execState.Nodes[id].Name = spec.Name
		}
		if execState.Nodes[id].Status == "" {
			execState.Nodes[id].Status = NodePending
		}
	}

	emit := func(ev Event) {
		if sink == nil {
			return
		}
		ev.WorkflowID = execState.WorkflowID
		ev.RunID = execState.RunID
		if ev.Time.IsZero() {
			ev.Time = time.Now()
		}
		sink.Emit(ctx, ev)
	}

	saveState := func() {
		if store == nil {
			return
		}
		stateMu.RLock()
		snapshot := execState.Clone()
		stateMu.RUnlock()
		_ = store.Save(ctx, snapshot)
	}

	if opts != nil && len(opts.SeedState) > 0 {
		stateMu.Lock()
		for k, v := range opts.SeedState {
			if k == "" || v == nil {
				continue
			}
			stateResults[k] = v
		}
		stateMu.Unlock()
	}

	activeCount := len(rt.active)
	readyCh := make(chan string, activeCount)
	trackers := make(map[string]*nodeTracker, activeCount)
	for id := range rt.active {
		spec := rt.nodes[id]
		join := JoinAny
		if spec != nil && spec.Options != nil && spec.Options.Join != "" {
			join = spec.Options.Join
		}
		trackers[id] = &nodeTracker{
			status:  NodePending,
			inbound: rt.inCount[id],
			join:    join,
		}
	}
	// mark inactive nodes as skipped for visibility
	for id := range rt.inactive {
		if nodeState := execState.Nodes[id]; nodeState != nil {
			nodeState.Status = NodeSkipped
			nodeState.FinishedAt = time.Now()
		}
	}
	if len(rt.inactive) > 0 {
		execState.UpdatedAt = time.Now()
	}
	pendingCount := activeCount
	if len(completedNodes) > 0 {
		for id, t := range trackers {
			if status, ok := completedNodes[id]; ok {
				t.status = status
				pendingCount--
			}
		}
	}
	if pendingCount < 0 {
		pendingCount = 0
	}
	sched := newScheduler(readyCh, trackers, pendingCount)

	var toSchedule []string
	for _, id := range rt.start {
		if t := trackers[id]; t != nil && t.status == NodePending {
			t.forced = true
			t.status = NodeQueued
			toSchedule = append(toSchedule, id)
		}
	}
	if len(rt.start) == 0 {
		for id, t := range trackers {
			if t.inbound == 0 && t.status == NodePending {
				t.status = NodeQueued
				toSchedule = append(toSchedule, id)
			}
		}
	}
	sched.scheduleNodes(toSchedule)

	if resumeState != nil && len(completedNodes) > 0 {
		seedResumeEdges(completedNodes, stateResults, sched, rt, execState, &stateMu, logger)
	}

	if sched.pending > 0 {
		execState.Status = RunRunning
		execState.UpdatedAt = time.Now()
		emit(Event{Type: EventRunStarted})
		saveState()
	} else if resumeState == nil {
		emit(Event{Type: EventRunStarted})
		saveState()
	}

	if sched.pending <= 0 {
		sched.closeReady()
	}
	if resumeState != nil && sched.pending <= 0 {
		saveState()
		return execState, nil
	}

	ctxRun, cancel := context.WithCancel(ctx)
	defer cancel()

	var runErr atomic.Value
	var hadErrors atomic.Bool
	if resumeHadErrors {
		hadErrors.Store(true)
	}
	var controlSignal atomic.Value

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range readyCh {
				if ctxRun.Err() != nil {
					return
				}
				if !sched.start(id) {
					continue
				}
				spec := rt.nodes[id]
				nodeState := execState.Nodes[id]

				stateMu.Lock()
				nodeState.Status = NodeRunning
				if nodeState.StartedAt.IsZero() {
					nodeState.StartedAt = time.Now()
				}
				stateMu.Unlock()

				emit(Event{Type: EventNodeStarted, NodeID: id})
				saveState()

				unit, ok := reg.New(spec.Unit)
				if !ok {
					err := fmt.Errorf("unit %s not registered", spec.Unit)
					handleNodeFailure(ctxRun, execState, stateResults, &stateMu, sched, rt, spec, id, err, 0, failFast, &runErr, &hadErrors, emit, saveState, logger)
					if failFast {
						cancel()
						sched.closeReady()
						return
					}
					continue
				}

				snapshot := snapshotState(&stateMu, stateResults)
				input := prepareInput(spec, snapshot, rt.nodes, seedExports)
				node := &Node{
					ID:           id,
					Name:         spec.Name,
					Input:        input,
					Params:       spec.Params,
					ExportFields: spec.ExportFields,
					Execute:      unit.Execute,
				}

				res, attempts, err := executeWithRetry(ctxRun, unit, snapshot, node, spec.Options)
				if err != nil {
					handleNodeFailure(ctxRun, execState, stateResults, &stateMu, sched, rt, spec, id, err, attempts, failFast, &runErr, &hadErrors, emit, saveState, logger)
					if failFast {
						cancel()
						sched.closeReady()
						return
					}
					continue
				}

				if res != nil && res.Control != "" && stopOnControl {
					handleNodeSuccess(ctxRun, execState, stateResults, &stateMu, sched, rt, spec, id, res, attempts, true, emit, saveState, logger)
					if controlSignal.Load() == nil {
						controlSignal.Store(&ControlSignalError{
							Signal: res.Control,
							Target: res.ControlTarget,
						})
					}
					cancel()
					sched.closeReady()
					return
				}
				handleNodeSuccess(ctxRun, execState, stateResults, &stateMu, sched, rt, spec, id, res, attempts, false, emit, saveState, logger)
			}
		}()
	}

	wg.Wait()

	var controlErr *ControlSignalError
	if v := controlSignal.Load(); v != nil {
		if ce, ok := v.(*ControlSignalError); ok {
			controlErr = ce
		}
	}
	if ctxRun.Err() != nil && runErr.Load() == nil && !(controlErr != nil && stopOnControl) {
		runErr.Store(ctxRun.Err())
	}

	stateMu.Lock()
	if errVal := runErr.Load(); errVal != nil {
		execState.Status = RunFailed
	} else if controlErr != nil {
		execState.Status = RunCancelled
	} else if ctxRun.Err() != nil {
		execState.Status = RunCancelled
	} else if hadErrors.Load() {
		execState.Status = RunCompletedWithErrors
	} else {
		execState.Status = RunSucceeded
	}
	execState.UpdatedAt = time.Now()
	stateMu.Unlock()

	emit(Event{Type: EventRunFinished, Data: execState.Status})
	saveState()

	if errVal := runErr.Load(); errVal != nil {
		if err, ok := errVal.(error); ok {
			return execState, err
		}
		return execState, fmt.Errorf("run failed")
	}
	if controlErr != nil && stopOnControl {
		return execState, controlErr
	}
	return execState, nil
}

func buildRuntime(def *WorkflowDefinition, opts *RunOptions) (*runtimeGraph, error) {
	nodes := make(map[string]*NodeSpec, len(def.Nodes))
	for id, spec := range def.Nodes {
		if spec == nil {
			return nil, fmt.Errorf("node %s is nil", id)
		}
		copied := *spec
		copied.ID = id
		if copied.Unit == "" {
			copied.Unit = copied.UnitID
		}
		nodes[id] = &copied
	}

	explicitOut := make(map[string][]*edgeRuntime, len(nodes))
	explicitIn := make(map[string]int, len(nodes))
	for id := range nodes {
		explicitIn[id] = 0
	}

	seq := 0
	for _, edge := range def.Edges {
		if edge.From == "" || edge.To == "" {
			return nil, fmt.Errorf("edge missing from/to: %+v", edge)
		}
		if nodes[edge.From] == nil || nodes[edge.To] == nil {
			return nil, fmt.Errorf("edge references missing node: %s -> %s", edge.From, edge.To)
		}
		er := &edgeRuntime{EdgeSpec: edge, seq: seq}
		seq++
		if edge.When != "" {
			prog, err := expr.Compile(edge.When, expr.Env(EdgeEnv{}), expr.AsBool())
			if err != nil {
				return nil, fmt.Errorf("edge when compile failed (%s->%s): %w", edge.From, edge.To, err)
			}
			er.prog = prog
		}
		explicitOut[edge.From] = append(explicitOut[edge.From], er)
		explicitIn[edge.To]++
	}

	start := def.Start
	startProvided := len(def.Start) > 0
	if opts != nil && len(opts.Start) > 0 {
		start = opts.Start
		startProvided = true
	}
	if len(start) == 0 {
		for id, spec := range nodes {
			if nodeRole(spec) == RoleExec && explicitIn[id] == 0 {
				start = append(start, id)
			}
		}
	}
	if len(start) == 0 {
		return nil, errors.New("no start node determined")
	}
	for _, s := range start {
		if nodes[s] == nil {
			return nil, fmt.Errorf("start node %s not found", s)
		}
	}

	startSet := make(map[string]struct{}, len(start))
	for _, id := range start {
		startSet[id] = struct{}{}
	}

	reachable := make(map[string]struct{}, len(nodes))
	queue := append([]string{}, start...)
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if _, ok := reachable[curr]; ok {
			continue
		}
		if isManual(nodes[curr]) {
			if _, ok := startSet[curr]; !ok {
				continue
			}
		}
		reachable[curr] = struct{}{}
		for _, e := range explicitOut[curr] {
			queue = append(queue, e.To)
		}
	}

	active := make(map[string]struct{}, len(nodes))
	for id := range reachable {
		active[id] = struct{}{}
	}
	for id, spec := range nodes {
		if nodeRole(spec) == RoleConfig && nodeRunMode(spec) == RunModeEager {
			if isManual(spec) {
				if _, ok := startSet[id]; !ok {
					continue
				}
			}
			active[id] = struct{}{}
		}
	}

	depQueue := make([]string, 0, len(active))
	for id := range active {
		depQueue = append(depQueue, id)
	}
	for len(depQueue) > 0 {
		curr := depQueue[0]
		depQueue = depQueue[1:]
		spec := nodes[curr]
		for _, dep := range collectDeps(spec) {
			if nodes[dep] == nil {
				continue
			}
			if isManual(nodes[dep]) {
				if _, ok := startSet[dep]; !ok {
					continue
				}
			}
			if _, ok := active[dep]; !ok {
				active[dep] = struct{}{}
				depQueue = append(depQueue, dep)
			}
		}
	}

	inactive := make(map[string]struct{}, len(nodes))
	for id := range nodes {
		if _, ok := active[id]; !ok {
			inactive[id] = struct{}{}
		}
	}

	outEdges := make(map[string][]*edgeRuntime, len(active))
	inCount := make(map[string]int, len(active))
	for id := range active {
		inCount[id] = 0
	}

	explicitEdgeSet := make(map[string]struct{})
	for from, edges := range explicitOut {
		if _, ok := active[from]; !ok {
			continue
		}
		for _, e := range edges {
			if _, ok := active[e.To]; !ok {
				continue
			}
			outEdges[from] = append(outEdges[from], e)
			inCount[e.To]++
			explicitEdgeSet[from+"->"+e.To] = struct{}{}
		}
	}

	for id := range active {
		spec := nodes[id]
		for _, dep := range collectDeps(spec) {
			if dep == "" || dep == id {
				continue
			}
			if _, ok := active[dep]; !ok {
				continue
			}
			key := dep + "->" + id
			if _, ok := explicitEdgeSet[key]; ok {
				continue
			}
			outEdges[dep] = append(outEdges[dep], &edgeRuntime{EdgeSpec: EdgeSpec{From: dep, To: id}})
			inCount[id]++
		}
	}

	// expand start with config/trigger nodes (inCount=0) to ensure they run
	startSet = make(map[string]struct{})
	var runStart []string
	for _, id := range start {
		if _, ok := active[id]; ok {
			if _, seen := startSet[id]; !seen {
				startSet[id] = struct{}{}
				runStart = append(runStart, id)
			}
		}
	}
	if !startProvided {
		if len(runStart) == 0 {
			for id := range active {
				if inCount[id] == 0 {
					if _, seen := startSet[id]; !seen {
						startSet[id] = struct{}{}
						runStart = append(runStart, id)
					}
				}
			}
		}
	} else {
		for id := range active {
			if inCount[id] == 0 && nodeRole(nodes[id]) != RoleExec {
				if isManual(nodes[id]) {
					if _, ok := startSet[id]; !ok {
						continue
					}
				}
				if _, seen := startSet[id]; !seen {
					startSet[id] = struct{}{}
					runStart = append(runStart, id)
				}
			}
		}
	}
	if len(runStart) == 0 {
		return nil, errors.New("no start node determined")
	}

	return &runtimeGraph{
		nodes:    nodes,
		outEdges: outEdges,
		inCount:  inCount,
		start:    runStart,
		active:   active,
		inactive: inactive,
	}, nil
}

func nodeRole(spec *NodeSpec) NodeRole {
	if spec == nil || spec.Role == "" {
		return RoleExec
	}
	return spec.Role
}

func nodeRunMode(spec *NodeSpec) RunMode {
	if spec == nil || spec.RunMode == "" {
		return RunModeLazy
	}
	return spec.RunMode
}

func isManual(spec *NodeSpec) bool {
	return nodeRunMode(spec) == RunModeManual
}

func collectDeps(spec *NodeSpec) []string {
	if spec == nil {
		return nil
	}
	var deps []string
	deps = append(deps, spec.DependsOn...)
	deps = append(deps, templateDeps(spec.Input)...)
	return uniqueStrings(deps)
}

func templateDeps(input *Input) []string {
	if input == nil || !input.Slottable {
		return nil
	}
	str, ok := input.Data.(string)
	if !ok || str == "" {
		return nil
	}
	parsed, err := fn.ParseTemplate(str)
	if err != nil || len(parsed) == 0 {
		return nil
	}
	var deps []string
	for key := range parsed {
		if idx := strings.Index(key, "."); idx > 0 {
			deps = append(deps, key[:idx])
		}
	}
	return uniqueStrings(deps)
}

func uniqueStrings(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	var out []string
	for _, it := range items {
		if it == "" {
			continue
		}
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

func applyResumeState(execState *ExecutionState, rt *runtimeGraph, stateResults map[string]*ExecutionResult) (map[string]NodeStatus, bool) {
	completed := make(map[string]NodeStatus)
	hadErrors := false
	if execState == nil || rt == nil {
		return completed, hadErrors
	}
	for id := range rt.active {
		node := execState.Nodes[id]
		if node == nil {
			node = &NodeState{ID: id, Status: NodePending}
			execState.Nodes[id] = node
		}
		switch node.Status {
		case NodeSucceeded, NodeFailed, NodeSkipped:
			completed[id] = node.Status
			if node.Result != nil {
				stateResults[id] = node.Result
			} else if node.Status == NodeFailed && node.Error != "" {
				res := &ExecutionResult{Error: node.Error}
				node.Result = res
				stateResults[id] = res
			}
			if node.Status == NodeFailed {
				hadErrors = true
			}
		default:
			node.Status = NodePending
			node.Error = ""
			node.Result = nil
		}
	}
	return completed, hadErrors
}

func seedResumeEdges(
	completed map[string]NodeStatus,
	stateResults map[string]*ExecutionResult,
	sched *scheduler,
	rt *runtimeGraph,
	execState *ExecutionState,
	stateMu *sync.RWMutex,
	logger *slog.Logger,
) {
	if len(completed) == 0 || sched == nil || rt == nil {
		return
	}
	noopEmit := func(Event) {}
	noopSave := func() {}
	for id, status := range completed {
		edges := rt.outEdges[id]
		if len(edges) == 0 {
			continue
		}
		res := stateResults[id]
		spec := rt.nodes[id]
		switch status {
		case NodeSucceeded:
			evaluateEdges(id, res, stateResults, sched, rt, noopEmit, execState, stateMu, noopSave, logger)
		case NodeFailed:
			if spec != nil && spec.Options != nil && spec.Options.ContinueOnError {
				evaluateEdges(id, res, stateResults, sched, rt, noopEmit, execState, stateMu, noopSave, logger)
			} else {
				results := make([]bool, len(edges))
				applyEdgeResults(id, edges, results, sched, rt, noopEmit, execState, stateMu, noopSave)
			}
		case NodeSkipped:
			results := make([]bool, len(edges))
			applyEdgeResults(id, edges, results, sched, rt, noopEmit, execState, stateMu, noopSave)
		}
	}
}

func orderEdges(edges []*edgeRuntime) []*edgeRuntime {
	if len(edges) <= 1 {
		return edges
	}
	ordered := make([]*edgeRuntime, len(edges))
	copy(ordered, edges)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Order != ordered[j].Order {
			return ordered[i].Order < ordered[j].Order
		}
		return ordered[i].seq < ordered[j].seq
	})
	return ordered
}

func detectCycles(rt *runtimeGraph) error {
	visiting := make(map[string]bool, len(rt.nodes))
	visited := make(map[string]bool, len(rt.nodes))
	var visit func(string) error
	visit = func(n string) error {
		if visiting[n] {
			return fmt.Errorf("cycle detected at node %s", n)
		}
		if visited[n] {
			return nil
		}
		visiting[n] = true
		for _, e := range rt.outEdges[n] {
			if err := visit(e.To); err != nil {
				return err
			}
		}
		visiting[n] = false
		visited[n] = true
		return nil
	}
	for id := range rt.active {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func snapshotState(mu *sync.RWMutex, data map[string]*ExecutionResult) ContextMap {
	mu.RLock()
	defer mu.RUnlock()
	cp := make(ContextMap, len(data))
	for k, v := range data {
		cp[k] = v
	}
	return cp
}

func prepareInput(spec *NodeSpec, snapshot ContextMap, nodes map[string]*NodeSpec, seedExports map[string][]string) *Input {
	if spec == nil || spec.Input == nil {
		return nil
	}
	inputCopy := *spec.Input
	if !inputCopy.Slottable {
		return &inputCopy
	}
	str, ok := inputCopy.Data.(string)
	if !ok {
		return &inputCopy
	}
	exported := buildExported(snapshot, nodes, seedExports)
	parsed, _ := fn.ParseTemplate(str)
	rendered := fn.RenderTemplateStrictly(str, parsed, exported, false)
	inputCopy.Data = rendered
	inputCopy.Slottable = false
	return &inputCopy
}

func buildExported(snapshot ContextMap, nodes map[string]*NodeSpec, seedExports map[string][]string) map[string]any {
	exported := make(map[string]any)
	for nodeName, result := range snapshot {
		if result == nil {
			continue
		}
		fields := []string{}
		if spec := nodes[nodeName]; spec != nil {
			fields = spec.ExportFields
		} else if seedExports != nil {
			fields = seedExports[nodeName]
		}
		if len(fields) == 0 {
			continue
		}
		resultMap, ok := result.Data.(map[string]any)
		if !ok {
			continue
		}
		sub, ok := exported[nodeName].(map[string]any)
		if !ok || sub == nil {
			sub = make(map[string]any)
			exported[nodeName] = sub
		}
		if len(fields) == 1 && fields[0] == "*" {
			for k, val := range resultMap {
				sub[k] = val
				exported[fmt.Sprintf("%s.%s", nodeName, k)] = val
			}
			continue
		}
		for _, field := range fields {
			if val, exists := resultMap[field]; exists {
				sub[field] = val
				exported[fmt.Sprintf("%s.%s", nodeName, field)] = val
			}
		}
	}
	return exported
}

func executeWithRetry(ctx context.Context, unit ExecutableUnit, snapshot ContextMap, node *Node, opts *NodeOptions) (*ExecutionResult, int, error) {
	retries := 0
	backoff := time.Duration(0)
	maxBackoff := time.Duration(0)
	if opts != nil {
		retries = opts.Retries
		backoff = opts.RetryBackoff.Duration
		maxBackoff = opts.RetryBackoffMax.Duration
	}
	if retries < 0 {
		retries = 0
	}
	if backoff <= 0 && retries > 0 {
		backoff = 100 * time.Millisecond
	}
	attempt := 0
	for {
		attempt++
		runCtx := ctx
		var cancel context.CancelFunc
		if opts != nil && opts.Timeout.Duration > 0 {
			runCtx, cancel = context.WithTimeout(ctx, opts.Timeout.Duration)
		}
		res, err := unit.Execute(runCtx, snapshot, node)
		if cancel != nil {
			cancel()
		}
		if err == nil {
			return res, attempt, nil
		}
		if attempt > retries {
			return nil, attempt, err
		}
		wait := backoff
		if wait > 0 && attempt > 1 {
			wait = backoff * time.Duration(1<<uint(attempt-1))
		}
		if maxBackoff > 0 && wait > maxBackoff {
			wait = maxBackoff
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, attempt, ctx.Err()
		}
	}
}

func handleNodeFailure(
	ctx context.Context,
	execState *ExecutionState,
	stateResults map[string]*ExecutionResult,
	stateMu *sync.RWMutex,
	sched *scheduler,
	rt *runtimeGraph,
	spec *NodeSpec,
	id string,
	err error,
	attempts int,
	failFast bool,
	runErr *atomic.Value,
	hadErrors *atomic.Bool,
	emit func(Event),
	saveState func(),
	logger *slog.Logger,
) {
	if err == nil {
		return
	}
	hadErrors.Store(true)
	if runErr.Load() == nil && failFast {
		runErr.Store(err)
	}

	res := &ExecutionResult{Error: err.Error()}
	stateMu.Lock()
	stateResults[id] = res
	nodeState := execState.Nodes[id]
	nodeState.Status = NodeFailed
	nodeState.Attempts = attempts
	nodeState.Error = err.Error()
	nodeState.Result = res
	nodeState.FinishedAt = time.Now()
	execState.UpdatedAt = time.Now()
	stateMu.Unlock()

	emit(Event{Type: EventNodeFailed, NodeID: id, Error: err.Error()})
	saveState()

	sched.markDone(id, NodeFailed)

	if spec != nil && spec.Options != nil && spec.Options.ContinueOnError {
		evaluateEdges(id, res, stateResults, sched, rt, emit, execState, stateMu, saveState, logger)
		return
	}

	// propagate blocked edges for downstream nodes
	edges := rt.outEdges[id]
	if len(edges) == 0 {
		return
	}
	results := make([]bool, len(edges))
	applyEdgeResults(id, edges, results, sched, rt, emit, execState, stateMu, saveState)
}

func handleNodeSuccess(
	ctx context.Context,
	execState *ExecutionState,
	stateResults map[string]*ExecutionResult,
	stateMu *sync.RWMutex,
	sched *scheduler,
	rt *runtimeGraph,
	spec *NodeSpec,
	id string,
	res *ExecutionResult,
	attempts int,
	skipEdges bool,
	emit func(Event),
	saveState func(),
	logger *slog.Logger,
) {
	stateMu.Lock()
	stateResults[id] = res
	nodeState := execState.Nodes[id]
	nodeState.Status = NodeSucceeded
	nodeState.Attempts = attempts
	nodeState.Result = res
	nodeState.FinishedAt = time.Now()
	execState.UpdatedAt = time.Now()
	stateMu.Unlock()

	emit(Event{Type: EventNodeFinished, NodeID: id, Data: res})
	saveState()

	sched.markDone(id, NodeSucceeded)
	if !skipEdges {
		evaluateEdges(id, res, stateResults, sched, rt, emit, execState, stateMu, saveState, logger)
	}
}

func evaluateEdges(
	id string,
	res *ExecutionResult,
	stateResults map[string]*ExecutionResult,
	sched *scheduler,
	rt *runtimeGraph,
	emit func(Event),
	execState *ExecutionState,
	stateMu *sync.RWMutex,
	saveState func(),
	logger *slog.Logger,
) {
	edges := rt.outEdges[id]
	if len(edges) == 0 {
		return
	}
	edges = orderEdges(edges)
	stateSnapshot := snapshotState(stateMu, stateResults)
	stateSnapshot[id] = res
	spec := rt.nodes[id]
	mode := BranchAll
	if spec != nil && spec.Options != nil && spec.Options.BranchMode != "" {
		mode = spec.Options.BranchMode
	}
	edgeResults := make([]bool, len(edges))
	if mode == BranchFirst {
		chosen := -1
		defaultIdx := -1
		for i, edge := range edges {
			if edge.prog == nil {
				if defaultIdx < 0 {
					defaultIdx = i
				}
				continue
			}
			out, err := expr.Run(edge.prog, EdgeEnv{
				State:  stateSnapshot,
				Result: res,
				Node:   id,
			})
			if err != nil {
				emit(Event{Type: EventEdgeError, NodeID: id, Error: err.Error()})
				if logger != nil {
					logger.Warn("edge condition eval failed", "from", edge.From, "to", edge.To, "err", err)
				}
				continue
			}
			if b, ok := out.(bool); ok && b {
				chosen = i
				break
			}
		}
		if chosen < 0 && defaultIdx >= 0 {
			chosen = defaultIdx
		}
		if chosen >= 0 {
			edgeResults[chosen] = true
		}
	} else {
		for i, edge := range edges {
			ok := true
			if edge.prog != nil {
				out, err := expr.Run(edge.prog, EdgeEnv{
					State:  stateSnapshot,
					Result: res,
					Node:   id,
				})
				if err != nil {
					ok = false
					emit(Event{Type: EventEdgeError, NodeID: id, Error: err.Error()})
					if logger != nil {
						logger.Warn("edge condition eval failed", "from", edge.From, "to", edge.To, "err", err)
					}
				} else if b, ok2 := out.(bool); ok2 {
					ok = b
				} else {
					ok = false
				}
			}
			edgeResults[i] = ok
		}
	}
	applyEdgeResults(id, edges, edgeResults, sched, rt, emit, execState, stateMu, saveState)
}

func applyEdgeResults(
	from string,
	edges []*edgeRuntime,
	results []bool,
	sched *scheduler,
	rt *runtimeGraph,
	emit func(Event),
	execState *ExecutionState,
	stateMu *sync.RWMutex,
	saveState func(),
) {
	if len(edges) == 0 {
		return
	}
	var toSchedule []string
	var toSkip []string
	sched.mu.Lock()
	for i, edge := range edges {
		ready, skipped := sched.resolveEdge(edge.To, results[i])
		if ready {
			toSchedule = append(toSchedule, edge.To)
		} else if skipped {
			toSkip = append(toSkip, edge.To)
		}
	}
	sched.mu.Unlock()

	if len(toSchedule) > 0 {
		sched.scheduleNodes(toSchedule)
	}
	for _, id := range toSkip {
		propagateBlocked(id, sched, rt, emit, execState, stateMu, saveState)
	}
}

func propagateBlocked(
	id string,
	sched *scheduler,
	rt *runtimeGraph,
	emit func(Event),
	execState *ExecutionState,
	stateMu *sync.RWMutex,
	saveState func(),
) {
	var queue []string
	queue = append(queue, id)
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		stateMu.Lock()
		nodeState := execState.Nodes[curr]
		if nodeState != nil {
			nodeState.Status = NodeSkipped
			nodeState.FinishedAt = time.Now()
			execState.UpdatedAt = time.Now()
		}
		stateMu.Unlock()
		emit(Event{Type: EventNodeSkipped, NodeID: curr})
		saveState()

		edges := rt.outEdges[curr]
		if len(edges) == 0 {
			continue
		}

		var toSchedule []string
		var toSkip []string
		sched.mu.Lock()
		for _, edge := range edges {
			ready, skipped := sched.resolveEdge(edge.To, false)
			if ready {
				toSchedule = append(toSchedule, edge.To)
			} else if skipped {
				toSkip = append(toSkip, edge.To)
			}
		}
		sched.mu.Unlock()
		if len(toSchedule) > 0 {
			sched.scheduleNodes(toSchedule)
		}
		if len(toSkip) > 0 {
			queue = append(queue, toSkip...)
		}
	}
}
