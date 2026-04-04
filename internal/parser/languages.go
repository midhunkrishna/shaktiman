package parser

import (
	"fmt"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// LanguageConfig holds tree-sitter grammar and node type mappings for a language.
type LanguageConfig struct {
	Name           string
	Grammar        *tree_sitter.Language
	ChunkableTypes map[string]string // node_type → chunk kind
	SymbolKindMap  map[string]string // node_type → symbol kind
	ClassBodyTypes map[string]bool   // method-like types inside class bodies
	ImportTypes    map[string]bool   // node types treated as imports
	ExportType     string            // export wrapper type (empty if N/A)
	ClassBodyType  string            // node type for class body
	ClassTypes     map[string]bool   // node types treated as classes
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
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// SupportedLanguage returns true if the language is supported.
func SupportedLanguage(lang string) bool {
	switch lang {
	case "typescript", "python", "go", "rust", "java", "bash", "javascript", "ruby":
		return true
	default:
		return false
	}
}

func typescriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "typescript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
		ChunkableTypes: map[string]string{
			"function_declaration":       "function",
			"class_declaration":          "class",
			"interface_declaration":      "interface",
			"type_alias_declaration":     "type",
			"enum_declaration":           "type",
			"export_statement":           "", // resolved based on child
			"lexical_declaration":        "block",
			"variable_declaration":       "block",
			"abstract_class_declaration": "class",
		},
		SymbolKindMap: map[string]string{
			"function_declaration":       "function",
			"class_declaration":          "class",
			"abstract_class_declaration": "class",
			"method_definition":          "method",
			"interface_declaration":      "interface",
			"type_alias_declaration":     "type",
			"enum_declaration":           "type",
			"lexical_declaration":        "variable",
			"variable_declaration":       "variable",
		},
		ClassBodyTypes: map[string]bool{
			"method_definition":       true,
			"public_field_definition": true,
		},
		ImportTypes: map[string]bool{
			"import_statement": true,
		},
		ExportType:    "export_statement",
		ClassBodyType: "class_body",
		ClassTypes: map[string]bool{
			"class_declaration":          true,
			"abstract_class_declaration": true,
		},
	}
}

func pythonConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "python",
		Grammar: tree_sitter.NewLanguage(tree_sitter_python.Language()),
		ChunkableTypes: map[string]string{
			"function_definition":   "function",
			"class_definition":      "class",
			"decorated_definition":  "", // resolved based on child
			"import_statement":      "",
			"import_from_statement": "",
		},
		SymbolKindMap: map[string]string{
			"function_definition": "function",
			"class_definition":    "class",
		},
		ClassBodyTypes: map[string]bool{
			"function_definition": true,
		},
		ImportTypes: map[string]bool{
			"import_statement":      true,
			"import_from_statement": true,
		},
		ExportType:    "", // Python has no export wrapper
		ClassBodyType: "block",
		ClassTypes: map[string]bool{
			"class_definition": true,
		},
	}
}

func goConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "go",
		Grammar: tree_sitter.NewLanguage(tree_sitter_go.Language()),
		ChunkableTypes: map[string]string{
			"function_declaration": "function",
			"method_declaration":   "method",
			"type_declaration":     "type",
			"import_declaration":   "",
			"var_declaration":      "block",
			"const_declaration":    "block",
		},
		SymbolKindMap: map[string]string{
			"function_declaration": "function",
			"method_declaration":   "method",
			"type_declaration":     "type",
			"var_declaration":      "variable",
			"const_declaration":    "variable",
		},
		ClassBodyTypes: map[string]bool{}, // Go has no class bodies
		ImportTypes: map[string]bool{
			"import_declaration": true,
		},
		ExportType:    "",
		ClassBodyType: "",
		ClassTypes:    map[string]bool{},
	}
}

func rustConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "rust",
		Grammar: tree_sitter.NewLanguage(tree_sitter_rust.Language()),
		ChunkableTypes: map[string]string{
			"function_item":    "function",
			"struct_item":      "type",
			"enum_item":        "type",
			"trait_item":       "interface",
			"impl_item":        "block",
			"type_item":        "type",
			"mod_item":         "block",
			"use_declaration":  "",
			"const_item":       "block",
			"static_item":      "block",
			"macro_definition": "function",
		},
		SymbolKindMap: map[string]string{
			"function_item":    "function",
			"struct_item":      "type",
			"enum_item":        "type",
			"trait_item":       "interface",
			"impl_item":        "type",
			"type_item":        "type",
			"const_item":       "variable",
			"static_item":      "variable",
			"macro_definition": "function",
		},
		ClassBodyTypes: map[string]bool{
			"function_item": true, // methods inside impl blocks
		},
		ImportTypes: map[string]bool{
			"use_declaration": true,
		},
		ExportType:    "", // Rust uses pub visibility, not export wrappers
		ClassBodyType: "declaration_list",
		ClassTypes: map[string]bool{
			"impl_item": true,
		},
	}
}

func javaConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "java",
		Grammar: tree_sitter.NewLanguage(tree_sitter_java.Language()),
		ChunkableTypes: map[string]string{
			"class_declaration":           "class",
			"interface_declaration":       "interface",
			"enum_declaration":            "type",
			"record_declaration":          "class",
			"annotation_type_declaration": "type",
			"method_declaration":          "method",
			"constructor_declaration":     "method",
			"import_declaration":          "",
			"package_declaration":         "",
			"field_declaration":           "block",
		},
		SymbolKindMap: map[string]string{
			"class_declaration":           "class",
			"interface_declaration":       "interface",
			"enum_declaration":            "type",
			"record_declaration":          "class",
			"annotation_type_declaration": "type",
			"method_declaration":          "method",
			"constructor_declaration":     "method",
			"field_declaration":           "variable",
		},
		ClassBodyTypes: map[string]bool{
			"method_declaration":      true,
			"constructor_declaration": true,
		},
		ImportTypes: map[string]bool{
			"import_declaration": true,
		},
		ExportType:    "",
		ClassBodyType: "class_body",
		ClassTypes: map[string]bool{
			"class_declaration":  true,
			"record_declaration": true,
		},
	}
}

// TODO: groovy support dropped pending official tree-sitter-groovy Go bindings.

func bashConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "bash",
		Grammar: tree_sitter.NewLanguage(tree_sitter_bash.Language()),
		ChunkableTypes: map[string]string{
			"function_definition": "function",
		},
		SymbolKindMap: map[string]string{
			"function_definition": "function",
		},
		ClassBodyTypes: map[string]bool{},
		ImportTypes:    map[string]bool{},
		ExportType:     "",
		ClassBodyType:  "",
		ClassTypes:     map[string]bool{},
	}
}

func javascriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "javascript",
		Grammar: tree_sitter.NewLanguage(tree_sitter_javascript.Language()),
		ChunkableTypes: map[string]string{
			"function_declaration":           "function",
			"class_declaration":              "class",
			"generator_function_declaration": "function",
			"export_statement":               "",
			"lexical_declaration":            "block",
			"variable_declaration":           "block",
		},
		SymbolKindMap: map[string]string{
			"function_declaration":           "function",
			"class_declaration":              "class",
			"generator_function_declaration": "function",
			"method_definition":              "method",
			"lexical_declaration":            "variable",
			"variable_declaration":           "variable",
		},
		ClassBodyTypes: map[string]bool{
			"method_definition": true,
			"field_definition":  true,
		},
		ImportTypes: map[string]bool{
			"import_statement": true,
		},
		ExportType:    "export_statement",
		ClassBodyType: "class_body",
		ClassTypes: map[string]bool{
			"class_declaration": true,
		},
	}
}

func rubyConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "ruby",
		Grammar: tree_sitter.NewLanguage(tree_sitter_ruby.Language()),
		ChunkableTypes: map[string]string{
			"method":           "function",
			"singleton_method": "function",
			"class":            "class",
			"module":           "class",
			"lambda":           "function",
		},
		SymbolKindMap: map[string]string{
			"method":           "method",
			"singleton_method": "method",
			"class":            "class",
			"module":           "class",
		},
		ClassBodyTypes: map[string]bool{
			"method":           true,
			"singleton_method": true,
		},
		ImportTypes: map[string]bool{
			// Ruby uses require/require_relative which are method calls, not special nodes.
			// We don't track them as imports for now.
		},
		ExportType:    "", // Ruby has no export wrapper
		ClassBodyType: "body_statement",
		ClassTypes: map[string]bool{
			"class":  true,
			"module": true,
		},
	}
}
