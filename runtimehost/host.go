package runtimehost

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ninenhan/go-workflow/units"
)

type Mode string

const (
	ModeServer   Mode = "server"
	ModeDesktop  Mode = "desktop"
	ModeHeadless Mode = "headless"
)

type Config struct {
	Enabled               bool
	Mode                  Mode
	Address               string
	DataDirectory         string
	WebDirectory          string
	DesktopToken          string
	Version               string
	DefaultScope          string
	AutomationPeriod      time.Duration
	DisableAutomations    bool
	DisableEmbeddedWorker bool
}

func (config Config) Validate() error {
	if config.Mode != ModeServer && config.Mode != ModeDesktop && config.Mode != ModeHeadless {
		return fmt.Errorf("unsupported workflow host mode %q", config.Mode)
	}
	if strings.TrimSpace(config.Address) == "" {
		return errors.New("workflow host address is required")
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil {
		return fmt.Errorf("invalid workflow host address: %w", err)
	}
	if strings.TrimSpace(config.DataDirectory) == "" {
		return errors.New("workflow data directory is required")
	}
	webDirectory := strings.TrimSpace(config.WebDirectory)
	token := strings.TrimSpace(config.DesktopToken)
	switch config.Mode {
	case ModeServer:
		if webDirectory == "" {
			return errors.New("server mode requires a workflow web directory")
		}
		if token != "" {
			return errors.New("desktop token is only valid in desktop mode")
		}
	case ModeDesktop:
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("desktop mode must listen on an explicit loopback IP address")
		}
		if webDirectory == "" {
			return errors.New("desktop mode requires a workflow web directory")
		}
		if len(token) < 32 {
			return errors.New("desktop mode requires a token of at least 32 characters")
		}
	case ModeHeadless:
		if webDirectory != "" {
			return errors.New("headless mode does not accept a workflow web directory")
		}
		if token != "" {
			return errors.New("desktop token is only valid in desktop mode")
		}
	}
	return nil
}

func NewHTTPHandler(api http.Handler, webDirectory, desktopToken string, requestShutdown context.CancelFunc) (http.Handler, error) {
	if api == nil {
		return nil, errors.New("workflow API handler is nil")
	}
	handler, err := withWeb(api, webDirectory)
	if err != nil {
		return nil, err
	}
	return withHostRoutes(handler, strings.TrimSpace(desktopToken), requestShutdown), nil
}

func withWeb(api http.Handler, webDirectory string) (http.Handler, error) {
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

func withHostRoutes(next http.Handler, desktopToken string, requestShutdown context.CancelFunc) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz", "/readyz":
			if request.Method != http.MethodGet {
				response.Header().Set("Allow", http.MethodGet)
				writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			if request.URL.Path == "/readyz" && desktopToken != "" {
				provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
				if len(provided) != len(desktopToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(desktopToken)) != 1 {
					writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "invalid desktop token"})
					return
				}
			}
			writeJSON(response, http.StatusOK, map[string]any{"ok": true})
			return
		case "/__workflow/desktop/shutdown":
			if desktopToken == "" || requestShutdown == nil {
				http.NotFound(response, request)
				return
			}
			if request.Method != http.MethodPost {
				response.Header().Set("Allow", http.MethodPost)
				writeJSON(response, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
				return
			}
			provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
			if len(provided) != len(desktopToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(desktopToken)) != 1 {
				writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "invalid desktop token"})
				return
			}
			writeJSON(response, http.StatusAccepted, map[string]any{"stopping": true})
			go requestShutdown()
			return
		default:
			next.ServeHTTP(response, request)
		}
	})
}

func WriteRuntimeUnits(destination io.Writer) error {
	if destination == nil {
		return errors.New("runtime unit destination is nil")
	}
	return json.NewEncoder(destination).Encode(units.BuiltinNames())
}

func writeJSON(response http.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
