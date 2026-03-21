package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shaktimanai/shaktiman/internal/core"
	"github.com/shaktimanai/shaktiman/internal/mcp"
	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Daemon manages the lifecycle of the Shaktiman indexing system.
type Daemon struct {
	cfg    types.Config
	db     *storage.DB
	store  *storage.Store
	writer *WriterManager
	engine *core.QueryEngine
	logger *slog.Logger
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

	return &Daemon{
		cfg:    cfg,
		db:     db,
		store:  store,
		writer: writer,
		engine: engine,
		logger: slog.Default().With("component", "daemon"),
	}, nil
}

// Start starts background services and the MCP server.
// It blocks until the context is cancelled or the MCP server exits.
func (d *Daemon) Start(ctx context.Context) error {
	// Start writer goroutine
	writerCtx, writerCancel := context.WithCancel(ctx)
	defer writerCancel()
	go d.writer.Run(writerCtx)

	pipeline := NewEnrichmentPipeline(d.store, d.writer, d.cfg.EnrichmentWorkers)

	// Run cold indexing in background, then start watcher
	go func() {
		d.logger.Info("starting cold index", "root", d.cfg.ProjectRoot)

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

		d.logger.Info("cold index complete")

		// Start file watcher after cold index
		if d.cfg.WatcherEnabled {
			d.startWatcher(ctx, pipeline)
		}
	}()

	// Start MCP server (blocks on stdio)
	s := mcp.NewServer(d.engine, d.store)
	d.logger.Info("MCP server starting on stdio")

	if err := mcpserver.ServeStdio(s); err != nil {
		return fmt.Errorf("MCP server: %w", err)
	}

	return nil
}

// Stop performs graceful shutdown.
func (d *Daemon) Stop() error {
	d.logger.Info("shutting down")

	// Wait for writer to finish draining
	<-d.writer.Done()

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
