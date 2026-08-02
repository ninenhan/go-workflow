package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ninenhan/go-workflow/core/definition"
	"github.com/ninenhan/go-workflow/packager"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printRootUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "check":
		return runCheck(args[1:])
	case "export":
		return runExport(args[1:])
	case "build":
		return runBuild(args[1:])
	case "-h", "--help", "help":
		printRootUsage(os.Stdout)
		return 0
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", args[0])
		printRootUsage(os.Stderr)
		return 2
	}
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("wfpack check", flag.ContinueOnError)
	workflow := fs.String("workflow", "", "path to workflow definition JSON")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitCode(err)
	}
	if *workflow == "" {
		_, _ = fmt.Fprintln(os.Stderr, "-workflow is required")
		return 2
	}

	def, err := packager.LoadDefinition(*workflow)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load workflow: %v\n", err)
		return 1
	}
	if err := packager.ValidateStandalone(def); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "standalone check failed: %v\n", err)
		return 1
	}

	name := packager.SuggestedBinaryName(def, *workflow)
	_, _ = fmt.Fprintf(os.Stdout, "ok: workflow can be packed as standalone binary (%s)\n", name)
	return 0
}

func runExport(args []string) int {
	fs := flag.NewFlagSet("wfpack export", flag.ContinueOnError)
	workflow := fs.String("workflow", "", "path to workflow definition JSON")
	dir := fs.String("dir", "", "target package directory")
	force := fs.Bool("force", false, "overwrite main.go if it exists")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitCode(err)
	}
	if *workflow == "" || *dir == "" {
		_, _ = fmt.Fprintln(os.Stderr, "-workflow and -dir are required")
		return 2
	}

	def, err := packAndWriteSource(*workflow, *dir, *force)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "export workflow: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(os.Stdout, "generated %s\n", filepath.Join(*dir, "main.go"))
	_, _ = fmt.Fprintf(os.Stdout, "build with: go build -o %s ./%s\n", packager.SuggestedBinaryName(def, *workflow), filepath.ToSlash(*dir))
	return 0
}

func runBuild(args []string) int {
	fs := flag.NewFlagSet("wfpack build", flag.ContinueOnError)
	workflow := fs.String("workflow", "", "path to workflow definition JSON")
	output := fs.String("output", "", "output binary path")
	dir := fs.String("dir", "", "package directory for generated source; default is a temp dir under module root")
	keepSource := fs.Bool("keep-source", false, "keep generated source directory after build")
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return exitCode(err)
	}
	if *workflow == "" {
		_, _ = fmt.Fprintln(os.Stderr, "-workflow is required")
		return 2
	}

	def, err := packager.LoadDefinition(*workflow)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load workflow: %v\n", err)
		return 1
	}
	moduleRoot, err := packager.FindModuleRoot(".")
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "find module root: %v\n", err)
		return 1
	}

	targetDir := *dir
	cleanup := func() {}
	if targetDir == "" {
		targetDir, err = os.MkdirTemp(moduleRoot, ".wfpack-*")
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "create temp package dir: %v\n", err)
			return 1
		}
		if !*keepSource {
			cleanup = func() {
				_ = os.RemoveAll(targetDir)
			}
		}
	}
	targetDir, err = filepath.Abs(targetDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve package dir: %v\n", err)
		return 1
	}
	defer cleanup()

	if _, err := packAndWriteSource(*workflow, targetDir, true); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "prepare source: %v\n", err)
		return 1
	}

	relDir, err := filepath.Rel(moduleRoot, targetDir)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve build package dir: %v\n", err)
		return 1
	}
	if relDir == ".." || strings.HasPrefix(relDir, ".."+string(filepath.Separator)) {
		_, _ = fmt.Fprintln(os.Stderr, "generated package dir must be inside the current module")
		return 1
	}

	binaryPath := *output
	if binaryPath == "" {
		binaryPath = filepath.Join("dist", packager.SuggestedBinaryName(def, *workflow))
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "resolve output path: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o755); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create output dir: %v\n", err)
		return 1
	}

	cmd := exec.Command("go", "build", "-o", binaryPath, "./"+filepath.ToSlash(relDir))
	cmd.Dir = moduleRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "go build failed: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(os.Stdout, "built %s\n", binaryPath)
	if *keepSource || *dir != "" {
		_, _ = fmt.Fprintf(os.Stdout, "source %s\n", targetDir)
	}
	return 0
}

func packAndWriteSource(workflowPath, dir string, force bool) (*definition.WorkflowDefinition, error) {
	def, err := packager.LoadDefinition(workflowPath)
	if err != nil {
		return nil, err
	}
	if err := packager.ValidateStandalone(def); err != nil {
		return nil, err
	}
	source, err := packager.RenderMain(def)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, "main.go")
	if !force {
		if _, err := os.Stat(target); err == nil {
			return nil, fmt.Errorf("%s already exists; use -force to overwrite", target)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := os.WriteFile(target, source, 0o644); err != nil {
		return nil, err
	}
	return def, nil
}

func exitCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

func printRootUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "wfpack <check|export|build> [flags]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "  check  validate whether a workflow can run as a standalone binary")
	_, _ = fmt.Fprintln(w, "  export generate a main.go that binds the workflow")
	_, _ = fmt.Fprintln(w, "  build  generate and compile a standalone executable")
}
