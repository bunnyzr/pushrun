// Package config models the pushrun server configuration, the data-root
// path layout, and the daemon auth token.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/fsutil"
)

// Config is the server configuration stored in <root>/config.yaml.
type Config struct {
	HTTP     HTTPConfig     `yaml:"http"`
	PortPool PortPoolConfig `yaml:"port_pool"`
	Auth     AuthConfig     `yaml:"auth"`
	Log      LogConfig      `yaml:"log"`
	Git      GitConfig      `yaml:"git"`
}

type HTTPConfig struct {
	Bind string `yaml:"bind"`
	Port int    `yaml:"port"`
}

type PortPoolConfig struct {
	From int `yaml:"from"`
	To   int `yaml:"to"`
}

type AuthConfig struct {
	Enabled bool   `yaml:"enabled"`
	Token   string `yaml:"token,omitempty"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// GitConfig configures the builtin git provider. Scheme expands host/path
// repo shorthands into full remote URLs for the one-time seed fetch
// (credentials for private remotes are the deployer's business: credential
// helpers, SSH keys, or a full URL with embedded credentials in the project
// definition).
type GitConfig struct {
	Scheme string `yaml:"scheme"`
}

func defaultConfig() *Config {
	return &Config{
		HTTP:     HTTPConfig{Bind: "0.0.0.0", Port: 8000},
		PortPool: PortPoolConfig{From: 20000, To: 21000},
		Auth:     AuthConfig{Enabled: true},
		Log:      LogConfig{Level: "info"},
		Git:      GitConfig{Scheme: "https"},
	}
}

// Load reads <root>/config.yaml. When the file is missing it is created
// with the default configuration and those defaults are returned.
func Load(root string) (*Config, error) {
	path := filepath.Join(root, "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		cfg := defaultConfig()
		if err := fsutil.WriteFileAtomic(path, mustMarshalYAML(cfg), 0o600); err != nil {
			return nil, fmt.Errorf("write default config: %w", err)
		}
		return cfg, nil
	}

	// Unmarshal over the defaults so a partial config (e.g. only an HTTP
	// port) never silently disables auth or zeroes the port pool (auth is
	// never off by default; see docs/design.md).
	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", path, err)
	}
	return cfg, nil
}

// Validate enforces the config invariants: a usable port pool and a known
// log level.
func (c *Config) Validate() error {
	if c.PortPool.From < 1 || c.PortPool.To > 65535 || c.PortPool.From > c.PortPool.To {
		return fmt.Errorf("port_pool: invalid range %d-%d (want 1 <= from <= to <= 65535)", c.PortPool.From, c.PortPool.To)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level: invalid level %q (want debug|info|warn|error)", c.Log.Level)
	}
	if c.Git.Scheme == "" || strings.ContainsAny(c.Git.Scheme, `:/`) {
		return fmt.Errorf("git.scheme: invalid scheme %q (want a URL scheme like https or ssh)", c.Git.Scheme)
	}
	return nil
}

func mustMarshalYAML(v any) []byte {
	data, err := yaml.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal default config: %v", err))
	}
	return data
}

// EnsureToken returns the daemon auth token stored in <root>/token. When
// the file is missing a random 32-byte hex token is generated and written
// with mode 0600; generated reports whether this call created it.
func EnsureToken(root string) (token string, generated bool, err error) {
	path := filepath.Join(root, "token")
	data, err := os.ReadFile(path)
	if err == nil {
		token = strings.TrimSpace(string(data))
		if token == "" {
			return "", false, fmt.Errorf("token file %s is empty", path)
		}
		return token, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, fmt.Errorf("read token: %w", err)
	}

	token, err = newSecret()
	if err != nil {
		return "", false, err
	}
	if err := fsutil.WriteFileAtomic(path, []byte(token+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write token: %w", err)
	}
	return token, true, nil
}

// EnsureHookSecret returns the hook shared secret stored in
// <root>/hooks-secret (0600), generating it on first use. Git hook scripts
// present it to the loopback-only /internal/v1/hooks/* endpoints; it is
// independent of the user-facing auth token.
func EnsureHookSecret(root string) (string, error) {
	path := filepath.Join(root, "hooks-secret")
	data, err := os.ReadFile(path)
	if err == nil {
		secret := strings.TrimSpace(string(data))
		if secret == "" {
			return "", fmt.Errorf("hook secret file %s is empty", path)
		}
		return secret, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read hook secret: %w", err)
	}

	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, []byte(secret+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write hook secret: %w", err)
	}
	return secret, nil
}

// newSecret returns a random 32-byte hex string.
func newSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
