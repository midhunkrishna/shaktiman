package parser

import (
	"context"
	"testing"
)

func TestParse_GoVarDeclaration(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

var x int = 5
var Exported = "val"
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Go var declarations are mapped as "variable" in SymbolKindMap
	syms := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "variable" {
			syms[s.Name] = s.IsExported
		}
	}

	if exp, ok := syms["x"]; !ok {
		t.Error("expected variable symbol 'x'")
	} else if exp {
		t.Error("'x' should not be exported")
	}

	if exp, ok := syms["Exported"]; !ok {
		t.Error("expected variable symbol 'Exported'")
	} else if !exp {
		t.Error("'Exported' should be exported")
	}
}

func TestParse_GoConstDeclaration(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

const MaxSize = 1024
const internal = "private"
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// const_declaration now correctly yields kind="constant" via extractGoVarSymbols
	found := map[string]bool{} // name → exported
	for _, s := range result.Symbols {
		if s.Kind == "constant" || s.Kind == "variable" {
			found[s.Name] = s.IsExported
		}
	}

	if exp, ok := found["MaxSize"]; !ok {
		t.Error("expected symbol 'MaxSize'")
	} else if !exp {
		t.Error("MaxSize should be exported")
	}

	if exp, ok := found["internal"]; !ok {
		t.Error("expected symbol 'internal'")
	} else if exp {
		t.Error("'internal' should not be exported")
	}
}

func TestParse_GoGroupedTypeDecl(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Grouped type declaration — extractName returns the first type name
	src := []byte(`package main

type (
	Foo struct{}
	Bar int
)
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Both types in the grouped declaration should be extracted
	foundTypes := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "type" {
			foundTypes[s.Name] = true
		}
	}
	if !foundTypes["Foo"] {
		t.Error("expected type symbol 'Foo'")
	}
	if !foundTypes["Bar"] {
		t.Error("expected type symbol 'Bar'")
	}

	// Verify chunks exist for the type group
	if len(result.Chunks) == 0 {
		t.Error("expected at least one chunk for grouped type declaration")
	}
}

func TestParse_TSConstDeclarations(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`const FOO = 42;
let bar = "hello";
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// extractVariableSymbols now yields "constant" for const and "variable" for let
	found := map[string]string{} // name → kind
	for _, s := range result.Symbols {
		found[s.Name] = s.Kind
	}

	if kind, ok := found["FOO"]; !ok {
		t.Error("expected symbol 'FOO'")
	} else if kind != "constant" {
		t.Errorf("FOO kind = %q, want 'constant'", kind)
	}
	if kind, ok := found["bar"]; !ok {
		t.Error("expected symbol 'bar'")
	} else if kind != "variable" {
		t.Errorf("bar kind = %q, want 'variable'", kind)
	}
}

func TestParse_TSExportedConst(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export const VERSION = "1.0";
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, s := range result.Symbols {
		if s.Name == "VERSION" {
			if !s.IsExported {
				t.Error("VERSION should be exported")
			}
			return
		}
	}
	t.Error("expected symbol 'VERSION'")
}

func TestParse_GoGoInterfaceDecl(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Writer interface {
	Write(p []byte) (n int, err error)
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	types := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "type" {
			types[s.Name] = true
		}
	}
	if !types["Reader"] {
		t.Error("expected type symbol 'Reader'")
	}
	if !types["Writer"] {
		t.Error("expected type symbol 'Writer'")
	}
}
