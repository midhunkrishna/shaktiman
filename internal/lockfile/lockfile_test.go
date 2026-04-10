package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquire_Leader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	// PID file should exist.
	pidPath := filepath.Join(dir, ".shaktiman", "daemon.pid")
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("pid file missing: %v", err)
	}

	// Release should succeed.
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Double release is safe.
	if err := lock.Release(); err != nil {
		t.Fatalf("double Release: %v", err)
	}
}

func TestAcquire_Contention(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lock1, err := Acquire(dir)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// Second acquire should fail.
	_, err = Acquire(dir)
	if err != ErrAlreadyLocked {
		t.Fatalf("expected ErrAlreadyLocked, got: %v", err)
	}

	// After release, second acquire should succeed.
	lock1.Release()

	lock2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("second Acquire after release: %v", err)
	}
	lock2.Release()
}

func TestAcquire_Canonicalization(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a symlink to the directory.
	symlinkDir := t.TempDir()
	symlinkPath := filepath.Join(symlinkDir, "link")
	if err := os.Symlink(dir, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// Acquire via the real path.
	lock1, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire via real path: %v", err)
	}
	defer lock1.Release()

	// Acquire via symlink should fail (same canonical path).
	_, err = Acquire(symlinkPath)
	if err != ErrAlreadyLocked {
		t.Fatalf("expected ErrAlreadyLocked via symlink, got: %v", err)
	}
}

func TestAcquire_CreatesDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// .shaktiman/ should not exist yet.
	shaktiDir := filepath.Join(dir, ".shaktiman")
	if _, err := os.Stat(shaktiDir); !os.IsNotExist(err) {
		t.Fatalf(".shaktiman should not exist initially")
	}

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	// .shaktiman/ should now exist.
	info, err := os.Stat(shaktiDir)
	if err != nil {
		t.Fatalf(".shaktiman not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf(".shaktiman is not a directory")
	}
}

func TestAcquire_PIDContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	pidPath := filepath.Join(dir, ".shaktiman", "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatalf("read pid file: %v", err)
	}

	expected := fmt.Sprintf("%d\n", os.Getpid())
	if string(data) != expected {
		t.Fatalf("pid content = %q, want %q", string(data), expected)
	}
}

func TestSocketPathFor_Deterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Same root produces same path.
	p1, err := SocketPathFor(dir)
	if err != nil {
		t.Fatalf("SocketPathFor: %v", err)
	}
	p2, err := SocketPathFor(dir)
	if err != nil {
		t.Fatalf("SocketPathFor: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("not deterministic: %q != %q", p1, p2)
	}

	// Different roots produce different paths.
	dir2 := t.TempDir()
	p3, err := SocketPathFor(dir2)
	if err != nil {
		t.Fatalf("SocketPathFor: %v", err)
	}
	if p1 == p3 {
		t.Fatalf("different roots should produce different paths")
	}

	// Path is under macOS 104-byte limit.
	if len(p1) >= 104 {
		t.Fatalf("socket path too long (%d bytes): %s", len(p1), p1)
	}

	// Symlink produces same path as real dir.
	symlinkDir := t.TempDir()
	symlinkPath := filepath.Join(symlinkDir, "link")
	if err := os.Symlink(dir, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	p4, err := SocketPathFor(symlinkPath)
	if err != nil {
		t.Fatalf("SocketPathFor via symlink: %v", err)
	}
	if p1 != p4 {
		t.Fatalf("symlink should produce same path: %q != %q", p1, p4)
	}

	// Lock's SocketPath matches SocketPathFor.
	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	if lock.SocketPath() != p1 {
		t.Fatalf("Lock.SocketPath() = %q, want %q", lock.SocketPath(), p1)
	}
}

func TestAcquire_NilRelease(t *testing.T) {
	t.Parallel()
	// Release on nil lock should not panic.
	var l *Lock
	if err := l.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
}

func TestAcquire_InvalidDir(t *testing.T) {
	t.Parallel()
	// Attempt to acquire on a path where .shaktiman cannot be created.
	lock, err := Acquire("/dev/null/impossible")
	if err == nil {
		lock.Release()
		t.Fatal("expected error for invalid dir")
	}
}

func TestSocketPathFor_NonexistentDir(t *testing.T) {
	t.Parallel()
	// SocketPathFor should still work for non-existent dirs (falls back to abs path).
	p, err := SocketPathFor("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("SocketPathFor: %v", err)
	}
	if p == "" {
		t.Fatal("empty socket path")
	}
}

func TestAcquire_MkdirFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create .shaktiman as a file so MkdirAll fails.
	shaktiPath := filepath.Join(dir, ".shaktiman")
	if err := os.WriteFile(shaktiPath, []byte("block"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := Acquire(dir)
	if err == nil {
		t.Fatal("expected error when .shaktiman is a file")
	}
}

func TestCanonicalRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	lock, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lock.Release()

	// CanonicalRoot should be an absolute path.
	root := lock.CanonicalRoot()
	if !filepath.IsAbs(root) {
		t.Fatalf("CanonicalRoot not absolute: %s", root)
	}

	// On macOS, TempDir might use /var which symlinks to /private/var.
	// Verify that canonicalization resolves this.
	resolved, _ := filepath.EvalSymlinks(dir)
	if root != resolved {
		t.Fatalf("CanonicalRoot = %q, want %q", root, resolved)
	}
}
