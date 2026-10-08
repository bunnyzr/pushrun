// Package project models a pushrun project definition (schema
// pushrun.project/v1, see docs/design.md) and provides validation for it.
package project

import (
	"fmt"
	"path"
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
}

// Health describes the readiness check of a background step.
type Health struct {
	Type     string `yaml:"type" json:"type"`
	Target   string `yaml:"target" json:"target"`
	Interval int    `yaml:"interval,omitempty" json:"interval,omitempty"`
	Retries  int    `yaml:"retries,omitempty" json:"retries,omitempty"`
}

// Validate enforces the project invariants: node paths are relative and
// clean, a mount node is a leaf (no other node's path has it as prefix), at
// most one mount is primary, and at most one step is background and it is
// the last step.
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
		if !s.Background {
			continue
		}
		if i != len(p.Pipeline)-1 {
			return fmt.Errorf("project: background step %q must be the last step", s.Name)
		}
	}
	return nil
}
