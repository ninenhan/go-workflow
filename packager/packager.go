package packager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/core/executor"
	"github.com/ninenhan/go-workflow/scheduler"
	"github.com/ninenhan/go-workflow/units"
)

func LoadDefinition(path string) (*definition.WorkflowDefinition, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var def definition.WorkflowDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

func ValidateStandalone(def *definition.WorkflowDefinition) error {
	if def == nil {
		return errors.New("workflow definition is nil")
	}
	builtins := builtinUnits()
	var issues []string
	for _, node := range def.Nodes {
		if node.Disabled || node.IsParallelGateway() {
			continue
		}
		execType := strings.TrimSpace(node.Executor.Type)
		switch execType {
		case string(executor.TypeUnit):
			ref := strings.TrimSpace(node.Executor.Ref)
			if ref == "" {
				issues = append(issues, fmt.Sprintf("node %s: unit executor ref is required", node.ID))
				continue
			}
			if _, ok := builtins[ref]; !ok {
				issues = append(issues, fmt.Sprintf("node %s: custom unit %q cannot be auto-packed; generate source and register it manually", node.ID, ref))
			}
		case string(executor.TypeHTTP), string(executor.TypeScript):
		case string(executor.TypeLocalGo):
			issues = append(issues, fmt.Sprintf("node %s: local_go requires manual function registration and cannot be auto-packed", node.ID))
		case string(executor.TypeQueue), string(executor.TypeRemote), string(executor.TypeContainer), string(executor.TypePython), string(executor.TypeNodeJS):
			issues = append(issues, fmt.Sprintf("node %s: executor type %q is not supported in standalone embedded binaries", node.ID, execType))
		default:
			issues = append(issues, fmt.Sprintf("node %s: unknown or unsupported executor type %q", node.ID, execType))
		}
	}
	if len(issues) == 0 {
		svc, err := scheduler.NewService(scheduler.Options{})
		if err != nil {
			return fmt.Errorf("create validator service: %w", err)
		}
		if _, err := svc.ValidateDefinition(context.Background(), def); err != nil {
			return fmt.Errorf("compile workflow definition: %w", err)
		}
		return nil
	}
	sort.Strings(issues)
	return errors.New(strings.Join(issues, "; "))
}

func RenderMain(def *definition.WorkflowDefinition) ([]byte, error) {
	if def == nil {
		return nil, errors.New("workflow definition is nil")
	}
	raw, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	source := fmt.Sprintf(`package main

import (
	"os"

	"github.com/ninenhan/go-workflow/standalone"
)

const workflowDefinitionJSON = %s

func main() {
	os.Exit(standalone.RunCLI(standalone.CLIConfig{
		DefinitionJSON: workflowDefinitionJSON,
	}))
}
`, strconv.Quote(string(raw)))
	return format.Source([]byte(source))
}

func SuggestedBinaryName(def *definition.WorkflowDefinition, workflowPath string) string {
	if def != nil {
		if name := sanitizeName(def.ID); name != "" {
			return name
		}
		if name := sanitizeName(def.Name); name != "" {
			return name
		}
	}
	base := strings.TrimSuffix(filepath.Base(workflowPath), filepath.Ext(workflowPath))
	if strings.HasSuffix(base, ".workflow") {
		base = strings.TrimSuffix(base, ".workflow")
	}
	if name := sanitizeName(base); name != "" {
		return name
	}
	return "bound-workflow"
}

func FindModuleRoot(start string) (string, error) {
	curr, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(curr, "go.mod")); err == nil {
			return curr, nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			return "", errors.New("go.mod not found from current directory upward")
		}
		curr = parent
	}
}

func builtinUnits() map[string]struct{} {
	names := units.BuiltinNames()
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func sanitizeName(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	out = strings.TrimLeft(out, "0123456789")
	return out
}
