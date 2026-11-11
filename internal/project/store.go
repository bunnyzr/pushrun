package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
)

func filePath(paths config.Paths, name string) (string, error) {
	if !fsutil.ValidName(name) {
		return "", fmt.Errorf("project: invalid name %q", name)
	}
	return filepath.Join(paths.Projects, name+".yaml"), nil
}

// Load reads and validates <root>/projects/<name>.yaml.
func Load(paths config.Paths, name string) (*Project, error) {
	fp, err := filePath(paths, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("project %q not found (create it via the API)", name)
		}
		return nil, fmt.Errorf("read project %q: %w", name, err)
	}
	var p Project
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse project %q: %w", name, err)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("invalid project %q: %w", name, err)
	}
	return &p, nil
}

// Save validates p and writes it atomically to
// <root>/projects/<name>.yaml.
func Save(paths config.Paths, p *Project) error {
	if p == nil {
		return errors.New("project: nil project")
	}
	if p.Schema == "" {
		p.Schema = SchemaV1
	}
	if err := p.Validate(); err != nil {
		return err
	}
	fp, err := filePath(paths, p.Name)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal project %q: %w", p.Name, err)
	}
	if err := fsutil.WriteFileAtomic(fp, data, 0o644); err != nil {
		return fmt.Errorf("write project %q: %w", p.Name, err)
	}
	return nil
}

// List returns the names of all projects in the store, sorted.
func List(paths config.Paths) ([]string, error) {
	entries, err := os.ReadDir(paths.Projects)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list projects: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// Delete removes <root>/projects/<name>.yaml.
func Delete(paths config.Paths, name string) error {
	fp, err := filePath(paths, name)
	if err != nil {
		return err
	}
	if err := os.Remove(fp); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("project %q not found", name)
		}
		return fmt.Errorf("delete project %q: %w", name, err)
	}
	return nil
}
