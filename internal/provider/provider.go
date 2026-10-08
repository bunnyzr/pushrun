// Package provider models a pushrun provider definition (see
// docs/provider-contract.md), validates it, and persists it under
// <root>/providers/<id>/.
package provider

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// SchemaV1 is the schema identifier of the current provider file format.
const SchemaV1 = "pushrun.provider/v1"

// Param types accepted by Validate.
const (
	ParamTypeString  = "string"
	ParamTypeNumber  = "number"
	ParamTypeBoolean = "boolean"
	ParamTypeSelect  = "select"
)

// Param scopes accepted by Validate.
const (
	ScopeWarmup  = "warmup"
	ScopeInstall = "install"
)

// Provider is a provider definition stored as provider.yaml inside
// <root>/providers/<id>/. Warmup and Install are script paths relative to
// the provider dir; Install is required.
type Provider struct {
	Schema      string  `yaml:"schema" json:"schema"`
	ID          string  `yaml:"id" json:"id"`
	Name        string  `yaml:"name" json:"name"`
	Description string  `yaml:"description,omitempty" json:"description,omitempty"`
	Warmup      string  `yaml:"warmup,omitempty" json:"warmup,omitempty"`
	Install     string  `yaml:"install" json:"install"`
	Params      []Param `yaml:"parameters,omitempty" json:"parameters,omitempty"`
}

// Param is one declared provider parameter. A secret param is never logged
// and cannot carry a default value. A select param must declare non-empty
// Options; every other type must not.
type Param struct {
	ID       string   `yaml:"id" json:"id"`
	Label    string   `yaml:"label,omitempty" json:"label,omitempty"`
	Type     string   `yaml:"type" json:"type"`
	Options  []string `yaml:"options,omitempty" json:"options,omitempty"`
	Scope    string   `yaml:"scope" json:"scope"`
	Required bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Default  string   `yaml:"default,omitempty" json:"default,omitempty"`
	Secret   bool     `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// Validate enforces the provider invariants: id and install are required,
// script paths are relative, clean, and contained in the provider dir, and
// every param has a unique id, a known type and scope, non-empty options
// exactly when its type is select, and — when secret — no default value.
func (p *Provider) Validate() error {
	if p.ID == "" {
		return errors.New("provider: id is required")
	}
	if p.Install == "" {
		return errors.New("provider: install script is required")
	}
	for _, script := range []struct {
		phase string
		path  string
	}{
		{"warmup", p.Warmup},
		{"install", p.Install},
	} {
		if script.path == "" {
			continue
		}
		if !cleanRelPath(script.path) {
			return fmt.Errorf("provider: %s script path %q must be relative, clean, and under the provider dir", script.phase, script.path)
		}
	}

	seen := make(map[string]bool, len(p.Params))
	for i, prm := range p.Params {
		if prm.ID == "" {
			return fmt.Errorf("provider: parameters[%d] id is required", i)
		}
		if seen[prm.ID] {
			return fmt.Errorf("provider: duplicate parameter id %q", prm.ID)
		}
		seen[prm.ID] = true
		switch prm.Type {
		case ParamTypeString, ParamTypeNumber, ParamTypeBoolean, ParamTypeSelect:
		default:
			return fmt.Errorf("provider: parameter %q has invalid type %q", prm.ID, prm.Type)
		}
		if prm.Type == ParamTypeSelect && len(prm.Options) == 0 {
			return fmt.Errorf("provider: select parameter %q requires non-empty options", prm.ID)
		}
		if prm.Type != ParamTypeSelect && len(prm.Options) > 0 {
			return fmt.Errorf("provider: parameter %q of type %q cannot have options (select only)", prm.ID, prm.Type)
		}
		switch prm.Scope {
		case ScopeWarmup, ScopeInstall:
		default:
			return fmt.Errorf("provider: parameter %q has invalid scope %q", prm.ID, prm.Scope)
		}
		if prm.Secret && prm.Default != "" {
			return fmt.Errorf("provider: secret parameter %q cannot have a default", prm.ID)
		}
	}
	return nil
}

// cleanRelPath reports whether s is a relative, clean slash path that does
// not escape its base directory.
func cleanRelPath(s string) bool {
	return s != "" && !path.IsAbs(s) && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../")
}

// Builtins returns the descriptors of the providers built into the daemon:
// git (clone/checkout into a mount point) and symlink (link an existing
// directory into a mount point). Their scripts are embedded.
func Builtins() []Provider {
	return []Provider{
		{
			Schema:      SchemaV1,
			ID:          "git",
			Name:        "Git",
			Description: "Seeds the repo's bare-repo snapshot from the source remote once during warmup and checks out the triggering commit (or the snapshot ref) during install.",
			Warmup:      "scripts/warmup.sh",
			Install:     "scripts/install.sh",
			Params: []Param{
				{ID: "repo", Label: "Repository URL", Type: ParamTypeString, Scope: ScopeWarmup, Required: true},
				{ID: "branch", Label: "Branch", Type: ParamTypeString, Scope: ScopeWarmup, Required: true},
				{ID: "commit", Label: "Commit", Type: ParamTypeString, Scope: ScopeInstall},
			},
		},
		{
			Schema:      SchemaV1,
			ID:          "symlink",
			Name:        "Symlink",
			Description: "Links an existing directory into the mount point during install.",
			Install:     "scripts/install.sh",
			Params: []Param{
				{ID: "source", Label: "Source path", Type: ParamTypeString, Scope: ScopeInstall, Required: true},
			},
		},
	}
}
