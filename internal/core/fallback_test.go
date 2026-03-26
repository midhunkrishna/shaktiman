package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemFallback_CancelledContext(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create several .go files.
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)),
			[]byte(fmt.Sprintf("package main\nfunc f%d() {}", i)), 0644)
	}

	// Cancel context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pkg, err := FilesystemFallback(ctx, dir, "query", 4096)
	// Should not error -- just return early with partial or empty results.
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	// With cancelled context, should have fewer results than total files.
	if pkg != nil && len(pkg.Chunks) >= 10 {
		t.Errorf("expected fewer chunks with cancelled context, got %d", len(pkg.Chunks))
	}
}

func TestFilesystemFallback_EmptyDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create only a non-source file.
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hello"), 0644)

	pkg, err := FilesystemFallback(context.Background(), dir, "query", 4096)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg != nil && len(pkg.Chunks) != 0 {
		t.Errorf("expected 0 chunks for directory with no source files, got %d", len(pkg.Chunks))
	}
}

func TestFilesystemFallback_BudgetBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create files with known sizes. Each char ~ 0.25 tokens, so 100 chars = 25 tokens.
	os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte(fmt.Sprintf("package a\n%s", string(make([]byte, 96)))), 0644) // ~106 chars = 26 tokens
	os.WriteFile(filepath.Join(dir, "b.go"),
		[]byte(fmt.Sprintf("package b\n%s", string(make([]byte, 96)))), 0644) // ~106 chars = 26 tokens

	// Budget of 30 tokens: should fit only 1 file (26 tokens), second truncated or skipped.
	pkg, err := FilesystemFallback(context.Background(), dir, "query", 30)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if pkg.TotalTokens > 30 {
		t.Errorf("total tokens %d exceeds budget 30", pkg.TotalTokens)
	}
	if pkg.Strategy != "filesystem_l3" {
		t.Errorf("expected strategy filesystem_l3, got %q", pkg.Strategy)
	}
}

func TestFilesystemFallback_TruncatesLargeFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a large file (1000 chars = 250 tokens).
	content := make([]byte, 1000)
	for i := range content {
		content[i] = 'x'
	}
	os.WriteFile(filepath.Join(dir, "big.go"), content, 0644)

	// Budget of 50 tokens: file should be truncated to fit.
	pkg, err := FilesystemFallback(context.Background(), dir, "query", 50)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if pkg.TotalTokens > 50 {
		t.Errorf("total tokens %d exceeds budget 50", pkg.TotalTokens)
	}
	if len(pkg.Chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(pkg.Chunks))
	}
	if len(pkg.Chunks) > 0 && len(pkg.Chunks[0].Content) >= 1000 {
		t.Error("expected content to be truncated")
	}
}

func TestFilesystemFallback_SkipsVendorAndGit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create files in skipped directories.
	vendorDir := filepath.Join(dir, "vendor")
	os.Mkdir(vendorDir, 0755)
	os.WriteFile(filepath.Join(vendorDir, "lib.go"),
		[]byte("package vendor"), 0644)

	gitDir := filepath.Join(dir, ".git")
	os.Mkdir(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "config.go"),
		[]byte("package git"), 0644)

	// Create a regular source file.
	os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() {}"), 0644)

	pkg, err := FilesystemFallback(context.Background(), dir, "query", 4096)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}

	// Should only find main.go, not vendor/ or .git/ files.
	for _, c := range pkg.Chunks {
		if c.Path == "vendor/lib.go" || c.Path == ".git/config.go" {
			t.Errorf("should not include file from excluded directory: %s", c.Path)
		}
	}
	if len(pkg.Chunks) != 1 {
		t.Errorf("expected 1 chunk (main.go only), got %d", len(pkg.Chunks))
	}
}

func TestFilesystemFallback_Symlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a real file and a symlink pointing to it.
	os.WriteFile(filepath.Join(dir, "real.go"),
		[]byte("package main\nfunc Real() {}"), 0644)

	// Create symlink to a file within the project (should be included).
	os.Symlink(filepath.Join(dir, "real.go"), filepath.Join(dir, "link.go"))

	pkg, err := FilesystemFallback(context.Background(), dir, "query", 4096)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}

	// Both real.go and link.go should be included (link resolves within project root).
	if len(pkg.Chunks) < 1 {
		t.Error("expected at least 1 chunk")
	}
}

func TestFilesystemFallback_InvalidRoot(t *testing.T) {
	t.Parallel()

	_, err := FilesystemFallback(context.Background(), "/nonexistent/path/that/does/not/exist", "query", 4096)
	if err == nil {
		t.Error("expected error for nonexistent project root")
	}
}

func TestFilesystemFallback_MultipleExtensions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create files with various supported extensions.
	os.WriteFile(filepath.Join(dir, "app.ts"), []byte("const x = 1;"), 0644)
	os.WriteFile(filepath.Join(dir, "app.py"), []byte("x = 1"), 0644)
	os.WriteFile(filepath.Join(dir, "app.rs"), []byte("fn main() {}"), 0644)
	os.WriteFile(filepath.Join(dir, "app.java"), []byte("class App {}"), 0644)
	os.WriteFile(filepath.Join(dir, "app.txt"), []byte("not code"), 0644) // should be skipped

	pkg, err := FilesystemFallback(context.Background(), dir, "query", 4096)
	if err != nil {
		t.Fatalf("FilesystemFallback: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}

	// Should have 4 chunks (ts, py, rs, java) but not txt.
	if len(pkg.Chunks) != 4 {
		t.Errorf("expected 4 chunks for supported extensions, got %d", len(pkg.Chunks))
	}
}
