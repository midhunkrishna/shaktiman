package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/mcp"
	"github.com/shaktimanai/shaktiman/internal/observability"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
	"github.com/shaktimanai/shaktiman/internal/vector"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Daemon manages the lifecycle of the Shaktiman indexing system.
type Daemon struct {
	cfg              types.Config
	db               *storage.DB
	store            *storage.Store
	writer           *WriterManager
	engine           *core.QueryEngine
	logger           *slog.Logger
	vectorStore      *vector.BruteForceStore
	embedWorker      *vector.EmbedWorker
	sessionID        string
	embeddingActive  atomic.Bool
	embedMu          sync.Mutex
}

// New creates a new Daemon, opening the database and running migrations.
func New(cfg types.Config) (*Daemon, error) {
	db, err := storage.Open(storage.OpenInput{Path: cfg.DBPath})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := storage.Migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	store := storage.NewStore(db)
	engine := core.NewQueryEngine(store, cfg.ProjectRoot)
	writer := NewWriterManager(store, cfg.WriterChannelSize)

	// Session-aware ranking
	sessionStore := core.NewSessionStore(2000)
	engine.SetSessionStore(sessionStore)

	d := &Daemon{
		cfg:       cfg,
		db:        db,
		store:     store,
		writer:    writer,
		engine:    engine,
		logger:    slog.Default().With("component", "daemon"),
		sessionID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	// Initialize vector store + embedding pipeline
	if cfg.EmbedEnabled {
		d.initEmbedding()
	}

	// Wire vector deleter to writer for stale embedding cleanup
	if d.vectorStore != nil {
		d.writer.SetVectorDeleter(d.vectorStore)
	}

	return d, nil
}

func (d *Daemon) initEmbedding() {
	vs := vector.NewBruteForceStore(d.cfg.EmbeddingDims)
	if err := vs.LoadFromDisk(d.cfg.EmbeddingsPath); err != nil {
		d.logger.Warn("load embeddings from disk failed", "err", err)
	} else {
		count, _ := vs.Count(context.Background())
		if count > 0 {
			d.logger.Info("loaded embeddings from disk", "count", count)
		}
	}

	client := vector.NewOllamaClient(vector.OllamaClientInput{
		BaseURL: d.cfg.OllamaURL,
		Model:   d.cfg.EmbeddingModel,
	})

	worker := vector.NewEmbedWorker(vector.EmbedWorkerInput{
		Store:     vs,
		Embedder:  client,
		BatchSize: d.cfg.EmbedBatchSize,
		OnBatchDone: func(chunkIDs []int64) {
			if err := d.store.MarkChunksEmbedded(context.Background(), chunkIDs); err != nil {
				d.logger.Warn("mark chunks embedded failed", "err", err)
			}
		},
	})

	d.vectorStore = vs
	d.embedWorker = worker
	d.engine.SetVectorStore(vs, client, worker.EmbedReady)
}

// Start starts background services and the MCP server.
// It blocks until the context is cancelled or the MCP server exits.
func (d *Daemon) Start(ctx context.Context) error {
	// Start writer goroutine
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	go d.writer.Run(writerCtx)

	// Start embedding worker if enabled
	if d.embedWorker != nil {
		embedCtx, embedCancel := context.WithCancel(ctx)
		defer embedCancel()
		go d.embedWorker.Run(embedCtx)
		go d.periodicEmbeddingSave(embedCtx)
	}

	pipeline := NewEnrichmentPipeline(d.store, d.writer, d.cfg.EnrichmentWorkers)

	// Recover FTS triggers in case of previous crash between disable/enable
	if err := d.store.EnsureFTSTriggers(ctx); err != nil {
		d.logger.Warn("FTS trigger recovery failed", "err", err)
	}
	// Rebuild FTS if stale (crash during cold index left FTS out of sync)
	if stale, err := d.store.IsFTSStale(ctx); err != nil {
		d.logger.Warn("FTS staleness check failed", "err", err)
	} else if stale {
		d.logger.Warn("FTS index is stale, rebuilding")
		if err := d.store.RebuildFTS(ctx); err != nil {
			d.logger.Error("FTS rebuild on startup failed", "err", err)
		}
	}

	// Run cold indexing in background, then start watcher
	go func() {
		defer observability.Op(d.logger, "cold_index", "root", d.cfg.ProjectRoot)()

		scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: d.cfg.ProjectRoot})
		if err != nil {
			d.logger.Error("scan failed", "err", err)
			return
		}

		if err := pipeline.IndexAll(ctx, IndexAllInput{
			ProjectRoot: d.cfg.ProjectRoot,
			Files:       scanResult.Files,
		}); err != nil {
			d.logger.Error("indexing failed", "err", err)
			return
		}

		// Embed chunks after cold index using pull-based cursor
		if d.embedWorker != nil {
			if _, err := d.embedFromDB(ctx); err != nil {
				d.logger.Warn("cold-index embedding failed", "err", err)
			}
		}

		// Start file watcher after cold index
		if d.cfg.WatcherEnabled {
			d.startWatcher(ctx, pipeline)
		}
	}()

	// Start metrics recorder
	recorder := mcp.NewMetricsRecorder(mcp.MetricsRecorderInput{
		DB:        d.db.Writer(),
		SessionID: d.sessionID,
		Logger:    d.logger.With("component", "metrics"),
	})
	metricsCtx, metricsCancel := context.WithCancel(ctx)
	defer metricsCancel()
	go recorder.Run(metricsCtx)

	// Start MCP server (blocks on stdio)
	s := mcp.NewServer(mcp.NewServerInput{
		Engine:      d.engine,
		Store:       d.store,
		VectorStore: d.vectorStore,
		EmbedWorker: d.embedWorker,
		Recorder:    recorder,
		Config:      d.cfg,
	})
	d.logger.Info("MCP server starting on stdio", "session_id", d.sessionID)

	if err := mcpserver.ServeStdio(s); err != nil {
		return fmt.Errorf("MCP server: %w", err)
	}

	return nil
}

// queueEmbeddings queues chunks that need embedding, filtering by both DB flag
// (files.embedding_status != 'complete') and vector store contents (reconciliation).
func (d *Daemon) queueEmbeddings(ctx context.Context) {
	records, err := d.store.GetChunksNeedingEmbedding(ctx, d.vectorStore)
	if err != nil {
		d.logger.Warn("failed to query chunks for embedding", "err", err)
		return
	}

	// Option A filter: also skip chunks already in vector store (reconciliation
	// for crash-before-save scenario where DB says 'complete' but vectors are lost)
	jobs := make([]vector.EmbedJob, 0, len(records))
	for _, r := range records {
		if d.vectorStore.Has(ctx, r.ChunkID) {
			continue
		}
		jobs = append(jobs, vector.EmbedJob{
			ChunkID: r.ChunkID,
			Content: r.Content,
		})
	}

	if len(jobs) == 0 {
		d.logger.Info("no chunks need embedding")
		return
	}

	submitted := d.embedWorker.SubmitBatch(jobs)
	d.logger.Info("queued chunks for embedding",
		"candidates", len(records), "filtered", len(jobs), "submitted", submitted)
}

// embedFromDB runs the pull-based embedding pipeline to completion.
// It uses cursor-based DB pagination instead of the channel-based Submit path,
// ensuring zero data loss regardless of chunk count.
// Serialized via embedMu to prevent concurrent runs from cold-index and branch-switch.
func (d *Daemon) embedFromDB(ctx context.Context) (int, error) {
	d.embedMu.Lock()
	defer d.embedMu.Unlock()

	d.embeddingActive.Store(true)
	defer d.embeddingActive.Store(false)

	// Trigger an immediate save checkpoint so the periodic saver switches to
	// 30s intervals on its next tick (instead of waiting up to 5 minutes).
	if count, _ := d.vectorStore.Count(ctx); count > 0 {
		if err := d.vectorStore.SaveToDisk(d.cfg.EmbeddingsPath); err != nil {
			d.logger.Warn("pre-embed save failed", "err", err)
		}
	}

	if err := d.embedWorker.RunFromDB(ctx, d.store, func(p types.EmbedProgress) {
		d.logger.Info("embedding progress",
			"embedded", p.Embedded, "total", p.Total)
	}); err != nil {
		return 0, fmt.Errorf("RunFromDB: %w", err)
	}

	count, _ := d.vectorStore.Count(ctx)
	return count, nil
}

// EmbedProject runs the embedding pipeline synchronously. Used by CLI --embed.
// Uses pull-based DB cursor (RunFromDB) for zero data loss at any scale.
// Saves embeddings to disk on completion.
func (d *Daemon) EmbedProject(ctx context.Context) (int, error) {
	if d.embedWorker == nil {
		return 0, fmt.Errorf("embedding not initialized (check Ollama is running at %s)", d.cfg.OllamaURL)
	}

	count, err := d.embedFromDB(ctx)
	if err != nil {
		return count, err
	}

	if count > 0 {
		if err := d.vectorStore.SaveToDisk(d.cfg.EmbeddingsPath); err != nil {
			return count, fmt.Errorf("save embeddings: %w", err)
		}
	}
	return count, nil
}

// periodicEmbeddingSave checkpoints embeddings to disk. Uses 30s interval during
// active embedding (crash safety) and 5min interval otherwise. A short poll
// interval detects embedding-active transitions without waiting for a full 5min tick.
func (d *Daemon) periodicEmbeddingSave(ctx context.Context) {
	const activeInterval = 30 * time.Second
	const idleInterval = 5 * time.Minute
	const pollInterval = 10 * time.Second // how often to check if embedding started

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastSave := time.Now()
	for {
		select {
		case <-ticker.C:
			active := d.embeddingActive.Load()
			saveInterval := idleInterval
			if active {
				saveInterval = activeInterval
			}

			if time.Since(lastSave) < saveInterval {
				// Not time to save yet, but keep polling at short interval
				// so we detect embedding-active transitions quickly.
				if active {
					ticker.Reset(pollInterval)
				} else {
					ticker.Reset(idleInterval)
				}
				continue
			}

			count, _ := d.vectorStore.Count(ctx)
			if count > 0 {
				if err := d.vectorStore.SaveToDisk(d.cfg.EmbeddingsPath); err != nil {
					d.logger.Error("periodic embedding save failed", "err", err)
				} else {
					d.logger.Info("periodic embedding checkpoint", "count", count)
					lastSave = time.Now()
				}
			}

			if active {
				ticker.Reset(activeInterval)
			} else {
				ticker.Reset(idleInterval)
			}
		case <-ctx.Done():
			return
		}
	}
}

// Stop performs graceful shutdown with a 15-second timeout.
func (d *Daemon) Stop() error {
	start := time.Now()
	d.logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Wait for writer to finish draining (with timeout)
	select {
	case <-d.writer.Done():
	case <-shutdownCtx.Done():
		d.logger.Warn("writer drain timeout")
	}

	// Persist embeddings
	if d.vectorStore != nil {
		count, _ := d.vectorStore.Count(context.Background())
		if count > 0 {
			if err := d.vectorStore.SaveToDisk(d.cfg.EmbeddingsPath); err != nil {
				d.logger.Error("save embeddings failed", "err", err)
			} else {
				d.logger.Info("saved embeddings to disk", "count", count)
			}
		}
	}

	// Close database
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}

	d.logger.Info("shutdown complete", "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// Engine returns the query engine.
func (d *Daemon) Engine() *core.QueryEngine {
	return d.engine
}

// Store returns the metadata store.
func (d *Daemon) Store() *storage.Store {
	return d.store
}

// startWatcher launches the file watcher and pipes events to incremental enrichment.
func (d *Daemon) startWatcher(ctx context.Context, pipeline *EnrichmentPipeline) {
	w, err := NewWatcher(d.cfg.ProjectRoot, d.cfg.WatcherDebounceMs)
	if err != nil {
		d.logger.Error("failed to start watcher", "err", err)
		return
	}

	d.logger.Info("file watcher starting", "root", d.cfg.ProjectRoot)

	// Process watcher events — registered as a writer producer so
	// drain() waits for in-flight EnrichFile calls before closing.
	d.writer.AddProducer()
	go func() {
		defer d.writer.RemoveProducer()
		for event := range w.Events() {
			if err := pipeline.EnrichFile(ctx, event); err != nil {
				d.logger.Warn("incremental enrich failed",
					"path", event.Path,
					"err", err)
			}
		}
	}()

	// Handle branch switch signals: re-scan and re-index
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.BranchSwitchCh():
				d.logger.Info("branch switch: re-scanning project")
				scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: d.cfg.ProjectRoot})
				if err != nil {
					d.logger.Error("branch switch scan failed", "err", err)
					continue
				}
				if err := pipeline.IndexAll(ctx, IndexAllInput{
					ProjectRoot: d.cfg.ProjectRoot,
					Files:       scanResult.Files,
				}); err != nil {
					d.logger.Error("branch switch index failed", "err", err)
					continue
				}
				if d.embedWorker != nil {
					if _, err := d.embedFromDB(ctx); err != nil {
						d.logger.Warn("branch-switch embedding failed", "err", err)
					}
				}
				d.logger.Info("branch switch re-index complete")
			}
		}
	}()

	// Start blocks until ctx is cancelled
	if err := w.Start(ctx); err != nil {
		d.logger.Error("watcher stopped", "err", err)
	}
}

// IndexProject runs cold indexing synchronously. Used by CLI.
func (d *Daemon) IndexProject(ctx context.Context) error {
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	go d.writer.Run(writerCtx)

	scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: d.cfg.ProjectRoot})
	if err != nil {
		return fmt.Errorf("scan repo: %w", err)
	}

	pipeline := NewEnrichmentPipeline(d.store, d.writer, d.cfg.EnrichmentWorkers)
	if err := pipeline.IndexAll(ctx, IndexAllInput{
		ProjectRoot: d.cfg.ProjectRoot,
		Files:       scanResult.Files,
	}); err != nil {
		return fmt.Errorf("index all: %w", err)
	}

	writerCancel()
	<-d.writer.Done()
	return nil
}
