package parser

import (
	sitter "github.com/smacker/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// symbolKindMap maps tree-sitter node types to symbol kinds.
var symbolKindMap = map[string]string{
	"function_declaration":       "function",
	"class_declaration":          "class",
	"abstract_class_declaration": "class",
	"method_definition":          "method",
	"interface_declaration":      "interface",
	"type_alias_declaration":     "type",
	"enum_declaration":           "type",
	"lexical_declaration":        "variable",
	"variable_declaration":       "variable",
}

// extractSymbols walks the tree and extracts symbols, associating each
// with its containing chunk based on line ranges.
func (p *Parser) extractSymbols(root *sitter.Node, source []byte, chunks []types.ChunkRecord) []types.SymbolRecord {
	var symbols []types.SymbolRecord

	p.walkForSymbols(root, source, false, chunks, &symbols)
	return symbols
}

func (p *Parser) walkForSymbols(node *sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord) {
	nodeType := node.Type()

	// Track export context
	if nodeType == "export_statement" {
		exported = true
		// Walk children with export context
		for i := 0; i < int(node.NamedChildCount()); i++ {
			p.walkForSymbols(node.NamedChild(i), source, true, chunks, symbols)
		}
		return
	}

	kind, isSymbol := symbolKindMap[nodeType]
	if isSymbol {
		name := extractName(node, source)
		if name == "" {
			// Lexical declarations may have multiple declarators
			if nodeType == "lexical_declaration" || nodeType == "variable_declaration" {
				p.extractVariableSymbols(node, source, exported, chunks, symbols)
				return
			}
		}

		if name != "" {
			line := int(node.StartPoint().Row) + 1
			sym := types.SymbolRecord{
				Name:       name,
				Kind:       kind,
				Line:       line,
				Signature:  extractSignature(node, source),
				IsExported: exported,
			}
			if exported {
				sym.Visibility = "exported"
			} else {
				sym.Visibility = "internal"
			}

			// Associate with containing chunk
			sym.ChunkID = findContainingChunk(line, chunks)

			*symbols = append(*symbols, sym)
		}
	}

	// Recurse into class bodies for methods
	if nodeType == "class_body" {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(i)
			if child.Type() == "method_definition" || child.Type() == "public_field_definition" {
				p.walkForSymbols(child, source, exported, chunks, symbols)
			}
		}
		return
	}

	// For class/interface declarations, recurse into their body
	if nodeType == "class_declaration" || nodeType == "abstract_class_declaration" {
		body := findChildByType(node, "class_body")
		if body != nil {
			p.walkForSymbols(body, source, exported, chunks, symbols)
		}
		return
	}

	// For container nodes (e.g. program), recurse into named children
	if !isSymbol {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			p.walkForSymbols(node.NamedChild(i), source, exported, chunks, symbols)
		}
	}
}

// extractVariableSymbols handles const/let/var declarations with multiple declarators.
func (p *Parser) extractVariableSymbols(node *sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "variable_declarator" {
			name := extractName(child, source)
			if name == "" {
				continue
			}

			line := int(child.StartPoint().Row) + 1
			kind := "variable"

			// Check if it's a const with a literal value → constant
			if isConstDeclaration(node) {
				kind = "constant"
			}

			sym := types.SymbolRecord{
				Name:       name,
				Kind:       kind,
				Line:       line,
				IsExported: exported,
			}
			if exported {
				sym.Visibility = "exported"
			} else {
				sym.Visibility = "internal"
			}

			sym.ChunkID = findContainingChunk(line, chunks)
			*symbols = append(*symbols, sym)
		}
	}
}

// findContainingChunk returns the ChunkID placeholder (index-based, resolved later)
// for the chunk whose line range contains the given line.
// Returns 0 if no chunk contains the line (should not happen).
func findContainingChunk(line int, chunks []types.ChunkRecord) int64 {
	for i := range chunks {
		if line >= chunks[i].StartLine && line <= chunks[i].EndLine {
			// Return the chunk index as a temporary ID — will be resolved
			// to actual DB ID during enrichment pipeline.
			return int64(i)
		}
	}
	return 0
}

// isConstDeclaration checks if a lexical_declaration uses "const".
func isConstDeclaration(node *sitter.Node) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "const" {
			return true
		}
	}
	return false
}
