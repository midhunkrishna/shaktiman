package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

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
