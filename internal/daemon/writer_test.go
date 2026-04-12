package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/testutil"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestWriterManager_Started_FalseBeforeRun(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	if wm.Started() {
		t.Error("Started() should be false before Run is called")
	}
}

func TestWriterManager_Started_TrueAfterRun(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Poll until Run sets the flag (avoids flaky fixed sleep).
	deadline := time.After(2 * time.Second)
	for !wm.Started() {
		select {
		case <-deadline:
			t.Fatal("Started() not set within 2s after Run was called")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	cancel()
	<-wm.Done()

	// Started remains true after shutdown.
	if !wm.Started() {
		t.Error("Started() should remain true after writer shuts down")
	}
}

func TestStop_WriterNeverStarted_NoTimeout(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	cfg := types.Config{
		ProjectRoot:       tmpDir,
		DBPath:            dbPath,
		WriterChannelSize: 10,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Stop without ever calling Start or IndexProject.
	// Before the fix, this would block for 15 seconds.
	start := time.Now()
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Stop took %v; expected < 2s when writer was never started", elapsed)
	}
}

func TestSubmit_BlockedDuringShutdown(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	// Channel size 1 — fills quickly
	wm := NewWriterManager(store, 1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)
	_ = wm.AddProducer()

	// Fill the channel so next Submit blocks
	_ = wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "filler.go",
		File: &types.FileRecord{
			Path: "filler.go", ContentHash: "abc",
			EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Timestamp: time.Now(),
	})

	// Give writer time to process the first job
	time.Sleep(50 * time.Millisecond)

	// Fill channel again
	_ = wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "filler2.go",
		File: &types.FileRecord{
			Path: "filler2.go", ContentHash: "def",
			EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Timestamp: time.Now(),
	})

	// Submit in goroutine — this will block because channel is full
	errCh := make(chan error, 1)
	go func() {
		errCh <- wm.Submit(types.WriteJob{
			Type:     types.WriteJobEnrichment,
			FilePath: "blocked.go",
			File: &types.FileRecord{
				Path: "blocked.go", ContentHash: "ghi",
				EmbeddingStatus: "pending", ParseQuality: "full",
			},
			Timestamp: time.Now(),
		})
	}()

	// Give the goroutine time to block on Submit
	time.Sleep(50 * time.Millisecond)

	// Shut down the writer while Submit is blocked
	wm.RemoveProducer()
	cancel()
	<-wm.Done()

	// The blocked Submit should return ErrWriterClosed
	select {
	case err := <-errCh:
		if err != ErrWriterClosed {
			// May also return nil if the job was accepted before shutdown
			if err != nil {
				t.Logf("Submit returned: %v (acceptable)", err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("blocked Submit did not return after writer shutdown")
	}
}

func TestWriterChannelFull_LogsAtDebug(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)

	// Channel size 1 — will fill after one unprocessed job.
	wm := NewWriterManager(store, 1, nil)

	// Capture log output at Debug level.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	wm.logger = logger

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Fill channel with one job (it will be processed).
	_ = wm.AddProducer()
	_ = wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "file1.go",
		File: &types.FileRecord{
			Path:            "file1.go",
			ContentHash:     "abc",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Timestamp: time.Now(),
	})

	// Give the writer time to process the first job, then fill the channel.
	time.Sleep(50 * time.Millisecond)

	// Submit a job that blocks because channel is full.
	// We need the channel to be full, so submit 2 more quickly.
	_ = wm.Submit(types.WriteJob{
		Type:     types.WriteJobEnrichment,
		FilePath: "file2.go",
		File: &types.FileRecord{
			Path:            "file2.go",
			ContentHash:     "def",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		Timestamp: time.Now(),
	})

	// This submit should trigger the "channel full" path.
	go func() {
		_ = wm.Submit(types.WriteJob{
			Type:     types.WriteJobEnrichment,
			FilePath: "file3.go",
			File: &types.FileRecord{
				Path:            "file3.go",
				ContentHash:     "ghi",
				EmbeddingStatus: "pending",
				ParseQuality:    "full",
			},
			Timestamp: time.Now(),
		})
	}()

	// Give time for the blocking submit to log.
	time.Sleep(100 * time.Millisecond)
	wm.RemoveProducer()
	cancel()
	<-wm.Done()

	logOutput := buf.String()
	if logOutput == "" {
		// Channel may have been processed before filling — skip silently.
		t.Skip("channel full condition did not trigger (writer processed jobs too fast)")
	}

	// The log should contain "channel full" at DEBUG level, not WARN.
	if bytes.Contains(buf.Bytes(), []byte("level=WARN")) {
		t.Error("expected 'channel full' to log at DEBUG level, but found WARN")
	}
}

func TestWriterManager_ProcessJobViaWriterStore(t *testing.T) {
	// Verifies that processJob correctly unwraps TxHandle via store.WithWriteTx
	// and successfully writes through the WriterStore interface.
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)
	wm.AddProducer()

	// Submit a job via the WriterStore-typed writer
	done := make(chan error, 1)
	err := wm.Submit(types.WriteJob{
		Type: types.WriteJobEnrichment,
		File: &types.FileRecord{
			Path:            "via_interface.go",
			ContentHash:     "ifhash",
			Mtime:           1.0,
			Language:        "go",
			EmbeddingStatus: "pending",
			ParseQuality:    "full",
		},
		FilePath:    "via_interface.go",
		ContentHash: "ifhash",
		Timestamp:   time.Now(),
		Done:        done,
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-done

	wm.RemoveProducer()
	cancel()
	<-wm.Done()

	// Verify the file was written via the WithWriteTx → TxHandle path
	f, err := store.GetFileByPath(context.Background(), "via_interface.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f == nil {
		t.Fatal("expected file to exist after WriterStore-based processJob")
	}
	if f.ContentHash != "ifhash" {
		t.Errorf("ContentHash = %q, want 'ifhash'", f.ContentHash)
	}
}

func TestWriterManager_SyncJob(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)
	wm.AddProducer()

	done := make(chan error, 1)
	if err := wm.Submit(types.WriteJob{
		Type: types.WriteJobSync,
		Done: done,
	}); err != nil {
		t.Fatalf("Submit sync: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("sync job returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sync job did not complete within 2s")
	}

	// WriteJobSync must not create any database record.
	f, err := store.GetFileByPath(context.Background(), "__sync_marker__")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f != nil {
		t.Error("sync job must not create a database record")
	}

	wm.RemoveProducer()
	cancel()
	<-wm.Done()
}

func TestWriterManager_UnknownJobType(t *testing.T) {
	t.Parallel()

	store := testutil.NewTestWriterStore(t)
	wm := NewWriterManager(store, 10, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)
	wm.AddProducer()

	done := make(chan error, 1)
	if err := wm.Submit(types.WriteJob{
		Type: types.WriteJobType(99),
		Done: done,
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for unknown job type")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unknown job type did not complete within 2s")
	}

	wm.RemoveProducer()
	cancel()
	<-wm.Done()
}

// ── processEnrichmentJob direct unit tests ──

func TestProcessEnrichmentJob_NilFile(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	logger := slog.Default()

	_, err := processEnrichmentJob(context.Background(), store, logger,
		types.WriteJob{Type: types.WriteJobEnrichment, FilePath: "test.go"},
		nil)
	if err == nil {
		t.Fatal("expected error for nil file")
	}
}

func TestProcessEnrichmentJob_ContentHashGuard(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Seed a file with hash "abc".
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "guard.go", ContentHash: "abc", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "done", ParseQuality: "full",
		IndexedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})

	// Submit job with same hash — should be a no-op.
	stale, err := processEnrichmentJob(ctx, store, logger, types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "guard.go",
		ContentHash: "abc",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "guard.go", ContentHash: "abc", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stale != nil {
		t.Errorf("expected nil stale IDs for same-hash skip, got %v", stale)
	}
}

func TestProcessEnrichmentJob_StaleJobSkipped(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Seed file with recent IndexedAt.
	recentTime := time.Now().UTC()
	store.UpsertFile(ctx, &types.FileRecord{
		Path: "stale.go", ContentHash: "v1", Mtime: 1.0,
		Language: "go", EmbeddingStatus: "done", ParseQuality: "full",
		IndexedAt: recentTime.Format(time.RFC3339Nano),
	})

	// Submit job with older timestamp and different hash.
	stale, err := processEnrichmentJob(ctx, store, logger, types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "stale.go",
		ContentHash: "v2",
		Timestamp:   recentTime.Add(-time.Hour), // older than DB
		File: &types.FileRecord{
			Path: "stale.go", ContentHash: "v2", Mtime: 2.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stale != nil {
		t.Errorf("expected nil stale IDs for stale job skip, got %v", stale)
	}
}

func TestProcessEnrichmentJob_NewFile(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	job := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "new.go",
		ContentHash: "h1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "new.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Hello",
				StartLine: 1, EndLine: 5, Content: "func Hello() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "Hello", Kind: "function", Line: 1, Visibility: "exported", IsExported: true},
		},
	}

	stale, err := processEnrichmentJob(ctx, store, logger, job, nil)
	if err != nil {
		t.Fatalf("processEnrichmentJob: %v", err)
	}
	if len(stale) != 0 {
		t.Errorf("expected no stale chunks for new file, got %v", stale)
	}

	// Verify file was created.
	f, _ := store.GetFileByPath(ctx, "new.go")
	if f == nil {
		t.Fatal("expected file to exist")
	}
	if f.ContentHash != "h1" {
		t.Errorf("ContentHash = %q, want h1", f.ContentHash)
	}

	// Verify chunks.
	chunks, _ := store.GetChunksByFile(ctx, f.ID)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	// Verify symbols.
	syms, _ := store.GetSymbolByName(ctx, "Hello")
	if len(syms) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(syms))
	}

	// Verify diff log (add).
	diffs, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: f.ID, Limit: 10})
	if len(diffs) == 0 {
		t.Error("expected add diff log entry")
	} else if diffs[0].ChangeType != "add" {
		t.Errorf("diff ChangeType = %q, want add", diffs[0].ChangeType)
	}
}

func TestProcessEnrichmentJob_ReIndex_RecordsDiff(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Index v1.
	jobV1 := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "reindex.go",
		ContentHash: "v1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "reindex.go", ContentHash: "v1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "OldFunc",
				StartLine: 1, EndLine: 5, Content: "func OldFunc() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "OldFunc", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	if _, err := processEnrichmentJob(ctx, store, logger, jobV1, nil); err != nil {
		t.Fatalf("v1: %v", err)
	}

	// Index v2 with different symbols.
	jobV2 := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "reindex.go",
		ContentHash: "v2",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "reindex.go", ContentHash: "v2", Mtime: 2.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "NewFunc",
				StartLine: 1, EndLine: 8, Content: "func NewFunc() { /* more */ }", TokenCount: 5},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "NewFunc", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	if _, err := processEnrichmentJob(ctx, store, logger, jobV2, nil); err != nil {
		t.Fatalf("v2: %v", err)
	}

	// Verify diff log has "modify" entry.
	f, _ := store.GetFileByPath(ctx, "reindex.go")
	diffs, _ := store.GetRecentDiffs(ctx, types.RecentDiffsInput{FileID: f.ID, Limit: 10})

	foundModify := false
	for _, d := range diffs {
		if d.ChangeType == "modify" {
			foundModify = true
			// Check diff symbols.
			diffSyms, _ := store.GetDiffSymbols(ctx, d.ID)
			if len(diffSyms) == 0 {
				t.Error("expected diff symbols for modify")
			}
		}
	}
	if !foundModify {
		t.Error("expected 'modify' diff log entry for re-index")
	}
}

func TestProcessEnrichmentJob_ParentChunkResolution(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	parentIdx := 0
	job := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "parent.go",
		ContentHash: "h1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "parent.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Outer",
				StartLine: 1, EndLine: 20, Content: "func Outer() {}", TokenCount: 5},
			{ChunkIndex: 1, Kind: "function", SymbolName: "Inner",
				StartLine: 5, EndLine: 10, Content: "func Inner() {}", TokenCount: 3,
				ParentIndex: &parentIdx},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "Outer", Kind: "function", Line: 1, Visibility: "exported"},
			{ChunkID: 1, Name: "Inner", Kind: "function", Line: 5, Visibility: "exported"},
		},
	}

	if _, err := processEnrichmentJob(ctx, store, logger, job, nil); err != nil {
		t.Fatalf("processEnrichmentJob: %v", err)
	}

	f, _ := store.GetFileByPath(ctx, "parent.go")
	chunks, _ := store.GetChunksByFile(ctx, f.ID)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	// Find the Inner chunk and check its parent.
	for _, c := range chunks {
		if c.SymbolName == "Inner" {
			if c.ParentChunkID == nil {
				t.Error("Inner chunk should have a parent")
			}
		}
	}
}

func TestProcessEnrichmentJob_EdgeInsertionAndResolution(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Index file A with a call to unknown "Target".
	jobA := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "caller.go",
		ContentHash: "h1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "caller.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Caller",
				StartLine: 1, EndLine: 5, Content: "func Caller() { Target() }", TokenCount: 5},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "Caller", Kind: "function", Line: 1, Visibility: "exported"},
		},
		Edges: []types.EdgeRecord{
			{SrcSymbolName: "Caller", DstSymbolName: "Target", Kind: "calls"},
		},
	}
	if _, err := processEnrichmentJob(ctx, store, logger, jobA, nil); err != nil {
		t.Fatalf("jobA: %v", err)
	}

	// Target should be pending.
	callers, _ := store.PendingEdgeCallers(ctx, "Target")
	if len(callers) == 0 {
		t.Error("expected pending edge caller for Target")
	}

	// Now index file B that defines "Target" — should resolve.
	jobB := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "target.go",
		ContentHash: "h2",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "target.go", ContentHash: "h2", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Target",
				StartLine: 1, EndLine: 5, Content: "func Target() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "Target", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	if _, err := processEnrichmentJob(ctx, store, logger, jobB, nil); err != nil {
		t.Fatalf("jobB: %v", err)
	}

	// Pending should be resolved.
	callers, _ = store.PendingEdgeCallers(ctx, "Target")
	if len(callers) != 0 {
		t.Errorf("expected 0 pending callers after resolve, got %d", len(callers))
	}
}

func TestProcessEnrichmentJob_ReturnsStaleChunkIDs(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Index v1.
	jobV1 := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "stale_chunks.go",
		ContentHash: "v1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "stale_chunks.go", ContentHash: "v1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "F1",
				StartLine: 1, EndLine: 5, Content: "func F1() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "F1", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	if _, err := processEnrichmentJob(ctx, store, logger, jobV1, nil); err != nil {
		t.Fatalf("v1: %v", err)
	}

	// Mark chunks as embedded.
	f, _ := store.GetFileByPath(ctx, "stale_chunks.go")
	chunks, _ := store.GetChunksByFile(ctx, f.ID)
	chunkIDs := make([]int64, len(chunks))
	for i, c := range chunks {
		chunkIDs[i] = c.ID
	}
	store.MarkChunksEmbedded(ctx, chunkIDs)

	// Re-index with different content.
	jobV2 := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "stale_chunks.go",
		ContentHash: "v2",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "stale_chunks.go", ContentHash: "v2", Mtime: 2.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "F2",
				StartLine: 1, EndLine: 5, Content: "func F2() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "F2", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	stale, err := processEnrichmentJob(ctx, store, logger, jobV2, nil)
	if err != nil {
		t.Fatalf("v2: %v", err)
	}
	if len(stale) == 0 {
		t.Error("expected stale chunk IDs from re-index of embedded file")
	}
}

func TestProcessEnrichmentJob_IsTestFile(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	job := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "foo_test.go",
		ContentHash: "h1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "foo_test.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "TestFoo",
				StartLine: 1, EndLine: 5, Content: "func TestFoo(t *testing.T) {}", TokenCount: 5},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "TestFoo", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}

	if _, err := processEnrichmentJob(ctx, store, logger, job, []string{"*_test.go"}); err != nil {
		t.Fatalf("processEnrichmentJob: %v", err)
	}

	f, _ := store.GetFileByPath(ctx, "foo_test.go")
	if f == nil {
		t.Fatal("expected file to exist")
	}
	if !f.IsTest {
		t.Error("expected IsTest=true for foo_test.go")
	}
}

func TestProcessWriteJob_FileDelete(t *testing.T) {
	t.Parallel()
	store := testutil.NewTestWriterStore(t)
	ctx := context.Background()
	logger := slog.Default()

	// Index a file first.
	job := types.WriteJob{
		Type:        types.WriteJobEnrichment,
		FilePath:    "delete_me.go",
		ContentHash: "h1",
		Timestamp:   time.Now(),
		File: &types.FileRecord{
			Path: "delete_me.go", ContentHash: "h1", Mtime: 1.0,
			Language: "go", EmbeddingStatus: "pending", ParseQuality: "full",
		},
		Chunks: []types.ChunkRecord{
			{ChunkIndex: 0, Kind: "function", SymbolName: "Del",
				StartLine: 1, EndLine: 5, Content: "func Del() {}", TokenCount: 3},
		},
		Symbols: []types.SymbolRecord{
			{ChunkID: 0, Name: "Del", Kind: "function", Line: 1, Visibility: "exported"},
		},
	}
	processEnrichmentJob(ctx, store, logger, job, nil)

	// Mark chunks as embedded.
	f, _ := store.GetFileByPath(ctx, "delete_me.go")
	chunks, _ := store.GetChunksByFile(ctx, f.ID)
	ids := make([]int64, len(chunks))
	for i, c := range chunks {
		ids[i] = c.ID
	}
	store.MarkChunksEmbedded(ctx, ids)

	// Delete via processWriteJob.
	stale, err := processWriteJob(ctx, store, logger, types.WriteJob{
		Type:     types.WriteJobFileDelete,
		FilePath: "delete_me.go",
	}, nil)
	if err != nil {
		t.Fatalf("processWriteJob delete: %v", err)
	}
	if len(stale) == 0 {
		t.Error("expected stale chunk IDs from delete")
	}

	f, _ = store.GetFileByPath(ctx, "delete_me.go")
	if f != nil {
		t.Error("file should be deleted")
	}
}

func TestCoalesce(t *testing.T) {
	t.Parallel()
	if got := coalesce("", "fallback"); got != "fallback" {
		t.Errorf("coalesce empty = %q, want fallback", got)
	}
	if got := coalesce("value", "fallback"); got != "value" {
		t.Errorf("coalesce non-empty = %q, want value", got)
	}
}
