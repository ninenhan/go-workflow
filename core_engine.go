package workflow

import (
	"context"
	"fmt"
	"github.com/ninenhan/go-workflow/fn"
	"sync"
)

type ContextMap map[string]*ExecutionResult

type Unit struct {
	ID          string `json:"id"`
	UnitName    string `json:"unit_name,omitempty"`
	DisplayName string `json:"display_name,omitempty"` // UI 显示名（如“调用接口”）
	Status      string `json:"status,omitempty"`
	ErrMsg      string `json:"err_msg,omitempty"`    // 如果失败，存错误
	OutputRef   string `json:"output_ref,omitempty"` // 输出导出的字段名（用于 UI 显示）
}

type ExecutableUnit interface {
	GetUnitMeta() *Unit
	Execute(ctx context.Context, state ContextMap, self *Node) (*ExecutionResult, error)
}

type Input struct {
	Data      any    `json:"data,omitempty"`      //最终输出
	DataType  string `json:"data_type,omitempty"` // plaintext, json, json_array，socket
	Slottable bool   `json:"slottable,omitempty"` // 是否是可插槽的
}

type ExecutionResult struct {
	NodeName string `json:"node_name,omitempty"`
	Data     any    `json:"data,omitempty"`
	Stream   bool   `json:"stream,omitempty"`
	Raw      any    `json:"raw,omitempty"`
	Error    string `json:"error,omitempty"`
}

func SimpleResult(data any) *ExecutionResult {
	return &ExecutionResult{
		Data: data,
	}
}

type NodeFunc func(ctx context.Context, state ContextMap, self *Node) (*ExecutionResult, error)
type BranchFunc func(result *ExecutionResult, state ContextMap) string
type LoopCondFunc func(state ContextMap) bool

type Node struct {
	ID           string
	Name         string
	Input        *Input
	Execute      NodeFunc
	Branch       BranchFunc            // 可选分支函数
	Parallel     bool                  // 是否并行节点
	LoopCond     func(ContextMap) bool // 可选循环条件
	ExportFields []string              // 导出字段，用于供下游引用
}

type Graph struct {
	Nodes map[string]*Node
	Edges map[string][]string
	Hooks struct {
		Before func(name string, state ContextMap)
		After  func(name string, result any, err error, state ContextMap)
	}
	start string
}

func (g *Graph) AddNode(name string, node *Node) {
	if fn.IsEmpty(node.ID) {
		id, _ := fn.GenerateShortID()
		node.ID = id
	}
	g.Nodes[name] = node
}

func (g *Graph) AddEdge(from, to string) {
	g.Edges[from] = append(g.Edges[from], to)
}

func (g *Graph) AddBranch(name string, exec NodeFunc, branch BranchFunc) {
	g.Nodes[name] = &Node{Name: name, Execute: exec, Branch: branch}
}

func (g *Graph) Run(ctx context.Context, start string, state ContextMap) error {
	var exec func(string) error
	exec = func(curr string) error {
		if curr == "END" {
			return nil
		}

		node, ok := g.Nodes[curr]
		if !ok {
			return fmt.Errorf("node %s not found", curr)
		}

		if g.Hooks.Before != nil {
			g.Hooks.Before(curr, state)
		}

		var result *ExecutionResult
		var err error
		for {
			result, err = node.Execute(ctx, state, node)
			if err != nil {
				state[curr] = &ExecutionResult{
					Error: err.Error(),
				}
				return fmt.Errorf("exec %s failed: %w", curr, err)
			}

			state[curr] = result

			if node.LoopCond == nil || !node.LoopCond(state) {
				break
			}
		}

		if g.Hooks.After != nil {
			g.Hooks.After(curr, result, err, state)
		}

		// 分支跳转
		var next string
		if node.Branch != nil {
			next = node.Branch(result, state)
		} else {
			nexts := g.Edges[curr]
			if len(nexts) == 0 {
				return nil
			}
			if node.Parallel {
				// 并行执行所有子节点
				var wg sync.WaitGroup
				for _, nxt := range nexts {
					wg.Add(1)
					go func(n string) {
						defer wg.Done()
						_ = exec(n)
					}(nxt)
				}
				wg.Wait()
				return nil
			}
			next = nexts[0]
		}

		return exec(next)
	}

	return exec(start)
}

//

// 提供一个用于构建工作流 DAG 的 DSL 构建器。

func NewDSLGraph() *Graph {
	return &Graph{
		Nodes: map[string]*Node{},
		Edges: map[string][]string{},
	}
}

func (g *Graph) StartWith(name string, fn NodeFunc) *Graph {
	g.start = name
	g.Nodes[name] = &Node{Name: name, Execute: fn}
	return g
}

func (g *Graph) Then(name string, fn NodeFunc) *Graph {
	last := g.lastNode()
	g.Nodes[name] = &Node{Name: name, Execute: fn}
	g.Edges[last] = append(g.Edges[last], name)
	return g
}

func (g *Graph) Branch(name string, fn NodeFunc, branch BranchFunc) *Graph {
	last := g.lastNode()
	g.Nodes[name] = &Node{Name: name, Execute: fn, Branch: branch}
	g.Edges[last] = append(g.Edges[last], name)
	return g
}

func (g *Graph) Loop(name string, fn NodeFunc, cond LoopCondFunc) *Graph {
	last := g.lastNode()
	g.Nodes[name] = &Node{Name: name, Execute: fn, LoopCond: cond}
	g.Edges[last] = append(g.Edges[last], name)
	return g
}

func (g *Graph) Parallel(name string, fns ...NodeFunc) *Graph {
	last := g.lastNode()
	for i, nodeFunc := range fns {
		child := fmt.Sprintf("%s_p%d", name, i)
		g.Nodes[child] = &Node{Name: child, Execute: nodeFunc, Parallel: true}
		g.Edges[last] = append(g.Edges[last], child)
	}
	return g
}

func (g *Graph) OnBefore(fn func(string, ContextMap)) *Graph {
	g.Hooks.Before = fn
	return g
}

func (g *Graph) OnAfter(fn func(string, any, error, ContextMap)) *Graph {
	g.Hooks.After = fn
	return g
}

func (g *Graph) RunWithDSL(ctx context.Context, state ContextMap) error {
	var exec func(name string) error

	exec = func(curr string) error {
		node, ok := g.Nodes[curr]
		if !ok {
			return fmt.Errorf("node %s not found", curr)
		}
		exported := make(map[string]any)
		//state 为执行过的状态映射
		//获取curr之前的nodes
		for nodeName, result := range state {
			node := g.Nodes[nodeName]
			if node == nil || len(node.ExportFields) == 0 {
				continue
			}
			if resultMap, ok := result.Data.(map[string]any); ok {
				for _, field := range node.ExportFields {
					if val, exists := resultMap[field]; exists {
						exported[fmt.Sprintf("%s.%s", nodeName, field)] = val
					}
				}
			}
		}

		if g.Hooks.Before != nil {
			g.Hooks.Before(curr, state)
		}

		//批量处理input
		if node.Input != nil && node.Input.Data != nil {
			if node.Input.Slottable {
				if inputData, ok := node.Input.Data.(string); ok {
					//AUTO FILL : ExportFields
					parsed, _ := fn.ParseTemplate(inputData)
					rendered := fn.RenderTemplateStrictly(inputData, parsed, exported, false)
					node.Input.Data = rendered
					node.Input.Slottable = false
				}
			}
		}

		var result *ExecutionResult
		var err error
		for {
			result, err = node.Execute(ctx, state, node)
			if err != nil {
				return err
			}
			state[curr] = result
			if node.LoopCond == nil || !node.LoopCond(state) {
				break
			}
		}

		if g.Hooks.After != nil {
			g.Hooks.After(curr, result, err, state)
		}

		if node.Branch != nil {
			next := node.Branch(result, state)
			return exec(next)
		}

		nexts := g.Edges[curr]
		if node.Parallel {
			var wg sync.WaitGroup
			for _, n := range nexts {
				wg.Add(1)
				go func(n string) {
					defer wg.Done()
					_ = exec(n)
				}(n)
			}
			wg.Wait()
			return nil
		} else if len(nexts) > 0 {
			return exec(nexts[0])
		}
		return nil
	}

	return exec(g.start)
}

func (g *Graph) lastNode() string {
	for k := range g.Nodes {
		if len(g.Edges[k]) == 0 {
			return k
		}
	}
	return ""
}
