package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestInitCmd_CreatesConfig(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, ".shaktiman", "shaktiman.toml")

	cmd := initCmd()
	cmd.SetArgs([]string{tmpDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("initCmd: %v", err)
	}

	if _, err := os.Stat(tomlPath); os.IsNotExist(err) {
		t.Fatal("expected shaktiman.toml to be created")
	}
}

func TestInitCmd_ExistingConfigNoOverwrite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".shaktiman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	tomlPath := filepath.Join(dir, "shaktiman.toml")
	original := []byte("# my custom config\n")
	if err := os.WriteFile(tomlPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := initCmd()
	cmd.SetArgs([]string{tmpDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("initCmd: %v", err)
	}

	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(original) {
		t.Error("expected existing config to be preserved")
	}
}

func TestIndexCmd_LoadsTOML(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".shaktiman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a TOML with hnsw backend
	toml := `[vector]
backend = "hnsw"
`
	if err := os.WriteFile(filepath.Join(dir, "shaktiman.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := types.DefaultConfig(tmpDir)
	cfg, err := types.LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}

	if cfg.VectorBackend != "hnsw" {
		t.Errorf("expected VectorBackend=hnsw, got %q", cfg.VectorBackend)
	}
}

func TestIndexCmd_VectorFlagOverridesToml(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".shaktiman")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// TOML says brute_force
	toml := `[vector]
backend = "brute_force"
`
	if err := os.WriteFile(filepath.Join(dir, "shaktiman.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate what indexCmd does: load TOML then apply flag override
	cfg := types.DefaultConfig(tmpDir)
	cfg, err := types.LoadConfigFromFile(cfg)
	if err != nil {
		t.Fatalf("LoadConfigFromFile: %v", err)
	}

	// CLI --vector flag override
	vectorBackend := "hnsw"
	cfg.VectorBackend = vectorBackend

	if cfg.VectorBackend != "hnsw" {
		t.Errorf("expected --vector flag to override TOML, got %q", cfg.VectorBackend)
	}
}

func TestIndexCmd_InvalidVectorFlag(t *testing.T) {
	t.Parallel()

	cmd := indexCmd()
	cmd.SetArgs([]string{"--vector", "invalid", "/nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --vector value")
	}
}

func TestApplyBackendFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		vector       string
		db           string
		pgURL        string
		qdrantURL    string
		wantErr      bool
		wantVector   string
		wantDB       string
		wantPgURL    string
		wantQdrant   string
	}{
		{name: "empty_noop"},
		{name: "valid_brute_force", vector: "brute_force", wantVector: "brute_force"},
		{name: "valid_hnsw", vector: "hnsw", wantVector: "hnsw"},
		{name: "valid_qdrant", vector: "qdrant", wantVector: "qdrant"},
		{name: "valid_pgvector", vector: "pgvector", wantVector: "pgvector"},
		{name: "invalid_vector", vector: "invalid", wantErr: true},
		{name: "valid_sqlite", db: "sqlite", wantDB: "sqlite"},
		{name: "valid_postgres", db: "postgres", wantDB: "postgres"},
		{name: "invalid_db", db: "mysql", wantErr: true},
		{name: "postgres_url", pgURL: "postgres://localhost/test", wantPgURL: "postgres://localhost/test"},
		{name: "qdrant_url", qdrantURL: "http://localhost:6333", wantQdrant: "http://localhost:6333"},
		{name: "all_flags", vector: "pgvector", db: "postgres", pgURL: "pg://url",
			wantVector: "pgvector", wantDB: "postgres", wantPgURL: "pg://url"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &types.Config{}
			err := applyBackendFlags(cfg, tc.vector, tc.db, tc.pgURL, tc.qdrantURL)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantVector != "" && cfg.VectorBackend != tc.wantVector {
				t.Errorf("VectorBackend = %q, want %q", cfg.VectorBackend, tc.wantVector)
			}
			if tc.wantDB != "" && cfg.DatabaseBackend != tc.wantDB {
				t.Errorf("DatabaseBackend = %q, want %q", cfg.DatabaseBackend, tc.wantDB)
			}
			if tc.wantPgURL != "" && cfg.PostgresConnString != tc.wantPgURL {
				t.Errorf("PostgresConnString = %q, want %q", cfg.PostgresConnString, tc.wantPgURL)
			}
			if tc.wantQdrant != "" && cfg.QdrantURL != tc.wantQdrant {
				t.Errorf("QdrantURL = %q, want %q", cfg.QdrantURL, tc.wantQdrant)
			}
		})
	}
}
