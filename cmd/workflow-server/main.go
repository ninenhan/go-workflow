package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ninenhan/go-workflow/runtimehost"
)

const defaultAddress = "127.0.0.1:55080"
const defaultDataDirectory = ".go-workflow-data"
const defaultConfigPath = "config.yml"
const workflowConfigEnvironment = "WORKFLOW_CONFIG"
const workflowWebDirectoryEnvironment = "WORKFLOW_WEB_DIR"

var (
	buildVersion  = "dev"
	buildRevision = "unknown"
	buildTime     = "unknown"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "--version":
			fmt.Printf(
				"workflow-server %s (%s, built %s)\n",
				buildVersion,
				buildRevision,
				buildTime,
			)
			return
		case "--runtime-units":
			if err := writeRuntimeUnits(os.Stdout); err != nil {
				log.Fatalf("write runtime units: %v", err)
			}
			return
		}
	}
	config, err := parseHostOptions(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		log.Fatalf("parse workflow host options: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config.DesktopToken = strings.TrimSpace(os.Getenv("WORKFLOW_DESKTOP_TOKEN"))
	config.Version = buildVersion
	if err := runtimehost.Run(ctx, config, log.Default()); err != nil {
		log.Fatalf("run workflow host: %v", err)
	}
}

func parseHostOptions(args []string, getenv func(string) string, output io.Writer) (runtimehost.Config, error) {
	config := runtimehost.Config{
		Mode:             runtimehost.ModeHeadless,
		Address:          defaultAddress,
		DataDirectory:    defaultDataDirectory,
		AutomationPeriod: time.Second,
	}
	configuredPath := strings.TrimSpace(getenv(workflowConfigEnvironment))
	configPathDefault := configuredPath
	if configPathDefault == "" {
		configPathDefault = defaultConfigPath
	}

	flags := flag.NewFlagSet("workflow-server", flag.ContinueOnError)
	flags.SetOutput(output)
	configPath := flags.String("config", configPathDefault, "YAML runtime configuration file")
	mode := flags.String("mode", string(runtimehost.ModeHeadless), "server, desktop, or headless")
	address := flags.String("addr", defaultAddress, "HTTP listen address")
	dataDirectory := flags.String("data-dir", defaultDataDirectory, "runtime data directory")
	webDirectory := flags.String("web-dir", "", "production Web build directory")
	embeddedWorker := flags.Bool("embedded-worker", true, "run the embedded workflow worker")
	automations := flags.Bool("automations", true, "run scheduled automations")
	automationPeriod := flags.Duration("automation-period", time.Second, "automation scan period")
	if err := flags.Parse(args); err != nil {
		return runtimehost.Config{}, err
	}
	if flags.NArg() != 0 {
		return runtimehost.Config{}, fmt.Errorf("unexpected workflow host arguments: %s", strings.Join(flags.Args(), " "))
	}
	visited := make(map[string]bool)
	flags.Visit(func(option *flag.Flag) { visited[option.Name] = true })

	pathExplicit := visited["config"] || configuredPath != ""
	fileConfig, err := runtimehost.LoadConfigFile(strings.TrimSpace(*configPath))
	if err == nil {
		config, err = fileConfig.Apply(config)
		if err != nil {
			return runtimehost.Config{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) || pathExplicit {
		return runtimehost.Config{}, err
	}

	if value := strings.TrimSpace(getenv("WORKFLOW_MODE")); value != "" {
		config.Mode = runtimehost.Mode(value)
	}
	if value := strings.TrimSpace(getenv("WORKFLOW_ADDR")); value != "" {
		config.Address = value
	}
	if value := strings.TrimSpace(getenv("WORKFLOW_DATA_DIR")); value != "" {
		config.DataDirectory = value
	}
	if value := strings.TrimSpace(getenv(workflowWebDirectoryEnvironment)); value != "" {
		config.WebDirectory = value
	}
	if value := strings.TrimSpace(getenv("WORKFLOW_EMBEDDED_WORKER")); value != "" {
		enabled, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			return runtimehost.Config{}, fmt.Errorf("WORKFLOW_EMBEDDED_WORKER: %w", parseErr)
		}
		config.DisableEmbeddedWorker = !enabled
	}
	if value := strings.TrimSpace(getenv("WORKFLOW_AUTOMATIONS")); value != "" {
		enabled, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			return runtimehost.Config{}, fmt.Errorf("WORKFLOW_AUTOMATIONS: %w", parseErr)
		}
		config.DisableAutomations = !enabled
	}
	if value := strings.TrimSpace(getenv("WORKFLOW_AUTOMATION_PERIOD")); value != "" {
		period, parseErr := time.ParseDuration(value)
		if parseErr != nil || period <= 0 {
			return runtimehost.Config{}, errors.New("WORKFLOW_AUTOMATION_PERIOD must be a positive duration")
		}
		config.AutomationPeriod = period
	}

	if visited["mode"] {
		config.Mode = runtimehost.Mode(strings.TrimSpace(*mode))
	}
	if visited["addr"] {
		config.Address = strings.TrimSpace(*address)
	}
	if visited["data-dir"] {
		config.DataDirectory = strings.TrimSpace(*dataDirectory)
	}
	if visited["web-dir"] {
		config.WebDirectory = strings.TrimSpace(*webDirectory)
	}
	if visited["embedded-worker"] {
		config.DisableEmbeddedWorker = !*embeddedWorker
	}
	if visited["automations"] {
		config.DisableAutomations = !*automations
	}
	if visited["automation-period"] {
		if *automationPeriod <= 0 {
			return runtimehost.Config{}, errors.New("--automation-period must be positive")
		}
		config.AutomationPeriod = *automationPeriod
	}
	return config, nil
}

func writeRuntimeUnits(destination io.Writer) error {
	return runtimehost.WriteRuntimeUnits(destination)
}

func workflowServerHandler(api http.Handler, webDirectory string) (http.Handler, error) {
	return runtimehost.NewHTTPHandler(api, webDirectory, "", nil)
}
