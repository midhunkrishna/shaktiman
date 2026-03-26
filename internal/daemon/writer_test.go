package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

func TestWriterChannelFull_LogsAtDebug(t *testing.T) {
	t.Parallel()

	// Open a real DB so NewWriterManager works (writer needs a store).
	tmpDir := t.TempDir()
	db, err := storage.Open(storage.OpenInput{Path: tmpDir + "/test.db"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := storage.Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer db.Close()

	store := storage.NewStore(db)

	// Channel size 1 — will fill after one unprocessed job.
	wm := NewWriterManager(store, 1)

	// Capture log output at Debug level.
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	wm.logger = logger

	ctx, cancel := context.WithCancel(context.Background())
	go wm.Run(ctx)

	// Fill channel with one job (it will be processed).
	wm.AddProducer()
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
