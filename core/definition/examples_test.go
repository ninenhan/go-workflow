package definition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExampleWorkflowJSONParses(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "*.workflow.json"))
	if err != nil {
		t.Fatalf("glob examples: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no example workflow json files found")
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read example: %v", err)
			}
			var def WorkflowDefinition
			if err := json.Unmarshal(raw, &def); err != nil {
				t.Fatalf("unmarshal example: %v", err)
			}
			if def.ID == "" || def.Name == "" || len(def.Nodes) == 0 {
				t.Fatalf("unexpected example definition: %+v", def)
			}
		})
	}
}
