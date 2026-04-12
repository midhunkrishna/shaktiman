//go:build sqlite_fts5 && sqlite

package backends

import (
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"

	// Register backends.
	_ "github.com/shaktimanai/shaktiman/internal/storage/sqlite"
	_ "github.com/shaktimanai/shaktiman/internal/vector/bruteforce"
)

func sqliteCfg(t *testing.T) types.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := types.DefaultConfig(dir)
	cfg.DBPath = filepath.Join(dir, "test.db")
	cfg.EmbeddingsPath = filepath.Join(dir, "embeddings.bin")
	cfg.DatabaseBackend = "sqlite"
	cfg.EmbedEnabled = false
	return cfg
}

func TestOpen_SQLite(t *testing.T) {
	cfg := sqliteCfg(t)

	b, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	if b.Store == nil {
		t.Fatal("expected non-nil Store")
	}
	if b.VectorStore != nil {
		t.Error("expected nil VectorStore when EmbedEnabled=false")
	}
}

func TestOpen_VectorStoreOptional(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.EmbedEnabled = false

	b, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	if b.VectorStore != nil {
		t.Error("expected nil VectorStore when EmbedEnabled=false")
	}
}

func TestOpenMetadataOnly(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.EmbedEnabled = true // should still skip vector store

	b, err := OpenMetadataOnly(cfg)
	if err != nil {
		t.Fatalf("OpenMetadataOnly: %v", err)
	}
	defer b.Close()

	if b.Store == nil {
		t.Fatal("expected non-nil Store")
	}
	if b.VectorStore != nil {
		t.Error("expected nil VectorStore from OpenMetadataOnly")
	}
}

func TestOpen_WithVectorStore(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.EmbedEnabled = true
	cfg.VectorBackend = "brute_force"
	cfg.EmbeddingDims = 4

	b, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	if b.Store == nil {
		t.Fatal("expected non-nil Store")
	}
	if b.VectorStore == nil {
		t.Fatal("expected non-nil VectorStore with EmbedEnabled + brute_force")
	}
}

func TestOpen_InvalidBackend(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.DatabaseBackend = "nonexistent"

	_, err := Open(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent backend")
	}
}

func TestOpenMetadataOnly_InvalidBackend(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.DatabaseBackend = "nonexistent"

	_, err := OpenMetadataOnly(cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent backend")
	}
}

func TestClose_FullStack(t *testing.T) {
	cfg := sqliteCfg(t)
	cfg.EmbedEnabled = true
	cfg.VectorBackend = "brute_force"
	cfg.EmbeddingDims = 4

	b, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Close should not error
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second close should not panic
	b.Close()
}
