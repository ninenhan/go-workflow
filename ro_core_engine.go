package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ninenhan/go-workflow/fn"
)

type NodeJSON struct {
	UnitID string         `json:"unit_id"`
	Name   string         `json:"name"`
	Input  *Input         `json:"input,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

type GraphJSON struct {
	Nodes map[string]NodeJSON `json:"nodes"`
	Edges map[string][]string `json:"edges"`
}

// BuildGraphFromJSON Graph represents a directed graph structure with nodes and edges.
func BuildGraphFromJSON(data []byte) (*Graph, error) {
	var def GraphJSON
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, err
	}

	graph := &Graph{
		Nodes: map[string]*Node{},
		Edges: def.Edges,
	}

	for id, nodeDef := range def.Nodes {

		unit, ok := FindUnit(nodeDef.UnitID)
		if !ok {
			return nil, fmt.Errorf("unit_id %s not found", nodeDef.UnitID)
		}
		executable, ok := unit.(ExecutableUnit)
		if !ok {
			return nil, fmt.Errorf("unit %T does not implement ExecutableUnit", unit)
		}
		//判断unit是否实现了 ExecutableUnit 接口
		graph.Nodes[id] = &Node{
			ID:      id,
			Name:    nodeDef.Name,
			Input:   nodeDef.Input,
			Execute: executable.Execute,
		}
	}

	return graph, nil
}

func Test_Json_To_Graph() {
	jsonData := []byte(`
	{
		"nodes": {
			"req1":{
				"unit_id": "HttpUnit",
				"name": "Http Request Node",
				"input": {
					"data": "InputUnit Demo"
				},
				"params": {
					"a": {"data": "input data"}
				}
			}
		},
		"edges": {
			"req1": [""]
		}
	}`)

	graph, err := BuildGraphFromJSON(jsonData)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Graph built successfully:", graph)
	//  完成了
	result := make(ContextMap)
	_ = graph.Run(context.Background(), "req1", result)
	fmt.Printf("DAG Graph Tests result : %v\n", fn.Stringify(result))
}
