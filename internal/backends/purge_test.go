package backends

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// ── PurgeFiles tests (moved from internal/daemon/purge_test.go) ──

func TestPurgeFiles_SQLite(t *testing.T) {
	dir := t.TempDir()
	shaktiDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(shaktiDir, 0o755)

	// Create dummy files
	dbPath := filepath.Join(shaktiDir, "index.db")
	embPath := filepath.Join(shaktiDir, "embeddings.bin")
	tomlPath := filepath.Join(shaktiDir, "shaktiman.toml")

	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", embPath, tomlPath} {
		if err := os.WriteFile(p, []byte("test"), 0o644); err != nil {
			t.Fatalf("create %s: %v", p, err)
		}
	}
	// Also create HNSW file
	hnswPath := filepath.Join(shaktiDir, "embeddings.hnsw")
	os.WriteFile(hnswPath, []byte("test"), 0o644)

	cfg := types.Config{
		DatabaseBackend: "sqlite",
		DBPath:          dbPath,
		EmbeddingsPath:  embPath,
	}

	if err := PurgeFiles(cfg); err != nil {
		t.Fatalf("PurgeFiles: %v", err)
	}

	// DB files should be gone
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", p)
		}
	}
	// Vector files should be gone
	for _, p := range []string{embPath, hnswPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed", p)
		}
	}
	// Config should survive
	if _, err := os.Stat(tomlPath); err != nil {
		t.Errorf("shaktiman.toml should survive purge, got: %v", err)
	}
}

func TestPurgeFiles_Postgres_SkipsDB(t *testing.T) {
	dir := t.TempDir()
	shaktiDir := filepath.Join(dir, ".shaktiman")
	os.MkdirAll(shaktiDir, 0o755)

	dbPath := filepath.Join(shaktiDir, "index.db")
	embPath := filepath.Join(shaktiDir, "embeddings.bin")

	// Create a dummy DB file that should NOT be deleted for postgres backend
	os.WriteFile(dbPath, []byte("test"), 0o644)
	os.WriteFile(embPath, []byte("test"), 0o644)

	cfg := types.Config{
		DatabaseBackend: "postgres",
		DBPath:          dbPath,
		EmbeddingsPath:  embPath,
	}

	if err := PurgeFiles(cfg); err != nil {
		t.Fatalf("PurgeFiles: %v", err)
	}

	// DB file should still exist (postgres backend doesn't delete local DB)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("index.db should NOT be deleted when backend is postgres")
	}
	// Vector file should still be gone (file-based vector stores are always cleaned)
	if _, err := os.Stat(embPath); !os.IsNotExist(err) {
		t.Error("embeddings.bin should be removed regardless of DB backend")
	}
}

func TestPurgeFiles_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := types.Config{
		DatabaseBackend: "sqlite",
		DBPath:          filepath.Join(dir, "nonexistent.db"),
		EmbeddingsPath:  filepath.Join(dir, "nonexistent.bin"),
	}

	// Should not error on missing files
	if err := PurgeFiles(cfg); err != nil {
		t.Fatalf("PurgeFiles on missing files: %v", err)
	}
}

// ── PurgeBackends tests ──

// mockStorePurger implements types.WriterStore (stub) + types.StorePurger.
type mockStorePurger struct {
	types.WriterStore
	purged bool
	err    error
}

func (m *mockStorePurger) PurgeAll(ctx context.Context) error {
	m.purged = true
	return m.err
}

// mockVectorPurger implements types.VectorStore (stub) + types.VectorPurger.
type mockVectorPurger struct {
	types.VectorStore
	purged bool
	err    error
}

func (m *mockVectorPurger) PurgeAll(ctx context.Context) error {
	m.purged = true
	return m.err
}

// plainStore is a WriterStore that does NOT implement StorePurger.
type plainStore struct {
	types.WriterStore
}

// plainVectorStore is a VectorStore that does NOT implement VectorPurger.
type plainVectorStore struct {
	types.VectorStore
}

func TestPurgeBackends_WithStorePurger(t *testing.T) {
	store := &mockStorePurger{}
	if err := PurgeBackends(context.Background(), store, nil); err != nil {
		t.Fatalf("PurgeBackends: %v", err)
	}
	if !store.purged {
		t.Error("expected StorePurger.PurgeAll to be called")
	}
}

func TestPurgeBackends_WithVectorPurger(t *testing.T) {
	store := &plainStore{}
	vs := &mockVectorPurger{}
	if err := PurgeBackends(context.Background(), store, vs); err != nil {
		t.Fatalf("PurgeBackends: %v", err)
	}
	if !vs.purged {
		t.Error("expected VectorPurger.PurgeAll to be called")
	}
}

func TestPurgeBackends_NilVectorStore(t *testing.T) {
	store := &mockStorePurger{}
	if err := PurgeBackends(context.Background(), store, nil); err != nil {
		t.Fatalf("PurgeBackends: %v", err)
	}
}

func TestPurgeBackends_NonPurgerStore(t *testing.T) {
	store := &plainStore{}
	vs := &plainVectorStore{}
	// Neither implements purger — should succeed silently
	if err := PurgeBackends(context.Background(), store, vs); err != nil {
		t.Fatalf("PurgeBackends: %v", err)
	}
}

func TestPurgeBackends_StoreErrorReturned(t *testing.T) {
	store := &mockStorePurger{err: errors.New("db down")}
	err := PurgeBackends(context.Background(), store, nil)
	if err == nil {
		t.Fatal("expected error from store purge")
	}
	if !errors.Is(err, store.err) {
		t.Errorf("expected wrapped db error, got: %v", err)
	}
}

func TestPurgeBackends_VectorErrorReturned(t *testing.T) {
	store := &plainStore{}
	vs := &mockVectorPurger{err: errors.New("qdrant down")}
	err := PurgeBackends(context.Background(), store, vs)
	if err == nil {
		t.Fatal("expected error from vector purge")
	}
	if !errors.Is(err, vs.err) {
		t.Errorf("expected wrapped qdrant error, got: %v", err)
	}
}
