package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
)

var ErrNilDefinition = errors.New("workflow definition is nil")

const (
	MaxNodeLoopIterations  = 1000
	MaxNodeRetryAttempts   = 10
	MaxNodeRetryBackoff    = 5 * time.Minute
	MaxWorkflowConcurrency = 1024
	shortcutAutoInputUIKey = "shortcut_auto_input"
)

type Compiler interface {
	Compile(version *definition.WorkflowVersion) (*ExecutionPlan, error)
}

type DefaultCompiler struct {
	Now func() time.Time
}

func NewCompiler() *DefaultCompiler {
	return &DefaultCompiler{
		Now: time.Now,
	}
}

func (c *DefaultCompiler) Compile(version *definition.WorkflowVersion) (*ExecutionPlan, error) {
	if version == nil || version.Definition == nil {
		return nil, ErrNilDefinition
	}
	def := version.Definition
	if err := validateDefinition(def); err != nil {
		return nil, err
	}
	effectiveEdges, disabledNodes, disabledSources, err := resolveDisabledNodes(def)
	if err != nil {
		return nil, err
	}
	effectiveEdges, gatewayEntries, err := compileParallelGateways(def, effectiveEdges)
	if err != nil {
		return nil, err
	}

	nodes := make(map[string]PlanNode, len(def.Nodes))
	concurrencyGroups := make(map[string]int)
	resourcePools := make(map[string]int)
	adjacency := make(map[string][]string, len(def.Nodes))
	dependencies := make(map[string][]string, len(def.Nodes))
	flowDependencies := make(map[string][]string, len(def.Nodes))
	indegree := make(map[string]int, len(def.Nodes))
	branches := make(map[string]BranchMeta)
	backEdges := make(map[string]BranchMeta)

	for _, node := range def.Nodes {
		if node.Disabled || node.IsParallelGateway() {
			continue
		}
		retry := RetryPolicy{}
		if node.Retry != nil {
			retry = RetryPolicy(*node.Retry)
		}
		planNode := PlanNode{
			ID:               node.ID,
			Name:             node.Name,
			Type:             node.Type,
			ExecutorType:     node.Executor.Type,
			ExecutorRef:      node.Executor.Ref,
			ExecutorConf:     cloneMap(node.Executor.Config),
			Input:            node.Input,
			InputSpec:        cloneInputSpec(node.InputSpec),
			Params:           cloneMap(node.Params),
			ParamBindings:    cloneParamBindings(node.ParamBindings),
			ParamTemplates:   cloneParamTemplates(node.ParamTemplates),
			ConcurrencyGroup: node.ConcurrencyGroup,
			ResourcePool:     node.ResourcePool,
			Retry:            retry,
			Loop:             cloneLoop(node.Loop),
			Timeout:          node.Timeout,
			ContinueOnError:  boolFromMap(node.Params, "continue_on_error"),
		}
		if err := rebindDisabledInputs(node, &planNode, disabledNodes, disabledSources); err != nil {
			return nil, err
		}
		nodes[node.ID] = planNode
		if node.ConcurrencyGroup != "" {
			concurrencyGroups[node.ConcurrencyGroup] = node.ConcurrencyLimit
		}
		if node.ResourcePool != "" {
			resourcePools[node.ResourcePool] = node.ResourceCapacity
		}
		adjacency[node.ID] = []string{}
		dependencies[node.ID] = []string{}
		flowDependencies[node.ID] = []string{}
		indegree[node.ID] = 0
	}

	for _, edge := range effectiveEdges {
		if _, ok := nodes[edge.From]; !ok {
			continue
		}
		if _, ok := nodes[edge.To]; !ok {
			continue
		}
		if edge.Kind == definition.EdgeKindBack {
			meta := backEdges[edge.From]
			if meta.From == "" {
				meta = BranchMeta{From: edge.From, Mode: string(definition.BranchFirst)}
			}
			meta.Edges = append(meta.Edges, BranchEdge{
				To:        edge.To,
				Condition: edge.Condition,
				Priority:  edge.Priority,
				Label:     edge.Label,
			})
			backEdges[edge.From] = meta
			continue
		}
		adjacency[edge.From] = append(adjacency[edge.From], edge.To)
		dependencies[edge.To] = appendUnique(dependencies[edge.To], edge.From)
		flowDependencies[edge.To] = appendUnique(flowDependencies[edge.To], edge.From)
		indegree[edge.To]++

		meta := branches[edge.From]
		if meta.From == "" {
			meta = BranchMeta{From: edge.From, Mode: planBranchMode(def, edge.From)}
		}
		meta.Edges = append(meta.Edges, BranchEdge{
			To:        edge.To,
			Condition: edge.Condition,
			Priority:  edge.Priority,
			Label:     edge.Label,
		})
		branches[edge.From] = meta
	}

	for _, node := range def.Nodes {
		if node.Disabled || node.IsParallelGateway() {
			continue
		}
		for _, dep := range node.DependsOn {
			if _, ok := nodes[dep]; !ok {
				continue
			}
			adjacency[dep] = appendUnique(adjacency[dep], node.ID)
			dependencies[node.ID] = appendUnique(dependencies[node.ID], dep)
		}
	}

	for id, deps := range dependencies {
		indegree[id] = len(deps)
	}

	entryNodes := def.EntryNodes
	if len(entryNodes) > 0 {
		entryNodes, err = resolveGatewayEntryNodes(entryNodes, gatewayEntries, nodes)
		if err != nil {
			return nil, err
		}
	}
	entry, err := resolveEntryNodes(entryNodes, nodes, disabledNodes, def.Edges, gatewayEntries)
	if err != nil {
		return nil, err
	}
	if len(entry) == 0 && len(def.EntryNodes) == 0 {
		for id := range nodes {
			if indegree[id] == 0 {
				entry = append(entry, id)
			}
		}
		sort.Strings(entry)
	}
	if len(entry) == 0 {
		return nil, errors.New("no entry node")
	}

	topo, err := topoSort(nodes, adjacency, indegree)
	if err != nil {
		return nil, err
	}

	exit := make([]string, 0, len(nodes))
	for id := range nodes {
		if len(adjacency[id]) == 0 {
			exit = append(exit, id)
		}
	}
	sort.Strings(exit)

	normalizeBranches(branches)
	normalizeBranches(backEdges)
	if err := validateBackEdges(backEdges, topo); err != nil {
		return nil, err
	}
	if err := validateInputBindings(nodes, dependencies); err != nil {
		return nil, err
	}
	loopGroups, err := compileLoopGroups(def.LoopGroups, nodes, adjacency, dependencies, flowDependencies, backEdges, entry, topo)
	if err != nil {
		return nil, err
	}
	if err := validateLoopGroupVariables(nodes, loopGroups); err != nil {
		return nil, err
	}

	planID, err := buildPlanID(version)
	if err != nil {
		return nil, err
	}
	plan := &ExecutionPlan{
		PlanID:            planID,
		WorkflowID:        version.WorkflowID,
		WorkflowVersionID: version.ID,
		MaxConcurrency:    def.MaxConcurrency,
		FailFast:          def.FailFast,
		ConcurrencyGroups: cloneIntMap(concurrencyGroups),
		ResourcePools:     cloneIntMap(resourcePools),
		EntryNodes:        append([]string{}, entry...),
		ExitNodes:         exit,
		Adjacency:         adjacency,
		Dependencies:      dependencies,
		TopologicalOrder:  topo,
		Nodes:             nodes,
		Branches:          branches,
		BackEdges:         backEdges,
		LoopGroups:        loopGroups,
		CreatedAt:         c.now(),
	}
	return plan, nil
}

func compileLoopGroups(
	groups []definition.LoopGroup,
	nodes map[string]PlanNode,
	adjacency map[string][]string,
	dependencies map[string][]string,
	flowDependencies map[string][]string,
	backEdges map[string]BranchMeta,
	entryNodes []string,
	topo []string,
) (map[string]LoopGroup, error) {
	result := make(map[string]LoopGroup, len(groups))
	compiled := make([]LoopGroup, 0, len(groups))
	entrySet := make(map[string]struct{}, len(entryNodes))
	for _, id := range entryNodes {
		entrySet[id] = struct{}{}
	}
	for _, group := range groups {
		id := strings.TrimSpace(group.ID)
		start := strings.TrimSpace(group.Start)
		end := strings.TrimSpace(group.End)
		mode := strings.TrimSpace(group.Mode)
		if mode == "" {
			mode = "count"
		}
		if id == "" {
			return nil, errors.New("loop group id is required")
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("duplicated loop group id: %s", id)
		}
		if _, ok := nodes[start]; !ok {
			return nil, fmt.Errorf("loop group %s start node not found or disabled: %s", id, start)
		}
		if _, ok := nodes[end]; !ok {
			return nil, fmt.Errorf("loop group %s end node not found or disabled: %s", id, end)
		}
		if mode != "count" && mode != "each" {
			return nil, fmt.Errorf("loop group %s mode must be count or each", id)
		}
		if group.MaxIterations < 2 || group.MaxIterations > MaxNodeLoopIterations {
			return nil, fmt.Errorf("loop group %s max_iterations must be between 2 and %d", id, MaxNodeLoopIterations)
		}
		var countBinding *InputBinding
		if group.CountBinding != nil {
			if mode != "count" {
				return nil, fmt.Errorf("loop group %s each-item mode cannot define count_binding", id)
			}
			cloned := cloneInputBinding(*group.CountBinding)
			depSet := make(map[string]struct{}, len(dependencies[start]))
			for _, dependency := range dependencies[start] {
				depSet[dependency] = struct{}{}
			}
			if err := validateBinding(start, fmt.Sprintf("loop group %s count", id), cloned, nodes, depSet); err != nil {
				return nil, err
			}
			countBinding = &cloned
		}
		if nodes[start].Loop != nil || nodes[end].Loop != nil {
			return nil, fmt.Errorf("loop group %s boundaries cannot also define node loops", id)
		}

		forward := reachableNodes(adjacency, start)
		backward := reachableNodes(dependencies, end)
		if _, ok := forward[end]; !ok {
			return nil, fmt.Errorf("loop group %s end %s is not reachable from start %s", id, end, start)
		}
		scopeSet := make(map[string]struct{})
		for nodeID := range forward {
			if _, ok := backward[nodeID]; ok {
				scopeSet[nodeID] = struct{}{}
			}
		}
		scope := make([]string, 0, len(scopeSet))
		for _, nodeID := range topo {
			if _, ok := scopeSet[nodeID]; !ok {
				continue
			}
			scope = append(scope, nodeID)
		}
		for _, nodeID := range scope {
			for _, dependency := range dependencies[nodeID] {
				if _, internal := scopeSet[dependency]; !internal &&
					nodeID != start &&
					containsString(flowDependencies[nodeID], dependency) {
					return nil, fmt.Errorf("loop group %s has external input %s -> %s outside its start node", id, dependency, nodeID)
				}
			}
			for _, target := range adjacency[nodeID] {
				if _, internal := scopeSet[target]; !internal && nodeID != end {
					return nil, fmt.Errorf("loop group %s has external output %s -> %s before its end node", id, nodeID, target)
				}
			}
			if _, entry := entrySet[nodeID]; entry && nodeID != start {
				return nil, fmt.Errorf("loop group %s contains entry node %s outside its start", id, nodeID)
			}
		}
		for source, meta := range backEdges {
			if _, internal := scopeSet[source]; internal {
				return nil, fmt.Errorf("loop group %s cannot contain back-edge source %s", id, source)
			}
			for _, edge := range meta.Edges {
				if _, internal := scopeSet[edge.To]; internal {
					return nil, fmt.Errorf("loop group %s cannot contain back-edge target %s", id, edge.To)
				}
			}
		}
		compiled = append(compiled, LoopGroup{
			ID:            id,
			Start:         start,
			End:           end,
			Mode:          mode,
			MaxIterations: group.MaxIterations,
			CountBinding:  countBinding,
			Scope:         scope,
		})
		result[id] = compiled[len(compiled)-1]
	}

	scopes := make(map[string]map[string]struct{}, len(compiled))
	for _, group := range compiled {
		scopes[group.ID] = stringSet(group.Scope)
	}
	for leftIndex := 0; leftIndex < len(compiled); leftIndex++ {
		left := compiled[leftIndex]
		for rightIndex := leftIndex + 1; rightIndex < len(compiled); rightIndex++ {
			right := compiled[rightIndex]
			if !setsIntersect(scopes[left.ID], scopes[right.ID]) {
				continue
			}
			leftInRight := setContains(scopes[right.ID], scopes[left.ID])
			rightInLeft := setContains(scopes[left.ID], scopes[right.ID])
			if leftInRight && rightInLeft {
				return nil, fmt.Errorf("loop groups %s and %s have the same scope", left.ID, right.ID)
			}
			if !leftInRight && !rightInLeft {
				return nil, fmt.Errorf("loop groups %s and %s partially overlap", left.ID, right.ID)
			}
			if loopGroupsShareBoundary(left, right) {
				return nil, fmt.Errorf("nested loop groups %s and %s cannot share a boundary node", left.ID, right.ID)
			}
		}
	}

	for _, group := range compiled {
		parent := ""
		parentSize := len(nodes) + 1
		depth := 0
		for _, candidate := range compiled {
			if candidate.ID == group.ID || !setContains(scopes[candidate.ID], scopes[group.ID]) {
				continue
			}
			depth++
			if len(candidate.Scope) < parentSize {
				parent = candidate.ID
				parentSize = len(candidate.Scope)
			}
		}
		group.Parent = parent
		group.Depth = depth
		result[group.ID] = group
	}
	return result, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func setsIntersect(left, right map[string]struct{}) bool {
	if len(left) > len(right) {
		left, right = right, left
	}
	for value := range left {
		if _, ok := right[value]; ok {
			return true
		}
	}
	return false
}

func setContains(container, values map[string]struct{}) bool {
	if len(container) < len(values) {
		return false
	}
	for value := range values {
		if _, ok := container[value]; !ok {
			return false
		}
	}
	return true
}

func loopGroupsShareBoundary(left, right LoopGroup) bool {
	return left.Start == right.Start || left.Start == right.End || left.End == right.Start || left.End == right.End
}

func reachableNodes(graph map[string][]string, start string) map[string]struct{} {
	seen := map[string]struct{}{start: {}}
	queue := []string{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range graph[current] {
			if _, exists := seen[next]; exists {
				continue
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return seen
}

func (c *DefaultCompiler) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// compileParallelGateways removes control-only gateways from the executable
// graph. Every task before a gateway chain is connected directly to every
// first task after that chain, which gives joins normal DAG dependency
// semantics without creating runtime records for gateways.
func compileParallelGateways(
	def *definition.WorkflowDefinition,
	edges []definition.Edge,
) ([]definition.Edge, map[string][]string, error) {
	nodes := make(map[string]definition.Node, len(def.Nodes))
	hasGateway := false
	for _, node := range def.Nodes {
		if node.Disabled {
			continue
		}
		nodes[node.ID] = node
		hasGateway = hasGateway || node.IsParallelGateway()
	}
	if !hasGateway {
		return edges, nil, nil
	}

	outgoing := make(map[string][]definition.Edge, len(nodes))
	incomingCount := make(map[string]int, len(nodes))
	for _, edge := range edges {
		from, fromOK := nodes[edge.From]
		to, toOK := nodes[edge.To]
		if !fromOK || !toOK {
			return nil, nil, fmt.Errorf("edge %s -> %s references a missing or disabled node", edge.From, edge.To)
		}
		if from.IsParallelGateway() || to.IsParallelGateway() {
			if edge.Kind == definition.EdgeKindBack {
				return nil, nil, fmt.Errorf("parallel gateway edge %s -> %s cannot be a back edge", edge.From, edge.To)
			}
			if strings.TrimSpace(edge.Condition) != "" || edge.Priority != 0 {
				return nil, nil, fmt.Errorf("parallel gateway edge %s -> %s must be unconditional", edge.From, edge.To)
			}
		}
		if edge.Kind != definition.EdgeKindBack {
			outgoing[edge.From] = append(outgoing[edge.From], edge)
			incomingCount[edge.To]++
		}
	}
	for _, node := range nodes {
		if node.IsParallelGateway() && incomingCount[node.ID] == 0 && len(outgoing[node.ID]) == 0 {
			return nil, nil, fmt.Errorf("parallel gateway %s is disconnected", node.ID)
		}
	}
	if err := validateAcyclicDefinitionGraph(nodes, outgoing); err != nil {
		return nil, nil, err
	}

	firstTasks := func(start string) ([]string, error) {
		seen := make(map[string]bool)
		result := make(map[string]struct{})
		var walk func(string) error
		walk = func(nodeID string) error {
			if seen[nodeID] {
				return fmt.Errorf("parallel gateway path contains cycle at %s", nodeID)
			}
			seen[nodeID] = true
			node := nodes[nodeID]
			if !node.IsParallelGateway() {
				result[nodeID] = struct{}{}
				seen[nodeID] = false
				return nil
			}
			for _, edge := range outgoing[nodeID] {
				if err := walk(edge.To); err != nil {
					return err
				}
			}
			seen[nodeID] = false
			return nil
		}
		if err := walk(start); err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(result))
		for id := range result {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids, nil
	}

	compiled := make([]definition.Edge, 0, len(edges))
	seenEdges := make(map[string]struct{}, len(edges))
	appendEdge := func(edge definition.Edge) {
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", edge.From, edge.To, edge.Kind, edge.Condition, edge.Label, edge.Priority)
		if _, exists := seenEdges[key]; exists {
			return
		}
		seenEdges[key] = struct{}{}
		compiled = append(compiled, edge)
	}
	for _, edge := range edges {
		from := nodes[edge.From]
		to := nodes[edge.To]
		if edge.Kind == definition.EdgeKindBack {
			appendEdge(edge)
			continue
		}
		if from.IsParallelGateway() {
			continue
		}
		if !to.IsParallelGateway() {
			appendEdge(edge)
			continue
		}
		targets, err := firstTasks(edge.To)
		if err != nil {
			return nil, nil, err
		}
		for _, target := range targets {
			appendEdge(definition.Edge{From: edge.From, To: target})
		}
	}

	gatewayEntries := make(map[string][]string)
	for _, node := range nodes {
		if !node.IsParallelGateway() {
			continue
		}
		targets, err := firstTasks(node.ID)
		if err != nil {
			return nil, nil, err
		}
		gatewayEntries[node.ID] = targets
	}
	return compiled, gatewayEntries, nil
}

func validateAcyclicDefinitionGraph(nodes map[string]definition.Node, outgoing map[string][]definition.Edge) error {
	state := make(map[string]uint8, len(nodes))
	var visit func(string) error
	visit = func(nodeID string) error {
		switch state[nodeID] {
		case 1:
			return fmt.Errorf("workflow contains cycle at %s; compiler expects DAG plan", nodeID)
		case 2:
			return nil
		}
		state[nodeID] = 1
		for _, edge := range outgoing[nodeID] {
			if err := visit(edge.To); err != nil {
				return err
			}
		}
		state[nodeID] = 2
		return nil
	}
	for nodeID := range nodes {
		if err := visit(nodeID); err != nil {
			return err
		}
	}
	return nil
}

func resolveGatewayEntryNodes(
	configured []string,
	gatewayEntries map[string][]string,
	tasks map[string]PlanNode,
) ([]string, error) {
	resolved := make(map[string]struct{}, len(configured))
	for _, nodeID := range configured {
		if _, ok := tasks[nodeID]; ok {
			resolved[nodeID] = struct{}{}
			continue
		}
		targets, ok := gatewayEntries[nodeID]
		if !ok {
			resolved[nodeID] = struct{}{}
			continue
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("entry parallel gateway %s does not reach a task node", nodeID)
		}
		for _, target := range targets {
			resolved[target] = struct{}{}
		}
	}
	result := make([]string, 0, len(resolved))
	for nodeID := range resolved {
		result = append(result, nodeID)
	}
	sort.Strings(result)
	return result, nil
}

func resolveDisabledNodes(def *definition.WorkflowDefinition) ([]definition.Edge, map[string]bool, map[string]string, error) {
	disabled := make(map[string]bool, len(def.Nodes))
	active := make(map[string]bool, len(def.Nodes))
	for _, node := range def.Nodes {
		disabled[node.ID] = node.Disabled
		active[node.ID] = !node.Disabled
	}

	outgoing := make(map[string][]definition.Edge, len(def.Nodes))
	incoming := make(map[string][]definition.Edge, len(def.Nodes))
	for _, edge := range def.Edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge)
		incoming[edge.To] = append(incoming[edge.To], edge)
	}

	effective := make([]definition.Edge, 0, len(def.Edges))
	seenEdges := make(map[string]struct{}, len(def.Edges))
	var walkForward func(string, definition.Edge, map[string]bool) error
	walkForward = func(source string, edge definition.Edge, path map[string]bool) error {
		if !disabled[edge.To] {
			edge.From = source
			key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%d", edge.From, edge.To, edge.Kind, edge.Condition, edge.Label, edge.Priority)
			if _, exists := seenEdges[key]; !exists {
				seenEdges[key] = struct{}{}
				effective = append(effective, edge)
			}
			return nil
		}
		if path[edge.To] {
			return fmt.Errorf("disabled node chain contains cycle at %s", edge.To)
		}
		nextPath := cloneBoolMap(path)
		nextPath[edge.To] = true
		for _, next := range outgoing[edge.To] {
			if err := walkForward(source, mergeBypassEdge(source, edge, next), nextPath); err != nil {
				return err
			}
		}
		return nil
	}

	for _, edge := range def.Edges {
		if !active[edge.From] {
			continue
		}
		if err := walkForward(edge.From, edge, map[string]bool{}); err != nil {
			return nil, nil, nil, err
		}
	}

	disabledSources := make(map[string]string)
	var collectSources func(string, map[string]bool, map[string]struct{}) error
	collectSources = func(nodeID string, path map[string]bool, sources map[string]struct{}) error {
		if path[nodeID] {
			return fmt.Errorf("disabled node chain contains cycle at %s", nodeID)
		}
		nextPath := cloneBoolMap(path)
		nextPath[nodeID] = true
		for _, edge := range incoming[nodeID] {
			if active[edge.From] {
				sources[edge.From] = struct{}{}
				continue
			}
			if disabled[edge.From] {
				if err := collectSources(edge.From, nextPath, sources); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for nodeID, isDisabled := range disabled {
		if !isDisabled {
			continue
		}
		sources := make(map[string]struct{})
		if err := collectSources(nodeID, map[string]bool{}, sources); err != nil {
			return nil, nil, nil, err
		}
		if len(sources) == 1 {
			for source := range sources {
				disabledSources[nodeID] = source
			}
		}
	}

	return effective, disabled, disabledSources, nil
}

func mergeBypassEdge(source string, inherited, next definition.Edge) definition.Edge {
	result := next
	result.From = source
	if inherited.Kind != "" && inherited.Kind != definition.EdgeKindNormal {
		result.Kind = inherited.Kind
	}
	if inherited.Condition != "" {
		result.Condition = inherited.Condition
	}
	if inherited.Label != "" {
		result.Label = inherited.Label
	}
	if inherited.Priority != 0 || inherited.Condition != "" || inherited.Label != "" {
		result.Priority = inherited.Priority
	}
	return result
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src)+1)
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func rebindDisabledInputs(
	node definition.Node,
	planNode *PlanNode,
	disabled map[string]bool,
	disabledSources map[string]string,
) error {
	autoSource, _ := node.UI[shortcutAutoInputUIKey].(string)
	if planNode.InputSpec != nil {
		bindings := make([]InputBinding, 0, len(planNode.InputSpec.Bindings))
		for _, binding := range planNode.InputSpec.Bindings {
			if binding.Source != "" && binding.Source != string(definition.InputSourceNode) || !disabled[binding.From] {
				bindings = append(bindings, binding)
				continue
			}
			if binding.From != autoSource {
				return fmt.Errorf("node %s input binding references disabled node %s", node.ID, binding.From)
			}
			if source := disabledSources[binding.From]; source != "" {
				binding.From = source
				bindings = append(bindings, binding)
			}
		}
		if len(bindings) == 0 {
			planNode.InputSpec = nil
		} else {
			planNode.InputSpec.Bindings = bindings
		}
	}
	for key, binding := range planNode.ParamBindings {
		if (binding.Source == "" || binding.Source == string(definition.InputSourceNode)) && disabled[binding.From] {
			return fmt.Errorf("node %s parameter %s references disabled node %s", node.ID, key, binding.From)
		}
	}
	for key, template := range planNode.ParamTemplates {
		for _, segment := range template.Segments {
			if segment.Type != "binding" || segment.Binding == nil {
				continue
			}
			binding := *segment.Binding
			if (binding.Source == "" || binding.Source == string(definition.InputSourceNode)) && disabled[binding.From] {
				return fmt.Errorf("node %s parameter template %s references disabled node %s", node.ID, key, binding.From)
			}
		}
	}
	return nil
}

func resolveEntryNodes(
	configured []string,
	activeNodes map[string]PlanNode,
	disabled map[string]bool,
	edges []definition.Edge,
	controlEntries map[string][]string,
) ([]string, error) {
	if len(configured) == 0 {
		return nil, nil
	}
	outgoing := make(map[string][]string)
	for _, edge := range edges {
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
	}
	resolved := make(map[string]struct{})
	var walk func(string, map[string]bool) error
	walk = func(nodeID string, path map[string]bool) error {
		if _, ok := activeNodes[nodeID]; ok {
			resolved[nodeID] = struct{}{}
			return nil
		}
		if targets, ok := controlEntries[nodeID]; ok {
			if len(targets) == 0 {
				return fmt.Errorf("entry control node %s does not reach an active task", nodeID)
			}
			for _, target := range targets {
				if err := walk(target, path); err != nil {
					return err
				}
			}
			return nil
		}
		if !disabled[nodeID] {
			return fmt.Errorf("entry node %s does not exist", nodeID)
		}
		if path[nodeID] {
			return fmt.Errorf("disabled entry chain contains cycle at %s", nodeID)
		}
		nextPath := cloneBoolMap(path)
		nextPath[nodeID] = true
		for _, target := range outgoing[nodeID] {
			if err := walk(target, nextPath); err != nil {
				return err
			}
		}
		return nil
	}
	for _, nodeID := range configured {
		if err := walk(nodeID, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	entry := make([]string, 0, len(resolved))
	for nodeID := range resolved {
		entry = append(entry, nodeID)
	}
	sort.Strings(entry)
	return entry, nil
}

func validateDefinition(def *definition.WorkflowDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if len(def.Nodes) == 0 {
		return errors.New("workflow has no nodes")
	}
	if def.MaxConcurrency < 0 || def.MaxConcurrency > MaxWorkflowConcurrency {
		return fmt.Errorf("max_concurrency must be between 0 and %d", MaxWorkflowConcurrency)
	}
	if def.MaxConcurrency > 1 {
		if len(def.LoopGroups) > 0 {
			return errors.New("max_concurrency greater than 1 cannot be combined with loop_groups")
		}
		for _, edge := range def.Edges {
			if edge.Kind == definition.EdgeKindBack {
				return errors.New("max_concurrency greater than 1 cannot be combined with back edges")
			}
		}
	}
	seen := make(map[string]struct{}, len(def.Nodes))
	for _, node := range def.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return errors.New("node id is required")
		}
		if def.MaxConcurrency > 1 && node.Loop != nil {
			return fmt.Errorf("max_concurrency greater than 1 cannot be combined with node loop %s", node.ID)
		}
		if node.IsParallelGateway() {
			if node.Disabled || !node.Executor.IsZero() || node.Input != nil || node.InputSpec != nil ||
				len(node.Params) > 0 || len(node.ParamBindings) > 0 || len(node.ParamTemplates) > 0 ||
				len(node.DependsOn) > 0 || node.ConcurrencyGroup != "" || node.ConcurrencyLimit != 0 ||
				node.ResourcePool != "" || node.ResourceCapacity != 0 || node.Retry != nil ||
				node.Loop != nil || node.Timeout != 0 || node.Branch != nil {
				return fmt.Errorf("parallel gateway %s can only contain control-flow and UI fields", node.ID)
			}
		} else if node.Type != "" && node.Type != definition.NodeTypeTask {
			return fmt.Errorf("node %s has unsupported type %q", node.ID, node.Type)
		} else if node.Executor.Type == "" {
			return fmt.Errorf("node %s missing executor type", node.ID)
		}
		if err := validateNodeConcurrency(node); err != nil {
			return err
		}
		if node.Retry != nil {
			if node.Retry.MaxAttempts < 0 || node.Retry.MaxAttempts > MaxNodeRetryAttempts {
				return fmt.Errorf("node %s retry max_attempts must be between 0 and %d", node.ID, MaxNodeRetryAttempts)
			}
			if node.Retry.Backoff < 0 || node.Retry.Backoff > MaxNodeRetryBackoff {
				return fmt.Errorf("node %s retry backoff must be between 0 and %s", node.ID, MaxNodeRetryBackoff)
			}
			if node.Retry.MaxBackoff < 0 || node.Retry.MaxBackoff > MaxNodeRetryBackoff {
				return fmt.Errorf("node %s retry max_backoff must be between 0 and %s", node.ID, MaxNodeRetryBackoff)
			}
			if node.Retry.Backoff > 0 && node.Retry.MaxBackoff > 0 && node.Retry.MaxBackoff < node.Retry.Backoff {
				return fmt.Errorf("node %s retry max_backoff cannot be less than backoff", node.ID)
			}
		}
		if node.Loop != nil {
			mode := strings.TrimSpace(node.Loop.Mode)
			if mode != "" && mode != "count" && mode != "each" {
				return fmt.Errorf("node %s loop mode must be count or each", node.ID)
			}
			if node.Loop.MaxIterations < 2 || node.Loop.MaxIterations > MaxNodeLoopIterations {
				return fmt.Errorf("node %s loop max_iterations must be between 2 and %d", node.ID, MaxNodeLoopIterations)
			}
			if mode == "each" && strings.TrimSpace(node.Loop.Condition) != "" {
				return fmt.Errorf("node %s each-item loop cannot define a condition", node.ID)
			}
			if mode == "each" && node.Loop.CountBinding != nil {
				return fmt.Errorf("node %s each-item loop cannot define count_binding", node.ID)
			}
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicated node id: %s", node.ID)
		}
		seen[node.ID] = struct{}{}
	}
	if err := validateNamedCapacities(def.Nodes); err != nil {
		return err
	}
	return nil
}

func validateNodeConcurrency(node definition.Node) error {
	group := strings.TrimSpace(node.ConcurrencyGroup)
	if group != node.ConcurrencyGroup {
		return fmt.Errorf("node %s concurrency_group cannot contain surrounding whitespace", node.ID)
	}
	if group == "" && node.ConcurrencyLimit != 0 {
		return fmt.Errorf("node %s concurrency_limit requires concurrency_group", node.ID)
	}
	if group != "" && (node.ConcurrencyLimit < 1 || node.ConcurrencyLimit > MaxWorkflowConcurrency) {
		return fmt.Errorf("node %s concurrency_limit must be between 1 and %d", node.ID, MaxWorkflowConcurrency)
	}
	pool := strings.TrimSpace(node.ResourcePool)
	if pool != node.ResourcePool {
		return fmt.Errorf("node %s resource_pool cannot contain surrounding whitespace", node.ID)
	}
	if pool == "" && node.ResourceCapacity != 0 {
		return fmt.Errorf("node %s resource_capacity requires resource_pool", node.ID)
	}
	if pool != "" && (node.ResourceCapacity < 1 || node.ResourceCapacity > MaxWorkflowConcurrency) {
		return fmt.Errorf("node %s resource_capacity must be between 1 and %d", node.ID, MaxWorkflowConcurrency)
	}
	return nil
}

func validateNamedCapacities(nodes []definition.Node) error {
	groups := make(map[string]int)
	pools := make(map[string]int)
	for _, node := range nodes {
		if node.Disabled || node.IsParallelGateway() {
			continue
		}
		if node.ConcurrencyGroup != "" {
			if limit, exists := groups[node.ConcurrencyGroup]; exists && limit != node.ConcurrencyLimit {
				return fmt.Errorf("concurrency group %s has conflicting limits %d and %d", node.ConcurrencyGroup, limit, node.ConcurrencyLimit)
			}
			groups[node.ConcurrencyGroup] = node.ConcurrencyLimit
		}
		if node.ResourcePool != "" {
			if capacity, exists := pools[node.ResourcePool]; exists && capacity != node.ResourceCapacity {
				return fmt.Errorf("resource pool %s has conflicting capacities %d and %d", node.ResourcePool, capacity, node.ResourceCapacity)
			}
			pools[node.ResourcePool] = node.ResourceCapacity
		}
	}
	return nil
}

func cloneIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func topoSort(nodes map[string]PlanNode, adjacency map[string][]string, indegree map[string]int) ([]string, error) {
	in := make(map[string]int, len(indegree))
	for k, v := range indegree {
		in[k] = v
	}
	ready := make([]string, 0, len(nodes))
	for id := range nodes {
		if in[id] == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		curr := ready[0]
		ready = ready[1:]
		order = append(order, curr)
		for _, next := range adjacency[curr] {
			in[next]--
			if in[next] == 0 {
				ready = append(ready, next)
			}
		}
		sort.Strings(ready)
	}
	if len(order) != len(nodes) {
		return nil, errors.New("workflow contains cycle; compiler expects DAG plan")
	}
	return order, nil
}

func buildPlanID(version *definition.WorkflowVersion) (string, error) {
	definitionJSON, err := json.Marshal(version.Definition)
	if err != nil {
		return "", fmt.Errorf("marshal workflow definition for plan identity: %w", err)
	}
	identity := fmt.Sprintf("%s:%s:%d:", version.WorkflowID, version.ID, version.Version)
	sum := sha256.Sum256(append([]byte(identity), definitionJSON...))
	return hex.EncodeToString(sum[:]), nil
}

func planBranchMode(def *definition.WorkflowDefinition, nodeID string) string {
	for _, node := range def.Nodes {
		if node.ID == nodeID && node.Branch != nil && node.Branch.Mode != "" {
			return string(node.Branch.Mode)
		}
	}
	return string(definition.BranchAll)
}

func normalizeBranches(branches map[string]BranchMeta) {
	for id, meta := range branches {
		sort.SliceStable(meta.Edges, func(i, j int) bool {
			if meta.Edges[i].Priority != meta.Edges[j].Priority {
				return meta.Edges[i].Priority < meta.Edges[j].Priority
			}
			return meta.Edges[i].To < meta.Edges[j].To
		})
		branches[id] = meta
	}
}

func validateBackEdges(backEdges map[string]BranchMeta, topo []string) error {
	if len(backEdges) == 0 {
		return nil
	}
	position := make(map[string]int, len(topo))
	for idx, nodeID := range topo {
		position[nodeID] = idx
	}
	for from, meta := range backEdges {
		fromPos, ok := position[from]
		if !ok {
			return fmt.Errorf("back edge source not found in topo order: %s", from)
		}
		for _, edge := range meta.Edges {
			toPos, ok := position[edge.To]
			if !ok {
				return fmt.Errorf("back edge target not found in topo order: %s", edge.To)
			}
			if toPos >= fromPos {
				return fmt.Errorf("back edge %s -> %s must point to an earlier node", from, edge.To)
			}
		}
	}
	return nil
}

func appendUnique(items []string, v string) []string {
	if v == "" {
		return items
	}
	for _, item := range items {
		if item == v {
			return items
		}
	}
	return append(items, v)
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func boolFromMap(src map[string]any, key string) bool {
	if len(src) == 0 {
		return false
	}
	v, ok := src[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func cloneLoop(src *definition.LoopPolicy) *LoopPolicy {
	if src == nil {
		return nil
	}
	cp := *src
	var countBinding *InputBinding
	if cp.CountBinding != nil {
		cloned := cloneInputBinding(*cp.CountBinding)
		countBinding = &cloned
	}
	return &LoopPolicy{
		MaxIterations: cp.MaxIterations,
		Condition:     cp.Condition,
		Mode:          cp.Mode,
		CountBinding:  countBinding,
	}
}

func cloneInputSpec(src *definition.InputSpec) *InputSpec {
	if src == nil {
		return nil
	}
	cp := &InputSpec{
		Mode:     string(src.Mode),
		Bindings: make([]InputBinding, 0, len(src.Bindings)),
	}
	for _, binding := range src.Bindings {
		cp.Bindings = append(cp.Bindings, cloneInputBinding(binding))
	}
	return cp
}

func cloneInputBinding(binding definition.InputBinding) InputBinding {
	return InputBinding{
		Source:    string(binding.Source),
		From:      binding.From,
		Path:      binding.Path,
		Label:     binding.Label,
		As:        binding.As,
		Required:  binding.Required,
		Default:   binding.Default,
		Transform: binding.Transform,
	}
}

func cloneParamBindings(src map[string]definition.InputBinding) map[string]InputBinding {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]InputBinding, len(src))
	for key, binding := range src {
		dst[key] = cloneInputBinding(binding)
	}
	return dst
}

func cloneParamTemplates(src map[string]definition.ParamTemplate) map[string]ParamTemplate {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]ParamTemplate, len(src))
	for key, template := range src {
		segments := make([]ParamTemplateSegment, 0, len(template.Segments))
		for _, segment := range template.Segments {
			var binding *InputBinding
			if segment.Binding != nil {
				cloned := cloneInputBinding(*segment.Binding)
				binding = &cloned
			}
			segments = append(segments, ParamTemplateSegment{Type: segment.Type, Value: segment.Value, Binding: binding})
		}
		dst[key] = ParamTemplate{Segments: segments}
	}
	return dst
}

func validateInputBindings(nodes map[string]PlanNode, dependencies map[string][]string) error {
	for nodeID, node := range nodes {
		depSet := make(map[string]struct{}, len(dependencies[nodeID]))
		for _, dep := range dependencies[nodeID] {
			depSet[dep] = struct{}{}
		}
		if node.Loop != nil && node.Loop.CountBinding != nil {
			if err := validateBinding(nodeID, "loop count", *node.Loop.CountBinding, nodes, depSet); err != nil {
				return err
			}
		}
		if node.InputSpec != nil {
			switch node.InputSpec.Mode {
			case "", string(definition.InputModeReplace), string(definition.InputModeObject), string(definition.InputModeArray):
			default:
				return fmt.Errorf("node %s has unsupported input mode: %s", nodeID, node.InputSpec.Mode)
			}
			for _, binding := range node.InputSpec.Bindings {
				if err := validateBinding(nodeID, "input", binding, nodes, depSet); err != nil {
					return err
				}
			}
		}
		keys := make([]string, 0, len(node.ParamBindings))
		for key := range node.ParamBindings {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "__") {
				return fmt.Errorf("node %s has invalid parameter binding key %q", nodeID, key)
			}
			if err := validateBinding(nodeID, "parameter "+key, node.ParamBindings[key], nodes, depSet); err != nil {
				return err
			}
		}
		templateKeys := make([]string, 0, len(node.ParamTemplates))
		for key := range node.ParamTemplates {
			templateKeys = append(templateKeys, key)
		}
		sort.Strings(templateKeys)
		for _, key := range templateKeys {
			if strings.TrimSpace(key) == "" || strings.HasPrefix(key, "__") {
				return fmt.Errorf("node %s has invalid parameter template key %q", nodeID, key)
			}
			if _, conflict := node.ParamBindings[key]; conflict {
				return fmt.Errorf("node %s parameter %s cannot use both a binding and a template", nodeID, key)
			}
			template := node.ParamTemplates[key]
			if len(template.Segments) == 0 {
				return fmt.Errorf("node %s parameter template %s has no segments", nodeID, key)
			}
			for index, segment := range template.Segments {
				switch segment.Type {
				case "text":
					if segment.Binding != nil {
						return fmt.Errorf("node %s parameter template %s text segment %d contains a binding", nodeID, key, index)
					}
				case "binding":
					if segment.Binding == nil {
						return fmt.Errorf("node %s parameter template %s binding segment %d is missing its binding", nodeID, key, index)
					}
					if err := validateBinding(nodeID, fmt.Sprintf("parameter template %s segment %d", key, index), *segment.Binding, nodes, depSet); err != nil {
						return err
					}
				default:
					return fmt.Errorf("node %s parameter template %s segment %d has unsupported type %q", nodeID, key, index, segment.Type)
				}
			}
		}
	}
	return nil
}

func validateBinding(nodeID, purpose string, binding InputBinding, nodes map[string]PlanNode, depSet map[string]struct{}) error {
	source := binding.Source
	if source == "" {
		source = string(definition.InputSourceNode)
	}
	switch source {
	case string(definition.InputSourceNode):
		if strings.TrimSpace(binding.From) == "" {
			return fmt.Errorf("node %s has %s binding without source node", nodeID, purpose)
		}
		if _, ok := nodes[binding.From]; !ok {
			return fmt.Errorf("node %s %s binding references unknown node %s", nodeID, purpose, binding.From)
		}
		if _, ok := depSet[binding.From]; !ok {
			return fmt.Errorf("node %s %s binding source %s is not a direct dependency", nodeID, purpose, binding.From)
		}
	case string(definition.InputSourceVar):
		if strings.TrimSpace(binding.From) == "" {
			return fmt.Errorf("node %s %s variable binding requires from", nodeID, purpose)
		}
	case string(definition.InputSourceRequest), string(definition.InputSourceRun):
	default:
		return fmt.Errorf("node %s has unsupported %s binding source: %s", nodeID, purpose, source)
	}
	return nil
}

func validateLoopGroupVariables(nodes map[string]PlanNode, groups map[string]LoopGroup) error {
	scopes := make(map[string]map[string]struct{}, len(groups))
	for groupID, group := range groups {
		scopes[groupID] = stringSet(group.Scope)
	}
	validate := func(nodeID, purpose string, binding InputBinding, input bool) error {
		if binding.Source != string(definition.InputSourceVar) || !strings.HasPrefix(binding.From, loopGroupVariablePrefix) {
			return nil
		}
		groupID, kind, ok := ParseLoopGroupVariable(binding.From)
		if !ok {
			return fmt.Errorf("node %s %s references malformed repeat variable %s", nodeID, purpose, binding.From)
		}
		group, exists := groups[groupID]
		if !exists {
			return fmt.Errorf("node %s %s references unknown repeat group %s", nodeID, purpose, groupID)
		}
		if _, inside := scopes[groupID][nodeID]; !inside {
			return fmt.Errorf("node %s %s references repeat group %s outside its scope", nodeID, purpose, groupID)
		}
		if kind == "item" && group.Mode != "each" {
			return fmt.Errorf("node %s %s references Repeat Item from count loop group %s", nodeID, purpose, groupID)
		}
		if input && nodeID == group.Start {
			return fmt.Errorf("node %s input cannot reference its own repeat group %s before the iteration starts", nodeID, groupID)
		}
		return nil
	}
	for nodeID, node := range nodes {
		if node.InputSpec != nil {
			for _, binding := range node.InputSpec.Bindings {
				if err := validate(nodeID, "input", binding, true); err != nil {
					return err
				}
			}
		}
		for key, binding := range node.ParamBindings {
			if err := validate(nodeID, "parameter "+key, binding, false); err != nil {
				return err
			}
		}
		for key, template := range node.ParamTemplates {
			for index, segment := range template.Segments {
				if segment.Binding == nil {
					continue
				}
				purpose := fmt.Sprintf("parameter template %s segment %d", key, index)
				if err := validate(nodeID, purpose, *segment.Binding, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
