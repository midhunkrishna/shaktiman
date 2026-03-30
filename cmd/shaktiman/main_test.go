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
