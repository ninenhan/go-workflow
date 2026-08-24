package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	workflow "github.com/ninenhan/go-workflow"
	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

type GreetingUnit struct {
	workerunit.Unit
	Prefix string `json:"prefix"`
}

func (u *GreetingUnit) GetUnitName() string { return "GreetingUnit" }

func (u *GreetingUnit) GetUnitMeta() *workerunit.Unit { return &u.Unit }

func (u *GreetingUnit) Execute(
	_ context.Context,
	_ workerunit.ContextMap,
	self *workerunit.Node,
) (*workerunit.ExecutionResult, error) {
	if self == nil || self.Input == nil {
		return nil, fmt.Errorf("GreetingUnit: input is required")
	}
	return workerunit.SimpleResult(u.Prefix + fmt.Sprint(self.Input.Data)), nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	ctx := context.Background()
	dataDirectory := filepath.Join("var", "go-workflow")

	application, err := openWorkflowApplication(ctx, dataDirectory, workflow.RuntimeOptions{
		CredentialScope: "demo",
		Builtins:        workflow.BuiltinStandard,
		ConfigureWorker: func(workerService *worker.Service) error {
			return workerService.UnitRegistry().RegisterUnitFactory("GreetingUnit", func() workerunit.ExecutableUnit {
				return &GreetingUnit{}
			})
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, application.Close())
	}()
	workflowService := application.Scheduler

	const workflowID = "embedded-greeting"
	if err := workflowService.SaveWorkflow(ctx, &definition.Workflow{
		ID:   workflowID,
		Name: "embedded greeting",
	}); err != nil {
		return err
	}

	version, err := workflowService.CreateVersion(ctx, workflowID, &definition.WorkflowDefinition{
		ID:         workflowID,
		Name:       "embedded greeting",
		EntryNodes: []string{"name"},
		Nodes: []definition.Node{
			{
				ID:       "name",
				Name:     "name",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "RemarkUnit"},
				Input:    "go-workflow",
			},
			{
				ID:       "greeting",
				Name:     "greeting",
				Executor: definition.ExecutorSpec{Type: definition.ExecutorTypeUnit, Ref: "GreetingUnit"},
				InputSpec: &definition.InputSpec{
					Mode: definition.InputModeReplace,
					Bindings: []definition.InputBinding{
						{Source: definition.InputSourceNode, From: "name", Required: true},
					},
				},
				Params: map[string]any{"prefix": "hello, "},
			},
		},
		Edges: []definition.Edge{{From: "name", To: "greeting"}},
	})
	if err != nil {
		return err
	}
	published, err := workflowService.PublishVersion(ctx, version.ID)
	if err != nil {
		return err
	}

	runResult, err := workflowService.RunVersion(ctx, published, &wfruntime.WorkflowRun{
		CredentialScope: "demo",
	})
	if err != nil {
		return err
	}
	if runResult.Status != wfruntime.StatusSuccess {
		return fmt.Errorf("workflow failed: status=%s", runResult.Status)
	}

	persisted, err := workflowService.LoadRun(ctx, runResult.ID)
	if err != nil {
		return err
	}
	fmt.Printf("run=%s status=%s result=%v\n", persisted.ID, persisted.Status, persisted.Context.NodeResults["greeting"])
	return nil
}
