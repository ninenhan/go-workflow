package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/ninenhan/go-workflow/core/definition"
)

func main() {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(definition.WorkflowDefinitionJSONSchema()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
