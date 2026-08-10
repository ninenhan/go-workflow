package runtimehost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigFileAppliesRuntimeSettings(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yml")
	source := `version: 1
runtime:
  listen_host: 0.0.0.0
  port: 58090
  data_directory: state
  embedded_worker: false
  automations: false
  automation_period: 3s
desktop:
  prevent_sleep: true
  launch_at_login: false
storage:
  database:
    driver: sqlite
redis:
  mode: disabled
extensions:
  acme.example/queue:
    replicas: 2
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	fileConfig, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	config, err := fileConfig.Apply(Config{
		Mode:             ModeHeadless,
		Address:          "127.0.0.1:55080",
		DataDirectory:    "default-data",
		AutomationPeriod: time.Second,
	})
	if err != nil {
		t.Fatalf("apply config: %v", err)
	}
	if config.Address != "0.0.0.0:58090" {
		t.Fatalf("address = %q", config.Address)
	}
	if config.DataDirectory != filepath.Join(directory, "state") {
		t.Fatalf("data directory = %q", config.DataDirectory)
	}
	if !config.DisableEmbeddedWorker || !config.DisableAutomations {
		t.Fatalf("runtime switches were not applied: %#v", config)
	}
	if config.AutomationPeriod != 3*time.Second {
		t.Fatalf("automation period = %s", config.AutomationPeriod)
	}
	if fileConfig.Desktop.PreventSleep == nil || !*fileConfig.Desktop.PreventSleep {
		t.Fatal("desktop prevent_sleep was not decoded")
	}
}

func TestLoadConfigFileUsesEcoConfigEnvironmentAndIncludeFeatures(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yml")
	if err := os.WriteFile(filepath.Join(directory, "database-dsn.txt"), []byte("file:included.db"), 0o600); err != nil {
		t.Fatalf("write included DSN: %v", err)
	}
	t.Setenv("WORKFLOW_TEST_CONFIG_PORT", "58092")
	source := `version: 1
runtime:
  port: ${WORKFLOW_TEST_CONFIG_PORT:58091}
storage:
  database:
    driver: sqlite
    dsn: !include database-dsn.txt
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if config.Runtime.Port == nil || *config.Runtime.Port != 58092 {
		t.Fatalf("environment port = %#v", config.Runtime.Port)
	}
	if strings.TrimSpace(config.Storage.Database.DSN) != "file:included.db" {
		t.Fatalf("included DSN = %q", config.Storage.Database.DSN)
	}
}

func TestFileConfigRejectsUnavailableStorageAdaptersExplicitly(t *testing.T) {
	for name, testCase := range map[string]struct {
		source string
		want   string
	}{
		"mysql": {
			source: "version: 1\nruntime: {}\nstorage:\n  database:\n    driver: mysql\n    dsn_env: WORKFLOW_MYSQL_DSN\n",
			want:   "not supported",
		},
		"redis": {
			source: "version: 1\nruntime: {}\nredis:\n  mode: remote\n  address: redis.internal:6379\n",
			want:   "not supported",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(testCase.source), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			fileConfig, err := LoadConfigFile(path)
			if err != nil {
				t.Fatalf("load structurally valid config: %v", err)
			}
			_, err = fileConfig.Apply(Config{Address: "127.0.0.1:55080"})
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("apply error = %v", err)
			}
		})
	}
}

func TestLoadConfigFileRejectsUnknownAndInvalidFields(t *testing.T) {
	for name, source := range map[string]string{
		"unknown":  "version: 1\nruntime:\n  mystery: true\n",
		"version":  "version: 2\nruntime: {}\n",
		"port":     "version: 1\nruntime:\n  port: 70000\n",
		"period":   "version: 1\nruntime:\n  automation_period: never\n",
		"multiple": "version: 1\nruntime: {}\n---\nversion: 1\nruntime: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := LoadConfigFile(path); err == nil {
				t.Fatal("invalid config was accepted")
			} else if !strings.Contains(err.Error(), path) {
				t.Fatalf("error omits config path: %v", err)
			}
		})
	}
}
