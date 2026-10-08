package config

import (
	"os"
	"path/filepath"
)

// Paths is the data-root layout (see docs/design.md). Every directory used by
// the daemon derives from Root.
type Paths struct {
	Root         string
	Projects     string
	Providers    string
	Repos        string
	Instances    string
	State        string
	Runs         string
	ProviderData string
	Logs         string
	Ports        string
	Locks        string
}

// NewPaths derives the full data-root layout from root.
func NewPaths(root string) Paths {
	return Paths{
		Root:         root,
		Projects:     filepath.Join(root, "projects"),
		Providers:    filepath.Join(root, "providers"),
		Repos:        filepath.Join(root, "repos"),
		Instances:    filepath.Join(root, "instances"),
		State:        filepath.Join(root, "state"),
		Runs:         filepath.Join(root, "runs"),
		ProviderData: filepath.Join(root, "provider-data"),
		Logs:         filepath.Join(root, "logs"),
		Ports:        filepath.Join(root, "ports"),
		Locks:        filepath.Join(root, "locks"),
	}
}

// DefaultRoot returns $XDG_DATA_HOME/pushrun, falling back to
// ~/.local/share/pushrun when XDG_DATA_HOME is unset.
func DefaultRoot() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "pushrun")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "pushrun")
	}
	return "pushrun"
}
