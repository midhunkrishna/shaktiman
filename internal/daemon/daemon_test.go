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
			name:          "Groovy",
			project:       "groovy_project",
			language:      "groovy",
			expectFiles:   2,
			expectChunks:  2,
			expectSymbols: 2,
			searchQuery:   "addUser removeUser",
			expectSymbol:  "addUser",
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
			if err := d.IndexProject(ctx); err != nil {
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
		"config.groovy": `def loadConfig(String path) {
    def config = new ConfigSlurper().parse(new File(path).toURL())
    return config
}

def mergeConfigs(Map base, Map overrides) {
    def merged = new HashMap(base)
    merged.putAll(overrides)
    return merged
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

	expectedLangs := []string{"go", "typescript", "python", "java", "groovy", "bash", "javascript"}
	for _, lang := range expectedLangs {
		if langCounts[lang] == 0 {
			t.Errorf("expected at least one %s file detected, got 0 (detected: %v)", lang, langCounts)
		}
	}

	// Index everything
	if err := d.IndexProject(ctx); err != nil {
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
		"loadConfig",     // Groovy
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
	if err := d1.IndexProject(ctx); err != nil {
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
	if err := d2.IndexProject(ctx); err != nil {
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
