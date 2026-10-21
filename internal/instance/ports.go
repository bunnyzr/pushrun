package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
	ports, err := p.AcquireN(project, inst, 1)
	if err != nil {
		return 0, err
	}
	return ports[0], nil
}

// AcquireN leases n ports for (project, inst). Sticky like Acquire: leases
// the instance already holds inside the current range are kept (lowest
// first) and topped up with the lowest free ports. Returns an error — with
// nothing newly leased — when the pool cannot supply n ports.
func (p *PortPool) AcquireN(project, inst string, n int) ([]int, error) {
	if !fsutil.ValidName(project) || !fsutil.ValidName(inst) {
		return nil, fmt.Errorf("instance: invalid project/instance name %q/%q", project, inst)
	}
	if n < 1 {
		return nil, fmt.Errorf("instance: AcquireN wants at least 1 port, got %d", n)
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	leases, err := p.scan()
	if err != nil {
		return nil, err
	}
	// Sticky: existing leases of this instance win, lowest first.
	var held []int
	for port, l := range leases {
		if l.Project == project && l.Instance == inst && port >= p.from && port <= p.to {
			held = append(held, port)
		}
	}
	sort.Ints(held)
	out := append([]int(nil), held...)
	if len(out) > n {
		out = out[:n]
	}
	// Fail before leasing anything when the range cannot supply the rest.
	free := 0
	for port := p.from; port <= p.to; port++ {
		if _, taken := leases[port]; !taken {
			free++
		}
	}
	if len(out)+free < n {
		return nil, fmt.Errorf("port pool exhausted: %d ports wanted, only %d free in %d-%d", n, free, p.from, p.to)
	}
	// Top up with the lowest free ports in range.
	leased := 0
	for port := p.from; port <= p.to && len(out) < n; port++ {
		if _, taken := leases[port]; taken {
			continue
		}
		l := lease{Project: project, Instance: inst, Port: port}
		data, err := json.Marshal(l)
		if err != nil {
			return nil, fmt.Errorf("marshal port lease: %w", err)
		}
		if err := os.MkdirAll(p.dir, 0o755); err != nil {
			return nil, fmt.Errorf("create ports dir: %w", err)
		}
		fp := filepath.Join(p.dir, strconv.Itoa(port))
		if err := claimLease(fp, port, data); err != nil {
			if errors.Is(err, fs.ErrExist) {
				continue // raced with another process; try next port
			}
			return nil, fmt.Errorf("write port lease %d: %w", port, err)
		}
		leases[port] = l
		out = append(out, port)
		leased++
	}
	if len(out) < n {
		// Lost races against other processes: drop the partial leases.
		for _, port := range out[len(out)-leased:] {
			_ = os.Remove(filepath.Join(p.dir, strconv.Itoa(port)))
		}
		return nil, fmt.Errorf("port pool exhausted: all ports in %d-%d are leased", p.from, p.to)
	}
	return out, nil
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

// ReleaseAll drops every lease held by (project, inst). It scans the pool
// rather than trusting any state record, so leases wedged by a shrunken or
// dropped named-port list are released too.
func (p *PortPool) ReleaseAll(project, inst string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	leases, err := p.scan()
	if err != nil {
		return
	}
	for port, l := range leases {
		if l.Project == project && l.Instance == inst {
			_ = os.Remove(filepath.Join(p.dir, strconv.Itoa(port)))
		}
	}
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
