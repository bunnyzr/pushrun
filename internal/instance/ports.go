package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/bunnyzr/pushrun/internal/config"
	"github.com/bunnyzr/pushrun/internal/fsutil"
)

// PortPool hands out TCP ports from a configured range. Leases are
// persisted as JSON files under <root>/ports/<port>, so a lease survives
// daemon restarts and re-acquiring for the same (project, instance)
// returns the same port while the lease is held.
type PortPool struct {
	dir  string
	from int
	to   int
	mu   sync.Mutex
}

type lease struct {
	Project  string `json:"project"`
	Instance string `json:"instance"`
	Port     int    `json:"port"`
}

// NewPortPool creates a pool leasing ports in [from, to], persisted under
// paths.Ports.
func NewPortPool(paths config.Paths, from, to int) *PortPool {
	return &PortPool{dir: paths.Ports, from: from, to: to}
}

// Acquire returns the leased port for (project, inst). When the instance
// already holds a lease inside the current range, that same port is
// returned (sticky). Otherwise the lowest free port in the range is
// leased. Returns an error when the pool is exhausted.
func (p *PortPool) Acquire(project, inst string) (int, error) {
	if !fsutil.ValidName(project) || !fsutil.ValidName(inst) {
		return 0, fmt.Errorf("instance: invalid project/instance name %q/%q", project, inst)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	leases, err := p.scan()
	if err != nil {
		return 0, err
	}
	// Sticky: an existing lease for this instance wins.
	for port, l := range leases {
		if l.Project == project && l.Instance == inst && port >= p.from && port <= p.to {
			return port, nil
		}
	}
	// Lowest free port in range.
	for port := p.from; port <= p.to; port++ {
		if _, taken := leases[port]; taken {
			continue
		}
		l := lease{Project: project, Instance: inst, Port: port}
		data, err := json.Marshal(l)
		if err != nil {
			return 0, fmt.Errorf("marshal port lease: %w", err)
		}
		if err := os.MkdirAll(p.dir, 0o755); err != nil {
			return 0, fmt.Errorf("create ports dir: %w", err)
		}
		fp := filepath.Join(p.dir, strconv.Itoa(port))
		if err := claimLease(fp, port, data); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue // raced with another process; try next port
			}
			return 0, fmt.Errorf("write port lease %d: %w", port, err)
		}
		return port, nil
	}
	return 0, fmt.Errorf("port pool exhausted: all ports in %d-%d are leased", p.from, p.to)
}

// claimLease atomically installs data as the lease file at fp: the content
// is written to a temp file first, then hard-linked into place, so a crash
// mid-write can only leave an ignored temp file behind — never a partial
// lease that permanently wedges the port. A pre-existing corrupt lease
// (e.g. left by an older version that crashed mid-write) is reclaimed.
// Returns fs.ErrExist when a valid lease already holds the port.
func claimLease(fp string, port int, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(fp), ".lease-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	err = os.Link(tmpName, fp)
	if err == nil {
		return nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	if validLease(fp, port) {
		return fs.ErrExist
	}
	// Corrupt leftover: reclaim the slot. Losing the race against another
	// claimant simply reports the port as taken.
	if err := os.Remove(fp); err != nil {
		return fs.ErrExist
	}
	if err := os.Link(tmpName, fp); err != nil {
		return fs.ErrExist
	}
	return nil
}

// validLease reports whether fp holds a parseable lease for port.
func validLease(fp string, port int) bool {
	data, err := os.ReadFile(fp)
	if err != nil {
		return false
	}
	var l lease
	return json.Unmarshal(data, &l) == nil && l.Port == port
}

// Release drops the lease on port. Releasing a port with no lease is a
// no-op.
func (p *PortPool) Release(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = os.Remove(filepath.Join(p.dir, strconv.Itoa(port)))
}

// scan reads all lease files, keyed by port. Corrupt or out-of-range
// files are ignored.
func (p *PortPool) scan() (map[int]lease, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[int]lease{}, nil
		}
		return nil, fmt.Errorf("list port leases: %w", err)
	}
	leases := make(map[int]lease, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		port, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read port lease %d: %w", port, err)
		}
		var l lease
		if err := json.Unmarshal(data, &l); err != nil {
			continue
		}
		leases[port] = l
	}
	return leases, nil
}
