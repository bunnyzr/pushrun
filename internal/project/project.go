// Package project models a pushrun project definition (schema
// pushrun.project/v1, see docs/design.md) and provides validation for it.
package project

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// SchemaV1 is the schema identifier of the current project file format.
const SchemaV1 = "pushrun.project/v1"

// Project is a project definition stored at <root>/projects/<name>.yaml.
type Project struct {
	Schema      string `yaml:"schema" json:"schema"`
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"display_name,omitempty" json:"display_name,omitempty"`
	Tree        []Node `yaml:"tree" json:"tree"`
	Pipeline    []Step `yaml:"pipeline" json:"pipeline"`
	// Env is injected into every pipeline step; a step's own Env wins on
	// conflict.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// LogDir is the instance business-log directory relative to the instance
	// root; empty defaults to "logs".
	LogDir string `yaml:"log_dir,omitempty" json:"log_dir,omitempty"`
	// Test, when set, is the only step the "test" action runs (after
	// assembly, with the leased port env); absent, "test" runs the pipeline.
	Test *Step `yaml:"test,omitempty" json:"test,omitempty"`
	// Ports names leased ports: the engine leases one port per name,
	// injecting CI_PORT_<NAME> per name and CI_PORT for the first. Empty
	// keeps the single-port CI_PORT behavior.
	Ports []string `yaml:"ports,omitempty" json:"ports,omitempty"`
}

// Node is one entry of the instance directory tree. A node without a Mount
// is a plain directory.
type Node struct {
	Path  string `yaml:"path" json:"path"`
	Mount *Mount `yaml:"mount,omitempty" json:"mount,omitempty"`
}

// Mount binds a node to a provider. Primary marks the mount that receives
// the triggering commit; at most one mount may be primary.
type Mount struct {
	Provider string            `yaml:"provider" json:"provider"`
	Primary  bool              `yaml:"primary,omitempty" json:"primary,omitempty"`
	Params   map[string]string `yaml:"params,omitempty" json:"params,omitempty"`
}

// Step is one pipeline command. A background step is started as a
// supervised process and must be the last step.
type Step struct {
	Name       string  `yaml:"name" json:"name"`
	Run        string  `yaml:"run" json:"run"`
	Timeout    int     `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Background bool    `yaml:"background,omitempty" json:"background,omitempty"`
	Health     *Health `yaml:"health,omitempty" json:"health,omitempty"`
	// Env is injected into this step only, on top of the project Env.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// Health describes the readiness check of a background step.
type Health struct {
	Type     string `yaml:"type" json:"type"`
	Target   string `yaml:"target" json:"target"`
	Interval int    `yaml:"interval,omitempty" json:"interval,omitempty"`
	Retries  int    `yaml:"retries,omitempty" json:"retries,omitempty"`
}

// DefaultLogDir is the business-log directory used when Project.LogDir is
// empty.
const DefaultLogDir = "logs"

// LogDirOrDefault returns the instance business-log directory: LogDir, or
// DefaultLogDir when unset.
func (p *Project) LogDirOrDefault() string {
	if p.LogDir == "" {
		return DefaultLogDir
	}
	return p.LogDir
}

// envKeyPattern constrains environment variable names.
var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// portNamePattern constrains named-port identifiers (letters and digits,
// starting with a letter).
var portNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

func validateEnv(scope string, env map[string]string) error {
	for k := range env {
		if !envKeyPattern.MatchString(k) {
			return fmt.Errorf("project: %s env key %q must match %s", scope, k, envKeyPattern)
		}
	}
	return nil
}

// Validate enforces the project invariants: node paths are relative and
// clean, a mount node is a leaf (no other node's path has it as prefix), at
// most one mount is primary, and at most one step is background and it is
// the last step. Env keys must be valid environment variable names, log_dir
// a clean relative path, port names letters/digits and unique, and the test
// step must not be background.
func (p *Project) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("project: name is required")
	}

	primary := 0
	for i, n := range p.Tree {
		if n.Path == "" || path.IsAbs(n.Path) || path.Clean(n.Path) != n.Path || strings.HasPrefix(n.Path, "..") {
			return fmt.Errorf("project: tree[%d] path %q must be relative and clean", i, n.Path)
		}
		if n.Mount == nil {
			continue
		}
		if n.Mount.Primary {
			primary++
		}
		for j, other := range p.Tree {
			if i != j && strings.HasPrefix(other.Path, n.Path+"/") {
				return fmt.Errorf("project: mount node %q has child node %q", n.Path, other.Path)
			}
		}
	}
	if primary > 1 {
		return fmt.Errorf("project: at most one mount may be primary, got %d", primary)
	}

	for i, s := range p.Pipeline {
		if err := validateEnv(fmt.Sprintf("pipeline step %q", s.Name), s.Env); err != nil {
			return err
		}
		if !s.Background {
			continue
		}
		if i != len(p.Pipeline)-1 {
			return fmt.Errorf("project: background step %q must be the last step", s.Name)
		}
	}

	if err := validateEnv("project", p.Env); err != nil {
		return err
	}
	if p.LogDir != "" && (path.IsAbs(p.LogDir) || path.Clean(p.LogDir) != p.LogDir || p.LogDir == ".." || strings.HasPrefix(p.LogDir, "../")) {
		return fmt.Errorf("project: log_dir %q must be a clean relative path inside the instance", p.LogDir)
	}
	if p.Test != nil {
		if p.Test.Background {
			return fmt.Errorf("project: test step %q must not be background", p.Test.Name)
		}
		if err := validateEnv("test step", p.Test.Env); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(p.Ports))
	for _, name := range p.Ports {
		if !portNamePattern.MatchString(name) {
			return fmt.Errorf("project: port name %q must match %s", name, portNamePattern)
		}
		// Names map to CI_PORT_<NAME> uppercased, so uniqueness is
		// case-insensitive: web and WEB would collide on CI_PORT_WEB.
		key := strings.ToUpper(name)
		if seen[key] {
			return fmt.Errorf("project: duplicate port name %q (names are case-insensitive)", name)
		}
		seen[key] = true
	}
	return nil
}
