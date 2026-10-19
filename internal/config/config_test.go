package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bunnyzr/pushrun/internal/config"
)

func TestLoadCreatesDefaults(t *testing.T) {
	root := t.TempDir()

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Port != 8000 {
		t.Errorf("HTTP.Port = %d, want 8000", cfg.HTTP.Port)
	}
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled = false, want true")
	}
	if cfg.PortPool.From != 20000 || cfg.PortPool.To != 21000 {
		t.Errorf("PortPool = %d-%d, want 20000-21000", cfg.PortPool.From, cfg.PortPool.To)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if _, err := os.Stat(filepath.Join(root, "config.yaml")); err != nil {
		t.Errorf("config.yaml not created: %v", err)
	}

	// A second Load must read back the same defaults.
	cfg2, err := config.Load(root)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if *cfg != *cfg2 {
		t.Errorf("second Load = %+v, want %+v", *cfg2, *cfg)
	}
}

func TestLoadReadsExisting(t *testing.T) {
	root := t.TempDir()
	content := []byte("http:\n  bind: 127.0.0.1\n  port: 9000\nauth:\n  enabled: false\n  token: secret\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Bind != "127.0.0.1" || cfg.HTTP.Port != 9000 {
		t.Errorf("HTTP = %+v, want bind 127.0.0.1 port 9000", cfg.HTTP)
	}
	if cfg.Auth.Enabled || cfg.Auth.Token != "secret" {
		t.Errorf("Auth = %+v, want disabled with token secret", cfg.Auth)
	}
}

func TestLoadPartialConfigKeepsDefaults(t *testing.T) {
	root := t.TempDir()
	// A hand-written config with only one key must not silently disable
	// auth or zero the port pool (auth is never off by default).
	content := []byte("http:\n  port: 9000\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Port != 9000 {
		t.Errorf("HTTP.Port = %d, want 9000", cfg.HTTP.Port)
	}
	if cfg.HTTP.Bind != "0.0.0.0" {
		t.Errorf("HTTP.Bind = %q, want default 0.0.0.0", cfg.HTTP.Bind)
	}
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled = false, want default true")
	}
	if cfg.PortPool.From != 20000 || cfg.PortPool.To != 21000 {
		t.Errorf("PortPool = %d-%d, want default 20000-21000", cfg.PortPool.From, cfg.PortPool.To)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want default info", cfg.Log.Level)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	for name, content := range map[string]string{
		"reversed pool": "port_pool:\n  from: 21000\n  to: 20000\n",
		"zero pool":     "port_pool:\n  from: 0\n  to: 0\n",
		"bad log level": "log:\n  level: shouty\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(root); err == nil {
				t.Fatalf("Load(%q) succeeded, want validation error", content)
			}
		})
	}
}

func TestEnsureTokenGeneratesAndPersists(t *testing.T) {
	root := t.TempDir()

	tok1, gen1, err := config.EnsureToken(root)
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	tok2, gen2, err := config.EnsureToken(root)
	if err != nil {
		t.Fatalf("EnsureToken (second): %v", err)
	}

	if !gen1 {
		t.Error("first call: generated = false, want true")
	}
	if gen2 {
		t.Error("second call: generated = true, want false")
	}
	if tok1 != tok2 {
		t.Errorf("tokens differ: %q vs %q", tok1, tok2)
	}
	if len(tok1) != 64 {
		t.Errorf("token length = %d, want 64", len(tok1))
	}

	info, err := os.Stat(filepath.Join(root, "token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
}

func TestPathsDeriveFromRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	p := config.NewPaths(root)

	v := reflect.ValueOf(p)
	typ := v.Type()
	for i := 0; i < v.NumField(); i++ {
		got, ok := v.Field(i).Interface().(string)
		if !ok {
			t.Fatalf("field %s is not a string", typ.Field(i).Name)
		}
		if !strings.HasPrefix(got, root+string(os.PathSeparator)) && got != root {
			t.Errorf("Paths.%s = %q, want prefix %q", typ.Field(i).Name, got, root)
		}
	}

	if p.Root != root {
		t.Errorf("Paths.Root = %q, want %q", p.Root, root)
	}
	if p.ProviderData != filepath.Join(root, "provider-data") {
		t.Errorf("Paths.ProviderData = %q", p.ProviderData)
	}
}

func TestDefaultRoot(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := config.DefaultRoot(); got != filepath.Join(xdg, "pushrun") {
		t.Errorf("DefaultRoot with XDG_DATA_HOME = %q, want %q", got, filepath.Join(xdg, "pushrun"))
	}

	t.Setenv("XDG_DATA_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	want := filepath.Join(home, ".local", "share", "pushrun")
	if got := config.DefaultRoot(); got != want {
		t.Errorf("DefaultRoot fallback = %q, want %q", got, want)
	}
}
