package gitx

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/bunnyzr/pushrun/internal/config"
)

// Repo identity: every git remote a project mounts is identified by
// "host/path" (e.g. "git.example.com/team/proto"). The identity keys the
// bare repo on disk (<root>/repos/<host>--<path>.git), rides in push URLs
// (/git/<host>/<path>.git/...), and matches a push to the projects that
// mount the repo.

// NormalizeRepo reduces a repo reference to its canonical "host/path"
// identity. Accepted forms:
//
//   - a full URL: https://git.example.com/team/proto(.git) — scheme and
//     userinfo are dropped, the host (including port) is kept;
//   - scp-like syntax as git accepts it: [user@]git.example.com:team/proto —
//     userinfo is dropped, the first colon separates host and path;
//   - a host/path shorthand: git.example.com/team/proto;
//   - a local absolute path: /srv/repos/proto — normalized by stripping the
//     leading slash, so the first path segment plays the role of the host.
//
// A trailing ".git" suffix is dropped. Anything with fewer than two
// segments, empty or "." or ".." segments, or backslashes is rejected, so an
// identity can never escape the repos dir or be mistaken for a project name.
func NormalizeRepo(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("gitx: empty repo reference")
	}
	s := repo
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("gitx: invalid repo URL %q: %v", repo, err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("gitx: repo URL %q has no host (use a host/path identity for local repos)", repo)
		}
		s = u.Host + u.Path
	} else if i := strings.Index(s, ":"); i >= 0 && !strings.HasPrefix(s, "/") && !strings.Contains(s[:i], "/") && !isPortTail(s[i+1:]) {
		host := s[:i]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		s = host + "/" + s[i+1:]
	}
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	segs := strings.Split(s, "/")
	if len(segs) < 2 {
		return "", fmt.Errorf("gitx: repo %q is not a host/path identity", repo)
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `\`) {
			return "", fmt.Errorf("gitx: repo %q contains invalid path segment %q", repo, seg)
		}
	}
	return strings.Join(segs, "/"), nil
}

// isPortTail reports whether rest looks like the "<port>/<path>" tail of an
// already-canonical "host:port/path" identity: a run of digits followed by a
// slash or the end. Such identities must be fixed points of NormalizeRepo,
// so a colon followed by a port is never treated as scp-like syntax (scp
// paths starting with a bare numeric segment are indistinguishable and are
// documented as unsupported).
func isPortTail(rest string) bool {
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	return j > 0 && (j == len(rest) || rest[j] == '/')
}

// BareRepoDir returns the on-disk location of the bare repo for a repo
// reference: <root>/repos/<host>--<path>.git (every "/" in the identity
// becomes "--").
func BareRepoDir(paths config.Paths, repo string) (string, error) {
	id, err := NormalizeRepo(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.Repos, strings.ReplaceAll(id, "/", "--")+".git"), nil
}

// RepoPath returns the path part of a repo identity ("team/proto" for
// "git.example.com/team/proto"). The input must already be normalized.
func RepoPath(identity string) string {
	_, p, _ := strings.Cut(identity, "/")
	return p
}

// SourceURL resolves a repo mount param to the remote URL warmup fetches
// from. Full URLs and absolute local paths pass through verbatim; scp-like
// [user@]host:path syntax is rewritten to its ssh:// URL form; a host/path
// shorthand (including host:port/path) is expanded with the configured
// scheme.
func SourceURL(repo, scheme string) string {
	if strings.Contains(repo, "://") || strings.HasPrefix(repo, "/") {
		return repo
	}
	if i := strings.Index(repo, ":"); i >= 0 && !strings.Contains(repo[:i], "/") && !isPortTail(repo[i+1:]) {
		return "ssh://" + repo[:i] + "/" + repo[i+1:]
	}
	return scheme + "://" + repo
}
