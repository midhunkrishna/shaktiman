package parser

import (
	"fmt"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// LanguageConfig holds tree-sitter grammar and node type mappings for a language.
type LanguageConfig struct {
	Name           string
	Grammar        *sitter.Language
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
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// SupportedLanguage returns true if the language is supported.
func SupportedLanguage(lang string) bool {
	switch lang {
	case "typescript", "python", "go":
		return true
	default:
		return false
	}
}

func typescriptConfig() *LanguageConfig {
	return &LanguageConfig{
		Name:    "typescript",
		Grammar: typescript.GetLanguage(),
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
			"method_definition":      true,
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
		Grammar: python.GetLanguage(),
		ChunkableTypes: map[string]string{
			"function_definition":  "function",
			"class_definition":     "class",
			"decorated_definition": "", // resolved based on child
			"import_statement":     "",
			"import_from_statement": "",
		},
		SymbolKindMap: map[string]string{
			"function_definition": "function",
			"class_definition":   "class",
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
		Grammar: golang.GetLanguage(),
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
