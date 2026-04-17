// Package lockfile provides single-instance enforcement for shaktimand.
// It uses flock-based advisory locking on .shaktiman/daemon.pid to ensure
// exactly one leader daemon per project directory.
package lockfile

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrAlreadyLocked is returned when the flock is held by another process.
var ErrAlreadyLocked = errors.New("daemon already running for this project")

// Lock represents an exclusive advisory lock on a daemon PID file.
type Lock struct {
	fl            *flock.Flock
	canonicalRoot string
}

// Acquire canonicalizes projectRoot via filepath.EvalSymlinks, creates
// .shaktiman/daemon.pid, and attempts a non-blocking exclusive flock.
// Returns (lock, nil) on success (caller is leader).
// Returns (nil, ErrAlreadyLocked) when another process holds the lock.
func Acquire(projectRoot string) (*Lock, error) {
	canonical, err := Canonicalize(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("canonicalize project root: %w", err)
	}

	dir := filepath.Join(canonical, ".shaktiman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create .shaktiman dir: %w", err)
	}

	pidPath := filepath.Join(dir, "daemon.pid")
	fl := flock.New(pidPath)

	locked, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("flock %s: %w", pidPath, err)
	}
	if !locked {
		return nil, ErrAlreadyLocked
	}

	// Write PID for human diagnostics (not used for locking).
	_ = os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)

	return &Lock{fl: fl, canonicalRoot: canonical}, nil
}

// Release releases the flock. Safe to call multiple times.
func (l *Lock) Release() error {
	if l == nil || l.fl == nil {
		return nil
	}
	return l.fl.Unlock()
}

// CanonicalRoot returns the resolved project root used for lock path derivation.
func (l *Lock) CanonicalRoot() string {
	return l.canonicalRoot
}

// SocketPath returns the Unix domain socket path for the leader daemon.
// Uses /tmp/shaktiman-<sha256(root)[:16]>.sock to avoid macOS 104-byte limit.
func (l *Lock) SocketPath() string {
	return socketPathFromRoot(l.canonicalRoot)
}

// Listen removes any stale socket and creates a Unix domain socket listener
// at the lock's socket path. The caller must close the returned listener.
// Safe to call only while the lock is held (guarantees any existing socket is stale).
func (l *Lock) Listen() (net.Listener, error) {
	sockPath := l.SocketPath()
	_ = os.Remove(sockPath) // remove stale socket from previous unclean exit
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", sockPath, err)
	}
	return ln, nil
}

// SocketPathFor computes the socket path for a given projectRoot without
// acquiring a lock. Used by the proxy to find the leader's socket.
func SocketPathFor(projectRoot string) (string, error) {
	canonical, err := Canonicalize(projectRoot)
	if err != nil {
		return "", fmt.Errorf("canonicalize project root: %w", err)
	}
	return socketPathFromRoot(canonical), nil
}

func socketPathFromRoot(canonicalRoot string) string {
	h := sha256.Sum256([]byte(canonicalRoot))
	return filepath.Join(os.TempDir(), fmt.Sprintf("shaktiman-%x.sock", h[:8]))
}

// Canonicalize resolves a project root to its absolute, symlink-resolved path.
// Falls back to the absolute path if symlink resolution fails (e.g., dir doesn't exist yet).
func Canonicalize(projectRoot string) (string, error) {
	absPath, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Directory may not exist yet. Fall back to abs path.
		return absPath, nil
	}
	return resolved, nil
}
