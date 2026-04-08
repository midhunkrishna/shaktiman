package parser

import (
	"fmt"

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

// NodeMeta describes a chunkable tree-sitter node type.
//
// Kind is the chunk kind emitted by the chunker ("function", "class",
// "method", "type", "interface", "block", or "" for wrappers whose kind
// must be resolved from an inner declaration). IsContainer tells
// walkForSymbols whether to recurse into the node's body to find nested
// symbol definitions. Containers include classes, interfaces, traits,
// modules, namespaces, impl blocks, and wrapper statements like
// export_statement / decorated_definition. Leaf declarations like
// function_item, const_item, field_declaration, type_alias_declaration,
// enum_declaration, and method_definition set IsContainer=false so the
// walker stops at the declaration node.
//
// Bug #4 in docs/review-findings/parser-bugs-from-recursive-chunking.md:
// previously ChunkableTypes was map[string]string and walkForSymbols had a
// hardcoded whitelist of container kinds + node types, duplicating
// information that should live in one place. NodeMeta is the single source
// of truth.
type NodeMeta struct {
	Kind        string
	IsContainer bool
}

// LanguageConfig holds tree-sitter grammar and node type mappings for a language.
type LanguageConfig struct {
	Name           string
	Grammar        *tree_sitter.Language
	ChunkableTypes map[string]NodeMeta // node_type → chunk kind + container flag
	SymbolKindMap  map[string]string   // node_type → symbol kind
	ImportTypes    map[string]bool     // node types treated as imports
	ExportType     string              // export wrapper type (empty if N/A)
	AmbientType    string              // ambient declaration wrapper type (empty if N/A)
}

// GetLanguageConfig returns the config for the given language name.
func GetLanguageConfig(lang string) (*LanguageConfig, error) {
	switch lang {
	case "typescript":
		return typescriptConfig(), nil
	case "python":
		return pythonConfig(), nil
	case "go":
		return goConfig(), nil
	case "rust":
		return rustConfig(), nil
	case "java":
		return javaConfig(), nil
	case "bash":
		return bashConfig(), nil
	case "javascript":
		return javascriptConfig(), nil
	case "ruby":
		return rubyConfig(), nil
	case "erb":
		return erbConfig(), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// SupportedLanguage returns true if the language is supported.
func SupportedLanguage(lang string) bool {
	switch lang {
	case "typescript", "python", "go", "rust", "java", "bash", "javascript", "ruby", "erb":
		return true
	default:
		return false
	}
}

func typescriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "typescript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
		ChunkableTypes: map[string]NodeMeta{
			"function_declaration":           {Kind: "function"},
			"generator_function_declaration": {Kind: "function"},
			"function_signature":             {Kind: "function"}, // overload signatures, ambient fn declarations
			"class_declaration":              {Kind: "class", IsContainer: true},
			"abstract_class_declaration":     {Kind: "class", IsContainer: true},
			"interface_declaration":          {Kind: "interface", IsContainer: true},
			"type_alias_declaration":         {Kind: "type"},
			"enum_declaration":               {Kind: "type"},
			"internal_module":                {Kind: "block", IsContainer: true}, // namespace declarations
			"export_statement":               {Kind: "", IsContainer: true},      // wrapper, resolved via inner child
			"ambient_declaration":            {Kind: "", IsContainer: true},      // declare wrapper, resolved via inner
			"lexical_declaration":            {Kind: "block"},
			"variable_declaration":           {Kind: "block"},
			"method_definition":              {Kind: "method"}, // class body methods
			"public_field_definition":        {Kind: "block"},  // class body fields
		},
		SymbolKindMap: map[string]string{
			"function_declaration":           "function",
			"generator_function_declaration": "function",
			"function_signature":             "function",
			"class_declaration":              "class",
			"abstract_class_declaration":     "class",
			"method_definition":              "method",
			"interface_declaration":          "interface",
			"type_alias_declaration":         "type",
			"enum_declaration":               "type",
			"internal_module":                "namespace",
			"lexical_declaration":            "variable",
			"variable_declaration":           "variable",
		},
		ImportTypes: map[string]bool{
			"import_statement": true,
		},
		ExportType:  "export_statement",
		AmbientType: "ambient_declaration",
	}
}

func pythonConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "python",
		Grammar: tree_sitter.NewLanguage(tree_sitter_python.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"function_definition":   {Kind: "function"},
			"class_definition":      {Kind: "class", IsContainer: true},
			"decorated_definition":  {Kind: "", IsContainer: true}, // wrapper, resolved via inner
			"type_alias_statement":  {Kind: "type"},
			"import_statement":      {Kind: ""},
			"import_from_statement": {Kind: ""},
		},
		SymbolKindMap: map[string]string{
			"function_definition":  "function",
			"class_definition":     "class",
			"type_alias_statement": "type",
		},
		ImportTypes: map[string]bool{
			"import_statement":      true,
			"import_from_statement": true,
		},
	}
}

func goConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "go",
		Grammar: tree_sitter.NewLanguage(tree_sitter_go.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"function_declaration": {Kind: "function"},
			"method_declaration":   {Kind: "method"},
			"type_declaration":     {Kind: "type"},
			"import_declaration":   {Kind: ""},
			"var_declaration":      {Kind: "block"},
			"const_declaration":    {Kind: "block"},
		},
		SymbolKindMap: map[string]string{
			"function_declaration": "function",
			"method_declaration":   "method",
			"type_declaration":     "type",
			"var_declaration":      "variable",
			"const_declaration":    "variable",
		},
		ImportTypes: map[string]bool{
			"import_declaration": true,
		},
	}
}

func rustConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "rust",
		Grammar: tree_sitter.NewLanguage(tree_sitter_rust.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"function_item":           {Kind: "function"},
			"function_signature_item": {Kind: "function"}, // trait method signatures, extern fn declarations
			"struct_item":             {Kind: "type"},
			"enum_item":               {Kind: "type"},
			"union_item":              {Kind: "type"},
			"trait_item":              {Kind: "interface", IsContainer: true},
			"impl_item":               {Kind: "block", IsContainer: true},
			"type_item":                {Kind: "type"},
			"mod_item":                 {Kind: "block", IsContainer: true},
			"foreign_mod_item":         {Kind: "block", IsContainer: true}, // extern "C" { } blocks
			"use_declaration":          {Kind: ""},
			"const_item":               {Kind: "block"},
			"static_item":              {Kind: "block"},
			"macro_definition":         {Kind: "function"},
		},
		SymbolKindMap: map[string]string{
			"function_item":           "function",
			"function_signature_item": "function",
			"struct_item":             "type",
			"enum_item":               "type",
			"union_item":              "type",
			"trait_item":              "interface",
			"impl_item":              "type",
			"type_item":               "type",
			"mod_item":                "module",
			"const_item":              "variable",
			"static_item":             "variable",
			"macro_definition":        "function",
		},
		ImportTypes: map[string]bool{
			"use_declaration": true,
		},
	}
}

func javaConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "java",
		Grammar: tree_sitter.NewLanguage(tree_sitter_java.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"class_declaration":               {Kind: "class", IsContainer: true},
			"interface_declaration":           {Kind: "interface", IsContainer: true},
			"enum_declaration":                {Kind: "type"},
			"record_declaration":              {Kind: "class", IsContainer: true},
			"annotation_type_declaration":     {Kind: "type"},
			"method_declaration":              {Kind: "method"},
			"constructor_declaration":         {Kind: "method"},
			"compact_constructor_declaration": {Kind: "method"},                     // record compact constructors
			"static_initializer":              {Kind: "block", IsContainer: true},   // static { } blocks
			"import_declaration":              {Kind: ""},
			"package_declaration":             {Kind: ""},
			"field_declaration":               {Kind: "block"},
		},
		SymbolKindMap: map[string]string{
			"class_declaration":               "class",
			"interface_declaration":           "interface",
			"enum_declaration":                "type",
			"record_declaration":              "class",
			"annotation_type_declaration":     "type",
			"method_declaration":              "method",
			"constructor_declaration":         "method",
			"compact_constructor_declaration": "method",
			// field_declaration is dispatched to extractJavaVariableSymbols in
			// symbols.go to handle multi-declarator statements like
			// `int x = 1, y = 2, z = 3;` where each variable_declarator becomes
			// its own symbol. Local variable declarations (inside method bodies)
			// aren't reached by walkForSymbols because it doesn't recurse into
			// method_declaration, consistent with Go and TS behavior.
			"field_declaration": "variable",
		},
		ImportTypes: map[string]bool{
			"import_declaration": true,
		},
	}
}

// TODO: groovy support dropped pending official tree-sitter-groovy Go bindings.

func bashConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "bash",
		Grammar: tree_sitter.NewLanguage(tree_sitter_bash.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"function_definition": {Kind: "function"},
		},
		SymbolKindMap: map[string]string{
			"function_definition": "function",
		},
	}
}

func javascriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "javascript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"function_declaration":           {Kind: "function"},
			"class_declaration":              {Kind: "class", IsContainer: true},
			"generator_function_declaration": {Kind: "function"},
			"export_statement":               {Kind: "", IsContainer: true},
			"lexical_declaration":            {Kind: "block"},
			"variable_declaration":           {Kind: "block"},
			"method_definition":              {Kind: "method"}, // class body methods
			"field_definition":               {Kind: "block"},  // class body fields
		},
		SymbolKindMap: map[string]string{
			"function_declaration":           "function",
			"class_declaration":              "class",
			"generator_function_declaration": "function",
			"method_definition":              "method",
			"lexical_declaration":            "variable",
			"variable_declaration":           "variable",
		},
		ImportTypes: map[string]bool{
			"import_statement": true,
		},
		ExportType: "export_statement",
	}
}

func rubyConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "ruby",
		Grammar: tree_sitter.NewLanguage(tree_sitter_ruby.Language()),
		ChunkableTypes: map[string]NodeMeta{
			"method":           {Kind: "function"},
			"singleton_method": {Kind: "function"},
			"class":            {Kind: "class", IsContainer: true},
			"module":           {Kind: "class", IsContainer: true},
			"singleton_class":  {Kind: "class", IsContainer: true}, // class << self
			"lambda":           {Kind: "function"},
		},
		SymbolKindMap: map[string]string{
			"method":           "method",
			"singleton_method": "method",
			"class":            "class",
			"module":           "class",
			"singleton_class":  "class",
		},
		ImportTypes: map[string]bool{
			// Ruby uses require/require_relative which are method calls, not special nodes.
		},
	}
}

func erbConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "erb",
		Grammar: tree_sitter.NewLanguage(tree_sitter_embedded_template.Language()),
		ChunkableTypes: map[string]NodeMeta{
			// ERB templates are chunked by their directive types
			"directive":        {Kind: "block"}, // <% code %>
			"output_directive": {Kind: "block"}, // <%= expression %>
			"template":         {Kind: "block"}, // entire template as fallback
		},
		SymbolKindMap: map[string]string{
			// ERB doesn't have traditional symbols like functions/classes
			// The Ruby code inside directives would need language injection to parse
		},
	}
}
