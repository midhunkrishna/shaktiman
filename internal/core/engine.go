package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shaktimanai/shaktiman/internal/storage"
	"github.com/shaktimanai/shaktiman/internal/types"
)

// QueryEngine orchestrates search and context assembly.
type QueryEngine struct {
	store       *storage.Store
	projectRoot string
	logger      *slog.Logger
}

// NewQueryEngine creates an engine backed by the given store.
func NewQueryEngine(store *storage.Store, projectRoot string) *QueryEngine {
	return &QueryEngine{
		store:       store,
		projectRoot: projectRoot,
		logger:      slog.Default().With("component", "engine"),
	}
}

// SearchInput configures a search operation.
type SearchInput struct {
	Query      string
	MaxResults int
	Explain    bool
}

// ContextInput configures a context assembly operation.
type ContextInput struct {
	Query        string
	BudgetTokens int
}

// Search executes a keyword search and returns scored results.
func (e *QueryEngine) Search(ctx context.Context, input SearchInput) ([]types.ScoredResult, error) {
	if input.MaxResults <= 0 {
		input.MaxResults = 50
	}

	level := DetermineLevel(ctx, e.store)

	switch level {
	case LevelKeyword:
		results, err := KeywordSearch(ctx, e.store, input.Query, input.MaxResults)
		if err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		// Fall through to filesystem if keyword returns nothing
		if len(results) == 0 {
			pkg, err := FilesystemFallback(ctx, e.projectRoot, input.Query, 4096)
			if err != nil {
				return nil, fmt.Errorf("filesystem fallback: %w", err)
			}
			return pkg.Chunks, nil
		}
		return results, nil

	case LevelFilesystem:
		pkg, err := FilesystemFallback(ctx, e.projectRoot, input.Query, 4096)
		if err != nil {
			return nil, fmt.Errorf("filesystem fallback: %w", err)
		}
		return pkg.Chunks, nil

	default:
		return nil, fmt.Errorf("unknown fallback level: %d", level)
	}
}

// Context assembles a budget-fitted context package for the given query.
func (e *QueryEngine) Context(ctx context.Context, input ContextInput) (*types.ContextPackage, error) {
	if input.BudgetTokens <= 0 {
		input.BudgetTokens = 8192
	}

	level := DetermineLevel(ctx, e.store)

	switch level {
	case LevelKeyword:
		// Fetch more candidates than budget might need
		results, err := KeywordSearch(ctx, e.store, input.Query, 200)
		if err != nil {
			return nil, fmt.Errorf("keyword search for context: %w", err)
		}
		if len(results) == 0 {
			return FilesystemFallback(ctx, e.projectRoot, input.Query, input.BudgetTokens)
		}

		pkg := Assemble(AssemblerInput{
			Candidates:   results,
			BudgetTokens: input.BudgetTokens,
		})
		return pkg, nil

	case LevelFilesystem:
		return FilesystemFallback(ctx, e.projectRoot, input.Query, input.BudgetTokens)

	default:
		return nil, fmt.Errorf("unknown fallback level: %d", level)
	}
}

// Store returns the underlying store for direct access.
func (e *QueryEngine) Store() *storage.Store {
	return e.store
}
