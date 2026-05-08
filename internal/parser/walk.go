package parser

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// iterNamedChildren invokes fn for each named child of node, in order. It
// encapsulates the uint32→int conversion tree-sitter forces on every walker
// so callers do not each carry a //nolint:gosec annotation, and so the
// off-by-one loop shape lives in exactly one place.
func iterNamedChildren(node *tree_sitter.Node, fn func(child *tree_sitter.Node)) {
	if node == nil {
		return
	}
	n := node.NamedChildCount()
	for i := uint(0); i < uint(n); i++ {
		fn(node.NamedChild(i))
	}
}

// findNamedChild returns the first named child of node for which pred
// returns true, or nil if none match. Use when the caller wants the matching
// child rather than to iterate all of them — this is the early-return
// counterpart of iterNamedChildren.
func findNamedChild(node *tree_sitter.Node, pred func(*tree_sitter.Node) bool) *tree_sitter.Node {
	if node == nil {
		return nil
	}
	n := node.NamedChildCount()
	for i := uint(0); i < uint(n); i++ {
		child := node.NamedChild(i)
		if pred(child) {
			return child
		}
	}
	return nil
}

// iterChildren invokes fn for each child of node (named + anonymous), in
// order. Used for token-level scans where keyword/punctuation children are
// the actual signal (e.g. detecting `const` in a JS lexical_declaration or
// walking a Java modifiers list).
func iterChildren(node *tree_sitter.Node, fn func(child *tree_sitter.Node)) {
	if node == nil {
		return
	}
	n := node.ChildCount()
	for i := uint(0); i < uint(n); i++ {
		fn(node.Child(i))
	}
}

// visibilityFor returns the visibility string for an exported / non-exported
// symbol in the simple two-state model (Go, JS/TS, Python, Rust, etc.).
// Languages with richer visibility models (Java public/private/protected)
// must set SymbolRecord.Visibility directly and must not route through this
// helper.
func visibilityFor(isExported bool) string {
	if isExported {
		return "exported"
	}
	return "internal"
}

// buildSymbol constructs a SymbolRecord for the simple two-state visibility
// model: visibility is derived from isExported and ChunkID is resolved from
// chunks. Pass signature == "" when the caller does not compute a signature
// (e.g. variable / type-spec extractors).
func buildSymbol(name, kind string, line int, isExported bool, signature string, chunks []types.ChunkRecord) types.SymbolRecord {
	return types.SymbolRecord{
		Name:       name,
		Kind:       kind,
		Line:       line,
		Signature:  signature,
		IsExported: isExported,
		Visibility: visibilityFor(isExported),
		ChunkID:    findContainingChunk(line, chunks),
	}
}
