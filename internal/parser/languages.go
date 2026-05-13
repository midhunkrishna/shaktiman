package parser

import (
	"fmt"
	"sort"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tree_sitter_embedded_template "github.com/tree-sitter/tree-sitter-embedded-template/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// NodeMeta describes how a tree-sitter node type maps onto shaktiman's
// chunking, symbol, and import models. A single node can play several
// roles at once: a Go `var_declaration` is chunkable (Kind: "block"),
// extracts a symbol (SymbolKind: "variable"), and is not an import.
// A TypeScript `export_statement` is chunkable as a wrapper (Kind: "",
// IsChunkable: true) but is not itself a symbol or import.
//
// Previously this info lived across three parallel maps —
// ChunkableTypes, SymbolKindMap, ImportTypes — that had to be kept in
// sync by hand. Bug #4 in
// docs/review-findings/parser-bugs-from-recursive-chunking.md tracked
// the class of mistakes that produced. Folding them here means each
// node type is described in exactly one place.
//
// Note for chunking-version reviewers: this collapse is a representation
// change, not a behavior change. The same set of nodes is recognized for
// chunking, the same set yields symbols, the same set is treated as
// imports. ChunkAlgorithmVersion is unchanged.
type NodeMeta struct {
	// IsChunkable: this node participates in chunking. When IsChunkable
	// is true and Kind == "", the node is a wrapper (export_statement,
	// ambient_declaration, decorated_definition) whose effective chunk
	// kind comes from an inner declaration.
	IsChunkable bool
	// Kind is the chunk kind the chunker emits. Empty for wrappers.
	Kind string
	// IsContainer tells walkForSymbols whether to recurse into the
	// node's body to find nested symbol definitions. Only meaningful
	// when IsChunkable.
	IsContainer bool
	// SymbolKind is the symbol kind walkForSymbols emits. Empty means
	// the node does not produce a symbol (wrappers and most imports).
	SymbolKind string
	// IsImport marks nodes the chunker should fold into the file's
	// header rather than treat as standalone chunks.
	IsImport bool
}

// LanguageConfig holds tree-sitter grammar and per-language node-type
// metadata. Nodes is the single source of truth for chunkable, symbol,
// and import classifications — see NodeMeta.
type LanguageConfig struct {
	Name        string
	Grammar     *tree_sitter.Language
	Nodes       map[string]NodeMeta
	ExportType  string // export wrapper type (empty if N/A)
	AmbientType string // ambient declaration wrapper type (empty if N/A)
}

// languageRegistry maps a language name to its config builder. New
// languages register here; GetLanguageConfig and SupportedLanguage
// dispatch through the same map so they can never disagree.
//
// Builders are stored as functions (not pre-built configs) because each
// returns a freshly-constructed *LanguageConfig — a few callers mutate
// the returned config (e.g. tests installing a stricter import-type
// set), and sharing one instance across goroutines would cause races.
var languageRegistry = map[string]func() *LanguageConfig{
	"typescript": typescriptConfig,
	"python":     pythonConfig,
	"go":         goConfig,
	"rust":       rustConfig,
	"java":       javaConfig,
	"bash":       bashConfig,
	"javascript": javascriptConfig,
	"ruby":       rubyConfig,
	"erb":        erbConfig,
}

// GetLanguageConfig returns the config for the given language name.
func GetLanguageConfig(lang string) (*LanguageConfig, error) {
	build, ok := languageRegistry[lang]
	if !ok {
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
	return build(), nil
}

// SupportedLanguage returns true if the language is supported.
func SupportedLanguage(lang string) bool {
	_, ok := languageRegistry[lang]
	return ok
}

// SupportedLanguages returns the sorted list of language names the
// parser can handle. Tests and CLI surfaces use this to enumerate
// without hard-coding.
func SupportedLanguages() []string {
	out := make([]string, 0, len(languageRegistry))
	for k := range languageRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func typescriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "typescript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
		Nodes: map[string]NodeMeta{
			"function_declaration":           {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"generator_function_declaration": {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"function_signature":             {IsChunkable: true, Kind: "function", SymbolKind: "function"}, // overload signatures, ambient fn declarations
			"class_declaration":              {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"abstract_class_declaration":     {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"interface_declaration":          {IsChunkable: true, Kind: "interface", IsContainer: true, SymbolKind: "interface"},
			"type_alias_declaration":         {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"enum_declaration":               {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"internal_module":                {IsChunkable: true, Kind: "block", IsContainer: true, SymbolKind: "namespace"}, // namespace declarations
			"export_statement":               {IsChunkable: true, IsContainer: true},                                         // wrapper, resolved via inner child
			"ambient_declaration":            {IsChunkable: true, IsContainer: true},                                         // declare wrapper, resolved via inner
			"lexical_declaration":            {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"variable_declaration":           {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"method_definition":              {IsChunkable: true, Kind: "method", SymbolKind: "method"}, // class body methods
			"public_field_definition":        {IsChunkable: true, Kind: "block"},                       // class body fields
			"import_statement":               {IsImport: true},
		},
		ExportType:  "export_statement",
		AmbientType: "ambient_declaration",
	}
}

func pythonConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "python",
		Grammar: tree_sitter.NewLanguage(tree_sitter_python.Language()),
		Nodes: map[string]NodeMeta{
			"function_definition":   {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"class_definition":      {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"decorated_definition":  {IsChunkable: true, IsContainer: true}, // wrapper, resolved via inner
			"type_alias_statement":  {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"import_statement":      {IsChunkable: true, IsImport: true},
			"import_from_statement": {IsChunkable: true, IsImport: true},
		},
	}
}

func goConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "go",
		Grammar: tree_sitter.NewLanguage(tree_sitter_go.Language()),
		Nodes: map[string]NodeMeta{
			"function_declaration": {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"method_declaration":   {IsChunkable: true, Kind: "method", SymbolKind: "method"},
			"type_declaration":     {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"import_declaration":   {IsChunkable: true, IsImport: true},
			"var_declaration":      {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"const_declaration":    {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
		},
	}
}

func rustConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "rust",
		Grammar: tree_sitter.NewLanguage(tree_sitter_rust.Language()),
		Nodes: map[string]NodeMeta{
			"function_item":           {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"function_signature_item": {IsChunkable: true, Kind: "function", SymbolKind: "function"}, // trait method signatures, extern fn declarations
			"struct_item":             {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"enum_item":               {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"union_item":              {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"trait_item":              {IsChunkable: true, Kind: "interface", IsContainer: true, SymbolKind: "interface"},
			"impl_item":               {IsChunkable: true, Kind: "block", IsContainer: true, SymbolKind: "type"},
			"type_item":               {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"mod_item":                {IsChunkable: true, Kind: "block", IsContainer: true, SymbolKind: "module"},
			"foreign_mod_item":        {IsChunkable: true, Kind: "block", IsContainer: true}, // extern "C" { } blocks
			"use_declaration":         {IsChunkable: true, IsImport: true},
			"const_item":              {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"static_item":             {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"macro_definition":        {IsChunkable: true, Kind: "function", SymbolKind: "function"},
		},
	}
}

func javaConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "java",
		Grammar: tree_sitter.NewLanguage(tree_sitter_java.Language()),
		Nodes: map[string]NodeMeta{
			"class_declaration":               {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"interface_declaration":           {IsChunkable: true, Kind: "interface", IsContainer: true, SymbolKind: "interface"},
			"enum_declaration":                {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"record_declaration":              {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"annotation_type_declaration":     {IsChunkable: true, Kind: "type", SymbolKind: "type"},
			"method_declaration":              {IsChunkable: true, Kind: "method", SymbolKind: "method"},
			"constructor_declaration":         {IsChunkable: true, Kind: "method", SymbolKind: "method"},
			"compact_constructor_declaration": {IsChunkable: true, Kind: "method", SymbolKind: "method"}, // record compact constructors
			"static_initializer":              {IsChunkable: true, Kind: "block", IsContainer: true},    // static { } blocks
			"import_declaration":              {IsChunkable: true, IsImport: true},
			"package_declaration":             {IsChunkable: true},
			// field_declaration is dispatched to extractJavaFieldSymbols in
			// symbols.go to handle multi-declarator statements like
			// `int x = 1, y = 2, z = 3;` where each variable_declarator becomes
			// its own symbol. Local variable declarations (inside method bodies)
			// aren't reached by walkForSymbols because it doesn't recurse into
			// method_declaration, consistent with Go and TS behavior.
			"field_declaration": {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
		},
	}
}

// TODO: groovy support dropped pending official tree-sitter-groovy Go bindings.

func bashConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "bash",
		Grammar: tree_sitter.NewLanguage(tree_sitter_bash.Language()),
		Nodes: map[string]NodeMeta{
			"function_definition": {IsChunkable: true, Kind: "function", SymbolKind: "function"},
		},
	}
}

func javascriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "javascript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
		Nodes: map[string]NodeMeta{
			"function_declaration":           {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"class_declaration":              {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"generator_function_declaration": {IsChunkable: true, Kind: "function", SymbolKind: "function"},
			"export_statement":               {IsChunkable: true, IsContainer: true},
			"lexical_declaration":            {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"variable_declaration":           {IsChunkable: true, Kind: "block", SymbolKind: "variable"},
			"method_definition":              {IsChunkable: true, Kind: "method", SymbolKind: "method"}, // class body methods
			"field_definition":               {IsChunkable: true, Kind: "block"},                       // class body fields
			"import_statement":               {IsImport: true},
		},
		ExportType: "export_statement",
	}
}

func rubyConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "ruby",
		Grammar: tree_sitter.NewLanguage(tree_sitter_ruby.Language()),
		Nodes: map[string]NodeMeta{
			"method":           {IsChunkable: true, Kind: "function", SymbolKind: "method"},
			"singleton_method": {IsChunkable: true, Kind: "function", SymbolKind: "method"},
			"class":            {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"module":           {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"},
			"singleton_class":  {IsChunkable: true, Kind: "class", IsContainer: true, SymbolKind: "class"}, // class << self
			"lambda":           {IsChunkable: true, Kind: "function"},
		},
		// Ruby uses require/require_relative which are method calls, not special nodes;
		// no import node types to register.
	}
}

func erbConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "erb",
		Grammar: tree_sitter.NewLanguage(tree_sitter_embedded_template.Language()),
		Nodes: map[string]NodeMeta{
			// ERB templates are chunked by their directive types
			"directive":        {IsChunkable: true, Kind: "block"}, // <% code %>
			"output_directive": {IsChunkable: true, Kind: "block"}, // <%= expression %>
			"template":         {IsChunkable: true, Kind: "block"}, // entire template as fallback
		},
		// ERB doesn't expose traditional symbols like functions/classes —
		// the Ruby code inside directives would need language injection.
	}
}
