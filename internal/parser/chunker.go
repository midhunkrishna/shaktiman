package parser

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// maxChunkTokens is the threshold above which a chunk is split.
const maxChunkTokens = 1024

// minChunkTokens is the threshold below which a chunk is merged with the previous.
const minChunkTokens = 20

// chunkableTypes are tree-sitter node types that become top-level chunks.
var chunkableTypes = map[string]string{
	"function_declaration":       "function",
	"class_declaration":          "class",
	"interface_declaration":      "interface",
	"type_alias_declaration":     "type",
	"enum_declaration":           "type",
	"export_statement":           "", // resolved based on child
	"lexical_declaration":        "block",
	"variable_declaration":       "block",
	"abstract_class_declaration": "class",
}

// classBodyMethodTypes are node types within a class body that become method chunks.
var classBodyMethodTypes = map[string]bool{
	"method_definition":  true,
	"public_field_definition": true,
}

// chunkFile splits a parsed tree into semantic chunks.
func (p *Parser) chunkFile(root *sitter.Node, source []byte) []types.ChunkRecord {
	var chunks []types.ChunkRecord
	var headerParts []headerFragment

	childCount := int(root.NamedChildCount())
	for i := 0; i < childCount; i++ {
		child := root.NamedChild(i)
		nodeType := child.Type()

		// Collect imports and non-chunkable top-level nodes for header
		if nodeType == "import_statement" || nodeType == "comment" {
			headerParts = append(headerParts, headerFragment{
				content:   child.Content(source),
				startLine: int(child.StartPoint().Row) + 1,
				endLine:   int(child.EndPoint().Row) + 1,
			})
			continue
		}

		kind, ok := chunkableTypes[nodeType]
		if !ok {
			// Non-chunkable top-level node → include in header
			headerParts = append(headerParts, headerFragment{
				content:   child.Content(source),
				startLine: int(child.StartPoint().Row) + 1,
				endLine:   int(child.EndPoint().Row) + 1,
			})
			continue
		}

		// Handle export_statement: look at the child declaration
		if nodeType == "export_statement" {
			chunks = append(chunks, p.chunkExportStatement(child, source, len(chunks))...)
			continue
		}

		// Handle class: create class chunk + method chunks
		if kind == "class" {
			chunks = append(chunks, p.chunkClass(child, source, len(chunks))...)
			continue
		}

		// Regular chunkable node
		name := extractName(child, source)
		content := child.Content(source)
		tokenCount := p.tokens.Count(content)

		chunks = append(chunks, types.ChunkRecord{
			ChunkIndex: len(chunks),
			SymbolName: name,
			Kind:       kind,
			StartLine:  int(child.StartPoint().Row) + 1,
			EndLine:    int(child.EndPoint().Row) + 1,
			Content:    content,
			TokenCount: tokenCount,
			Signature:  extractSignature(child, source),
		})
	}

	// Build header chunk from collected parts
	if len(headerParts) > 0 {
		header := buildHeaderChunk(headerParts, p.tokens)
		// Prepend header as chunk index 0
		header.ChunkIndex = 0
		for i := range chunks {
			chunks[i].ChunkIndex = i + 1
		}
		chunks = append([]types.ChunkRecord{header}, chunks...)
	}

	// Post-process: merge tiny chunks, split oversized chunks
	chunks = p.mergeSmallChunks(chunks)
	chunks = p.splitLargeChunks(chunks)

	// Re-index after merge/split
	for i := range chunks {
		chunks[i].ChunkIndex = i
		chunks[i].ParseQuality = "full"
	}

	return chunks
}

// chunkExportStatement handles `export function/class/interface/type/const`.
func (p *Parser) chunkExportStatement(node *sitter.Node, source []byte, baseIndex int) []types.ChunkRecord {
	// The exported declaration is usually the first named child after "export"
	declChild := findDeclarationChild(node)
	if declChild == nil {
		// Export without recognizable declaration (e.g., `export { x }`)
		content := node.Content(source)
		return []types.ChunkRecord{{
			ChunkIndex: baseIndex,
			SymbolName: "",
			Kind:       "block",
			StartLine:  int(node.StartPoint().Row) + 1,
			EndLine:    int(node.EndPoint().Row) + 1,
			Content:    content,
			TokenCount: p.tokens.Count(content),
		}}
	}

	declType := declChild.Type()
	kind, ok := chunkableTypes[declType]
	if !ok {
		kind = "block"
	}

	if kind == "class" {
		return p.chunkClass(node, source, baseIndex)
	}

	name := extractName(declChild, source)
	content := node.Content(source)
	return []types.ChunkRecord{{
		ChunkIndex: baseIndex,
		SymbolName: name,
		Kind:       kind,
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Content:    content,
		TokenCount: p.tokens.Count(content),
		Signature:  extractSignature(declChild, source),
	}}
}

// chunkClass creates a class chunk and child method chunks.
// node may be a class_declaration or an export_statement wrapping one.
func (p *Parser) chunkClass(node *sitter.Node, source []byte, baseIndex int) []types.ChunkRecord {
	var chunks []types.ChunkRecord

	// If node is an export_statement, find the inner class_declaration
	// for tree walking, but use the outer node for content.
	classNode := node
	if node.Type() == "export_statement" {
		if inner := findDeclarationChild(node); inner != nil {
			classNode = inner
		}
	}

	className := extractName(classNode, source)
	classContent := node.Content(source)

	parentIdx := baseIndex
	classChunk := types.ChunkRecord{
		ChunkIndex: baseIndex,
		SymbolName: className,
		Kind:       "class",
		StartLine:  int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Content:    classContent,
		TokenCount: p.tokens.Count(classContent),
		Signature:  extractSignature(classNode, source),
	}

	// Find class body and extract methods
	var methodChunks []types.ChunkRecord
	classBody := findChildByType(classNode, "class_body")
	if classBody != nil {
		bodyChildCount := int(classBody.NamedChildCount())
		for i := 0; i < bodyChildCount; i++ {
			member := classBody.NamedChild(i)
			if classBodyMethodTypes[member.Type()] {
				methodName := extractName(member, source)
				methodContent := member.Content(source)
				pi := parentIdx
				methodChunks = append(methodChunks, types.ChunkRecord{
					ChunkIndex:  baseIndex + 1 + len(methodChunks),
					SymbolName:  methodName,
					Kind:        "method",
					StartLine:   int(member.StartPoint().Row) + 1,
					EndLine:     int(member.EndPoint().Row) + 1,
					Content:     methodContent,
					TokenCount:  p.tokens.Count(methodContent),
					Signature:   extractSignature(member, source),
					ParentIndex: &pi,
				})
			}
		}
	}

	// If class has methods, use class signature as the class chunk content
	// instead of the full body (methods are separate chunks)
	if len(methodChunks) > 0 {
		sigContent := buildClassSignature(classNode, source, className)
		classChunk.Content = sigContent
		classChunk.TokenCount = p.tokens.Count(sigContent)
	}

	chunks = append(chunks, classChunk)
	chunks = append(chunks, methodChunks...)
	return chunks
}

// mergeSmallChunks merges chunks below minChunkTokens with the previous chunk.
func (p *Parser) mergeSmallChunks(chunks []types.ChunkRecord) []types.ChunkRecord {
	if len(chunks) <= 1 {
		return chunks
	}

	var merged []types.ChunkRecord
	for _, c := range chunks {
		if c.TokenCount < minChunkTokens && len(merged) > 0 && c.Kind != "header" {
			prev := &merged[len(merged)-1]
			prev.Content += "\n\n" + c.Content
			prev.TokenCount = p.tokens.Count(prev.Content)
			prev.EndLine = c.EndLine
		} else {
			merged = append(merged, c)
		}
	}
	return merged
}

// splitLargeChunks splits chunks exceeding maxChunkTokens.
func (p *Parser) splitLargeChunks(chunks []types.ChunkRecord) []types.ChunkRecord {
	var result []types.ChunkRecord
	for _, c := range chunks {
		if c.TokenCount <= maxChunkTokens {
			result = append(result, c)
			continue
		}

		// Split by lines at roughly equal token points
		lines := strings.Split(c.Content, "\n")
		var current strings.Builder
		currentStart := c.StartLine
		partIndex := 0

		for i, line := range lines {
			current.WriteString(line)
			if i < len(lines)-1 {
				current.WriteString("\n")
			}

			tokensSoFar := p.tokens.Count(current.String())
			if tokensSoFar >= maxChunkTokens && i < len(lines)-1 {
				part := current.String()
				result = append(result, types.ChunkRecord{
					ChunkIndex:  len(result),
					SymbolName:  c.SymbolName,
					Kind:        c.Kind,
					StartLine:   currentStart,
					EndLine:     c.StartLine + i,
					Content:     part,
					TokenCount:  p.tokens.Count(part),
					Signature:   c.Signature,
					ParentIndex: c.ParentIndex,
				})
				partIndex++
				current.Reset()
				currentStart = c.StartLine + i + 1
			}
		}

		// Remaining content
		if current.Len() > 0 {
			part := current.String()
			result = append(result, types.ChunkRecord{
				ChunkIndex:  len(result),
				SymbolName:  c.SymbolName,
				Kind:        c.Kind,
				StartLine:   currentStart,
				EndLine:     c.EndLine,
				Content:     part,
				TokenCount:  p.tokens.Count(part),
				Signature:   c.Signature,
				ParentIndex: c.ParentIndex,
			})
		}
	}
	return result
}

// ── Tree-sitter helpers ──

type headerFragment struct {
	content   string
	startLine int
	endLine   int
}

func buildHeaderChunk(parts []headerFragment, tc *tokenCounter) types.ChunkRecord {
	var sb strings.Builder
	startLine := parts[0].startLine
	endLine := parts[len(parts)-1].endLine

	for i, p := range parts {
		sb.WriteString(p.content)
		if i < len(parts)-1 {
			sb.WriteString("\n")
		}
	}

	content := sb.String()
	return types.ChunkRecord{
		Kind:       "header",
		StartLine:  startLine,
		EndLine:    endLine,
		Content:    content,
		TokenCount: tc.Count(content),
	}
}

// extractName finds the identifier name of a declaration node.
func extractName(node *sitter.Node, source []byte) string {
	// Try common field names
	for _, field := range []string{"name", "property"} {
		if child := node.ChildByFieldName(field); child != nil {
			if child.Type() == "identifier" || child.Type() == "type_identifier" ||
				child.Type() == "property_identifier" {
				return child.Content(source)
			}
		}
	}

	// Walk named children for an identifier
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "identifier" || child.Type() == "type_identifier" {
			return child.Content(source)
		}
		// For export_statement, look at the declaration child
		if child.Type() != "comment" {
			name := extractName(child, source)
			if name != "" {
				return name
			}
		}
	}
	return ""
}

// extractSignature extracts a function/method signature (params + return type).
func extractSignature(node *sitter.Node, source []byte) string {
	params := node.ChildByFieldName("parameters")
	retType := node.ChildByFieldName("return_type")

	if params == nil {
		return ""
	}

	sig := params.Content(source)
	if retType != nil {
		sig += ": " + retType.Content(source)
	}
	return sig
}

// findChildByType finds the first named child with the given type.
func findChildByType(node *sitter.Node, nodeType string) *sitter.Node {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == nodeType {
			return child
		}
	}
	return nil
}

// findDeclarationChild finds the declaration within an export_statement.
func findDeclarationChild(node *sitter.Node) *sitter.Node {
	declTypes := map[string]bool{
		"function_declaration":       true,
		"class_declaration":          true,
		"abstract_class_declaration": true,
		"interface_declaration":      true,
		"type_alias_declaration":     true,
		"enum_declaration":           true,
		"lexical_declaration":        true,
		"variable_declaration":       true,
	}

	if decl := node.ChildByFieldName("declaration"); decl != nil {
		return decl
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if declTypes[child.Type()] {
			return child
		}
	}
	return nil
}

// buildClassSignature creates a summary of a class (name + method signatures)
// used as the class chunk content when methods are split into their own chunks.
func buildClassSignature(node *sitter.Node, source []byte, className string) string {
	var sb strings.Builder

	// Get the class header line(s) — everything before the class body
	classBody := findChildByType(node, "class_body")
	if classBody != nil {
		headerEnd := classBody.StartByte()
		sb.Write(source[node.StartByte():headerEnd])
		sb.WriteString("{\n")

		bodyChildCount := int(classBody.NamedChildCount())
		for i := 0; i < bodyChildCount; i++ {
			member := classBody.NamedChild(i)
			if classBodyMethodTypes[member.Type()] {
				name := extractName(member, source)
				sig := extractSignature(member, source)
				sb.WriteString("  ")
				sb.WriteString(name)
				sb.WriteString(sig)
				sb.WriteString(" { ... }\n")
			}
		}
		sb.WriteString("}")
	} else {
		sb.WriteString("class ")
		sb.WriteString(className)
		sb.WriteString(" { ... }")
	}
	return sb.String()
}
