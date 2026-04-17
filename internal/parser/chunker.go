package parser

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// maxChunkTokens is the threshold above which a chunk is split.
const maxChunkTokens = 1024

// minChunkTokens is the threshold below which a chunk is merged with the previous.
const minChunkTokens = 20

// maxChunkDepth prevents unbounded recursion on pathological ASTs.
const maxChunkDepth = 10

// ChunkAlgorithmVersion identifies the current chunking algorithm. Bump this
// whenever chunk boundaries, signature format, or traversal semantics change
// in a way that would make previously indexed chunks incompatible or
// misleading for search/ranking. The daemon stores this value in the
// config table on first run and triggers a full reindex on mismatch.
//
// v3 — bug fix pass from docs/review-findings/parser-bugs-from-recursive-chunking.md:
//   - Bug #11: chunkNode size gate — small containers emit as a single chunk,
//     so chunk boundaries shift for every file under ~1024 tokens.
//   - Bug #1 + #2: Java field_declaration symbols are now indexed (new
//     variable/constant symbols that didn't exist before).
//   - Bug #4: NodeMeta refactor — no behavioral change for existing containers,
//     but adding a new container kind no longer requires editing walkForSymbols.
//   - Bug #6: signature headers now include full multi-line declarations
//     (name, generics, extends/implements) so signature chunk content differs.
//   - Bug #8: call-graph edge targets include receivers (e.g., `a.foo` instead
//     of `foo`), changing edge dst names everywhere member calls appear.
//   - Bug #9: recursive self-calls now present in the call graph.
//   - Bug #10: heritage edges include generic type arguments (String inside
//     `Map<String, User>`).
const ChunkAlgorithmVersion = "3"

// chunkFile splits a parsed tree into semantic chunks using language-specific config.
func (p *Parser) chunkFile(root *tree_sitter.Node, source []byte, cfg *LanguageConfig) []types.ChunkRecord {
	var chunks []types.ChunkRecord
	var headerParts []headerFragment

	childCount := int(root.NamedChildCount()) //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
	for i := 0; i < childCount; i++ {
		child := root.NamedChild(uint(i))
		nodeType := child.Kind()

		// Skip comments entirely — not indexed
		if nodeType == "comment" {
			continue
		}

		// Collect imports for header
		if cfg.ImportTypes[nodeType] {
			headerParts = append(headerParts, headerFragment{
				content:   child.Utf8Text(source),
				startLine: int(child.StartPosition().Row) + 1, //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
				endLine:   int(child.EndPosition().Row) + 1,   //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			})
			continue
		}

		// Package declarations go to header (Go, Java)
		if nodeType == "package_clause" || nodeType == "package_declaration" {
			headerParts = append(headerParts, headerFragment{
				content:   child.Utf8Text(source),
				startLine: int(child.StartPosition().Row) + 1, //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
				endLine:   int(child.EndPosition().Row) + 1,   //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			})
			continue
		}

		_, ok := cfg.ChunkableTypes[nodeType]
		if !ok {
			// Non-chunkable top-level node → include in header
			headerParts = append(headerParts, headerFragment{
				content:   child.Utf8Text(source),
				startLine: int(child.StartPosition().Row) + 1, //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
				endLine:   int(child.EndPosition().Row) + 1,   //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			})
			continue
		}

		// Unwrap export wrapper (TypeScript/JavaScript)
		target := child
		if cfg.ExportType != "" && nodeType == cfg.ExportType {
			if inner := findDeclarationChild(child, cfg); inner != nil {
				target = inner
			}
		}

		// Unwrap ambient wrapper (TypeScript declare)
		if cfg.AmbientType != "" && target.Kind() == cfg.AmbientType {
			if inner := findDeclarationChild(target, cfg); inner != nil {
				target = inner
			}
		}

		chunks = append(chunks, p.chunkNode(target, source, cfg, 0)...)
	}

	// Build header chunk from collected parts
	if len(headerParts) > 0 {
		header := buildHeaderChunk(headerParts, p.tokens)
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

// chunkNode recursively decomposes a node into semantic chunks.
// It extracts chunkable children, emits a signature for the parent,
// and falls back to line-splitting for oversized leaf nodes.
func (p *Parser) chunkNode(node *tree_sitter.Node, source []byte, cfg *LanguageConfig, depth int) []types.ChunkRecord {
	content := node.Utf8Text(source)
	tokens := p.tokens.Count(content)
	nodeType := node.Kind()

	kind := cfg.ChunkableTypes[nodeType].Kind

	// Resolve kind for wrapper types (decorated_definition, export_statement, etc.)
	// by looking at the inner definition child.
	nameNode := node
	if kind == "" {
		if inner := findDeclarationChild(node, cfg); inner != nil {
			if innerKind := cfg.ChunkableTypes[inner.Kind()].Kind; innerKind != "" {
				kind = innerKind
			}
			nameNode = inner
		}
		if kind == "" {
			kind = "block"
		}
	}

	// Go type_declaration: extract type name from type_spec child
	name := extractName(nameNode, source)
	if nodeType == "type_declaration" {
		name = extractGoTypeName(node, source)
	}

	// Size gate — if the whole node fits, emit it as a single chunk. ADR-004
	// §"Core Algorithm" and §"Key behaviors" specify that decomposition is
	// only triggered when `tokens > maxChunkTokens`: "A 20-line module with a
	// 15-line class doesn't decompose — the whole thing fits in one chunk."
	// The gate must run before findChunkableChildren so tiny containers stay
	// whole instead of being replaced with a signature summary + child chunks.
	if tokens <= maxChunkTokens {
		return []types.ChunkRecord{{
			SymbolName: name,
			Kind:       kind,
			StartLine:  int(node.StartPosition().Row) + 1, //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			EndLine:    int(node.EndPosition().Row) + 1,   //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			Content:    content,
			TokenCount: tokens,
			Signature:  extractSignature(nameNode, source),
		}}
	}

	// Depth guard — fall back to line splitting for pathologically deep ASTs.
	// Use `>` (not `>=`) so `depth == maxChunkDepth` still recurses, matching
	// ADR-004 §6 pseudocode. With maxChunkDepth = 10 the chunker handles 11
	// levels of nested chunkable containers before the fallback kicks in.
	// Bug #7: emit a structured warning so pathological ASTs that trigger
	// the fallback are observable rather than silently degrading chunk
	// quality.
	if depth > maxChunkDepth {
		if p.logger != nil {
			p.logger.Warn("parser depth guard triggered; falling back to line splitting",
				"file", p.curPath,
				"node_type", nodeType,
				"depth", depth,
				"max", maxChunkDepth,
				"tokens", tokens,
				"symbol_name", name,
			)
		}
		return p.splitNodeByLines(node, source, name, kind)
	}

	// Node exceeds maxChunkTokens — try to decompose via chunkable descendants.
	var extracted []types.ChunkRecord
	p.findChunkableChildren(node, source, cfg, depth, &extracted)

	if len(extracted) > 0 {
		// Parent becomes a signature chunk
		sig := buildSignatureFromExtracted(node, source, name, extracted)
		parent := types.ChunkRecord{
			SymbolName: name,
			Kind:       kind,
			StartLine:  int(node.StartPosition().Row) + 1, //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			EndLine:    int(node.EndPosition().Row) + 1,   //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
			Content:    sig,
			TokenCount: p.tokens.Count(sig),
			Signature:  extractSignature(nameNode, source),
		}

		// Set ParentIndex on direct extracted children
		result := []types.ChunkRecord{parent}
		parentIdx := 0 // will be reindexed later
		for i := range extracted {
			if extracted[i].ParentIndex == nil {
				pi := parentIdx
				extracted[i].ParentIndex = &pi
			}
		}
		return append(result, extracted...)
	}

	// No chunkable descendants and node is too large — line-split it.
	return p.splitNodeByLines(node, source, name, kind)
}

// findChunkableChildren walks a node's named children (and their structural
// descendants) to find chunkable nodes. This traverses through non-chunkable
// structural nodes (body_statement, block, declaration_list, class_body, etc.)
// to reach the chunkable leaves.
func (p *Parser) findChunkableChildren(node *tree_sitter.Node, source []byte, cfg *LanguageConfig, depth int, extracted *[]types.ChunkRecord) {
	childCount := int(node.NamedChildCount()) //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
	for i := 0; i < childCount; i++ {
		child := node.NamedChild(uint(i))
		childType := child.Kind()

		// Skip comments
		if childType == "comment" {
			continue
		}

		// Unwrap export wrapper
		if cfg.ExportType != "" && childType == cfg.ExportType {
			if inner := findDeclarationChild(child, cfg); inner != nil {
				child = inner
				childType = child.Kind()
			}
		}

		// Unwrap ambient wrapper
		if cfg.AmbientType != "" && childType == cfg.AmbientType {
			if inner := findDeclarationChild(child, cfg); inner != nil {
				child = inner
				childType = child.Kind()
			}
		}

		if _, chunkable := cfg.ChunkableTypes[childType]; chunkable {
			*extracted = append(*extracted, p.chunkNode(child, source, cfg, depth+1)...)
		} else {
			// Recurse through structural nodes (body_statement, block,
			// declaration_list, class_body, etc.) to find nested chunkables
			p.findChunkableChildren(child, source, cfg, depth, extracted)
		}
	}
}

// splitNodeByLines splits an oversized node into line-based chunks.
func (p *Parser) splitNodeByLines(node *tree_sitter.Node, source []byte, name string, kind string) []types.ChunkRecord {
	content := node.Utf8Text(source)
	startLine := int(node.StartPosition().Row) + 1 //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit
	endLine := int(node.EndPosition().Row) + 1     //nolint:gosec // tree-sitter Row is 0-based line number; fits int on 64-bit

	// Use the existing splitLargeChunks machinery via a temporary chunk
	temp := []types.ChunkRecord{{
		SymbolName: name,
		Kind:       kind,
		StartLine:  startLine,
		EndLine:    endLine,
		Content:    content,
		TokenCount: p.tokens.Count(content),
	}}
	return p.splitLargeChunks(temp)
}

// buildSignatureFromExtracted creates a compact summary of a container node
// (class, module, trait, etc.) showing its full declaration header and a
// member listing. The header is reconstructed from tree-sitter's AST: the
// range from the node's start to the `body` child's start. Everything in
// that range is the header (modifiers, keywords, name, generics,
// extends/implements clauses), possibly spanning multiple source lines.
// The closing delimiter is derived from the source bytes after the body.
// Comment children in the header range are stripped so they don't leak
// into the signature.
//
// Bug #6 in docs/review-findings/parser-bugs-from-recursive-chunking.md:
// the previous implementation scanned raw source lines and picked only the
// first non-comment line as the declaration, truncating multi-line headers
// and brittly matching comment prefixes with string operations.
func buildSignatureFromExtracted(node *tree_sitter.Node, source []byte, _ string, children []types.ChunkRecord) string {
	var sb strings.Builder

	// Find the body child — most grammars expose it via a `body` field.
	// Fallback: the last named child, which is the body for grammars that
	// use positional children (rare).
	bodyNode := node.ChildByFieldName("body")
	if bodyNode == nil {
		if n := int(node.NamedChildCount()); n > 0 { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
			bodyNode = node.NamedChild(uint(n - 1))
		}
	}

	// Header range: [node.StartByte(), bodyNode.StartByte()) when body is
	// known; the whole node otherwise.
	headerEnd := node.EndByte()
	if bodyNode != nil {
		headerEnd = bodyNode.StartByte()
	}

	header := stripHeaderComments(node, source, headerEnd)
	header = strings.TrimRight(header, " \t\n\r")
	if header != "" {
		sb.WriteString(header)
		sb.WriteString("\n")
	}

	// Member listing
	for _, child := range children {
		if child.ParentIndex != nil {
			continue // skip nested grandchildren — only list direct children
		}
		sb.WriteString("  ")
		if child.SymbolName != "" {
			sb.WriteString(child.SymbolName)
		} else {
			sb.WriteString(child.Kind)
		}
		if child.Signature != "" {
			sb.WriteString(child.Signature)
		}
		sb.WriteString(" { ... }\n")
	}

	// Closing delimiter: derive from the source bytes following the body
	// node, or from the node's trailing source if the body spans to the
	// node's end (common when the grammar includes the closing token
	// inside the body).
	if bodyNode != nil {
		after := strings.TrimSpace(string(source[bodyNode.EndByte():node.EndByte()]))
		if after != "" {
			sb.WriteString(after)
			sb.WriteString("\n")
		} else {
			trimmed := strings.TrimRight(string(source[node.StartByte():node.EndByte()]), " \t\n\r")
			switch {
			case strings.HasSuffix(trimmed, "end"):
				sb.WriteString("end\n")
			case strings.HasSuffix(trimmed, "}"):
				sb.WriteString("}\n")
			}
		}
	}

	return sb.String()
}

// stripHeaderComments returns the source between node.StartByte() and
// headerEnd with any `comment` child nodes in that range replaced by a
// single space (so tokens on either side of the comment don't fuse). This
// walks named children directly rather than doing line-prefix string
// matching, so it correctly handles block comments that span multiple
// lines and Python docstrings that are string literals rather than
// comments.
func stripHeaderComments(node *tree_sitter.Node, source []byte, headerEnd uint) string {
	var buf strings.Builder
	cursor := node.StartByte()
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		if child.StartByte() >= headerEnd {
			break
		}
		if child.Kind() != "comment" {
			continue
		}
		// Append source up to the comment, then skip the comment itself.
		if child.StartByte() > cursor {
			buf.Write(source[cursor:child.StartByte()])
		}
		buf.WriteByte(' ')
		cursor = child.EndByte()
	}
	if cursor < headerEnd {
		buf.Write(source[cursor:headerEnd])
	}
	return buf.String()
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
// Uses per-line incremental token counting to avoid O(n²) re-tokenization.
// Note: splits BEFORE the line that would exceed the limit, so emitted chunks
// stay within maxChunkTokens. This may produce different boundaries than
// previous versions that split after — a re-index will update chunk boundaries
// and embeddings will be regenerated naturally.
func (p *Parser) splitLargeChunks(chunks []types.ChunkRecord) []types.ChunkRecord {
	var result []types.ChunkRecord
	for _, c := range chunks {
		if c.TokenCount <= maxChunkTokens {
			result = append(result, c)
			continue
		}

		lines := strings.Split(c.Content, "\n")
		var current strings.Builder
		currentStart := c.StartLine
		tokensSoFar := 0

		for i, line := range lines {
			// Count tokens for this line (including newline separator)
			lineTokens := p.tokens.Count(line)
			if i < len(lines)-1 {
				lineTokens++ // account for newline token
			}

			if tokensSoFar+lineTokens >= maxChunkTokens && current.Len() > 0 {
				// Emit current chunk with exact token count
				part := current.String()
				result = append(result, types.ChunkRecord{
					ChunkIndex:  len(result),
					SymbolName:  c.SymbolName,
					Kind:        c.Kind,
					StartLine:   currentStart,
					EndLine:     c.StartLine + i - 1,
					Content:     part,
					TokenCount:  p.tokens.Count(part),
					Signature:   c.Signature,
					ParentIndex: c.ParentIndex,
				})
				current.Reset()
				currentStart = c.StartLine + i
				tokensSoFar = 0
			}

			current.WriteString(line)
			if i < len(lines)-1 {
				current.WriteString("\n")
			}
			tokensSoFar += lineTokens
		}

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
func extractName(node *tree_sitter.Node, source []byte) string {
	// Try common field names — most tree-sitter grammars expose the declared
	// name as a `name` field on the declaration node itself (Python
	// function_definition, Java class_declaration, Rust struct_item, etc.).
	for _, field := range []string{"name", "property", "function"} {
		if child := node.ChildByFieldName(field); child != nil {
			t := child.Kind()
			if t == "identifier" || t == "type_identifier" ||
				t == "property_identifier" || t == "package_identifier" ||
				t == "field_identifier" || t == "word" || t == "constant" {
				return child.Utf8Text(source)
			}
		}
	}

	// Prefer nested declarators over sibling type identifiers. Java's
	// field_declaration / local_variable_declaration has the shape
	//   field_declaration
	//     type_identifier       (ExecutorService)
	//     variable_declarator
	//       name: identifier    (executor)
	// Walking named children in order would return the type_identifier first
	// because it appears before the declarator. Looking for a
	// variable_declarator child explicitly (and then delegating to
	// extractName on it, which hits the `name` field above) returns the
	// actual variable name. Bug #1 in
	// docs/review-findings/parser-bugs-from-recursive-chunking.md.
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		if child.Kind() == "variable_declarator" {
			if name := extractName(child, source); name != "" {
				return name
			}
		}
	}

	// Walk named children for an identifier. type_identifier is excluded
	// from the top-level walk because in the contexts we reach here (field
	// declarations, wrappers without a `name` field) type_identifier is
	// almost always a type annotation, not the declared name. Legitimate
	// type-name contexts (Go type_spec, Rust struct_item) are served by
	// ChildByFieldName("name") above.
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		t := child.Kind()
		if t == "identifier" || t == "field_identifier" || t == "constant" {
			return child.Utf8Text(source)
		}
		if t != "comment" && t != "decorator" && t != "type_identifier" {
			name := extractName(child, source)
			if name != "" {
				return name
			}
		}
	}

	// Final fallback: accept a sibling type_identifier when no other name was
	// found. This preserves existing behavior for corner cases we haven't
	// catalogued.
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		if child.Kind() == "type_identifier" {
			return child.Utf8Text(source)
		}
	}
	return ""
}

// extractSignature extracts a function/method signature (params + return type).
func extractSignature(node *tree_sitter.Node, source []byte) string {
	params := node.ChildByFieldName("parameters")
	retType := node.ChildByFieldName("return_type")

	if params == nil {
		return ""
	}

	sig := params.Utf8Text(source)
	if retType != nil {
		sig += ": " + retType.Utf8Text(source)
	}
	return sig
}

// extractGoTypeName extracts the type name from a Go type_declaration via type_spec.
func extractGoTypeName(node *tree_sitter.Node, source []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		if child.Kind() == "type_spec" {
			return extractName(child, source)
		}
	}
	return extractName(node, source)
}

// findChildByType finds the first named child with the given type.
func findChildByType(node *tree_sitter.Node, nodeType string) *tree_sitter.Node {
	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		if child.Kind() == nodeType {
			return child
		}
	}
	return nil
}

// findDeclarationChild finds the declaration wrapped by an export_statement,
// ambient_declaration, or decorated_definition node. It uses
// `cfg.ChunkableTypes` as the single source of truth: any child whose kind
// is a registered chunkable (except the wrapper kinds themselves) qualifies
// as the unwrapped declaration.
//
// Wrappers are skipped to prevent infinite unwrap cycles when one wrapper
// type nests another (e.g., TypeScript `export declare ...` parses as
// export_statement > ambient_declaration > declaration).
//
// Bug #5 in docs/review-findings/parser-bugs-from-recursive-chunking.md:
// previously this helper carried a hardcoded whitelist that had to be kept
// in sync with every language's ChunkableTypes map.
func findDeclarationChild(node *tree_sitter.Node, cfg *LanguageConfig) *tree_sitter.Node {
	if decl := node.ChildByFieldName("declaration"); decl != nil {
		return decl
	}

	for i := 0; i < int(node.NamedChildCount()); i++ { //nolint:gosec // tree-sitter NamedChildCount is uint32, fits int on 64-bit
		child := node.NamedChild(uint(i))
		childKind := child.Kind()
		meta, ok := cfg.ChunkableTypes[childKind]
		if !ok {
			continue
		}
		// Skip wrapper kinds so export_statement > ambient_declaration >
		// class_declaration unwraps to the class, not the ambient wrapper.
		if childKind == cfg.ExportType || childKind == cfg.AmbientType {
			continue
		}
		// Skip empty-kind wrappers (like Python decorated_definition) to
		// avoid returning another wrapper. The caller will recurse into the
		// inner declaration if needed.
		if meta.Kind == "" {
			continue
		}
		return child
	}
	return nil
}
