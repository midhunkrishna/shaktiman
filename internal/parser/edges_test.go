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

func TestEdges_GroovyImports(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import groovy.transform.ToString
import java.util.List

class MyService {
    String name
    List items
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.groovy",
		Content:  src,
		Language: "groovy",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	imports := map[string]bool{}
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			imports[e.DstSymbolName] = true
		}
	}
	for _, name := range []string{"ToString", "List"} {
		if !imports[name] {
			t.Errorf("expected import edge for %q", name)
		}
	}
}

func TestEdges_PythonDottedBase(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import module

class Child(module.Base):
    def run(self):
        pass
`)

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
		if e.Kind == "inherits" && e.DstSymbolName == "Base" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'inherits' edge with DstSymbolName='Base' for dotted base class")
	}
}

func TestEdges_PythonMetaclassSkipped(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class Meta(type):
    pass

class MyClass(metaclass=Meta):
    def run(self):
        pass
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// MyClass should NOT have an inherits edge for "metaclass" — keyword_argument is skipped.
	// It should only inherit from nothing (no inherits edge for MyClass).
	for _, e := range result.Edges {
		if e.Kind == "inherits" && e.SrcSymbolName == "MyClass" && e.DstSymbolName == "metaclass" {
			t.Error("expected keyword_argument 'metaclass=Meta' to be skipped in inheritance edges")
		}
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

// ── Rust import tests ──

func TestEdges_RustImports(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`use std::collections::HashMap;
use std::io;

fn main() {
    let map: HashMap<String, String> = HashMap::new();
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "main.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	imports := map[string]bool{}
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			imports[e.DstSymbolName] = true
		}
	}
	for _, name := range []string{"HashMap", "io"} {
		if !imports[name] {
			t.Errorf("expected import edge for %q, got imports: %v", name, imports)
		}
	}
}

func TestEdges_RustUseList(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`use std::{fs, io};

fn process() {}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "lib.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	imports := map[string]bool{}
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			imports[e.DstSymbolName] = true
		}
	}
	for _, name := range []string{"fs", "io"} {
		if !imports[name] {
			t.Errorf("expected import edge for %q, got imports: %v", name, imports)
		}
	}
}

// ── Import owner tests (file-level edges get first symbol as source) ──

func TestEdges_JavaImportOwner(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package com.example;

import java.util.concurrent.ExecutorService;
import java.util.List;

public class TaskRunner {
    private ExecutorService executor;

    public void run() {}
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "TaskRunner.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, e := range result.Edges {
		if e.Kind == "imports" {
			if e.SrcSymbolName == "" {
				t.Errorf("import edge for %q has empty SrcSymbolName; expected first symbol", e.DstSymbolName)
			}
			if e.SrcSymbolName != "TaskRunner" {
				t.Errorf("import edge for %q: SrcSymbolName=%q, want %q", e.DstSymbolName, e.SrcSymbolName, "TaskRunner")
			}
		}
	}

	// Verify both imports were extracted
	imports := map[string]bool{}
	for _, e := range result.Edges {
		if e.Kind == "imports" {
			imports[e.DstSymbolName] = true
		}
	}
	for _, name := range []string{"ExecutorService", "List"} {
		if !imports[name] {
			t.Errorf("expected import edge for %q", name)
		}
	}
}

func TestEdges_GoImportOwner(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "main.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "fmt" {
			if e.SrcSymbolName == "" {
				t.Error("import edge for 'fmt' has empty SrcSymbolName; expected 'main'")
			}
			return
		}
	}
	t.Error("expected import edge for 'fmt'")
}

func TestEdges_TypeScriptImportOwner(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import { foo } from 'bar';

export function doStuff(): string {
  return foo();
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "foo" {
			if e.SrcSymbolName == "" {
				t.Error("import edge for 'foo' has empty SrcSymbolName; expected first symbol")
			}
			return
		}
	}
	t.Error("expected import edge for 'foo'")
}

func TestEdges_PythonTopLevelCallNotMisattributed(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Top-level print() call appears before the function definition.
	// It should NOT be attributed to "process" — it should keep
	// SrcSymbolName="" so InsertEdges drops it (there is no graph
	// node for module-level code).
	src := []byte(`import os

print(os.getcwd())

def process(items):
    return items
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "script.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Import edge should have importOwner fallback.
	foundImport := false
	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "os" {
			foundImport = true
			if e.SrcSymbolName == "" {
				t.Error("import edge for 'os' has empty SrcSymbolName; expected importOwner fallback")
			}
		}
	}
	if !foundImport {
		t.Error("expected import edge for 'os'")
	}

	// Top-level call edges should NOT be attributed to the first symbol.
	for _, e := range result.Edges {
		if e.Kind == "calls" && e.SrcSymbolName == "process" {
			// "process" only contains "return items" — no calls.
			// Any call edge sourced from "process" means a top-level
			// call was misattributed.
			t.Errorf("top-level call to %q was misattributed to 'process'", e.DstSymbolName)
		}
	}
}

func TestEdges_GoBlankIdentifierSkippedAsImportOwner(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Common Go pattern: var _ Interface = (*Impl)(nil) before imports.
	// The blank identifier "_" should NOT become the importOwner.
	src := []byte(`package main

import "fmt"

var _ fmt.Stringer = (*MyType)(nil)

type MyType struct{}

func (m *MyType) String() string { return "" }
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "impl.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "fmt" {
			if e.SrcSymbolName == "_" {
				t.Error("import edge for 'fmt' has SrcSymbolName='_'; blank identifier should be skipped as importOwner")
			}
			if e.SrcSymbolName == "" {
				t.Error("import edge for 'fmt' has empty SrcSymbolName; expected a non-blank symbol")
			}
			return
		}
	}
	t.Error("expected import edge for 'fmt'")
}
