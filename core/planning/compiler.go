package planning

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/core/definition"
)

var ErrNilDefinition = errors.New("workflow definition is nil")

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

	nodes := make(map[string]PlanNode, len(def.Nodes))
	adjacency := make(map[string][]string, len(def.Nodes))
	dependencies := make(map[string][]string, len(def.Nodes))
	indegree := make(map[string]int, len(def.Nodes))
	branches := make(map[string]BranchMeta)
	backEdges := make(map[string]BranchMeta)

	for _, node := range def.Nodes {
		if node.Disabled {
			continue
		}
		nodes[node.ID] = PlanNode{
			ID:              node.ID,
			Name:            node.Name,
			Type:            node.Type,
			ExecutorType:    node.Executor.Type,
			ExecutorRef:     node.Executor.Ref,
			ExecutorConf:    cloneMap(node.Executor.Config),
			Input:           node.Input,
			InputSpec:       cloneInputSpec(node.InputSpec),
			Params:          cloneMap(node.Params),
			Retry:           RetryPolicy(node.Retry),
			Loop:            cloneLoop(node.Loop),
			Timeout:         node.Timeout,
			ContinueOnError: boolFromMap(node.Params, "continue_on_error"),
		}
		adjacency[node.ID] = []string{}
		dependencies[node.ID] = []string{}
		indegree[node.ID] = 0
	}

	for _, edge := range def.Edges {
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
		if node.Disabled {
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

	entry := def.EntryNodes
	if len(entry) == 0 {
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

	plan := &ExecutionPlan{
		PlanID:            buildPlanID(version),
		WorkflowID:        version.WorkflowID,
		WorkflowVersionID: version.ID,
		EntryNodes:        append([]string{}, entry...),
		ExitNodes:         exit,
		Adjacency:         adjacency,
		Dependencies:      dependencies,
		TopologicalOrder:  topo,
		Nodes:             nodes,
		Branches:          branches,
		BackEdges:         backEdges,
		CreatedAt:         c.now(),
	}
	return plan, nil
}

func (c *DefaultCompiler) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func validateDefinition(def *definition.WorkflowDefinition) error {
	if def == nil {
		return ErrNilDefinition
	}
	if len(def.Nodes) == 0 {
		return errors.New("workflow has no nodes")
	}
	seen := make(map[string]struct{}, len(def.Nodes))
	for _, node := range def.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return errors.New("node id is required")
		}
		if node.Executor.Type == "" {
			return fmt.Errorf("node %s missing executor type", node.ID)
		}
		if _, exists := seen[node.ID]; exists {
			return fmt.Errorf("duplicated node id: %s", node.ID)
		}
		seen[node.ID] = struct{}{}
	}
	return nil
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

func buildPlanID(version *definition.WorkflowVersion) string {
	s := fmt.Sprintf("%s:%s:%d", version.WorkflowID, version.ID, version.Version)
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
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
	return &LoopPolicy{
		MaxIterations: cp.MaxIterations,
		Condition:     cp.Condition,
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
		cp.Bindings = append(cp.Bindings, InputBinding{
			Source:    string(binding.Source),
			From:      binding.From,
			Path:      binding.Path,
			As:        binding.As,
			Required:  binding.Required,
			Default:   binding.Default,
			Transform: binding.Transform,
		})
	}
	return cp
}

func validateInputBindings(nodes map[string]PlanNode, dependencies map[string][]string) error {
	for nodeID, node := range nodes {
		if node.InputSpec == nil {
			continue
		}
		switch node.InputSpec.Mode {
		case "", string(definition.InputModeReplace), string(definition.InputModeObject), string(definition.InputModeArray):
		default:
			return fmt.Errorf("node %s has unsupported input mode: %s", nodeID, node.InputSpec.Mode)
		}
		depSet := make(map[string]struct{}, len(dependencies[nodeID]))
		for _, dep := range dependencies[nodeID] {
			depSet[dep] = struct{}{}
		}
		for _, binding := range node.InputSpec.Bindings {
			source := binding.Source
			if source == "" {
				source = string(definition.InputSourceNode)
			}
			switch source {
			case string(definition.InputSourceNode):
				if strings.TrimSpace(binding.From) == "" {
					return fmt.Errorf("node %s has node input binding without source node", nodeID)
				}
				if _, ok := nodes[binding.From]; !ok {
					return fmt.Errorf("node %s input binding references unknown node %s", nodeID, binding.From)
				}
				if _, ok := depSet[binding.From]; !ok {
					return fmt.Errorf("node %s input binding source %s is not a direct dependency", nodeID, binding.From)
				}
			case string(definition.InputSourceVar):
				if strings.TrimSpace(binding.From) == "" {
					return fmt.Errorf("node %s var input binding requires from", nodeID)
				}
			case string(definition.InputSourceRequest), string(definition.InputSourceRun):
			default:
				return fmt.Errorf("node %s has unsupported input source: %s", nodeID, source)
			}
		}
	}
	return nil
}
