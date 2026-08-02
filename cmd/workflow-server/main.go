package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ninenhan/go-workflow/core/credential"
	"github.com/ninenhan/go-workflow/persist/localdb"
	"github.com/ninenhan/go-workflow/scheduler"
	workerunit "github.com/ninenhan/go-workflow/worker/unit"
)

const defaultAddress = "127.0.0.1:55080"
const defaultDataDirectory = ".go-workflow-data"
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

	address := strings.TrimSpace(os.Getenv("WORKFLOW_ADDR"))
	if address == "" {
		address = defaultAddress
	}

	dataDirectory := strings.TrimSpace(os.Getenv("WORKFLOW_DATA_DIR"))
	if dataDirectory == "" {
		dataDirectory = defaultDataDirectory
	}
	credentialStore, err := credential.OpenFileStore(filepath.Join(dataDirectory, "credentials"))
	if err != nil {
		log.Fatalf("open encrypted credential store: %v", err)
	}
	database, err := localdb.Open(filepath.Join(dataDirectory, "workflow.db"))
	if err != nil {
		log.Fatalf("open workflow database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("close workflow database: %v", err)
		}
	}()

	service, err := scheduler.NewService(scheduler.Options{
		EnableEmbeddedWorker:   true,
		Store:                  database.Runtime,
		Definitions:            database.Definitions,
		Automations:            database.Automations,
		Credentials:            credentialStore,
		DefaultCredentialScope: "local-workspace",
	})
	if err != nil {
		log.Fatalf("create scheduler service: %v", err)
	}
	if err := service.StartAutomations(context.Background(), time.Second); err != nil {
		log.Fatalf("start workflow automations: %v", err)
	}
	handler, err := workflowServerHandler(
		scheduler.NewHTTPHandler(service).Handler(),
		os.Getenv(workflowWebDirectoryEnvironment),
	)
	if err != nil {
		log.Fatalf("configure workflow web application: %v", err)
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		log.Printf("workflow server %s listening on http://%s", buildVersion, address)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve workflow API: %v", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown workflow API: %v", err)
		}
		if err := service.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown workflow scheduler: %v", err)
		}
	}
}

func writeRuntimeUnits(destination io.Writer) error {
	if destination == nil {
		return errors.New("runtime unit destination is nil")
	}
	return json.NewEncoder(destination).Encode(workerunit.DefaultRegistry.Names())
}

func workflowServerHandler(api http.Handler, webDirectory string) (http.Handler, error) {
	webDirectory = strings.TrimSpace(webDirectory)
	if webDirectory == "" {
		return api, nil
	}
	absoluteDirectory, err := filepath.Abs(filepath.Clean(webDirectory))
	if err != nil {
		return nil, fmt.Errorf("resolve web directory: %w", err)
	}
	indexPath := filepath.Join(absoluteDirectory, "index.html")
	indexInfo, err := os.Stat(indexPath)
	if err != nil {
		return nil, fmt.Errorf("inspect web entrypoint: %w", err)
	}
	if !indexInfo.Mode().IsRegular() {
		return nil, errors.New("web entrypoint must be a regular file")
	}
	assetsPath := filepath.Join(absoluteDirectory, "assets")
	assetsInfo, err := os.Stat(assetsPath)
	if err != nil {
		return nil, fmt.Errorf("inspect web assets: %w", err)
	}
	if !assetsInfo.IsDir() {
		return nil, errors.New("web assets must be a directory")
	}

	staticFiles := http.FileServer(http.Dir(absoluteDirectory))
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticFiles.ServeHTTP(response, request)
	}))
	mux.HandleFunc("GET /{$}", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(response, request, indexPath)
	})
	mux.Handle("/", api)
	return mux, nil
}
