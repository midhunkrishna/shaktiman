package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
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
	if err := d.IndexProject(ctx); err != nil {
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
	if pkg.TotalTokens > 8192 {
		t.Errorf("expected total_tokens <= 8192, got %d", pkg.TotalTokens)
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

func TestWriterManager_ProcessJob(t *testing.T) {
	t.Parallel()

	db, err := storage.Open(storage.OpenInput{InMemory: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	wm := NewWriterManager(store, 10)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Submit a job with a Done channel to wait for completion
	done := make(chan error, 1)
	wm.AddProducer()
	wm.Submit(types.WriteJob{
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
	})
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
