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
		iterNamedChildren(node, func(child *tree_sitter.Node) {
			p.walkForSymbols(child, source, true, chunks, symbols, cfg)
		})
		return
	}

	// Python decorated_definition — recurse into the definition child
	if nodeType == "decorated_definition" {
		iterNamedChildren(node, func(child *tree_sitter.Node) {
			if child.Kind() != "decorator" {
				p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
			}
		})
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
		case "field_declaration":
			p.extractJavaFieldSymbols(node, source, chunks, symbols)
			return
		default:
			name := extractName(node, source)
			if name != "" {
				line := int(node.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
				isExp := exported || isGoExported(name, cfg)
				*symbols = append(*symbols, buildSymbol(name, kind, line, isExp, extractSignature(node, source), chunks))
			}
		}
	}

	// Unwrap ambient declaration wrapper (TypeScript declare)
	if cfg.AmbientType != "" && nodeType == cfg.AmbientType {
		iterNamedChildren(node, func(child *tree_sitter.Node) {
			p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
		})
		return
	}

	// Recurse into containers and structural nodes to find nested symbols.
	// For symbol nodes, recursion is driven by NodeMeta.IsContainer — the
	// single source of truth set in the language config. Non-symbol
	// structural nodes (body_statement, class_body, declaration_list, etc.)
	// always recurse so symbol nodes nested inside them are reachable.
	shouldRecurse := !isSymbol
	if isSymbol {
		shouldRecurse = cfg.ChunkableTypes[nodeType].IsContainer
	}
	if shouldRecurse {
		iterNamedChildren(node, func(child *tree_sitter.Node) {
			p.walkForSymbols(child, source, exported, chunks, symbols, cfg)
		})
	}
}

// extractVariableSymbols handles TS const/let/var declarations with multiple declarators.
func (p *Parser) extractVariableSymbols(node *tree_sitter.Node, source []byte, exported bool, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord) {
	kind := "variable"
	if isConstDeclaration(node) {
		kind = "constant"
	}
	iterNamedChildren(node, func(child *tree_sitter.Node) {
		if child.Kind() != "variable_declarator" {
			return
		}
		name := extractName(child, source)
		if name == "" {
			return
		}
		line := int(child.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
		*symbols = append(*symbols, buildSymbol(name, kind, line, exported, "", chunks))
	})
}

// extractGoVarSymbols handles Go var/const declarations with var_spec/const_spec children.
func (p *Parser) extractGoVarSymbols(node *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
	specType := "var_spec"
	kind := "variable"
	if node.Kind() == "const_declaration" {
		specType = "const_spec"
		kind = "constant"
	}
	iterNamedChildren(node, func(child *tree_sitter.Node) {
		if child.Kind() != specType {
			return
		}
		name := extractName(child, source)
		if name == "" {
			return
		}
		line := int(child.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
		*symbols = append(*symbols, buildSymbol(name, kind, line, isGoExported(name, cfg), "", chunks))
	})
}

// extractJavaFieldSymbols handles Java field_declaration nodes, which can
// declare multiple variables in one statement (`int x = 1, y = 2;`).
// Tree-sitter exposes each variable as its own variable_declarator child;
// extractName alone would only return the first declarator's name and lose
// the rest. This mirrors extractVariableSymbols for TypeScript/JavaScript
// lexical_declaration. Visibility is inferred from the node's `modifiers`
// child (public/private/protected) with package-private as the default.
// `static final` fields are indexed as "constant", regular fields as
// "variable".
func (p *Parser) extractJavaFieldSymbols(node *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord) {
	visibility, isConstant := inspectJavaFieldModifiers(node, source)
	isExported := visibility == "public"
	kind := "variable"
	if isConstant {
		kind = "constant"
	}

	iterNamedChildren(node, func(child *tree_sitter.Node) {
		if child.Kind() != "variable_declarator" {
			return
		}
		name := extractName(child, source)
		if name == "" {
			return
		}
		line := int(child.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
		// Java has its own three-state visibility (public/private/internal)
		// derived from modifiers — bypass the simple buildSymbol helper.
		sym := types.SymbolRecord{
			Name:       name,
			Kind:       kind,
			Line:       line,
			Visibility: visibility,
			IsExported: isExported,
			ChunkID:    findContainingChunk(line, chunks),
		}
		*symbols = append(*symbols, sym)
	})
}

// inspectJavaFieldModifiers walks a field_declaration's `modifiers` child to
// determine visibility (public/private/protected/internal — internal means
// package-private) and whether the field is `static final` (indexed as
// "constant" rather than "variable").
func inspectJavaFieldModifiers(node *tree_sitter.Node, _ []byte) (visibility string, isConstant bool) {
	visibility = "internal" // package-private default
	sawStatic := false
	sawFinal := false

	iterNamedChildren(node, func(child *tree_sitter.Node) {
		if child.Kind() != "modifiers" {
			return
		}
		iterChildren(child, func(mod *tree_sitter.Node) {
			switch mod.Kind() {
			case "public":
				visibility = "public"
			case "private":
				visibility = "private"
			case "protected":
				visibility = "internal" // treat protected as package-visible for our taxonomy
			case "static":
				sawStatic = true
			case "final":
				sawFinal = true
			}
		})
	})
	return visibility, sawStatic && sawFinal
}

// extractGoTypeSymbols handles Go type declarations with type_spec children.
func (p *Parser) extractGoTypeSymbols(node *tree_sitter.Node, source []byte, chunks []types.ChunkRecord, symbols *[]types.SymbolRecord, cfg *LanguageConfig) {
	iterNamedChildren(node, func(child *tree_sitter.Node) {
		if child.Kind() != "type_spec" {
			return
		}
		name := extractName(child, source)
		if name == "" {
			return
		}
		line := int(child.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
		*symbols = append(*symbols, buildSymbol(name, "type", line, isGoExported(name, cfg), "", chunks))
	})
}

// findContainingChunk returns the chunk index for the chunk whose line range
// contains the given line, or -1 if no chunk contains the line. Callers must
// treat -1 as a sentinel (skip the symbol, log, etc.): a zero return was
// previously ambiguous with "valid chunk at index 0", causing orphan
// symbols to be silently mis-attributed to the first (often header) chunk.
func findContainingChunk(line int, chunks []types.ChunkRecord) int64 {
	for i := range chunks {
		if line >= chunks[i].StartLine && line <= chunks[i].EndLine {
			return int64(i)
		}
	}
	return -1
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
	found := false
	iterChildren(node, func(child *tree_sitter.Node) {
		if !found && child.Kind() == "const" {
			found = true
		}
	})
	return found
}
