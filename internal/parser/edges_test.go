package parser

import (
	"context"
	"testing"
)

func TestEdges_TypeScriptImports(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import { foo } from 'bar';
import { baz } from 'qux';

export function doStuff(input: string): string {
  return foo(input) + baz(input);
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := 0
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			found++
		}
	}
	if found < 2 {
		t.Errorf("expected at least 2 import edges, got %d", found)
	}
}

func TestEdges_TypeScriptCalls(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`function helper(): string {
  return "help";
}

function main(): void {
  const result = helper();
  console.log(result);
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, e := range result.Edges {
		if e.Kind == "calls" && e.DstSymbolName == "helper" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a 'calls' edge targeting 'helper'")
	}
}

func TestEdges_TypeScriptInheritance(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class Base {
  run(): void {}
}

class Child extends Base {
  run(): void {}
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, e := range result.Edges {
		if e.Kind == "inherits" && e.SrcSymbolName == "Child" && e.DstSymbolName == "Base" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an 'inherits' edge from 'Child' to 'Base'")
	}
}

func TestEdges_PythonCalls(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte("def helper():\n    return \"help\"\n\ndef main():\n    result = helper()\n    print(result)\n")

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, e := range result.Edges {
		if e.Kind == "calls" && e.DstSymbolName == "helper" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a 'calls' edge targeting 'helper'")
	}
}

func TestEdges_PythonInheritance(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte("class Base:\n    def run(self):\n        pass\n\nclass Child(Base):\n    def run(self):\n        pass\n")

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, e := range result.Edges {
		if e.Kind == "inherits" && e.SrcSymbolName == "Child" && e.DstSymbolName == "Base" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected an 'inherits' edge from 'Child' to 'Base'")
	}
}

func TestEdges_GoCalls(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte("package main\n\nfunc helper() string {\n\treturn \"help\"\n}\n\nfunc main() {\n\tresult := helper()\n\tprintln(result)\n}")

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, e := range result.Edges {
		if e.Kind == "calls" && e.DstSymbolName == "helper" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a 'calls' edge targeting 'helper'")
	}
}
