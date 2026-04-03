package parser

import (
	"unicode"
	"unicode/utf8"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// extractSymbols walks the tree and extracts symbols, associating each
// with its containing chunk based on line ranges.
func (p *Parser) extractSymbols(root *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, cfg *LanguageConfig) []types.SymbolRecord {
	var symbols []types.SymbolRecord
	p.walkForSymbols(root, source, false, chunks, &symbols, cfg)
	return symbols
}

func (p *Parser) walkForSymbols(node *tree_sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
	nodeType := node.Kind()

	// Track export context (TypeScript)
	if cfg.ExportType != "" && nodeType == cfg.ExportType {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			p.walkForSymbols(node.NamedChild(uint(i)), source, true, chunks, symbols, cfg)
		}
		return
	}

	// Python decorated_definition — recurse into the definition child
	if nodeType == "decorated_definition" {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(uint(i))
			if child.Kind() != "decorator" {
				p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
			}
		}
		return
	}

	kind, isSymbol := cfg.SymbolKindMap[nodeType]
	if isSymbol {
		// Multi-declarator types: dispatch to specialized extractors that
		// handle each spec/declarator child individually. extractName would
		// only return the first child's name, losing subsequent declarations.
		switch nodeType {
		case "lexical_declaration", "variable_declaration":
			p.extractVariableSymbols(node, source, exported, chunks, symbols)
			return
		case "var_declaration", "const_declaration":
			p.extractGoVarSymbols(node, source, chunks, symbols, cfg)
			return
		case "type_declaration":
			p.extractGoTypeSymbols(node, source, chunks, symbols, cfg)
			return
		default:
			name := extractName(node, source)
			if name != "" {
				line := int(node.StartPosition().Row) + 1
				isExp := exported || isGoExported(name, cfg)
				sym := types.SymbolRecord{
					Name:       name,
					Kind:       kind,
					Line:       line,
					Signature:  extractSignature(node, source),
					IsExported: isExp,
				}
				if isExp {
					sym.Visibility = "exported"
				} else {
					sym.Visibility = "internal"
				}
				sym.ChunkID = findContainingChunk(line, chunks)
				*symbols = append(*symbols, sym)
			}
		}
	}

	// Recurse into class bodies for methods
	if cfg.ClassBodyType != "" && nodeType == cfg.ClassBodyType {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(uint(i))
			if cfg.ClassBodyTypes[child.Kind()] {
				p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
			}
		}
		return
	}

	// For class declarations, recurse into their body
	if cfg.ClassTypes[nodeType] {
		if cfg.ClassBodyType != "" {
			body := findChildByType(node, cfg.ClassBodyType)
			if body != nil {
				p.walkForSymbols(body, source, exported, chunks, symbols, cfg)
			}
		}
		return
	}

	// For container nodes, recurse into named children
	if !isSymbol {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			p.walkForSymbols(node.NamedChild(uint(i)), source, exported, chunks, symbols, cfg)
		}
	}
}

// extractVariableSymbols handles TS const/let/var declarations with multiple declarators.
func (p *Parser) extractVariableSymbols(node *tree_sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(uint(i))
		if child.Kind() == "variable_declarator" {
			name := extractName(child, source)
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row) + 1
			kind := "variable"
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

// extractGoVarSymbols handles Go var/const declarations with var_spec/const_spec children.
func (p *Parser) extractGoVarSymbols(node *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
	specType := "var_spec"
	kind := "variable"
	if node.Kind() == "const_declaration" {
		specType = "const_spec"
		kind = "constant"
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(uint(i))
		if child.Kind() == specType {
			name := extractName(child, source)
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row) + 1
			isExp := isGoExported(name, cfg)
			sym := types.SymbolRecord{
				Name:       name,
				Kind:       kind,
				Line:       line,
				IsExported: isExp,
			}
			if isExp {
				sym.Visibility = "exported"
			} else {
				sym.Visibility = "internal"
			}
			sym.ChunkID = findContainingChunk(line, chunks)
			*symbols = append(*symbols, sym)
		}
	}
}

// extractGoTypeSymbols handles Go type declarations with type_spec children.
func (p *Parser) extractGoTypeSymbols(node *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(uint(i))
		if child.Kind() == "type_spec" {
			name := extractName(child, source)
			if name == "" {
				continue
			}
			line := int(child.StartPosition().Row) + 1
			isExp := isGoExported(name, cfg)
			sym := types.SymbolRecord{
				Name:       name,
				Kind:       "type",
				Line:       line,
				IsExported: isExp,
			}
			if isExp {
				sym.Visibility = "exported"
			} else {
				sym.Visibility = "internal"
			}
			sym.ChunkID = findContainingChunk(line, chunks)
			*symbols = append(*symbols, sym)
		}
	}
}

// findContainingChunk returns the chunk index for the chunk whose line range contains the given line.
// Returns 0 if no chunk contains the line.
func findContainingChunk(line int, chunks []types.ChunkRecord) int64 {
	for i := range chunks {
		if line >= chunks[i].StartLine && line <= chunks[i].EndLine {
			return int64(i)
		}
	}
	return 0
}

// isGoExported returns true if the name starts with an uppercase letter (Go export rule).
func isGoExported(name string, cfg *LanguageConfig) bool {
	if cfg.Name != "go" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// isConstDeclaration checks if a lexical_declaration uses "const".
func isConstDeclaration(node *tree_sitter.Node) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(uint(i))
		if child.Kind() == "const" {
			return true
		}
	}
	return false
}
