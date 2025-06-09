package main

import (
	"context"
	"fmt"
	core "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/fn"
	"strings"
)

type Node = core.Node
type Graph = core.Graph
type ContextMap = core.ContextMap

func DagGraphTests() {
	graph := &Graph{
		Nodes: map[string]*Node{},
		Edges: map[string][]string{},
	}

	graph.AddNode("input", &Node{
		Execute: func(ctx context.Context, state ContextMap, self *Node) (*core.ExecutionResult, error) {
			return core.SimpleResult("用户问题"), nil
		},
	})

	graph.AddNode("model", &Node{
		Execute: func(ctx context.Context, state ContextMap, self *Node) (*core.ExecutionResult, error) {
			input := state["input"]
			str := input.Data.(string)
			return core.SimpleResult(fmt.Sprintf("回答：%s , 调用工具", str)), nil
		},
	})

	graph.AddNode("judge", &Node{
		Execute: func(ctx context.Context, state ContextMap, self *Node) (*core.ExecutionResult, error) {
			reply := state["model"].Data.(string)
			if strings.Contains(reply, "工具") {
				return core.SimpleResult("tools"), nil
			}
			return core.SimpleResult("final"), nil
		},
		Branch: func(result *core.ExecutionResult, _ ContextMap) string {
			return result.Data.(string)
		},
	})

	graph.AddNode("tools", &Node{
		Execute: func(ctx context.Context, _ ContextMap, self *Node) (*core.ExecutionResult, error) {
			return core.SimpleResult("工具已调用"), nil
		},
	})

	graph.AddNode("final", &Node{
		Execute: func(ctx context.Context, _ ContextMap, self *Node) (*core.ExecutionResult, error) {
			return core.SimpleResult("输出完成"), nil
		},
	})

	//// 循环节点示例
	//graph.AddNode("loop", &Node{
	//	Execute: func(ctx context.Context, state ContextMap, self *Node) (*core.ExecutionResult, error) {
	//		cnt := state["cnt"].Data.(int)
	//		state["cnt"] = cnt + 1
	//		return core.SimpleResult(cnt), nil
	//	},
	//	LoopCond: func(state ContextMap) bool {
	//		return state["cnt"].(int) < 3
	//	},
	//})

	// 流程图
	graph.AddEdge("input", "model")
	graph.AddEdge("model", "judge")
	graph.AddEdge("tools", "final")

	//// 并行节点例子（多个同时执行）
	//graph.AddNode("parallel1", &Node{Execute: ..., Parallel: true})
	//graph.AddNode("parallel2", &Node{Execute: ..., Parallel: true})
	//graph.AddEdge("judge", "parallel1")
	//graph.AddEdge("judge", "parallel2")

	result := make(core.ContextMap)
	_ = graph.Run(context.Background(), "input", result)
	fmt.Printf("DAG Graph Tests result : %v\n", fn.Stringify(result))
}
