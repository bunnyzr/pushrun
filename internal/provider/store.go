package provider

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
)

// fileName is the provider descriptor file inside each provider dir.
const fileName = "provider.yaml"

func dir(paths config.Paths, id string) (string, error) {
	if !fsutil.ValidName(id) {
		return "", fmt.Errorf("provider: invalid id %q", id)
	}
	return filepath.Join(paths.Providers, id), nil
}

// Load reads and validates <root>/providers/<id>/provider.yaml and checks
// that the declared script files exist under the provider dir.
func Load(paths config.Paths, id string) (*Provider, error) {
	d, err := dir(paths, id)
	if err != nil {
		return nil, err
	}
	fp := filepath.Join(d, fileName)
	data, err := os.ReadFile(fp)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("provider %q not found (create it via the API)", id)
		}
		return nil, fmt.Errorf("read provider %q: %w", id, err)
	}
	var p Provider
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse provider %q: %w", id, err)
	}
	if p.ID != id {
		return nil, fmt.Errorf("provider %q: id mismatch, file declares %q", id, p.ID)
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("invalid provider %q: %w", id, err)
	}
	for _, script := range []string{p.Warmup, p.Install} {
		if script == "" {
			continue
		}
		sp := filepath.Join(d, filepath.FromSlash(script))
		if _, err := os.Stat(sp); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("provider %q: script %q missing", id, script)
			}
			return nil, fmt.Errorf("provider %q: stat script %q: %w", id, script, err)
		}
	}
	return &p, nil
}

// Save validates p and writes it atomically to
// <root>/providers/<id>/provider.yaml. Script files are managed separately;
// Save neither creates nor removes them.
func Save(paths config.Paths, p *Provider) error {
	if p == nil {
		return errors.New("provider: nil provider")
	}
	if p.Schema == "" {
		p.Schema = SchemaV1
	}
	if err := p.Validate(); err != nil {
		return err
	}
	d, err := dir(paths, p.ID)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal provider %q: %w", p.ID, err)
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(d, fileName), data, 0o644); err != nil {
		return fmt.Errorf("write provider %q: %w", p.ID, err)
	}
	return nil
}

// List returns the ids of all providers in the store, sorted. A directory
// without a provider.yaml is not a provider and is skipped.
func List(paths config.Paths) ([]string, error) {
	entries, err := os.ReadDir(paths.Providers)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list providers: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(paths.Providers, e.Name(), fileName)); err != nil {
			continue
		}
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

// Delete removes <root>/providers/<id>/ including its script files.
func Delete(paths config.Paths, id string) error {
	d, err := dir(paths, id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(d); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("provider %q not found", id)
		}
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	if err := os.RemoveAll(d); err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}
