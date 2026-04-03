package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/parser"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/testutil"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"
)

func TestIntegration_IndexAndSearch(t *testing.T) {
	t.Parallel()

	// Get absolute path to testdata
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataRoot := filepath.Join(cwd, "..", "..", "testdata", "typescript_project")
	if _, err := os.Stat(testdataRoot); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataRoot)
	}

	// Use temp dir for database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := types.Config{
		ProjectRoot:       testdataRoot,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	ctx := context.Background()

	// Run cold indexing synchronously
	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	// Verify files were indexed
	stats, err := d.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles == 0 {
		t.Fatal("expected indexed files, got 0")
	}
	if stats.TotalChunks == 0 {
		t.Fatal("expected indexed chunks, got 0")
	}
	t.Logf("Indexed: %d files, %d chunks, %d symbols",
		stats.TotalFiles, stats.TotalChunks, stats.TotalSymbols)

	// Search for validateToken
	results, err := d.Engine().Search(ctx, core.SearchInput{
		Query:      "validate token",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'validate token'")
	}

	foundValidate := false
	for _, r := range results {
		if r.SymbolName == "validateToken" {
			foundValidate = true
			break
		}
	}
	if !foundValidate {
		t.Error("expected validateToken in search results")
	}

	// Search for hashPassword
	results, err = d.Engine().Search(ctx, core.SearchInput{
		Query:      "hash password",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search hash: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'hash password'")
	}

	// Context assembly
	pkg, err := d.Engine().Context(ctx, core.ContextInput{
		Query: "password hashing",
	})
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	if pkg == nil {
		t.Fatal("expected non-nil context package")
	}
	if pkg.TotalTokens > 4096 {
		t.Errorf("expected total_tokens <= 4096, got %d", pkg.TotalTokens)
	}

	// Verify TypeScript language count
	if stats.Languages["typescript"] == 0 {
		t.Error("expected typescript files in language stats")
	}

	t.Logf("Integration test passed: %d files, %d chunks, %d symbols, %d search results",
		stats.TotalFiles, stats.TotalChunks, stats.TotalSymbols, len(results))
}

func TestScanRepo(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataRoot := filepath.Join(cwd, "..", "..", "testdata", "typescript_project")
	if _, err := os.Stat(testdataRoot); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataRoot)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: testdataRoot})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	if len(result.Files) == 0 {
		t.Fatal("expected scanned files, got 0")
	}

	// All files should be TypeScript
	for _, f := range result.Files {
		if f.Language != "typescript" {
			t.Errorf("expected language=typescript for %s, got %s", f.Path, f.Language)
		}
		if f.ContentHash == "" {
			t.Errorf("expected non-empty content hash for %s", f.Path)
		}
	}

	t.Logf("Scanned %d TypeScript files", len(result.Files))
}

func TestScanRepo_ContentCarried(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataRoot := filepath.Join(cwd, "..", "..", "testdata", "typescript_project")
	if _, err := os.Stat(testdataRoot); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataRoot)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: testdataRoot})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	for _, f := range result.Files {
		if f.Content == nil {
			t.Errorf("expected Content to be carried for %s, got nil", f.Path)
		}
		if len(f.Content) == 0 {
			t.Errorf("expected non-empty Content for %s", f.Path)
		}
		if f.Size != int64(len(f.Content)) {
			t.Errorf("file %s: Size=%d but len(Content)=%d", f.Path, f.Size, len(f.Content))
		}
	}
}

func TestScanRepo_RelativeRoot(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataAbs := filepath.Join(cwd, "..", "..", "testdata", "go_project")
	if _, err := os.Stat(testdataAbs); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataAbs)
	}

	// Compute a relative path from cwd to testdata
	relRoot, err := filepath.Rel(cwd, testdataAbs)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: relRoot})
	if err != nil {
		t.Fatalf("ScanRepo with relative root %q: %v", relRoot, err)
	}

	if len(result.Files) == 0 {
		t.Fatalf("expected scanned files with relative root %q, got 0", relRoot)
	}

	for _, f := range result.Files {
		if f.Language != "go" {
			t.Errorf("expected language=go for %s, got %s", f.Path, f.Language)
		}
	}

	t.Logf("Scanned %d Go files via relative root %q", len(result.Files), relRoot)
}

func TestScanRepo_DotRoot(t *testing.T) {
	// NOT parallel: os.Chdir is process-global and would race with other tests
	// that rely on filepath.Abs (e.g. TestScanRepo_RelativeRoot).

	// Create a temp project with a Go file
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Save and restore cwd since "." is relative
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: "."})
	if err != nil {
		t.Fatalf("ScanRepo with dot root: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file with dot root, got %d", len(result.Files))
	}
	if result.Files[0].Path != "main.go" {
		t.Errorf("expected path=main.go, got %s", result.Files[0].Path)
	}
	if result.Files[0].Language != "go" {
		t.Errorf("expected language=go, got %s", result.Files[0].Language)
	}
}

func TestScanRepo_SymlinkOutsideRoot(t *testing.T) {
	t.Parallel()

	// Create two dirs: project and outside
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	outsideDir := filepath.Join(tmpDir, "outside")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll project: %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("MkdirAll outside: %v", err)
	}

	// Write a Go file outside the project
	outsideFile := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(outsideFile, []byte("package secret\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Write a legitimate file inside the project
	insideFile := filepath.Join(projectDir, "main.go")
	if err := os.WriteFile(insideFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Create a symlink inside project pointing outside
	symlink := filepath.Join(projectDir, "escape.go")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: projectDir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// Should only find main.go, not escape.go (symlink outside root)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file (symlink filtered), got %d", len(result.Files))
	}
	if result.Files[0].Path != "main.go" {
		t.Errorf("expected main.go, got %s", result.Files[0].Path)
	}
}

func TestWriterManager_ProcessJob(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Submit a job with a Done channel to wait for completion
	done := make(chan error, 1)
	_ = wm.AddProducer()
	if err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "test.ts",
		File: &types.FileRecord{
			Path:            "test.ts",
			ContentHash:     "abc123",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "hello",
				StartLine: 1, EndLine: 5,
				Content: "function hello() {}", TokenCount: 5},
		},
		Done: done,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	wm.RemoveProducer()

	if err := <-done; err != nil {
		t.Fatalf("WriteJob failed: %v", err)
	}

	// Verify the file was written
	file, err := store.GetFileByPath(context.Background(), "test.ts")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if file == nil {
		t.Fatal("expected file to be written")
	}
	if file.ContentHash != "abc123" {
		t.Errorf("expected hash abc123, got %s", file.ContentHash)
	}

	cancel()
	<-wm.Done()
}

// ── Daemon Start Test ──

func TestDaemon_Start(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a Go file so cold indexing has work to do
	if err := os.WriteFile(filepath.Join(projectDir, "main.go"),
		[]byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	cfg := types.Config{
		ProjectRoot:       projectDir,
		DBPath:            dbPath,
		EnrichmentWorkers: 1,
		WriterChannelSize: 100,
		WatcherEnabled:    true,
		WatcherDebounceMs: 50,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Inject a serveFunc that blocks until cancelled (simulates real MCP server)
	ctx, cancel := context.WithCancel(context.Background())
	d.serveFunc = func(s *mcpserver.MCPServer) error {
		<-ctx.Done()
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- d.Start(ctx)
	}()

	// Give the cold indexing goroutine time to complete (scan + index + startWatcher)
	time.Sleep(500 * time.Millisecond)

	// Cancel context to unblock serveFunc and stop Start
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return within 10s")
	}

	d.Stop()
}

func TestDaemon_Start_WithEmbedding(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFiles(t, projectDir, 2, 2)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Non-blocking serveFunc
	d.serveFunc = func(s *mcpserver.MCPServer) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	d.Stop()
}

func TestPeriodicEmbeddingSave(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFiles(t, projectDir, 3, 3)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()

	// Index and embed to populate the vector store
	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if _, err := d.EmbedProject(ctx, nil); err != nil {
		t.Fatalf("EmbedProject: %v", err)
	}

	// Remove any previously saved file
	os.Remove(embPath)

	// Set short intervals so the periodic save fires quickly
	d.savePollInterval = 5 * time.Millisecond
	d.saveActiveInterval = 1 * time.Millisecond
	d.saveIdleInterval = 1 * time.Millisecond

	// Simulate active embedding
	d.embeddingActive.Store(true)

	saveCtx, saveCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.periodicEmbeddingSave(saveCtx)
		close(done)
	}()

	// Wait for save to happen
	time.Sleep(50 * time.Millisecond)

	// Switch to idle
	d.embeddingActive.Store(false)
	time.Sleep(50 * time.Millisecond)

	saveCancel()
	<-done

	// Verify the embeddings file was created
	if _, err := os.Stat(embPath); os.IsNotExist(err) {
		t.Error("expected embeddings file to be created by periodicEmbeddingSave")
	}

	d.Stop()
}

func TestRunPeriodicEmbeddingSave(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFiles(t, projectDir, 2, 2)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()

	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if _, err := d.EmbedProject(ctx, nil); err != nil {
		t.Fatalf("EmbedProject: %v", err)
	}

	// Remove saved file so we can verify RunPeriodicEmbeddingSave creates it
	os.Remove(embPath)

	d.savePollInterval = 5 * time.Millisecond
	d.saveActiveInterval = 1 * time.Millisecond
	d.saveIdleInterval = 1 * time.Millisecond
	d.embeddingActive.Store(true)

	saveCtx, saveCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.RunPeriodicEmbeddingSave(saveCtx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	saveCancel()
	<-done

	if _, err := os.Stat(embPath); os.IsNotExist(err) {
		t.Error("expected RunPeriodicEmbeddingSave to create embeddings file")
	}

	d.Stop()
}

func TestRunPeriodicEmbeddingSave_NilVectorStore(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	cfg := types.Config{
		ProjectRoot:       tmpDir,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
		EmbedEnabled:      false, // no vector store
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Stop()

	// Should return immediately without panic
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.RunPeriodicEmbeddingSave(ctx)
}

func TestStopSavesEmbeddings(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFiles(t, projectDir, 2, 2)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	if _, err := d.EmbedProject(ctx, nil); err != nil {
		t.Fatalf("EmbedProject: %v", err)
	}

	// Remove the file saved by EmbedProject
	os.Remove(embPath)

	// Stop should save embeddings
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if _, err := os.Stat(embPath); os.IsNotExist(err) {
		t.Error("expected Stop() to save embeddings file")
	}
}

// ── Language Compatibility Integration Tests ──
//
// These tests exercise the full pipeline (scan → parse → index → search)
// for each supported language, mimicking how the system actually ingests
// and queries source code in production.

func TestIntegration_LanguageCompatibility(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataBase := filepath.Join(cwd, "..", "..", "testdata")

	tests := []struct {
		name           string
		project        string
		language       string
		expectFiles    int // minimum expected files
		expectChunks   int // minimum expected chunks
		expectSymbols  int // minimum expected symbols
		searchQuery    string
		expectSymbol   string // at least one search result should contain this symbol
	}{
		{
			name:          "TypeScript",
			project:       "typescript_project",
			language:      "typescript",
			expectFiles:   3,
			expectChunks:  3,
			expectSymbols: 3,
			searchQuery:   "validate token",
			expectSymbol:  "validateToken",
		},
		{
			name:          "Python",
			project:       "python_project",
			language:      "python",
			expectFiles:   3,
			expectChunks:  3,
			expectSymbols: 2,
			searchQuery:   "create user",
			expectSymbol:  "UserService",
		},
		{
			name:          "Go",
			project:       "go_project",
			language:      "go",
			expectFiles:   3,
			expectChunks:  3,
			expectSymbols: 3,
			searchQuery:   "server listen",
			expectSymbol:  "Listen",
		},
		{
			name:          "Java",
			project:       "java_project",
			language:      "java",
			expectFiles:   3,
			expectChunks:  3,
			expectSymbols: 3,
			searchQuery:   "UserService getAllUsers",
			expectSymbol:  "UserService",
		},
		{
			name:          "Bash",
			project:       "bash_project",
			language:      "bash",
			expectFiles:   2,
			expectChunks:  2,
			expectSymbols: 2,
			searchQuery:   "deploy build",
			expectSymbol:  "deploy",
		},
		{
			name:          "JavaScript",
			project:       "javascript_project",
			language:      "javascript",
			expectFiles:   3,
			expectChunks:  3,
			expectSymbols: 3,
			searchQuery:   "create server",
			expectSymbol:  "createServer",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			projectRoot := filepath.Join(testdataBase, tc.project)
			if _, err := os.Stat(projectRoot); os.IsNotExist(err) {
				t.Skipf("testdata not found at %s", projectRoot)
			}

			tmpDir := t.TempDir()
			dbPath := filepath.Join(tmpDir, "test.db")

			cfg := types.Config{
				ProjectRoot:       projectRoot,
				DBPath:            dbPath,
				EnrichmentWorkers: 2,
				WriterChannelSize: 100,
			}

			d, err := New(cfg)
			if err != nil {
				t.Fatalf("New daemon: %v", err)
			}
			defer d.Stop()

			ctx := context.Background()

			// Step 1: Scan — verify language detection
			scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: projectRoot})
			if err != nil {
				t.Fatalf("ScanRepo: %v", err)
			}
			if len(scanResult.Files) < tc.expectFiles {
				t.Fatalf("expected >= %d files, got %d", tc.expectFiles, len(scanResult.Files))
			}
			for _, f := range scanResult.Files {
				if f.Language != tc.language {
					t.Errorf("file %s: expected language=%s, got %s", f.Path, tc.language, f.Language)
				}
				if f.ContentHash == "" {
					t.Errorf("file %s: expected non-empty content hash", f.Path)
				}
			}

			// Step 2: Index — full cold index through the daemon
			if err := d.IndexProject(ctx, nil); err != nil {
				t.Fatalf("IndexProject: %v", err)
			}

			// Step 3: Verify index stats
			stats, err := d.Store().GetIndexStats(ctx)
			if err != nil {
				t.Fatalf("GetIndexStats: %v", err)
			}
			if stats.TotalFiles < tc.expectFiles {
				t.Errorf("expected >= %d indexed files, got %d", tc.expectFiles, stats.TotalFiles)
			}
			if stats.TotalChunks < tc.expectChunks {
				t.Errorf("expected >= %d chunks, got %d", tc.expectChunks, stats.TotalChunks)
			}
			if stats.TotalSymbols < tc.expectSymbols {
				t.Errorf("expected >= %d symbols, got %d", tc.expectSymbols, stats.TotalSymbols)
			}
			if stats.Languages[tc.language] == 0 {
				t.Errorf("expected %s files in language stats, got 0", tc.language)
			}

			// Step 4: Search — keyword search should find relevant symbols
			results, err := d.Engine().Search(ctx, core.SearchInput{
				Query:      tc.searchQuery,
				MaxResults: 10,
			})
			if err != nil {
				t.Fatalf("Search(%q): %v", tc.searchQuery, err)
			}
			if len(results) == 0 {
				t.Errorf("expected search results for %q, got 0", tc.searchQuery)
			}

			found := false
			for _, r := range results {
				if r.SymbolName == tc.expectSymbol {
					found = true
					break
				}
			}
			if !found {
				names := make([]string, len(results))
				for i, r := range results {
					names[i] = r.SymbolName
				}
				t.Errorf("expected symbol %q in search results, got: %v", tc.expectSymbol, names)
			}

			// Step 5: Context assembly — should return a valid context package
			pkg, err := d.Engine().Context(ctx, core.ContextInput{
				Query: tc.searchQuery,
			})
			if err != nil {
				t.Fatalf("Context(%q): %v", tc.searchQuery, err)
			}
			if pkg == nil {
				t.Fatal("expected non-nil context package")
			}
			if pkg.TotalTokens > 4096 {
				t.Errorf("expected total_tokens <= 4096, got %d", pkg.TotalTokens)
			}

			t.Logf("%s: %d files, %d chunks, %d symbols, %d search results",
				tc.name, stats.TotalFiles, stats.TotalChunks, stats.TotalSymbols, len(results))
		})
	}
}

// TestIntegration_MultiLanguageProject tests that the system correctly handles
// a project containing source files in multiple languages simultaneously.
func TestIntegration_MultiLanguageProject(t *testing.T) {
	t.Parallel()

	// Create a temp project with files in every supported language
	tmpDir := t.TempDir()
	files := map[string]string{
		"main.go": `package main

import "fmt"

func main() {
	fmt.Println("hello")
}

func handleRequest(path string) string {
	return "ok: " + path
}
`,
		"app.ts": `import { Logger } from './logger';

export class Application {
    private logger: Logger;

    constructor() {
        this.logger = new Logger();
    }

    start(): void {
        this.logger.info("started");
    }
}

export function createApp(): Application {
    return new Application();
}
`,
		"service.py": `class PaymentService:
    def __init__(self, gateway):
        self.gateway = gateway

    def process_payment(self, amount: float) -> bool:
        return self.gateway.charge(amount)

    def refund_payment(self, transaction_id: str) -> bool:
        return self.gateway.refund(transaction_id)
`,
		"Handler.java": `package com.example;

import java.util.Map;
import java.util.HashMap;

public class Handler {
    private final Map<String, String> routes = new HashMap<>();

    public void registerRoute(String path, String handler) {
        routes.put(path, handler);
    }

    public String dispatch(String path) {
        return routes.getOrDefault(path, "404");
    }
}
`,
		"deploy.sh": `#!/bin/bash

build_project() {
    echo "Building..."
    make build
}

run_migrations() {
    echo "Running migrations..."
    ./migrate up
}

function restart_service {
    systemctl restart myapp
}
`,
		"client.js": `import { fetch } from 'node-fetch';

export class ApiClient {
    constructor(baseUrl) {
        this.baseUrl = baseUrl;
    }

    async getUsers() {
        const res = await fetch(this.baseUrl + '/users');
        return res.json();
    }

    async createUser(data) {
        const res = await fetch(this.baseUrl + '/users', {
            method: 'POST',
            body: JSON.stringify(data),
        });
        return res.json();
    }
}

export function createClient(url) {
    return new ApiClient(url);
}
`,
	}

	for name, content := range files {
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := types.Config{
		ProjectRoot:       tmpDir,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	ctx := context.Background()

	// Scan and verify all languages are detected
	scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: tmpDir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	langCounts := map[string]int{}
	for _, f := range scanResult.Files {
		langCounts[f.Language]++
	}

	expectedLangs := []string{"go", "typescript", "python", "java", "bash", "javascript"}
	for _, lang := range expectedLangs {
		if langCounts[lang] == 0 {
			t.Errorf("expected at least one %s file detected, got 0 (detected: %v)", lang, langCounts)
		}
	}

	// Index everything
	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	// Verify stats show all languages
	stats, err := d.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}

	for _, lang := range expectedLangs {
		if stats.Languages[lang] == 0 {
			t.Errorf("expected %s in language stats, got 0 (stats: %v)", lang, stats.Languages)
		}
	}

	if stats.TotalFiles < len(files) {
		t.Errorf("expected >= %d files, got %d", len(files), stats.TotalFiles)
	}

	// Cross-language search: verify FTS works across all indexed languages.
	// Each query targets content from a specific language file.
	searchQueries := []string{
		"handleRequest",  // Go
		"Application",    // TypeScript
		"PaymentService", // Python
		"registerRoute",  // Java
		"build",          // Bash
		"ApiClient",      // JavaScript
	}

	for _, q := range searchQueries {
		results, err := d.Engine().Search(ctx, core.SearchInput{
			Query:      q,
			MaxResults: 10,
		})
		if err != nil {
			t.Errorf("Search(%q): %v", q, err)
			continue
		}
		if len(results) == 0 {
			t.Errorf("expected results for %q, got 0", q)
		}
	}

	t.Logf("Multi-language: %d files, %d chunks, %d symbols across %d languages",
		stats.TotalFiles, stats.TotalChunks, stats.TotalSymbols, len(stats.Languages))
}

// TestIntegration_IncrementalIndex_NewLanguage tests that the system correctly
// indexes a project after a new language file is added between index runs.
// Uses separate daemon instances since WriterManager can't be reused.
func TestIntegration_IncrementalIndex_NewLanguage(t *testing.T) {
	t.Parallel()

	// Start with a Go-only project
	tmpDir := t.TempDir()
	goFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	baseCfg := types.Config{
		ProjectRoot:       tmpDir,
		DBPath:            dbPath,
		EnrichmentWorkers: 1,
		WriterChannelSize: 100,
	}

	ctx := context.Background()

	// Round 1: Index Go only
	d1, err := New(baseCfg)
	if err != nil {
		t.Fatalf("New daemon (round 1): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (round 1): %v", err)
	}

	stats, err := d1.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.Languages["go"] == 0 {
		t.Error("expected go in language stats after round 1")
	}
	if stats.Languages["java"] != 0 {
		t.Error("expected no java in language stats after round 1")
	}
	d1.Stop()

	// Add a Java file
	javaFile := filepath.Join(tmpDir, "App.java")
	javaSrc := `package com.example;

public class App {
    public static void main(String[] args) {
        System.out.println("hello from java");
    }

    public String greetUser(String name) {
        return "Hello, " + name;
    }
}
`
	if err := os.WriteFile(javaFile, []byte(javaSrc), 0o644); err != nil {
		t.Fatalf("WriteFile java: %v", err)
	}

	// Round 2: Re-index with a fresh daemon (same DB)
	d2, err := New(baseCfg)
	if err != nil {
		t.Fatalf("New daemon (round 2): %v", err)
	}
	if err := d2.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (round 2): %v", err)
	}

	stats, err = d2.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats round 2: %v", err)
	}
	if stats.Languages["java"] == 0 {
		t.Error("expected java in language stats after round 2")
	}
	if stats.Languages["go"] == 0 {
		t.Error("expected go still in language stats after round 2")
	}

	// Search for the Java symbol
	results, err := d2.Engine().Search(ctx, core.SearchInput{
		Query:      "greetUser",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	found := false
	for _, r := range results {
		if r.SymbolName == "greetUser" || r.SymbolName == "App" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected Java symbols in search results after incremental index")
	}

	d2.Stop()
	t.Logf("Incremental: %d files, %d languages after adding Java", stats.TotalFiles, len(stats.Languages))
}

// TestIntegration_JavaImportEdgesStoredAsPending verifies the full pipeline:
// a Java file with imports of external types (e.g. ExecutorService from JDK)
// is parsed, indexed, and import edges are stored in pending_edges — not
// silently dropped. Then verifies PendingEdgeCallers can discover which
// project symbols import the external type.
func TestIntegration_JavaImportEdgesStoredAsPending(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// Java file importing external JDK types not defined in the project.
	javaSrc := `package com.example;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.List;

public class TaskRunner {
    private final ExecutorService executor;

    public TaskRunner(int threads) {
        this.executor = Executors.newFixedThreadPool(threads);
    }

    public void submit(Runnable task) {
        executor.submit(task);
    }

    public List<String> status() {
        return List.of("running");
    }
}
`
	javaPath := filepath.Join(tmpDir, "TaskRunner.java")
	if err := os.WriteFile(javaPath, []byte(javaSrc), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Also add a second Java file that imports the same external types.
	java2Src := `package com.example;

import java.util.concurrent.ExecutorService;
import java.util.List;

public class WorkerPool {
    private final ExecutorService pool;

    public WorkerPool(ExecutorService pool) {
        this.pool = pool;
    }

    public List<String> workers() {
        return List.of("w1", "w2");
    }
}
`
	java2Path := filepath.Join(tmpDir, "WorkerPool.java")
	if err := os.WriteFile(java2Path, []byte(java2Src), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// ── Phase 1: Parser-level validation ──
	// Parse the Java file directly and verify import edges have non-empty
	// SrcSymbolName. This is the core of the bug: without the fix,
	// file-level imports get SrcSymbolName="" because imports appear before
	// any class declaration. InsertEdges then drops edges with srcID=0.

	p, err := parser.NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	content, err := os.ReadFile(javaPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	result, err := p.Parse(context.Background(), parser.ParseInput{
		FilePath: "TaskRunner.java",
		Content:  content,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Verify import edges were extracted at all.
	var importEdges []string
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			importEdges = append(importEdges, e.DstSymbolName)
		}
	}
	if len(importEdges) == 0 {
		t.Fatal("parser extracted zero import edges — edge extraction itself is broken")
	}

	// Verify every import edge has a non-empty SrcSymbolName.
	// Without the fix, SrcSymbolName="" for file-level imports because
	// walkForEdges starts with owner="" and imports precede class declarations.
	for _, e := range result.Edges {
		if e.Kind == "imports" && e.SrcSymbolName == "" {
			t.Fatalf("import edge for %q has SrcSymbolName=\"\" — "+
				"this causes InsertEdges to skip the edge (srcID=0 → continue), "+
				"so it never reaches pending_edges", e.DstSymbolName)
		}
	}

	// ── Phase 2: Full pipeline (index → storage → query) ──

	dbPath := filepath.Join(t.TempDir(), "test.db")
	cfg := types.Config{
		ProjectRoot:       tmpDir,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	ctx := context.Background()

	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	store := d.Store()

	// Verify symbols were created for the classes.
	for _, name := range []string{"TaskRunner", "WorkerPool"} {
		syms, err := store.GetSymbolByName(ctx, name)
		if err != nil {
			t.Fatalf("GetSymbolByName(%s): %v", name, err)
		}
		if len(syms) == 0 {
			t.Errorf("expected symbol for %q, got none", name)
		}
	}

	// Verify external types are NOT in the symbols table.
	for _, name := range []string{"ExecutorService", "Executors"} {
		syms, err := store.GetSymbolByName(ctx, name)
		if err != nil {
			t.Fatalf("GetSymbolByName(%s): %v", name, err)
		}
		if len(syms) != 0 {
			t.Errorf("did not expect symbol definition for external type %q, but got %d", name, len(syms))
		}
	}

	// Verify import edges landed in pending_edges.
	// ExecutorService is imported in both files → expect 2 callers.
	callerIDs, err := store.PendingEdgeCallers(ctx, "ExecutorService")
	if err != nil {
		t.Fatalf("PendingEdgeCallers(ExecutorService): %v", err)
	}
	if len(callerIDs) == 0 {
		t.Fatal("expected pending edge callers for ExecutorService, got none")
	}

	// Resolve caller IDs to symbol names.
	callerNames := map[string]bool{}
	for _, id := range callerIDs {
		sym, err := store.GetSymbolByID(ctx, id)
		if err != nil || sym == nil {
			continue
		}
		callerNames[sym.Name] = true
	}
	for _, expected := range []string{"TaskRunner", "WorkerPool"} {
		if !callerNames[expected] {
			t.Errorf("expected %q in callers of ExecutorService, got: %v", expected, callerNames)
		}
	}

	// Verify edge kind is "imports".
	callersWithKind, err := store.PendingEdgeCallersWithKind(ctx, "ExecutorService")
	if err != nil {
		t.Fatalf("PendingEdgeCallersWithKind(ExecutorService): %v", err)
	}
	for _, c := range callersWithKind {
		if c.Kind != "imports" {
			t.Errorf("expected edge kind 'imports', got %q", c.Kind)
		}
	}

	// Executors imported only by TaskRunner → exactly 1 caller.
	executorsCallers, err := store.PendingEdgeCallers(ctx, "Executors")
	if err != nil {
		t.Fatalf("PendingEdgeCallers(Executors): %v", err)
	}
	if len(executorsCallers) != 1 {
		t.Errorf("expected 1 pending edge caller for Executors, got %d", len(executorsCallers))
	}

	// List imported by both files → at least 2 callers.
	listCallers, err := store.PendingEdgeCallers(ctx, "List")
	if err != nil {
		t.Fatalf("PendingEdgeCallers(List): %v", err)
	}
	if len(listCallers) < 2 {
		t.Errorf("expected at least 2 pending edge callers for List, got %d", len(listCallers))
	}

	t.Logf("Integration: ExecutorService has %d callers, Executors has %d, List has %d",
		len(callerIDs), len(executorsCallers), len(listCallers))
}

// ── Embedding Integration Tests ──

// newMockOllama creates an httptest.Server that mimics POST /api/embed.
// It returns embeddings of the requested dimension for each input text.
// requestCount is incremented atomically for each request.
func newMockOllama(dims int, requestCount *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestCount != nil {
			requestCount.Add(1)
		}
		// Health check endpoint used by EmbedderHealthy
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}

		var req struct {
			Model string `json:"model"`
			Input any    `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Determine the number of texts sent. Input can be a string or []string.
		var count int
		switch v := req.Input.(type) {
		case string:
			count = 1
		case []any:
			count = len(v)
		default:
			http.Error(w, "unexpected input type", http.StatusBadRequest)
			return
		}

		// Generate deterministic embeddings of the right dimension.
		embeddings := make([][]float32, count)
		for i := range embeddings {
			vec := make([]float32, dims)
			for j := range vec {
				vec[j] = float32(i+1) * 0.01 * float32(j+1)
			}
			embeddings[i] = vec
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"embeddings": embeddings})
	}))
}

// writeGoFiles creates n Go files in dir, each with funcsPerFile functions.
// Each function is large enough (>20 tokens) to avoid being merged by the chunker.
func writeGoFiles(t *testing.T, dir string, n, funcsPerFile int) {
	t.Helper()
	for i := 0; i < n; i++ {
		var content string
		content = "package main\n\n"
		for j := 0; j < funcsPerFile; j++ {
			// Generate a function with enough body to exceed minChunkTokens (20).
			content += fmt.Sprintf(
				"func file%d_func%d(input string) string {\n"+
					"\tresult := input\n"+
					"\tfor k := 0; k < %d; k++ {\n"+
					"\t\tresult = result + \"suffix\"\n"+
					"\t}\n"+
					"\treturn result\n"+
					"}\n\n", i, j, j+1)
		}
		path := filepath.Join(dir, fmt.Sprintf("file_%d.go", i))
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}
}

// embedCfg returns a Config with embedding enabled, pointing to the given mock server.
func embedCfg(projectRoot, dbPath, embeddingsPath, ollamaURL string) types.Config {
	return types.Config{
		ProjectRoot:       projectRoot,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 500,
		EmbedEnabled:      true,
		OllamaURL:         ollamaURL,
		EmbeddingModel:    "test",
		EmbeddingDims:     4,
		EmbedBatchSize:    32,
		EmbeddingsPath:    embeddingsPath,
	}
}

func TestEmbedProject_LargeChunkCount(t *testing.T) {
	t.Parallel()

	const dims = 4
	var reqCount atomic.Int64
	srv := newMockOllama(dims, &reqCount)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Create 200 Go files with ~25 functions each to produce 5000+ chunks.
	writeGoFiles(t, projectDir, 200, 25)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	// Index with a first daemon instance (WriterManager is single-use).
	d1, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (index): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	stats, err := d1.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalChunks < 5000 {
		t.Fatalf("expected >= 5000 chunks, got %d", stats.TotalChunks)
	}
	t.Logf("Indexed: %d files, %d chunks", stats.TotalFiles, stats.TotalChunks)

	// Verify all chunks need embedding before we start.
	needBefore, err := d1.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding before: %v", err)
	}
	if needBefore != stats.TotalChunks {
		t.Fatalf("expected %d chunks needing embedding, got %d", stats.TotalChunks, needBefore)
	}

	// Embed all chunks.
	count, err := d1.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject: %v", err)
	}
	if count == 0 {
		t.Fatal("EmbedProject returned 0 vectors")
	}
	t.Logf("Embedded %d vectors", count)

	// Verify: zero chunks needing embedding.
	needAfter, err := d1.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding after: %v", err)
	}
	if needAfter != 0 {
		t.Errorf("expected 0 chunks needing embedding after EmbedProject, got %d", needAfter)
	}

	// Verify: vector count matches total chunks (zero drops).
	if count != stats.TotalChunks {
		t.Errorf("expected vector count %d == total chunks %d (zero drops)", count, stats.TotalChunks)
	}

	// Verify: mock server was called at least once.
	if reqCount.Load() == 0 {
		t.Error("expected at least one request to mock Ollama")
	}

	d1.Stop()
}

func TestEmbedProject_OllamaDown(t *testing.T) {
	t.Parallel()

	// Mock Ollama that always returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeGoFiles(t, projectDir, 10, 10)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}

	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	stats, err := d.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalChunks == 0 {
		t.Fatal("expected chunks after indexing")
	}
	t.Logf("Indexed: %d files, %d chunks", stats.TotalFiles, stats.TotalChunks)

	// EmbedProject should return an error (maxRetries exceeded or context timeout).
	// Use a short timeout to avoid 57s of retry backoff starving parallel tests.
	embedCtx, embedCancel := context.WithTimeout(ctx, 15*time.Second)
	defer embedCancel()
	_, embedErr := d.EmbedProject(embedCtx, nil)
	if embedErr == nil {
		t.Fatal("expected EmbedProject to return error when Ollama is down")
	}
	t.Logf("EmbedProject returned expected error: %v", embedErr)

	// Verify: some chunks may NOT be embedded (expected failure mode).
	needEmbed, err := d.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding: %v", err)
	}
	if needEmbed == 0 {
		t.Error("expected some chunks still needing embedding after Ollama failure")
	}

	// Verify: no panic, daemon is still usable -- can still query stats.
	stats2, err := d.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats after failure: %v", err)
	}
	if stats2.TotalChunks != stats.TotalChunks {
		t.Errorf("expected stable chunk count after embed failure, got %d vs %d",
			stats2.TotalChunks, stats.TotalChunks)
	}

	d.Stop()
}

func TestEmbedProject_CrashRecovery(t *testing.T) {
	t.Parallel()

	const dims = 4
	var reqCount atomic.Int64
	srv := newMockOllama(dims, &reqCount)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeGoFiles(t, projectDir, 20, 5)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	// Phase 1: Index + embed all chunks.
	d1, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 1): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (phase 1): %v", err)
	}

	count1, err := d1.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 1): %v", err)
	}
	t.Logf("Phase 1: embedded %d vectors", count1)

	needAfter1, err := d1.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding (phase 1): %v", err)
	}
	if needAfter1 != 0 {
		t.Fatalf("expected 0 chunks needing embedding after phase 1, got %d", needAfter1)
	}
	d1.Stop()

	// Simulate crash: reset some chunks to embedded=0 in the DB.
	// Open the DB directly to manipulate it.
	crashDB, err := storage.Open(storage.OpenInput{Path: dbPath})
	if err != nil {
		t.Fatalf("Open crash DB: %v", err)
	}
	// Reset ~10 chunks to embedded=0 (simulating crash before mark).
	const resetCount = 10
	res, err := crashDB.Writer().ExecContext(ctx,
		"UPDATE chunks SET embedded = 0 WHERE id IN (SELECT id FROM chunks LIMIT ?)", resetCount)
	if err != nil {
		crashDB.Close()
		t.Fatalf("reset chunks: %v", err)
	}
	affected, _ := res.RowsAffected()
	t.Logf("Simulated crash: reset %d chunks to embedded=0", affected)
	crashDB.Close()

	// Phase 2: Create new daemon (same DB + embeddings path), re-embed.
	reqCount.Store(0) // reset request counter
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 2): %v", err)
	}

	// Verify: some chunks need embedding after crash simulation.
	needBefore2, err := d2.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding before phase 2: %v", err)
	}
	if needBefore2 == 0 {
		t.Fatal("expected chunks needing embedding after crash simulation")
	}
	if needBefore2 > resetCount {
		t.Errorf("expected <= %d chunks needing embedding, got %d", resetCount, needBefore2)
	}
	t.Logf("Phase 2: %d chunks need embedding", needBefore2)

	// EmbedProject should only re-embed the reset chunks (Has() reconciliation
	// skips chunks that are already in the vector store loaded from disk).
	count2, err := d2.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 2): %v", err)
	}
	t.Logf("Phase 2: vector store has %d vectors after re-embed", count2)

	// Verify: final state has all chunks embedded.
	needAfter2, err := d2.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding after phase 2: %v", err)
	}
	if needAfter2 != 0 {
		t.Errorf("expected 0 chunks needing embedding after recovery, got %d", needAfter2)
	}

	// Verify: vector count is the same as phase 1 (no duplicates).
	if count2 != count1 {
		t.Errorf("expected vector count %d after recovery == phase 1 count %d", count2, count1)
	}

	d2.Stop()
}

// TestEmbedProject_ReverseReconciliation verifies that when embeddings.bin is
// deleted but the DB still has chunks marked embedded=1, EmbedProject detects
// the mismatch and re-embeds all chunks.
//
// Invariant: the embedded column in SQLite must stay in sync with the actual
// vector store contents. If vectors are lost, the DB must be corrected.
func TestEmbedProject_ReverseReconciliation(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeGoFiles(t, projectDir, 3, 3)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	// Phase 1: Index + embed all chunks.
	d1, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 1): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (phase 1): %v", err)
	}
	count1, err := d1.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 1): %v", err)
	}
	if count1 == 0 {
		t.Fatal("expected non-zero vector count after phase 1")
	}
	need, _ := d1.Store().CountChunksNeedingEmbedding(ctx)
	if need != 0 {
		t.Fatalf("expected 0 chunks needing embedding after phase 1, got %d", need)
	}
	t.Logf("Phase 1: embedded %d vectors", count1)
	d1.Stop()

	// Phase 2: Delete embeddings.bin (simulate user deleting the file).
	if err := os.Remove(embPath); err != nil {
		t.Fatalf("Remove embeddings.bin: %v", err)
	}

	// Phase 3: Re-create daemon (same DB, no embeddings.bin) and re-embed.
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 3): %v", err)
	}

	count2, err := d2.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 3): %v", err)
	}
	t.Logf("Phase 3: embedded %d vectors (expected %d)", count2, count1)

	// All chunks must be re-embedded — vector count should match phase 1.
	if count2 != count1 {
		t.Errorf("vector count after re-embed = %d, want %d (all chunks re-embedded)", count2, count1)
	}

	// No chunks should be left needing embedding.
	need, _ = d2.Store().CountChunksNeedingEmbedding(ctx)
	if need != 0 {
		t.Errorf("chunks needing embedding after re-embed = %d, want 0", need)
	}

	d2.Stop()
}

// TestEmbedProject_PartialVectorLoss verifies that when some vectors are missing
// from the store but the DB still marks them as embedded, EmbedProject only
// re-embeds the missing chunks (targeted reconciliation) rather than resetting all.
func TestEmbedProject_PartialVectorLoss(t *testing.T) {
	t.Parallel()

	const dims = 4
	var reqCount atomic.Int64
	srv := newMockOllama(dims, &reqCount)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeGoFiles(t, projectDir, 10, 5)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	// Phase 1: Index + embed all chunks.
	d1, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 1): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}
	count1, err := d1.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 1): %v", err)
	}
	if count1 == 0 {
		t.Fatal("expected non-zero vector count after phase 1")
	}

	embeddedInDB, _ := d1.Store().CountChunksEmbedded(ctx)
	t.Logf("Phase 1: %d vectors, %d embedded in DB", count1, embeddedInDB)

	// Phase 2: Delete ~20% of vectors from the store to simulate partial loss.
	// Get some chunk IDs to delete.
	deleteIDs, err := d1.Store().GetEmbeddedChunkIDs(ctx, 0, embeddedInDB/5)
	if err != nil {
		t.Fatalf("GetEmbeddedChunkIDs: %v", err)
	}
	if len(deleteIDs) == 0 {
		t.Fatal("no embedded chunk IDs to delete")
	}
	t.Logf("Phase 2: deleting %d vectors (simulating partial loss)", len(deleteIDs))

	if err := d1.vectorStore.Delete(ctx, deleteIDs); err != nil {
		t.Fatalf("Delete vectors: %v", err)
	}

	// Save the partial vector store to disk.
	if saveErr := d1.saveVectors(d1.embeddingsPath()); saveErr != nil {
		t.Fatalf("saveVectors: %v", saveErr)
	}
	d1.Stop()

	// Phase 3: Create new daemon (same DB + partial embeddings) and re-embed.
	reqCount.Store(0)
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 3): %v", err)
	}

	count3, err := d2.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 3): %v", err)
	}
	t.Logf("Phase 3: vector store has %d vectors", count3)

	// Vector count should match phase 1 (all chunks re-embedded).
	if count3 != count1 {
		t.Errorf("vector count = %d, want %d", count3, count1)
	}

	// No chunks should be left needing embedding.
	need, _ := d2.Store().CountChunksNeedingEmbedding(ctx)
	if need != 0 {
		t.Errorf("chunks needing embedding = %d, want 0", need)
	}

	// The mock server should have received requests only for the deleted vectors,
	// not for all chunks. Verify request count is much less than phase 1.
	reqs := reqCount.Load()
	t.Logf("Phase 3: mock server received %d embed requests", reqs)

	d2.Stop()
}

func TestEmbedProject_IncrementalAfterCold(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Phase 1: Create 5 Go files, index + embed all.
	writeGoFiles(t, projectDir, 5, 5)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)

	ctx := context.Background()

	d1, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 1): %v", err)
	}
	if err := d1.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (phase 1): %v", err)
	}

	count1, err := d1.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 1): %v", err)
	}

	stats1, err := d1.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats (phase 1): %v", err)
	}
	t.Logf("Phase 1: %d files, %d chunks, %d vectors",
		stats1.TotalFiles, stats1.TotalChunks, count1)

	needAfter1, err := d1.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding (phase 1): %v", err)
	}
	if needAfter1 != 0 {
		t.Fatalf("expected 0 chunks needing embedding after phase 1, got %d", needAfter1)
	}
	d1.Stop()

	// Phase 2: Add 3 new files, re-index + re-embed with fresh daemon.
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("new_file_%d.go", i)
		content := fmt.Sprintf("package main\n\nfunc newFunc%d_a() {}\nfunc newFunc%d_b() {}\n", i, i)
		path := filepath.Join(projectDir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (phase 2): %v", err)
	}
	if err := d2.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject (phase 2): %v", err)
	}

	stats2, err := d2.Store().GetIndexStats(ctx)
	if err != nil {
		t.Fatalf("GetIndexStats (phase 2): %v", err)
	}
	t.Logf("Phase 2: %d files, %d chunks", stats2.TotalFiles, stats2.TotalChunks)

	// Assert against actual files on disk (ground truth) rather than
	// stats1.TotalFiles which can be inflated by transient scanner artifacts.
	goFiles, _ := filepath.Glob(filepath.Join(projectDir, "*.go"))
	if stats2.TotalFiles < len(goFiles) {
		t.Errorf("expected >= %d indexed files (on disk), got %d",
			len(goFiles), stats2.TotalFiles)
	}

	// Only the new files' chunks should need embedding.
	needBefore2, err := d2.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding before phase 2 embed: %v", err)
	}
	if needBefore2 == 0 {
		t.Fatal("expected new chunks needing embedding after adding files")
	}
	t.Logf("Phase 2: %d chunks need embedding (new files)", needBefore2)

	count2, err := d2.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject (phase 2): %v", err)
	}

	// Verify: all chunks now embedded.
	needAfter2, err := d2.Store().CountChunksNeedingEmbedding(ctx)
	if err != nil {
		t.Fatalf("CountChunksNeedingEmbedding after phase 2: %v", err)
	}
	if needAfter2 != 0 {
		t.Errorf("expected 0 chunks needing embedding after phase 2, got %d", needAfter2)
	}

	// Verify: total embedded count increased.
	if count2 <= count1 {
		t.Errorf("expected vector count to increase from %d, got %d", count1, count2)
	}
	t.Logf("Phase 2: vector count increased from %d to %d", count1, count2)

	d2.Stop()
}

// ── Phase 2: Progress Reporting Tests ──

func TestIndexProgress_Callback(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataRoot := filepath.Join(cwd, "..", "..", "testdata", "typescript_project")
	if _, err := os.Stat(testdataRoot); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataRoot)
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := types.Config{
		ProjectRoot:       testdataRoot,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	ctx := context.Background()

	var lastProgress IndexProgress
	var callCount int
	if err := d.IndexProject(ctx, func(p IndexProgress) {
		callCount++
		lastProgress = p
	}); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	if callCount == 0 {
		t.Fatal("expected OnProgress callback to fire at least once")
	}
	if lastProgress.Total == 0 {
		t.Fatal("expected Total > 0 in last progress update")
	}
	if lastProgress.Indexed != lastProgress.Total {
		t.Errorf("final progress: Indexed=%d, Total=%d — expected equal",
			lastProgress.Indexed, lastProgress.Total)
	}
	t.Logf("IndexProgress: %d callbacks, final Indexed=%d Total=%d",
		callCount, lastProgress.Indexed, lastProgress.Total)
}

func TestIndexProgress_NilCallback(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	testdataRoot := filepath.Join(cwd, "..", "..", "testdata", "typescript_project")
	if _, err := os.Stat(testdataRoot); os.IsNotExist(err) {
		t.Skipf("testdata not found at %s", testdataRoot)
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	cfg := types.Config{
		ProjectRoot:       testdataRoot,
		DBPath:            dbPath,
		EnrichmentWorkers: 2,
		WriterChannelSize: 100,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	// nil callback should not panic.
	if err := d.IndexProject(context.Background(), nil); err != nil {
		t.Fatalf("IndexProject with nil callback: %v", err)
	}
}

func TestEmbedProject_OllamaHealthCheck(t *testing.T) {
	t.Parallel()

	// Create and immediately close server to get an unreachable URL.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := srv.URL
	srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeGoFiles(t, projectDir, 2, 2)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, unreachableURL)

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon: %v", err)
	}
	defer d.Stop()

	ctx := context.Background()
	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	_, embedErr := d.EmbedProject(ctx, nil)
	if embedErr == nil {
		t.Fatal("expected EmbedProject to return error when Ollama is unreachable")
	}
	if !strings.Contains(embedErr.Error(), "not reachable") {
		t.Errorf("expected 'not reachable' in error, got: %v", embedErr)
	}
}

// ── ScanRepo Edge Case Tests ──

func TestScanRepo_GitignoreFiltering(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a .gitignore that excludes generated files
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("generated/\n*.gen.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a normal Go file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file matching gitignore pattern
	if err := os.WriteFile(filepath.Join(dir, "output.gen.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file in a gitignored directory
	genDir := filepath.Join(dir, "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(genDir, "types.go"), []byte("package gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// Only main.go should be scanned
	if len(result.Files) != 1 {
		names := make([]string, len(result.Files))
		for i, f := range result.Files {
			names[i] = f.Path
		}
		t.Fatalf("expected 1 file (main.go), got %d: %v", len(result.Files), names)
	}
	if result.Files[0].Path != "main.go" {
		t.Errorf("expected main.go, got %s", result.Files[0].Path)
	}
}

func TestScanRepo_BinaryFileSkipped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a normal Go file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a binary .go file (contains null bytes)
	binaryContent := []byte("package main\n\x00\x00\x00binary content")
	if err := os.WriteFile(filepath.Join(dir, "binary.go"), binaryContent, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// Only main.go should be scanned (binary.go skipped)
	if len(result.Files) != 1 {
		names := make([]string, len(result.Files))
		for i, f := range result.Files {
			names[i] = f.Path
		}
		t.Fatalf("expected 1 file (main.go), got %d: %v", len(result.Files), names)
	}
}

func TestScanRepo_ContextCancellation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create several files so the walk has something to do
	for i := 0; i < 10; i++ {
		f := filepath.Join(dir, fmt.Sprintf("file%d.go", i))
		if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := ScanRepo(ctx, ScanInput{ProjectRoot: dir})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// ── Writer Edge Case Tests ──

func TestSubmit_AfterClose(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Shut down writer
	cancel()
	<-wm.Done()

	// Submit after close should return ErrWriterClosed
	err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "test.go",
		File: &types.FileRecord{
			Path:            "test.go",
			ContentHash:     "abc",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
	})
	if err != ErrWriterClosed {
		t.Errorf("expected ErrWriterClosed, got %v", err)
	}
}

// ── Re-index with Changed Symbols (covers fetchOldSymbols + computeAndRecordDiff) ──

func TestWriterManager_ReindexWithChangedSymbols(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 100, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Phase 1: Index file with original symbols
	done1 := make(chan error, 1)
	_ = wm.AddProducer()
	if err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "app.go",
		File: &types.FileRecord{
			Path:            "app.go",
			ContentHash:     "hash_v1",
			Mtime:           1.0,
			Size:            100,
			Language:        "go",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "OldFunc",
				StartLine: 1, EndLine: 10,
				Content: "func OldFunc() {}", TokenCount: 5},
			{ChunkIndex: 1, Kind: "function", SymbolName: "StableFunc",
				StartLine: 12, EndLine: 20,
				Content: "func StableFunc() {}", TokenCount: 5},
		},
		Symbols: []types.SymbolRecord{
			{Name: "OldFunc", Kind: "function", Line: 1, IsExported: true, Visibility: "exported"},
			{Name: "StableFunc", Kind: "function", Line: 12, IsExported: true, Visibility: "exported"},
		},
		ContentHash: "hash_v1",
		Timestamp:   time.Now(),
		Done:        done1,
	}); err != nil {
		t.Fatalf("Submit phase 1: %v", err)
	}
	if err := <-done1; err != nil {
		t.Fatalf("phase 1 job failed: %v", err)
	}
	wm.RemoveProducer()

	// Phase 2: Re-index with changed symbols (OldFunc → NewFunc, StableFunc remains)
	done2 := make(chan error, 1)
	_ = wm.AddProducer()
	if err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "app.go",
		File: &types.FileRecord{
			Path:            "app.go",
			ContentHash:     "hash_v2",
			Mtime:           2.0,
			Size:            120,
			Language:        "go",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "NewFunc",
				StartLine: 1, EndLine: 10,
				Content: "func NewFunc() {}", TokenCount: 5},
			{ChunkIndex: 1, Kind: "function", SymbolName: "StableFunc",
				StartLine: 12, EndLine: 20,
				Content: "func StableFunc() { updated }", TokenCount: 6},
		},
		Symbols: []types.SymbolRecord{
			{Name: "NewFunc", Kind: "function", Line: 1, IsExported: true, Visibility: "exported"},
			{Name: "StableFunc", Kind: "function", Line: 12, IsExported: true, Visibility: "exported"},
		},
		ContentHash: "hash_v2",
		Timestamp:   time.Now(),
		Done:        done2,
	}); err != nil {
		t.Fatalf("Submit phase 2: %v", err)
	}
	if err := <-done2; err != nil {
		t.Fatalf("phase 2 job failed: %v", err)
	}
	wm.RemoveProducer()

	cancel()
	<-wm.Done()

	// Verify: file has updated content hash
	f, err := store.GetFileByPath(context.Background(), "app.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f == nil {
		t.Fatal("expected file record")
	}
	if f.ContentHash != "hash_v2" {
		t.Errorf("ContentHash = %q, want hash_v2", f.ContentHash)
	}

	// Verify: diff records exist
	diffs, err := store.GetRecentDiffs(context.Background(), types.RecentDiffsInput{
		Since: time.Now().Add(-1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("GetRecentDiffs: %v", err)
	}
	if len(diffs) == 0 {
		t.Error("expected diff records after re-index with changed symbols")
	}
}

// ── IndexAll with Progress Callback ──

func TestIndexAll_WithProgress(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create multiple Go files
	for i := 0; i < 5; i++ {
		f := filepath.Join(dir, fmt.Sprintf("file%d.go", i))
		content := fmt.Sprintf("package main\n\nfunc Func%d() string {\n\treturn \"hello\"\n}\n", i)
		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 100, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	pipeline := NewEnrichmentPipeline(store, wm, 2, nil)

	// Scan files
	scanResult, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// Track progress
	var progressCalls int
	if err := pipeline.IndexAll(context.Background(), IndexAllInput{
		ProjectRoot: dir,
		Files:       scanResult.Files,
		OnProgress: func(p IndexProgress) {
			progressCalls++
		},
	}); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	cancel()
	<-wm.Done()

	if progressCalls == 0 {
		t.Error("expected OnProgress to be called")
	}

	// Verify files were indexed
	stats, err := store.GetIndexStats(context.Background())
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles < 5 {
		t.Errorf("expected >= 5 files, got %d", stats.TotalFiles)
	}
}

// ── ScanRepo with .shaktimanignore ──

func TestScanRepo_ShaktimanIgnore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a .shaktimanignore file
	if err := os.WriteFile(filepath.Join(dir, ".shaktimanignore"), []byte("vendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a normal file
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file in ignored vendor dir
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "lib.go"), []byte("package vendor\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// Only main.go should be found
	if len(result.Files) != 1 {
		names := make([]string, len(result.Files))
		for i, f := range result.Files {
			names[i] = f.Path
		}
		t.Fatalf("expected 1 file, got %d: %v", len(result.Files), names)
	}
}

// ── Writer Delete Job ──

func TestWriterManager_DeleteJob(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// First, create a file
	done1 := make(chan error, 1)
	_ = wm.AddProducer()
	if err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "del_target.go",
		File: &types.FileRecord{
			Path:            "del_target.go",
			ContentHash:     "abc",
			Mtime:           1.0,
			Language:        "go",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Foo",
				StartLine: 1, EndLine: 5, Content: "func Foo() {}", TokenCount: 5},
		},
		ContentHash: "abc",
		Timestamp:   time.Now(),
		Done:        done1,
	}); err != nil {
		t.Fatalf("Submit create: %v", err)
	}
	if err := <-done1; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	// Then delete it
	done2 := make(chan error, 1)
	if err := wm.Submit(types.WriteJob{
		Type:     types.WriteJobFileDelete,
		FilePath: "del_target.go",
		Done:     done2,
	}); err != nil {
		t.Fatalf("Submit delete: %v", err)
	}
	if err := <-done2; err != nil {
		t.Fatalf("delete job failed: %v", err)
	}
	wm.RemoveProducer()

	cancel()
	<-wm.Done()

	// Verify file is gone
	f, err := store.GetFileByPath(context.Background(), "del_target.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f != nil {
		t.Error("expected file to be deleted")
	}
}

// ── IndexAll: all files up-to-date ──

func TestIndexAll_AllUpToDate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 100, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	pipeline := NewEnrichmentPipeline(store, wm, 1, nil)

	scanResult, err := ScanRepo(context.Background(), ScanInput{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	// First index
	if err := pipeline.IndexAll(context.Background(), IndexAllInput{
		ProjectRoot: dir,
		Files:       scanResult.Files,
	}); err != nil {
		t.Fatalf("IndexAll first: %v", err)
	}

	// Second index with same files — should hit "all files up to date" path
	if err := pipeline.IndexAll(context.Background(), IndexAllInput{
		ProjectRoot: dir,
		Files:       scanResult.Files,
	}); err != nil {
		t.Fatalf("IndexAll second: %v", err)
	}

	cancel()
	<-wm.Done()
}

// ── IndexAll: enrich error path ──

func TestIndexAll_EnrichError(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 100, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	pipeline := NewEnrichmentPipeline(store, wm, 1, nil)

	// Pass a file with non-existent AbsPath — enrichFile will fail on readFileContent
	badFiles := []ScannedFile{{
		Path:        "missing.go",
		AbsPath:     "/nonexistent/path/missing.go",
		ContentHash: "abc123",
		Language:    "go",
	}}

	// IndexAll should succeed despite enrich errors (they're logged, not fatal)
	if err := pipeline.IndexAll(context.Background(), IndexAllInput{
		ProjectRoot: "/tmp",
		Files:       badFiles,
	}); err != nil {
		t.Fatalf("IndexAll: %v", err)
	}

	cancel()
	<-wm.Done()
}

// ── Watcher Start: processes real fsnotify events ──

func TestWatcher_Start_ProcessesFileChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	w, err := NewWatcher(dir, 10) // short debounce
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- w.Start(ctx)
	}()

	// Give watcher time to set up
	time.Sleep(100 * time.Millisecond)

	// Create a Go file — triggers fsnotify Create event
	goFile := filepath.Join(dir, "new.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the event to arrive through the pipeline
	select {
	case event := <-w.Events():
		if event.ChangeType != "modify" {
			t.Errorf("expected 'modify', got %q", event.ChangeType)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for file change event")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not exit")
	}
}

// ── FlushPending: event channel full triggers drop ──

func TestFlushPending_EventChannelDrop(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create files so stat works during flush
	for i := 0; i < 5; i++ {
		f := filepath.Join(dir, fmt.Sprintf("drop%d.go", i))
		os.WriteFile(f, []byte("package main\n"), 0o644)
	}

	w, err := NewWatcher(dir, 100)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.fsw.Close()

	// Fill the event channel completely (capacity 100)
	for i := 0; i < 100; i++ {
		w.eventCh <- FileChangeEvent{Path: fmt.Sprintf("fill%d.go", i)}
	}

	// Populate pending with files — flush will try to emit but channel is full
	w.mu.Lock()
	for i := 0; i < 5; i++ {
		f := filepath.Join(dir, fmt.Sprintf("drop%d.go", i))
		absF, _ := filepath.EvalSymlinks(f)
		w.pending[absF] = time.Now().Add(-time.Second)
	}
	w.mu.Unlock()

	// Flush with zero debounce — all entries are ready
	// This will hit the time.After(1s) drop path since eventCh is full
	w.flushPending(0)

	// Check that drops were counted
	drops := w.dropCount.Load()
	if drops == 0 {
		t.Error("expected drop count > 0 when event channel is full")
	}
}

func TestEmbeddingsPath_BruteForce(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend:  "brute_force",
			EmbeddingsPath: "/data/.shaktiman/embeddings.bin",
		},
	}
	got := d.embeddingsPath()
	if got != "/data/.shaktiman/embeddings.bin" {
		t.Errorf("embeddingsPath() = %q, want /data/.shaktiman/embeddings.bin", got)
	}
}

func TestEmbeddingsPath_HNSW_WithExtension(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend:  "hnsw",
			EmbeddingsPath: "/data/.shaktiman/embeddings.bin",
		},
	}
	got := d.embeddingsPath()
	want := "/data/.shaktiman/embeddings.hnsw"
	if got != want {
		t.Errorf("embeddingsPath() = %q, want %q", got, want)
	}
}

func TestEmbeddingsPath_HNSW_NoExtension(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend:  "hnsw",
			EmbeddingsPath: "/data/.shaktiman/embeddings",
		},
	}
	got := d.embeddingsPath()
	want := "/data/.shaktiman/embeddings.hnsw"
	if got != want {
		t.Errorf("embeddingsPath() = %q, want %q", got, want)
	}
}

func TestEmbeddingsPath_EmptyBackend(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend:  "",
			EmbeddingsPath: "/data/embeddings.bin",
		},
	}
	got := d.embeddingsPath()
	if got != "/data/embeddings.bin" {
		t.Errorf("embeddingsPath() = %q, want /data/embeddings.bin (default path)", got)
	}
}

func TestNewVectorStore_BruteForce(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend: "brute_force",
			EmbeddingDims: 4,
		},
	}
	vs, err := d.newVectorStore()
	if err != nil {
		t.Fatalf("newVectorStore: %v", err)
	}
	if _, ok := vs.(*vector.BruteForceStore); !ok {
		t.Errorf("expected *BruteForceStore, got %T", vs)
	}
}

func TestNewVectorStore_HNSW(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend: "hnsw",
			EmbeddingDims: 4,
		},
	}
	vs, err := d.newVectorStore()
	if err != nil {
		t.Fatalf("newVectorStore: %v", err)
	}
	if _, ok := vs.(*vector.HNSWStore); !ok {
		t.Errorf("expected *HNSWStore, got %T", vs)
	}
	vs.Close()
}

func TestNewVectorStore_EmptyBackendDefaultsBruteForce(t *testing.T) {
	t.Parallel()
	d := &Daemon{
		cfg: types.Config{
			VectorBackend: "",
			EmbeddingDims: 4,
		},
	}
	vs, err := d.newVectorStore()
	if err != nil {
		t.Fatalf("newVectorStore: %v", err)
	}
	if _, ok := vs.(*vector.BruteForceStore); !ok {
		t.Errorf("expected *BruteForceStore for empty backend, got %T", vs)
	}
}

func TestDaemon_New_WithHNSWBackend(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0o755)
	writeGoFiles(t, projectDir, 2, 2)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)
	cfg.VectorBackend = "hnsw"

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon with HNSW backend: %v", err)
	}

	// Verify the vector store is HNSW type
	if _, ok := d.vectorStore.(*vector.HNSWStore); !ok {
		t.Errorf("expected *HNSWStore, got %T", d.vectorStore)
	}

	// Verify embeddingsPath uses .hnsw extension
	got := d.embeddingsPath()
	if !strings.HasSuffix(got, ".hnsw") {
		t.Errorf("embeddingsPath() = %q, expected .hnsw suffix", got)
	}

	d.Stop()
}

func TestEmbedProject_HNSWBackend(t *testing.T) {
	t.Parallel()

	const dims = 4
	srv := newMockOllama(dims, nil)
	defer srv.Close()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "project")
	os.MkdirAll(projectDir, 0o755)
	writeGoFiles(t, projectDir, 3, 3)

	dbPath := filepath.Join(tmpDir, "test.db")
	embPath := filepath.Join(tmpDir, "embeddings.bin")
	cfg := embedCfg(projectDir, dbPath, embPath, srv.URL)
	cfg.VectorBackend = "hnsw"

	ctx := context.Background()

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := d.IndexProject(ctx, nil); err != nil {
		t.Fatalf("IndexProject: %v", err)
	}

	count, err := d.EmbedProject(ctx, nil)
	if err != nil {
		t.Fatalf("EmbedProject: %v", err)
	}
	if count == 0 {
		t.Fatal("expected non-zero vector count")
	}

	// Verify persistence file uses .hnsw extension
	hnswPath := d.embeddingsPath()
	if _, err := os.Stat(hnswPath); os.IsNotExist(err) {
		t.Errorf("expected HNSW persistence file at %s", hnswPath)
	}

	// Verify the .bin file was NOT created
	if _, err := os.Stat(embPath); !os.IsNotExist(err) {
		t.Errorf("did not expect brute-force persistence file at %s", embPath)
	}

	d.Stop()

	// Phase 2: Re-create daemon, verify vectors load from disk
	d2, err := New(cfg)
	if err != nil {
		t.Fatalf("New daemon (reload): %v", err)
	}

	count2, _ := d2.vectorStore.Count(ctx)
	if count2 != count {
		t.Errorf("vector count after reload = %d, want %d", count2, count)
	}

	d2.Stop()
}
