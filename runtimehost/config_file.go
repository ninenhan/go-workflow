package runtimehost

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	profilecore "github.com/ninenhan/go-profile/core"
	"gopkg.in/yaml.v3"
)

const ConfigFileVersion = 1

type FileConfig struct {
	Version    int                    `yaml:"version" mapstructure:"version"`
	Runtime    FileRuntimeConfig      `yaml:"runtime" mapstructure:"runtime"`
	Desktop    FileDesktopConfig      `yaml:"desktop,omitempty" mapstructure:"desktop"`
	Storage    FileStorageConfig      `yaml:"storage,omitempty" mapstructure:"storage"`
	Redis      FileRedisConfig        `yaml:"redis,omitempty" mapstructure:"redis"`
	Extensions map[string]interface{} `yaml:"extensions,omitempty" mapstructure:"extensions"`
}

type FileRuntimeConfig struct {
	ListenHost       string `yaml:"listen_host,omitempty" mapstructure:"listen_host"`
	Port             *int   `yaml:"port,omitempty" mapstructure:"port"`
	DataDirectory    string `yaml:"data_directory,omitempty" mapstructure:"data_directory"`
	EmbeddedWorker   *bool  `yaml:"embedded_worker,omitempty" mapstructure:"embedded_worker"`
	Automations      *bool  `yaml:"automations,omitempty" mapstructure:"automations"`
	AutomationPeriod string `yaml:"automation_period,omitempty" mapstructure:"automation_period"`
}

type FileDesktopConfig struct {
	PreventSleep  *bool `yaml:"prevent_sleep,omitempty" mapstructure:"prevent_sleep"`
	LaunchAtLogin *bool `yaml:"launch_at_login,omitempty" mapstructure:"launch_at_login"`
}

type FileStorageConfig struct {
	Database FileDatabaseConfig `yaml:"database,omitempty" mapstructure:"database"`
}

type FileDatabaseConfig struct {
	Driver string `yaml:"driver,omitempty" mapstructure:"driver"`
	DSN    string `yaml:"dsn,omitempty" mapstructure:"dsn"`
	DSNEnv string `yaml:"dsn_env,omitempty" mapstructure:"dsn_env"`
}

type FileRedisConfig struct {
	Mode        string `yaml:"mode,omitempty" mapstructure:"mode"`
	Address     string `yaml:"address,omitempty" mapstructure:"address"`
	Username    string `yaml:"username,omitempty" mapstructure:"username"`
	PasswordEnv string `yaml:"password_env,omitempty" mapstructure:"password_env"`
	Database    int    `yaml:"database,omitempty" mapstructure:"database"`
	TLS         bool   `yaml:"tls,omitempty" mapstructure:"tls"`
}

func LoadConfigFile(path string) (FileConfig, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	if err := validateConfigStructure(source); err != nil {
		return FileConfig{}, fmt.Errorf("decode workflow config %s: %w", path, err)
	}
	loaded, err := loadEcoConfig(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("decode workflow config %s: %w", path, err)
	}
	config := *loaded
	if err := config.Validate(); err != nil {
		return FileConfig{}, fmt.Errorf("validate workflow config %s: %w", path, err)
	}
	if directory := strings.TrimSpace(config.Runtime.DataDirectory); directory != "" && !filepath.IsAbs(directory) {
		absoluteConfigPath, err := filepath.Abs(path)
		if err != nil {
			return FileConfig{}, fmt.Errorf("resolve workflow config path: %w", err)
		}
		config.Runtime.DataDirectory = filepath.Join(filepath.Dir(absoluteConfigPath), directory)
	}
	return config, nil
}

// LoadEcoConfig uses Viper's process-global registry internally. Serializing
// calls here prevents concurrent host/config tests from resetting each other.
var ecoConfigLoad sync.Mutex

func loadEcoConfig(path string) (*FileConfig, error) {
	ecoConfigLoad.Lock()
	defer ecoConfigLoad.Unlock()
	return profilecore.LoadEcoConfig[FileConfig](path)
}

var configFields = map[string]map[string]struct{}{
	"": {
		"version": {}, "runtime": {}, "desktop": {}, "storage": {}, "redis": {}, "extensions": {},
	},
	"runtime": {
		"listen_host": {}, "port": {}, "data_directory": {}, "embedded_worker": {}, "automations": {}, "automation_period": {},
	},
	"desktop": {
		"prevent_sleep": {}, "launch_at_login": {},
	},
	"storage": {
		"database": {},
	},
	"storage.database": {
		"driver": {}, "dsn": {}, "dsn_env": {},
	},
	"redis": {
		"mode": {}, "address": {}, "username": {}, "password_env": {}, "database": {}, "tls": {},
	},
}

func validateConfigStructure(source []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	if len(document.Content) != 1 {
		return errors.New("configuration must contain one YAML mapping")
	}
	if err := validateConfigMapping(document.Content[0], ""); err != nil {
		return err
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func validateConfigMapping(node *yaml.Node, configPath string) error {
	if node.Kind != yaml.MappingNode {
		label := configPath
		if label == "" {
			label = "configuration"
		}
		return fmt.Errorf("%s must be a mapping", label)
	}
	allowed := configFields[configPath]
	for index := 0; index < len(node.Content); index += 2 {
		key := node.Content[index]
		value := node.Content[index+1]
		if key.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s field name must be a string", configPath)
		}
		if _, ok := allowed[key.Value]; !ok {
			label := configPath
			if label == "" {
				label = "configuration"
			}
			return fmt.Errorf("%s contains unknown field %q", label, key.Value)
		}
		childPath := key.Value
		if configPath != "" {
			childPath = configPath + "." + key.Value
		}
		if _, nested := configFields[childPath]; nested {
			if err := validateConfigMapping(value, childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (config FileConfig) Validate() error {
	if config.Version != ConfigFileVersion {
		return fmt.Errorf("version must be %d", ConfigFileVersion)
	}
	if host := strings.TrimSpace(config.Runtime.ListenHost); host != "" && net.ParseIP(host) == nil && host != "localhost" {
		return fmt.Errorf("runtime.listen_host %q must be an IP address or localhost", host)
	}
	if config.Runtime.Port != nil && (*config.Runtime.Port < 1 || *config.Runtime.Port > 65535) {
		return errors.New("runtime.port must be between 1 and 65535")
	}
	if period := strings.TrimSpace(config.Runtime.AutomationPeriod); period != "" {
		parsed, err := time.ParseDuration(period)
		if err != nil || parsed <= 0 {
			return errors.New("runtime.automation_period must be a positive Go duration such as 1s")
		}
	}
	driver := strings.ToLower(strings.TrimSpace(config.Storage.Database.Driver))
	if driver != "" && driver != "sqlite" && driver != "mysql" && driver != "postgres" {
		return fmt.Errorf("storage.database.driver %q is invalid", driver)
	}
	if strings.TrimSpace(config.Storage.Database.DSN) != "" && strings.TrimSpace(config.Storage.Database.DSNEnv) != "" {
		return errors.New("storage.database.dsn and storage.database.dsn_env are mutually exclusive")
	}
	redisMode := strings.ToLower(strings.TrimSpace(config.Redis.Mode))
	if redisMode != "" && redisMode != "disabled" && redisMode != "local" && redisMode != "remote" {
		return fmt.Errorf("redis.mode %q is invalid", redisMode)
	}
	if config.Redis.Database < 0 {
		return errors.New("redis.database must not be negative")
	}
	return nil
}

func (fileConfig FileConfig) Apply(config Config) (Config, error) {
	driver := strings.ToLower(strings.TrimSpace(fileConfig.Storage.Database.Driver))
	if driver != "" && driver != "sqlite" {
		return Config{}, fmt.Errorf("database driver %q is not supported by this build; supported driver: sqlite", driver)
	}
	if strings.TrimSpace(fileConfig.Storage.Database.DSN) != "" || strings.TrimSpace(fileConfig.Storage.Database.DSNEnv) != "" {
		return Config{}, errors.New("custom database DSNs are not supported by this build; SQLite is stored below runtime.data_directory")
	}
	redisMode := strings.ToLower(strings.TrimSpace(fileConfig.Redis.Mode))
	if redisMode != "" && redisMode != "disabled" {
		return Config{}, fmt.Errorf("redis mode %q is not supported by this build", redisMode)
	}
	host, portText, err := net.SplitHostPort(config.Address)
	if err != nil {
		return Config{}, fmt.Errorf("split default workflow address: %w", err)
	}
	if configuredHost := strings.TrimSpace(fileConfig.Runtime.ListenHost); configuredHost != "" {
		host = configuredHost
	}
	if fileConfig.Runtime.Port != nil {
		portText = strconv.Itoa(*fileConfig.Runtime.Port)
	}
	config.Address = net.JoinHostPort(host, portText)
	if directory := strings.TrimSpace(fileConfig.Runtime.DataDirectory); directory != "" {
		config.DataDirectory = directory
	}
	if fileConfig.Runtime.EmbeddedWorker != nil {
		config.DisableEmbeddedWorker = !*fileConfig.Runtime.EmbeddedWorker
	}
	if fileConfig.Runtime.Automations != nil {
		config.DisableAutomations = !*fileConfig.Runtime.Automations
	}
	if period := strings.TrimSpace(fileConfig.Runtime.AutomationPeriod); period != "" {
		config.AutomationPeriod, err = time.ParseDuration(period)
		if err != nil {
			return Config{}, fmt.Errorf("parse automation period: %w", err)
		}
	}
	return config, nil
}
