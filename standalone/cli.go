package standalone

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ninenhan/go-workflow/core/definition"
	wfruntime "github.com/ninenhan/go-workflow/core/runtime"
	"github.com/ninenhan/go-workflow/worker"
)

type CLIConfig struct {
	DefinitionJSON string
	RegisterUnits  func(*worker.Service) error
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Args           []string
}

func RunCLI(cfg CLIConfig) int {
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	def, err := parseDefinitionJSON(cfg.DefinitionJSON)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load workflow definition: %v\n", err)
		return 1
	}

	opts, err := parseCLIArgs(cfg.Args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "parse flags: %v\n", err)
		return 2
	}

	run, err := RunDefinition(context.Background(), RunnerConfig{
		Definition:    def,
		RegisterUnits: cfg.RegisterUnits,
	}, RunInput{
		Vars:    opts.vars,
		Request: opts.request,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "run workflow: %v\n", err)
		return 1
	}

	if err := writeRunOutput(stdout, run, opts.nodeOutput, opts.pretty); err != nil {
		_, _ = fmt.Fprintf(stderr, "write output: %v\n", err)
		return 1
	}
	if run.Status != wfruntime.StatusSuccess {
		return 1
	}
	return 0
}

type cliOptions struct {
	vars       map[string]any
	request    map[string]any
	nodeOutput string
	pretty     bool
}

type multiFlag []string

func (m *multiFlag) String() string {
	return strings.Join(*m, ",")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func parseCLIArgs(args []string, stderr io.Writer) (*cliOptions, error) {
	fs := flag.NewFlagSet("bound-workflow", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		requestJSON     string
		requestFile     string
		requestBodyJSON string
		requestBodyFile string
		nodeOutput      string
		pretty          bool
		varFlags        multiFlag
		varJSONFlags    multiFlag
	)

	fs.StringVar(&requestJSON, "request-json", "", "full request JSON object")
	fs.StringVar(&requestFile, "request-file", "", "path to full request JSON file")
	fs.StringVar(&requestBodyJSON, "request-body-json", "", "request.body JSON value")
	fs.StringVar(&requestBodyFile, "request-body-file", "", "path to request.body JSON file")
	fs.Var(&varFlags, "var", "workflow variable in key=value form; repeatable")
	fs.Var(&varJSONFlags, "var-json", "workflow variable in key=<json> form; repeatable")
	fs.StringVar(&nodeOutput, "node-output", "", "print only one node result")
	fs.BoolVar(&pretty, "pretty", true, "pretty-print JSON output")

	if err := fs.Parse(argsOrDefault(args)); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected positional args: %s", strings.Join(fs.Args(), " "))
	}

	request, err := buildRequestObject(requestJSON, requestFile, requestBodyJSON, requestBodyFile)
	if err != nil {
		return nil, err
	}
	vars, err := buildVars(varFlags, varJSONFlags)
	if err != nil {
		return nil, err
	}

	return &cliOptions{
		vars:       vars,
		request:    request,
		nodeOutput: strings.TrimSpace(nodeOutput),
		pretty:     pretty,
	}, nil
}

func argsOrDefault(args []string) []string {
	if args != nil {
		return args
	}
	return os.Args[1:]
}

func buildRequestObject(requestJSON, requestFile, requestBodyJSON, requestBodyFile string) (map[string]any, error) {
	var request map[string]any
	if requestJSON != "" && requestFile != "" {
		return nil, errors.New("request-json and request-file cannot be used together")
	}
	if requestJSON != "" {
		value, err := parseJSONValue(requestJSON)
		if err != nil {
			return nil, fmt.Errorf("parse request-json: %w", err)
		}
		request, err = asObject(value, "request-json")
		if err != nil {
			return nil, err
		}
	}
	if requestFile != "" {
		value, err := parseJSONFile(requestFile)
		if err != nil {
			return nil, fmt.Errorf("parse request-file: %w", err)
		}
		request, err = asObject(value, "request-file")
		if err != nil {
			return nil, err
		}
	}
	if request == nil {
		request = map[string]any{}
	}

	if requestBodyJSON != "" && requestBodyFile != "" {
		return nil, errors.New("request-body-json and request-body-file cannot be used together")
	}
	if requestBodyJSON != "" {
		value, err := parseJSONValue(requestBodyJSON)
		if err != nil {
			return nil, fmt.Errorf("parse request-body-json: %w", err)
		}
		request["body"] = value
	}
	if requestBodyFile != "" {
		value, err := parseJSONFile(requestBodyFile)
		if err != nil {
			return nil, fmt.Errorf("parse request-body-file: %w", err)
		}
		request["body"] = value
	}

	if len(request) == 0 {
		return nil, nil
	}
	return request, nil
}

func buildVars(rawVars, jsonVars []string) (map[string]any, error) {
	if len(rawVars) == 0 && len(jsonVars) == 0 {
		return nil, nil
	}
	vars := map[string]any{}
	for _, item := range rawVars {
		key, value, err := splitKV(item)
		if err != nil {
			return nil, fmt.Errorf("parse var %q: %w", item, err)
		}
		vars[key] = value
	}
	for _, item := range jsonVars {
		key, raw, err := splitKV(item)
		if err != nil {
			return nil, fmt.Errorf("parse var-json %q: %w", item, err)
		}
		value, err := parseJSONValue(raw)
		if err != nil {
			return nil, fmt.Errorf("parse var-json %q: %w", item, err)
		}
		vars[key] = value
	}
	return vars, nil
}

func splitKV(value string) (string, string, error) {
	idx := strings.Index(value, "=")
	if idx <= 0 {
		return "", "", errors.New("expected key=value")
	}
	key := strings.TrimSpace(value[:idx])
	raw := value[idx+1:]
	if key == "" {
		return "", "", errors.New("key is empty")
	}
	return key, raw, nil
}

func parseDefinitionJSON(raw string) (*definition.WorkflowDefinition, error) {
	value, err := parseJSONValue(raw)
	if err != nil {
		return nil, err
	}
	buf, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var def definition.WorkflowDefinition
	if err := json.Unmarshal(buf, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

func parseJSONValue(raw string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func parseJSONFile(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseJSONValue(string(raw))
}

func asObject(value any, name string) (map[string]any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", name)
	}
	return obj, nil
}

func writeRunOutput(w io.Writer, run *wfruntime.WorkflowRun, nodeOutput string, pretty bool) error {
	if run == nil {
		return errors.New("run is nil")
	}
	if nodeOutput != "" {
		value, ok := run.Context.NodeResults[nodeOutput]
		if !ok {
			return fmt.Errorf("node output not found: %s", nodeOutput)
		}
		return writeJSON(w, value, pretty)
	}
	return writeJSON(w, run, pretty)
}

func writeJSON(w io.Writer, value any, pretty bool) error {
	encoder := json.NewEncoder(w)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(value); err != nil {
		return err
	}
	return nil
}
