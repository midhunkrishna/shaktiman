package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
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
	dbCloser         func() error             // closes the underlying database
	store            types.WriterStore
	lifecycle        types.StoreLifecycle     // nil if backend needs no lifecycle hooks
	writer           *WriterManager
	engine           *core.QueryEngine
	logger           *slog.Logger
	vectorStore      types.VectorStore
	embedWorker      *vector.EmbedWorker
	sessionID        string
	embeddingActive  atomic.Bool
	embedMu          sync.Mutex
	serveFunc        func(*mcpserver.MCPServer) error // injectable for testing; defaults to mcpserver.ServeStdio

	// Configurable intervals for periodicEmbeddingSave (tests use short values).
	saveActiveInterval time.Duration
	saveIdleInterval   time.Duration
	savePollInterval   time.Duration
}

// New creates a new Daemon, opening the database and running migrations.
func New(cfg types.Config) (*Daemon, error) {
	store, lifecycle, dbCloser, err := storage.NewMetadataStore(storage.MetadataStoreConfig{
		Backend:    cfg.DatabaseBackend,
		SQLitePath: cfg.DBPath,
	})
	if err != nil {
		return nil, fmt.Errorf("create metadata store: %w", err)
	}

	engine := core.NewQueryEngine(store, cfg.ProjectRoot)
	writer := NewWriterManager(store, cfg.WriterChannelSize, cfg.TestPatterns)

	// Session-aware ranking
	sessionStore := core.NewSessionStore(2000)
	engine.SetSessionStore(sessionStore)

	d := &Daemon{
		cfg:       cfg,
		dbCloser:  dbCloser,
		store:     store,
		lifecycle: lifecycle,
		writer:    writer,
		engine:    engine,
		logger:    slog.Default().With("component", "daemon"),
		sessionID: fmt.Sprintf("%d", time.Now().UnixNano()),
		serveFunc:          func(s *mcpserver.MCPServer) error { return mcpserver.ServeStdio(s) },
		saveActiveInterval: 30 * time.Second,
		saveIdleInterval:   5 * time.Minute,
		savePollInterval:   10 * time.Second,
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
	vs, err := d.newVectorStore()
	if err != nil {
		d.logger.Error("create vector store failed", "err", err)
		return
	}
	d.vectorStore = vs

	if err := d.loadVectors(d.embeddingsPath()); err != nil {
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

	pipeline := NewEnrichmentPipeline(d.store, d.writer, d.cfg.EnrichmentWorkers, d.lifecycle)

	// Run backend-specific startup hooks (FTS trigger recovery for SQLite)
	if d.lifecycle != nil {
		if err := d.lifecycle.OnStartup(ctx); err != nil {
			d.logger.Warn("store lifecycle startup failed", "err", err)
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
			if _, err := d.embedFromDB(ctx, nil); err != nil {
				d.logger.Warn("cold-index embedding failed", "err", err)
			}
		}

		// Start file watcher after cold index
		if d.cfg.WatcherEnabled {
			d.startWatcher(ctx, pipeline)
		}
	}()

	// Start metrics recorder.
	// MetricsRecorder needs raw *sql.DB — access via concrete Store.DB().Writer().
	// This will be replaced with a MetricsWriter interface in a future PR.
	var recorder *mcp.MetricsRecorder
	if sqliteStore, ok := d.store.(*storage.Store); ok {
		recorder = mcp.NewMetricsRecorder(mcp.MetricsRecorderInput{
			DB:        sqliteStore.DB().Writer(),
			SessionID: d.sessionID,
			Logger:    d.logger.With("component", "metrics"),
		})
	}
	if recorder != nil {
		metricsCtx, metricsCancel := context.WithCancel(ctx)
		defer metricsCancel()
		go recorder.Run(metricsCtx)
	}

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

	if err := d.serveFunc(s); err != nil {
		return fmt.Errorf("MCP server: %w", err)
	}

	return nil
}


// embedFromDB runs the pull-based embedding pipeline to completion.
// It uses cursor-based DB pagination instead of the channel-based Submit path,
// ensuring zero data loss regardless of chunk count.
// Serialized via embedMu to prevent concurrent runs from cold-index and branch-switch.
func (d *Daemon) embedFromDB(ctx context.Context, onProgress func(types.EmbedProgress)) (int, error) {
	d.embedMu.Lock()
	defer d.embedMu.Unlock()

	d.embeddingActive.Store(true)
	defer d.embeddingActive.Store(false)

	// Trigger an immediate save checkpoint so the periodic saver switches to
	// 30s intervals on its next tick (instead of waiting up to 5 minutes).
	if count, _ := d.vectorStore.Count(ctx); count > 0 {
		if err := d.saveVectors(d.embeddingsPath()); err != nil {
			d.logger.Warn("pre-embed save failed", "err", err)
		}
	}

	if onProgress == nil {
		onProgress = func(p types.EmbedProgress) {
			d.logger.Info("embedding progress",
				"embedded", p.Embedded, "total", p.Total)
		}
	}

	err := d.embedWorker.RunFromDB(ctx, d.store, onProgress)

	// Return vector count even on partial failure so callers can report progress.
	count, _ := d.vectorStore.Count(ctx)
	if err != nil {
		return count, fmt.Errorf("RunFromDB: %w", err)
	}
	return count, nil
}

// EmbedProject runs the embedding pipeline synchronously. Used by CLI --embed.
// Uses pull-based DB cursor (RunFromDB) for zero data loss at any scale.
// Saves embeddings to disk on completion (including partial failure).
func (d *Daemon) EmbedProject(ctx context.Context, onProgress func(types.EmbedProgress)) (int, error) {
	if d.embedWorker == nil {
		return 0, fmt.Errorf("embedding not initialized (check Ollama is running at %s)", d.cfg.OllamaURL)
	}

	if !d.embedWorker.EmbedderHealthy(ctx) {
		return 0, fmt.Errorf("Ollama is not reachable at %s", d.cfg.OllamaURL)
	}

	// Reverse reconciliation: if vector store has fewer vectors than DB claims
	// are embedded, reconcile. Two cases:
	//   vectorCount == 0: embeddings.bin deleted entirely → nuclear reset
	//   vectorCount > 0:  partial loss (e.g. crash mid-save) → targeted reset
	embeddedInDB, _ := d.store.CountChunksEmbedded(ctx)
	vectorCount, _ := d.vectorStore.Count(ctx)
	if embeddedInDB > 0 && vectorCount == 0 {
		d.logger.Info("vector store empty but DB has embedded chunks, resetting all flags",
			"db_embedded", embeddedInDB)
		if err := d.store.ResetAllEmbeddedFlags(ctx); err != nil {
			return 0, fmt.Errorf("reset embedded flags: %w", err)
		}
	} else if embeddedInDB > 0 && vectorCount < embeddedInDB {
		d.logger.Info("vector store partially out of sync with DB, reconciling",
			"db_embedded", embeddedInDB, "vector_count", vectorCount)
		if err := d.reconcileEmbeddedFlags(ctx); err != nil {
			return 0, fmt.Errorf("reconcile embedded flags: %w", err)
		}
	}

	count, err := d.embedFromDB(ctx, onProgress)

	// Save embeddings even on partial failure (crash safety).
	if count > 0 {
		if saveErr := d.saveVectors(d.embeddingsPath()); saveErr != nil {
			d.logger.Warn("save embeddings failed", "err", saveErr)
		}
	}

	if err != nil {
		return count, err
	}
	return count, nil
}

// reconcileEmbeddedFlags finds chunks marked embedded in the DB whose vectors
// are missing from the store, and resets only those flags. This handles partial
// vector loss (e.g. process killed between periodic saves) without re-embedding
// chunks whose vectors are still intact.
func (d *Daemon) reconcileEmbeddedFlags(ctx context.Context) error {
	const pageSize = 256
	var missing []int64
	afterID := int64(0)

	for {
		ids, err := d.store.GetEmbeddedChunkIDs(ctx, afterID, pageSize)
		if err != nil {
			return fmt.Errorf("get embedded chunk IDs after %d: %w", afterID, err)
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			has, err := d.vectorStore.Has(ctx, id)
			if err != nil {
				return fmt.Errorf("check vector store for chunk %d: %w", id, err)
			}
			if !has {
				missing = append(missing, id)
			}
		}
		afterID = ids[len(ids)-1]
	}

	if len(missing) == 0 {
		return nil
	}

	d.logger.Info("resetting embedded flags for missing vectors",
		"missing_count", len(missing))
	return d.store.ResetEmbeddedFlags(ctx, missing)
}

// RunPeriodicEmbeddingSave is an exported wrapper around periodicEmbeddingSave.
// Used by the CLI to run periodic checkpoint saves during embedding.
func (d *Daemon) RunPeriodicEmbeddingSave(ctx context.Context) {
	if d.vectorStore == nil {
		return
	}
	d.periodicEmbeddingSave(ctx)
}

// periodicEmbeddingSave checkpoints embeddings to disk. Uses 30s interval during
// active embedding (crash safety) and 5min interval otherwise. A short poll
// interval detects embedding-active transitions without waiting for a full 5min tick.
func (d *Daemon) periodicEmbeddingSave(ctx context.Context) {
	activeInterval := d.saveActiveInterval
	idleInterval := d.saveIdleInterval
	pollInterval := d.savePollInterval

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
				if err := d.saveVectors(d.embeddingsPath()); err != nil {
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

	// Wait for writer to finish draining (with timeout).
	// Skip if Run() was never called (e.g. CLI-only use of IndexProject/EmbedProject).
	if d.writer.Started() {
		select {
		case <-d.writer.Done():
		case <-shutdownCtx.Done():
			d.logger.Warn("writer drain timeout")
		}
	}

	// Persist embeddings
	if d.vectorStore != nil {
		count, _ := d.vectorStore.Count(context.Background())
		if count > 0 {
			if err := d.saveVectors(d.embeddingsPath()); err != nil {
				d.logger.Error("save embeddings failed", "err", err)
			} else {
				d.logger.Info("saved embeddings to disk", "count", count)
			}
		}
	}

	// Close database
	if d.dbCloser != nil {
		if err := d.dbCloser(); err != nil {
			return fmt.Errorf("close database: %w", err)
		}
	}

	d.logger.Info("shutdown complete", "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// saveVectors persists vectors if the store supports explicit persistence.
func (d *Daemon) saveVectors(path string) error {
	if p, ok := d.vectorStore.(types.VectorPersister); ok {
		return p.SaveToDisk(path)
	}
	return nil
}

// loadVectors loads vectors if the store supports explicit persistence.
func (d *Daemon) loadVectors(path string) error {
	if p, ok := d.vectorStore.(types.VectorPersister); ok {
		return p.LoadFromDisk(path)
	}
	return nil
}

// newVectorStore creates a vector store based on the configured backend.
func (d *Daemon) newVectorStore() (types.VectorStore, error) {
	switch d.cfg.VectorBackend {
	case "hnsw":
		return vector.NewHNSWStore(vector.HNSWStoreInput{
			Dim: d.cfg.EmbeddingDims,
		})
	default:
		return vector.NewBruteForceStore(d.cfg.EmbeddingDims), nil
	}
}

// embeddingsPath returns the persistence file path for the active vector backend.
// HNSW and BruteForce use incompatible binary formats, so each gets a distinct path.
func (d *Daemon) embeddingsPath() string {
	if d.cfg.VectorBackend == "hnsw" {
		base := d.cfg.EmbeddingsPath
		ext := filepath.Ext(base)
		if ext != "" {
			return base[:len(base)-len(ext)] + ".hnsw"
		}
		return base + ".hnsw"
	}
	return d.cfg.EmbeddingsPath
}

// Engine returns the query engine.
func (d *Daemon) Engine() *core.QueryEngine {
	return d.engine
}

// Store returns the metadata store.
func (d *Daemon) Store() types.WriterStore {
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
	if !d.writer.AddProducer() {
		d.logger.Info("writer draining, skipping watcher")
		return
	}
	go func() {
		defer d.writer.RemoveProducer()
		for event := range w.Events() {
			start := time.Now()
			if err := pipeline.EnrichFile(ctx, event); err != nil {
				d.logger.Warn("incremental enrich failed",
					"path", event.Path,
					"err", err)
			}
			d.logger.Debug("watcher enrich", "path", event.Path, "type", event.ChangeType, "duration_ms", time.Since(start).Milliseconds())
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
					if _, err := d.embedFromDB(ctx, nil); err != nil {
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
func (d *Daemon) IndexProject(ctx context.Context, onProgress func(IndexProgress)) error {
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	go d.writer.Run(writerCtx)

	scanResult, err := ScanRepo(ctx, ScanInput{ProjectRoot: d.cfg.ProjectRoot})
	if err != nil {
		return fmt.Errorf("scan repo: %w", err)
	}

	pipeline := NewEnrichmentPipeline(d.store, d.writer, d.cfg.EnrichmentWorkers, d.lifecycle)
	if err := pipeline.IndexAll(ctx, IndexAllInput{
		ProjectRoot: d.cfg.ProjectRoot,
		Files:       scanResult.Files,
		OnProgress:  onProgress,
	}); err != nil {
		return fmt.Errorf("index all: %w", err)
	}

	writerCancel()
	<-d.writer.Done()
	return nil
}
