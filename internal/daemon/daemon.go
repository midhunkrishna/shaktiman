package daemon

import (
	"context"
	"fmt"
	"log/slog"
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
	cfg         types.Config
	db          *storage.DB
	store       *storage.Store
	writer      *WriterManager
	engine      *core.QueryEngine
	logger      *slog.Logger
	vectorStore *vector.BruteForceStore
	embedWorker *vector.EmbedWorker
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

	d := &Daemon{
		cfg:    cfg,
		db:     db,
		store:  store,
		writer: writer,
		engine: engine,
		logger: slog.Default().With("component", "daemon"),
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

		// Queue chunks for embedding after cold index
		if d.embedWorker != nil {
			d.queueEmbeddings(ctx)
		}

		// Start file watcher after cold index
		if d.cfg.WatcherEnabled {
			d.startWatcher(ctx, pipeline)
		}
	}()

	// Start MCP server (blocks on stdio)
	s := mcp.NewServer(d.engine, d.store, d.vectorStore, d.embedWorker)
	d.logger.Info("MCP server starting on stdio")

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

// periodicEmbeddingSave checkpoints embeddings to disk every 5 minutes.
func (d *Daemon) periodicEmbeddingSave(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			count, _ := d.vectorStore.Count(ctx)
			if count > 0 {
				if err := d.vectorStore.SaveToDisk(d.cfg.EmbeddingsPath); err != nil {
					d.logger.Error("periodic embedding save failed", "err", err)
				} else {
					d.logger.Info("periodic embedding checkpoint", "count", count)
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// Stop performs graceful shutdown.
func (d *Daemon) Stop() error {
	d.logger.Info("shutting down")

	// Wait for writer to finish draining
	<-d.writer.Done()

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

	d.logger.Info("shutdown complete")
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

	// Process watcher events
	go func() {
		for event := range w.Events() {
			if err := pipeline.EnrichFile(ctx, event); err != nil {
				d.logger.Warn("incremental enrich failed",
					"path", event.Path,
					"err", err)
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
