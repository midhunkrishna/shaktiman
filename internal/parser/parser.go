// Package parser provides tree-sitter based code parsing, chunking, and symbol extraction.
// Each Parser instance is NOT goroutine-safe (IP-2) — create one per worker.
package parser

import (
	"context"
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// Parser wraps a tree-sitter parser and token counter.
// NOT goroutine-safe — each worker goroutine must own its own instance (IP-2).
type Parser struct {
	ts     *sitter.Parser
	lang   *sitter.Language
	tokens *tokenCounter
}

// NewParser creates a fresh Parser for TypeScript.
func NewParser() (*Parser, error) {
	ts := sitter.NewParser()
	lang := typescript.GetLanguage()
	ts.SetLanguage(lang)

	tc, err := newTokenCounter("cl100k_base")
	if err != nil {
		ts.Close()
		return nil, fmt.Errorf("init token counter: %w", err)
	}

	return &Parser{ts: ts, lang: lang, tokens: tc}, nil
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
}

// Parse parses a source file and returns chunks and symbols.
func (p *Parser) Parse(ctx context.Context, input ParseInput) (*ParseResult, error) {
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

	chunks := p.chunkFile(root, input.Content)
	symbols := p.extractSymbols(root, input.Content, chunks)

	return &ParseResult{
		Chunks:  chunks,
		Symbols: symbols,
	}, nil
}

// CountTokens returns the token count for the given text.
func (p *Parser) CountTokens(text string) int {
	return p.tokens.Count(text)
}
