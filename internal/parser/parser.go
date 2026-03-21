// Package parser provides tree-sitter based code parsing, chunking, and symbol extraction.
// Each Parser instance is NOT goroutine-safe (IP-2) — create one per worker.
package parser

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Parser wraps a tree-sitter parser and token counter.
// NOT goroutine-safe — each worker goroutine must own its own instance (IP-2).
type Parser struct {
	ts      *sitter.Parser
	tokens  *tokenCounter
	configs map[string]*LanguageConfig // cached per language
}

// NewParser creates a fresh Parser supporting all registered languages.
func NewParser() (*Parser, error) {
	ts := sitter.NewParser()

	tc, err := newTokenCounter("cl100k_base")
	if err != nil {
		ts.Close()
		return nil, fmt.Errorf("init token counter: %w", err)
	}

	return &Parser{
		ts:      ts,
		tokens:  tc,
		configs: make(map[string]*LanguageConfig),
	}, nil
}

// Close releases tree-sitter parser resources.
func (p *Parser) Close() {
	p.ts.Close()
}

// ParseInput configures a parse operation.
type ParseInput struct {
	FilePath string
	Content  []byte
	Language string
}

// ParseResult holds the output of parsing a single file.
type ParseResult struct {
	Chunks  []types.ChunkRecord
	Symbols []types.SymbolRecord
	Edges   []types.EdgeRecord
}

// Parse parses a source file and returns chunks, symbols, and edges.
func (p *Parser) Parse(ctx context.Context, input ParseInput) (*ParseResult, error) {
	cfg, err := p.getConfig(input.Language)
	if err != nil {
		return nil, fmt.Errorf("get language config for %s: %w", input.Language, err)
	}

	p.ts.SetLanguage(cfg.Grammar)

	tree, err := p.ts.ParseCtx(ctx, nil, input.Content)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse %s: %w", input.FilePath, err)
	}
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter returned nil tree for %s", input.FilePath)
	}

	root := tree.RootNode()
	if root == nil {
		return nil, fmt.Errorf("nil root node for %s", input.FilePath)
	}

	chunks := p.chunkFile(root, input.Content, cfg)
	symbols := p.extractSymbols(root, input.Content, chunks, cfg)
	edges := p.extractEdges(root, input.Content, symbols, cfg)

	return &ParseResult{
		Chunks:  chunks,
		Symbols: symbols,
		Edges:   edges,
	}, nil
}

// CountTokens returns the token count for the given text.
func (p *Parser) CountTokens(text string) int {
	return p.tokens.Count(text)
}

// getConfig returns a cached LanguageConfig, loading it on first access.
func (p *Parser) getConfig(lang string) (*LanguageConfig, error) {
	if cfg, ok := p.configs[lang]; ok {
		return cfg, nil
	}
	cfg, err := GetLanguageConfig(lang)
	if err != nil {
		return nil, err
	}
	p.configs[lang] = cfg
	return cfg, nil
}
