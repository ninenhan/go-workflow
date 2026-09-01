package runtimehost

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDisabledHostHasNoRuntimeSideEffects(t *testing.T) {
	dataDirectory := filepath.Join(t.TempDir(), "disabled-runtime")
	host, err := New(Config{DataDirectory: dataDirectory}, nil)
	if err != nil {
		t.Fatalf("new disabled host: %v", err)
	}
	if host.Service() != nil || host.Address() != "" {
		t.Fatalf("disabled host exposed runtime state: service=%v address=%q", host.Service(), host.Address())
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start disabled host: %v", err)
	}
	if err := host.Wait(); err != nil {
		t.Fatalf("wait disabled host: %v", err)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("disabled host touched data directory: %v", err)
	}
}

func TestHostLifecycleRunsCanonicalSingleNode(t *testing.T) {
	host, err := New(Config{
		Enabled:       true,
		Mode:          ModeHeadless,
		Address:       "127.0.0.1:0",
		DataDirectory: filepath.Join(t.TempDir(), "runtime"),
	}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new host: %v", err)
	}
	if host.Service() == nil || host.Service().EmbeddedWorker() == nil {
		t.Fatal("single-node host did not expose scheduler and embedded worker")
	}
	if host.Address() != "" {
		t.Fatalf("host bound before Start: %q", host.Address())
	}
	if err := host.Wait(); err == nil {
		t.Fatal("Wait accepted a host that was not started")
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err := host.Start(ctx); err != nil {
		t.Fatalf("start host: %v", err)
	}
	if host.Address() == "" {
		t.Fatal("started host has no bound address")
	}
	response, err := http.Get("http://" + host.Address() + "/readyz")
	if err != nil {
		t.Fatalf("read host readiness: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d", response.StatusCode)
	}

	cancel()
	waitDone := make(chan error, 1)
	go func() { waitDone <- host.Wait() }()
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatalf("wait host: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("host did not stop after context cancellation")
	}
	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeat shutdown: %v", err)
	}
	if err := host.Start(context.Background()); err == nil {
		t.Fatal("closed host started again")
	}
}
