package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfigValidatesHostModeBoundaries(t *testing.T) {
	base := Config{Address: "127.0.0.1:55080", DataDirectory: t.TempDir()}
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{name: "headless", config: withMode(base, ModeHeadless)},
		{name: "headless rejects web", config: withWebDirectory(withMode(base, ModeHeadless), t.TempDir()), wantErr: "does not accept"},
		{name: "server requires web", config: withMode(base, ModeServer), wantErr: "requires"},
		{name: "desktop requires token", config: withWebDirectory(withMode(base, ModeDesktop), t.TempDir()), wantErr: "token"},
		{name: "desktop rejects wildcard", config: Config{Mode: ModeDesktop, Address: "0.0.0.0:55080", DataDirectory: t.TempDir(), WebDirectory: t.TempDir(), DesktopToken: strings.Repeat("x", 32)}, wantErr: "loopback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.Validate()
			if test.wantErr == "" && err != nil {
				t.Fatalf("validate config: %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("validation error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func TestDesktopShutdownRequiresExactToken(t *testing.T) {
	token := strings.Repeat("a", 32)
	shutdown := make(chan struct{}, 1)
	handler, err := NewHTTPHandler(http.NotFoundHandler(), "", token, func() { shutdown <- struct{}{} })
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/__workflow/desktop/shutdown", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/__workflow/desktop/shutdown", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("authorized status = %d", authorized.Code)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("desktop shutdown was not requested")
	}
}

func TestDesktopReadinessRequiresExactToken(t *testing.T) {
	token := strings.Repeat("b", 32)
	handler, err := NewHTTPHandler(http.NotFoundHandler(), "", token, func() {})
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized readiness status = %d", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized readiness status = %d", authorized.Code)
	}
}

func TestHealthRoutesDoNotDependOnWebUI(t *testing.T) {
	handler, err := NewHTTPHandler(http.NotFoundHandler(), "", "", nil)
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
			t.Fatalf("%s status = %d, body = %q", path, response.Code, response.Body.String())
		}
	}
}

func withMode(config Config, mode Mode) Config {
	config.Mode = mode
	return config
}

func withWebDirectory(config Config, webDirectory string) Config {
	config.WebDirectory = webDirectory
	return config
}
