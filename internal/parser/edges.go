package parser

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/shaktimanai/shaktiman/internal/types"
)

// edgeContext tracks state during edge extraction walk.
type edgeContext struct {
	source      []byte
	cfg         *LanguageConfig
	edges       []types.EdgeRecord
	seen        map[string]bool // "src|dst|kind" dedup
	importOwner string          // fallback owner for file-level imports
}

func (c *edgeContext) addEdge(src, dst, kind string) {
	c.addEdgeQualified(src, dst, "", kind)
}

func (c *edgeContext) addEdgeQualified(src, dst, qualifiedDst, kind string) {
	if dst == "" {
		return
	}
	key := src + "|" + dst + "|" + kind
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.edges = append(c.edges, types.EdgeRecord{
		SrcSymbolName:    src,
		DstSymbolName:    dst,
		DstQualifiedName: qualifiedDst,
		Kind:             kind,
		IsCrossFile:      kind == "imports",
	})
}

// extractEdges extracts dependency edges (imports, calls, inherits) from the AST.
func (p *Parser) extractEdges(root *tree_sitter.Node, source []byte, symbols []types.SymbolRecord, cfg *LanguageConfig) []types.EdgeRecord {
	// importOwner is the fallback source for file-level import edges that
	// appear before any class/function declaration. Without this, file-level
	// imports get SrcSymbolName="" and are dropped during storage because no
	// symbol ID can be resolved for "".
	//
	// Only imports use this fallback. Top-level call edges keep owner=""
	// and are correctly dropped (the graph has no node for module-level code).
	importOwner := ""
	for _, s := range symbols {
		if s.Name != "" && s.Name != "_" {
			importOwner = s.Name
			break
		}
	}

	ctx := &edgeContext{
		source:      source,
		cfg:         cfg,
		seen:        make(map[string]bool),
		importOwner: importOwner,
	}

	p.walkForEdges(root, "", ctx)
	return ctx.edges
}

func (p *Parser) walkForEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	nodeType := node.Kind()

	// Handle export wrapper (TypeScript)
	if ctx.cfg.ExportType != "" && nodeType == ctx.cfg.ExportType {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			p.walkForEdges(node.NamedChild(uint(i)), owner, ctx)
		}
		return
	}

	// Handle decorated_definition (Python)
	if nodeType == "decorated_definition" {
		for i := 0; i < int(node.NamedChildCount()); i++ {
			child := node.NamedChild(uint(i))
			if child.Kind() != "decorator" {
				p.walkForEdges(child, owner, ctx)
			}
		}
		return
	}

	// Import edges — don't recurse further into import nodes
	if ctx.cfg.ImportTypes[nodeType] {
		p.extractImportEdgesFrom(node, owner, ctx)
		return
	}

	// Update owner for symbol-defining nodes
	newOwner := owner
	if _, isSymbol := ctx.cfg.SymbolKindMap[nodeType]; isSymbol {
		name := extractName(node, ctx.source)
		if name != "" {
			newOwner = name
		}
	}

	// Call expressions
	if nodeType == "call_expression" || nodeType == "call" || nodeType == "method_invocation" || nodeType == "function_call" {
		callee := extractCalleeName(node, ctx.source)
		if callee != "" && callee != newOwner {
			ctx.addEdge(newOwner, callee, "calls")
		}
	}

	// TypeScript/JavaScript class heritage
	if nodeType == "extends_clause" || nodeType == "class_heritage" {
		p.extractHeritageTypeNames(node, newOwner, "inherits", ctx)
		return
	}
	if nodeType == "implements_clause" {
		p.extractHeritageTypeNames(node, newOwner, "implements", ctx)
		return
	}

	// Java inheritance
	if nodeType == "superclass" {
		p.extractHeritageTypeNames(node, newOwner, "inherits", ctx)
		return
	}
	if nodeType == "super_interfaces" {
		p.extractHeritageTypeNames(node, newOwner, "implements", ctx)
		return
	}

	// Python class bases
	if nodeType == "class_definition" && ctx.cfg.Name == "python" {
		superclasses := node.ChildByFieldName("superclasses")
		if superclasses != nil {
			p.extractPythonBases(superclasses, newOwner, ctx)
		}
	}

	// Recurse into children
	for i := 0; i < int(node.NamedChildCount()); i++ {
		p.walkForEdges(node.NamedChild(uint(i)), newOwner, ctx)
	}
}

// ── Import edge extraction ──

func (p *Parser) extractImportEdgesFrom(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	// For file-level imports (owner=""), use the importOwner fallback so
	// the edge gets a valid SrcSymbolName and isn't dropped by InsertEdges.
	if owner == "" && ctx.importOwner != "" {
		owner = ctx.importOwner
	}
	switch ctx.cfg.Name {
	case "typescript":
		p.tsImportEdges(node, owner, ctx)
	case "javascript":
		p.tsImportEdges(node, owner, ctx) // same AST structure as TypeScript
	case "python":
		p.pyImportEdges(node, owner, ctx)
	case "go":
		p.goImportEdges(node, owner, ctx)
	case "java":
		p.javaImportEdges(node, owner, ctx)
	case "rust":
		p.rustImportEdges(node, owner, ctx)
	// bash has no imports
	}
}

func (p *Parser) tsImportEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	clause := findChildByType(node, "import_clause")
	if clause == nil {
		return
	}

	// Extract module path from the import source (e.g. 'bar' in `import { Foo } from 'bar'`)
	modulePath := ""
	src := findChildByType(node, "string")
	if src != nil {
		modulePath = strings.Trim(src.Utf8Text(ctx.source), "\"'`")
	}

	for i := 0; i < int(clause.NamedChildCount()); i++ {
		child := clause.NamedChild(uint(i))
		switch child.Kind() {
		case "identifier":
			// Default import: import Foo from 'bar'
			shortName := child.Utf8Text(ctx.source)
			ctx.addEdgeQualified(owner, shortName, modulePath+"/"+shortName, "imports")
		case "named_imports":
			// Named imports: import { Foo, Bar } from 'baz'
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(uint(j))
				if spec.Kind() == "import_specifier" {
					name := spec.ChildByFieldName("name")
					if name != nil {
						shortName := name.Utf8Text(ctx.source)
						ctx.addEdgeQualified(owner, shortName, modulePath+"/"+shortName, "imports")
					}
				}
			}
		case "namespace_import":
			// import * as ns from 'bar'
			name := extractName(child, ctx.source)
			if name != "" {
				ctx.addEdgeQualified(owner, name, modulePath, "imports")
			}
		}
	}
}

func (p *Parser) pyImportEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	moduleField := node.ChildByFieldName("module_name")

	// Extract module prefix for "from X import Y" statements
	modulePrefix := ""
	if moduleField != nil {
		modulePrefix = moduleField.Utf8Text(ctx.source)
	}

	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		// Skip the module_name subtree for import_from_statement
		if n == moduleField {
			return
		}
		switch n.Kind() {
		case "dotted_name":
			content := n.Utf8Text(ctx.source)
			qualified := content
			if modulePrefix != "" {
				qualified = modulePrefix + "." + content
			}
			ctx.addEdgeQualified(owner, content, qualified, "imports")
			return
		case "aliased_import":
			name := n.ChildByFieldName("name")
			if name != nil {
				content := name.Utf8Text(ctx.source)
				qualified := content
				if modulePrefix != "" {
					qualified = modulePrefix + "." + content
				}
				ctx.addEdgeQualified(owner, content, qualified, "imports")
			}
			return
		case "wildcard_import":
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(node)
}

func (p *Parser) goImportEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if n.Kind() == "import_spec" {
			path := n.ChildByFieldName("path")
			if path != nil {
				pkgPath := strings.Trim(path.Utf8Text(ctx.source), "\"")
				parts := strings.Split(pkgPath, "/")
				pkgName := parts[len(parts)-1]
				ctx.addEdgeQualified(owner, pkgName, pkgPath, "imports")
			}
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(node)
}

func (p *Parser) javaImportEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	// Java: import foo.bar.Baz; → extract "Baz" with qualified "foo.bar.Baz"
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if n.Kind() == "scoped_identifier" {
			qualified := n.Utf8Text(ctx.source)
			name := n.ChildByFieldName("name")
			if name != nil {
				ctx.addEdgeQualified(owner, name.Utf8Text(ctx.source), qualified, "imports")
			}
			return
		}
		if n.Kind() == "identifier" {
			content := n.Utf8Text(ctx.source)
			ctx.addEdgeQualified(owner, content, content, "imports")
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(node)
}

// TODO: groovyImportEdges removed — groovy support dropped pending official Go bindings.

func (p *Parser) rustImportEdges(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	// Rust: use std::collections::HashMap; → extract "HashMap" with qualified "std::collections::HashMap"
	// Also handles: use std::{io, fs}; (use_list) and use foo as bar (use_as_clause)
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		switch n.Kind() {
		case "use_as_clause":
			// use foo::Bar as Baz → extract the alias "Baz" with the original path as qualified
			alias := n.ChildByFieldName("alias")
			if alias != nil {
				// Extract the path part (before "as")
				path := n.ChildByFieldName("path")
				qualified := ""
				if path != nil {
					qualified = path.Utf8Text(ctx.source)
				}
				ctx.addEdgeQualified(owner, alias.Utf8Text(ctx.source), qualified, "imports")
				return
			}
			// No alias field, fall through to extract the path's last component
		case "scoped_identifier":
			qualified := n.Utf8Text(ctx.source)
			name := n.ChildByFieldName("name")
			if name != nil {
				ctx.addEdgeQualified(owner, name.Utf8Text(ctx.source), qualified, "imports")
			}
			return
		case "identifier":
			content := n.Utf8Text(ctx.source)
			ctx.addEdgeQualified(owner, content, content, "imports")
			return
		case "scoped_use_list":
			// use std::{io, fs}; → recurse into the use_list child
			// Extract the prefix path for qualification
			pathNode := n.ChildByFieldName("path")
			prefix := ""
			if pathNode != nil {
				prefix = pathNode.Utf8Text(ctx.source)
			}
			list := n.ChildByFieldName("list")
			if list != nil {
				for i := 0; i < int(list.NamedChildCount()); i++ {
					child := list.NamedChild(uint(i))
					if child.Kind() == "identifier" {
						shortName := child.Utf8Text(ctx.source)
						qualified := shortName
						if prefix != "" {
							qualified = prefix + "::" + shortName
						}
						ctx.addEdgeQualified(owner, shortName, qualified, "imports")
					} else {
						walk(child)
					}
				}
			}
			return
		case "use_wildcard":
			// use foo::*; → skip, no specific name to extract
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(node)
}

// ── Call expression helpers ──

func extractCalleeName(node *tree_sitter.Node, source []byte) string {
	fn := node.ChildByFieldName("function")
	if fn != nil {
		return resolveCallee(fn, source)
	}
	// Java method_invocation uses "name" field
	name := node.ChildByFieldName("name")
	if name != nil {
		return name.Utf8Text(source)
	}
	return ""
}

// resolveCallee returns a qualified name for the callee of a call expression,
// preserving the receiver for member-style invocations. For `a.foo()` this
// returns "a.foo"; for `pkg1.Func()` this returns "pkg1.Func"; for nested
// chains like `a.b.c()` it recursively resolves to "a.b.c". Plain identifier
// callees return just the identifier.
//
// Bug #8 in docs/review-findings/parser-bugs-from-recursive-chunking.md:
// previously this helper returned only the .property / .field / .attribute
// leaf, causing `a.foo()` and `b.foo()` to collapse to a single "foo" edge
// in the call graph.
func resolveCallee(node *tree_sitter.Node, source []byte) string {
	switch node.Kind() {
	case "identifier", "type_identifier", "property_identifier",
		"field_identifier", "package_identifier", "constant", "word":
		return node.Utf8Text(source)
	case "member_expression":
		// TypeScript / JavaScript: obj.method
		obj := node.ChildByFieldName("object")
		prop := node.ChildByFieldName("property")
		return joinReceiverProp(obj, prop, source)
	case "selector_expression":
		// Go: pkg.Func or recv.Method
		operand := node.ChildByFieldName("operand")
		field := node.ChildByFieldName("field")
		return joinReceiverProp(operand, field, source)
	case "attribute":
		// Python: obj.method
		obj := node.ChildByFieldName("object")
		attr := node.ChildByFieldName("attribute")
		return joinReceiverProp(obj, attr, source)
	case "field_access":
		// Java: obj.field (not a call, but member calls on qualified fields)
		obj := node.ChildByFieldName("object")
		field := node.ChildByFieldName("field")
		return joinReceiverProp(obj, field, source)
	case "scoped_identifier":
		// Rust: foo::bar
		path := node.ChildByFieldName("path")
		name := node.ChildByFieldName("name")
		if path != nil && name != nil {
			leftStr := resolveCallee(path, source)
			rightStr := name.Utf8Text(source)
			if leftStr != "" && rightStr != "" {
				return leftStr + "::" + rightStr
			}
			if rightStr != "" {
				return rightStr
			}
		}
	}
	return ""
}

// joinReceiverProp concatenates a receiver (object/operand) and a property
// (method/field) with a dot separator. If the receiver can't be resolved
// (complex expression, e.g. `(a + b).foo()`), fall back to just the
// property name so the caller still gets a best-effort target.
func joinReceiverProp(receiver, prop *tree_sitter.Node, source []byte) string {
	if prop == nil {
		return ""
	}
	propText := prop.Utf8Text(source)
	if receiver == nil {
		return propText
	}
	receiverText := resolveCallee(receiver, source)
	if receiverText == "" {
		return propText
	}
	return receiverText + "." + propText
}

// ── Inheritance/heritage helpers ──

func (p *Parser) extractHeritageTypeNames(node *tree_sitter.Node, owner string, kind string, ctx *edgeContext) {
	var walk func(n *tree_sitter.Node)
	walk = func(n *tree_sitter.Node) {
		if n.Kind() == "type_identifier" || n.Kind() == "identifier" {
			ctx.addEdge(owner, n.Utf8Text(ctx.source), kind)
			return
		}
		// Skip generic type arguments
		if n.Kind() == "type_arguments" {
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(uint(i)))
		}
	}
	walk(node)
}

func (p *Parser) extractPythonBases(node *tree_sitter.Node, owner string, ctx *edgeContext) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(uint(i))
		switch child.Kind() {
		case "identifier":
			ctx.addEdge(owner, child.Utf8Text(ctx.source), "inherits")
		case "attribute":
			attr := child.ChildByFieldName("attribute")
			if attr != nil {
				ctx.addEdge(owner, attr.Utf8Text(ctx.source), "inherits")
			}
		case "keyword_argument":
			// metaclass=Meta — skip
		default:
			name := extractName(child, ctx.source)
			if name != "" {
				ctx.addEdge(owner, name, "inherits")
			}
		}
	}
}
