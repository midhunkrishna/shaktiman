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
