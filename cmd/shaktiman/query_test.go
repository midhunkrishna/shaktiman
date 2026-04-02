package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// seedProject creates a temp dir with a Go file and a fully indexed database.
// Returns the project root directory (containing .shaktiman/index.db).
func seedProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create a Go source file
	goFile := filepath.Join(dir, "main.go")
	src := []byte("package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n\nfunc Caller() { Hello() }\n")
	if err := os.WriteFile(goFile, src, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create the database via registry
	dbDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(dbDir, 0o755)
	dbPath := filepath.Join(dbDir, "index.db")

	db, err := storage.Open(storage.OpenInput{Path: dbPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store := storage.NewStore(db)
	ctx := context.Background()

	// Seed file + chunks + symbols
	fileID, _ := store.UpsertFile(ctx, &types.FileRecord{
		Path: "main.go", ContentHash: "h1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
	})
	chunkIDs, _ := store.InsertChunks(ctx, fileID, []types.ChunkRecord{
		{ChunkIndex: 0, Kind: "function", SymbolName: "Hello",
			StartLine: 3, EndLine: 5, Content: "func Hello() string { return \"hello\" }", TokenCount: 10},
		{ChunkIndex: 1, Kind: "function", SymbolName: "Caller",
			StartLine: 7, EndLine: 7, Content: "func Caller() { Hello() }", TokenCount: 8},
	})
	symIDs, _ := store.InsertSymbols(ctx, fileID, []types.SymbolRecord{
		{ChunkID: chunkIDs[0], Name: "Hello", Kind: "function", Line: 3, Visibility: "exported", IsExported: true},
		{ChunkID: chunkIDs[1], Name: "Caller", Kind: "function", Line: 7, Visibility: "exported", IsExported: true},
	})

	// Insert edge: Caller -> Hello
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		return store.InsertEdges(ctx, txh, fileID, []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Hello", Kind: "calls"},
		}, map[string]int64{"Caller": symIDs[1], "Hello": symIDs[0]}, "go")
	})

	// Insert a diff
	_ = store.WithWriteTx(ctx, func(txh types.TxHandle) error {
		diffID, err := store.InsertDiffLog(ctx, txh, types.DiffLogEntry{
			FileID: fileID, ChangeType: "add", LinesAdded: 7, HashAfter: "h1",
		})
		if err != nil {
			return err
		}
		return store.InsertDiffSymbols(ctx, txh, diffID, []types.DiffSymbolEntry{
			{SymbolName: "Hello", ChangeType: "added"},
			{SymbolName: "Caller", ChangeType: "added"},
		})
	})

	db.Close()
	return dir
}

func TestOpenStore(t *testing.T) {
	t.Parallel()
	dir := seedProject(t)

	cfg := types.DefaultConfig(dir)
	store, closer, err := openStore(cfg)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	defer closer()

	stats, err := store.GetIndexStats(context.Background())
	if err != nil {
		t.Fatalf("GetIndexStats: %v", err)
	}
	if stats.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1", stats.TotalFiles)
	}
}

func TestOpenStore_InvalidBackend(t *testing.T) {
	t.Parallel()

	cfg := types.DefaultConfig(t.TempDir())
	cfg.DatabaseBackend = "nonexistent"
	_, _, err := openStore(cfg)
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
}

func TestSearchCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := searchCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("searchCmd: %v", err)
	}
}

func TestSearchCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := searchCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("searchCmd text: %v", err)
	}
}

func TestSearchCmd_WithPathFilter(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := searchCmd()
	cmd.SetArgs([]string{"--root", dir, "--path", "main", "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("searchCmd path filter: %v", err)
	}
}

func TestContextCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := contextCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("contextCmd: %v", err)
	}
}

func TestContextCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := contextCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("contextCmd text: %v", err)
	}
}

func TestSymbolsCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := symbolsCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("symbolsCmd: %v", err)
	}
}

func TestSymbolsCmd_WithKindFilter(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := symbolsCmd()
	cmd.SetArgs([]string{"--root", dir, "--kind", "function", "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("symbolsCmd kind filter: %v", err)
	}
}

func TestSymbolsCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := symbolsCmd()
	cmd.SetArgs([]string{"--root", dir, "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("symbolsCmd text: %v", err)
	}
}

func TestSymbolsCmd_NotFound(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := symbolsCmd()
	cmd.SetArgs([]string{"--root", dir, "NonExistent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("symbolsCmd not found: %v", err)
	}
}

func TestDepsCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "callees", "Caller"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("depsCmd: %v", err)
	}
}

func TestDepsCmd_Callers(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "callers", "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("depsCmd callers: %v", err)
	}
}

func TestDepsCmd_Both(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "both", "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("depsCmd both: %v", err)
	}
}

func TestDepsCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "both", "Hello"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("depsCmd text: %v", err)
	}
}

func TestDepsCmd_InvalidDirection(t *testing.T) {
	dir := seedProject(t)

	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "sideways", "Hello"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid direction")
	}
}

func TestDepsCmd_SymbolNotFound(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := depsCmd()
	cmd.SetArgs([]string{"--root", dir, "--direction", "both", "NonExistent"})
	// Should not error — just return empty results
	if err := cmd.Execute(); err != nil {
		t.Fatalf("depsCmd not found: %v", err)
	}
}

func TestDiffCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := diffCmd()
	cmd.SetArgs([]string{"--root", dir, "--since", "1h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("diffCmd: %v", err)
	}
}

func TestDiffCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := diffCmd()
	cmd.SetArgs([]string{"--root", dir, "--since", "1h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("diffCmd text: %v", err)
	}
}

func TestDiffCmd_InvalidDuration(t *testing.T) {
	dir := seedProject(t)

	cmd := diffCmd()
	cmd.SetArgs([]string{"--root", dir, "--since", "not-a-duration"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestDiffCmd_LongDurationCapped(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := diffCmd()
	// 10000h > 720h cap
	cmd.SetArgs([]string{"--root", dir, "--since", "10000h"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("diffCmd long duration: %v", err)
	}
}

func TestEnrichmentStatusCmd_JSON(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "json"
	cmd := enrichmentStatusCmd()
	cmd.SetArgs([]string{"--root", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("enrichmentStatusCmd: %v", err)
	}
}

func TestEnrichmentStatusCmd_Text(t *testing.T) {
	dir := seedProject(t)

	outputFormat = "text"
	cmd := enrichmentStatusCmd()
	cmd.SetArgs([]string{"--root", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("enrichmentStatusCmd text: %v", err)
	}
}

