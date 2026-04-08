package parser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func TestNewParser(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()
}

func TestParse_SimpleFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`function hello(): string {
  return "hello";
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Should have a function chunk named "hello"
	found := false
	for _, c := range result.Chunks {
		if c.SymbolName == "hello" && c.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a function chunk named 'hello'")
	}
}

func TestParse_ExportedFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export function greet(name: string): string {
  return "hello " + name;
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected chunks")
	}

	// Should have an exported symbol
	foundExported := false
	for _, s := range result.Symbols {
		if s.Name == "greet" && s.IsExported {
			foundExported = true
			break
		}
	}
	if !foundExported {
		t.Error("expected exported symbol 'greet'")
	}
}

func TestParse_ClassWithMethods(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export class UserService {
  private users: Map<string, User> = new Map();
  private cache: Map<string, User> = new Map();

  async findById(id: string): Promise<User | null> {
    if (this.cache.has(id)) {
      return this.cache.get(id) ?? null;
    }
    const user = this.users.get(id) ?? null;
    if (user) {
      this.cache.set(id, user);
    }
    return user;
  }

  async create(data: CreateUserInput): Promise<User> {
    const id = crypto.randomUUID();
    const user = { id, ...data, createdAt: new Date() };
    this.users.set(user.id, user);
    this.cache.set(user.id, user);
    console.log("Created user", user.id);
    return user;
  }
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Small class (~90 tokens) fits under maxChunkTokens so ADR-004
	// emits it as a single class chunk — method content lives inside
	// the class chunk's source, not as separate method chunks.
	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk for the class")
	}

	foundClass := false
	for _, c := range result.Chunks {
		if c.SymbolName == "UserService" && c.Kind == "class" {
			foundClass = true
			if !strings.Contains(c.Content, "findById") || !strings.Contains(c.Content, "create") {
				t.Errorf("class chunk should contain method source, got content length %d", len(c.Content))
			}
			break
		}
	}
	if !foundClass {
		t.Error("expected class chunk 'UserService'")
	}

	// Symbols should still list the class and its methods — symbol
	// extraction is independent of chunk decomposition.
	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}
	if syms["UserService"] != "class" {
		t.Errorf("expected class symbol 'UserService', got kind=%q", syms["UserService"])
	}
	for _, m := range []string{"findById", "create"} {
		if syms[m] != "method" {
			t.Errorf("expected method symbol %q, got kind=%q", m, syms[m])
		}
	}
}

func TestParse_ImportsAsHeader(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import { foo } from 'bar';
import { baz } from 'qux';

export function doStuff(input: string): string {
  const result = foo(input);
  const processed = baz(result);
  const validated = input.length > 0 ? processed : "";
  console.log("processing", input, result, processed);
  return validated;
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have a header chunk and a function chunk
	foundHeader := false
	foundFunc := false
	for _, c := range result.Chunks {
		if c.Kind == "header" {
			foundHeader = true
		}
		if c.SymbolName == "doStuff" {
			foundFunc = true
		}
	}
	if !foundHeader {
		t.Error("expected header chunk for imports")
	}
	if !foundFunc {
		t.Error("expected function chunk 'doStuff'")
	}
}

func TestParse_Interface(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export interface User {
  id: string;
  name: string;
  email: string;
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundInterface := false
	for _, c := range result.Chunks {
		if c.SymbolName == "User" && c.Kind == "interface" {
			foundInterface = true
			break
		}
	}
	if !foundInterface {
		t.Error("expected interface chunk 'User'")
	}
}

func TestParse_Enum(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export enum UserRole {
  Admin = 'admin',
  User = 'user',
  Guest = 'guest',
}`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundEnum := false
	for _, c := range result.Chunks {
		if c.SymbolName == "UserRole" && c.Kind == "type" {
			foundEnum = true
			break
		}
	}
	if !foundEnum {
		t.Error("expected enum chunk 'UserRole'")
	}
}

func TestParse_EmptyFile(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "empty.ts",
		Content:  []byte(""),
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) != 0 {
		t.Errorf("expected 0 chunks for empty file, got %d", len(result.Chunks))
	}
}

func TestCountTokens(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	count := p.CountTokens("function hello() { return 42; }")
	if count <= 0 {
		t.Errorf("expected positive token count, got %d", count)
	}
}

// ── Python parsing tests ──

func TestParse_PythonFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`def hello(name, greeting, suffix):
    """Return a greeting string for the given name."""
    if not name:
        raise ValueError("name must not be empty")
    parts = [greeting, name, suffix]
    result = " ".join(parts)
    return result.strip()
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	found := false
	for _, c := range result.Chunks {
		if c.SymbolName == "hello" && c.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a function chunk named 'hello'")
	}
}

func TestParse_PythonClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class Greeter:
    """A service that produces greetings and farewells."""

    def greet(self, name, formal=False):
        """Produce a greeting for the given name."""
        if formal:
            prefix = "Dear"
        else:
            prefix = "Hello"
        message = prefix + " " + name
        timestamp = self._get_timestamp()
        return message + " (" + timestamp + ")"

    def farewell(self, name, formal=False):
        """Produce a farewell for the given name."""
        if formal:
            prefix = "Goodbye"
        else:
            prefix = "Bye"
        message = prefix + " " + name
        timestamp = self._get_timestamp()
        return message + " (" + timestamp + ")"

    def _get_timestamp(self):
        """Return a placeholder timestamp string."""
        return "2024-01-01T00:00:00Z"
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Verify class chunk exists
	foundClass := false
	for _, c := range result.Chunks {
		if c.SymbolName == "Greeter" {
			foundClass = true
			break
		}
	}
	if !foundClass {
		t.Error("expected class chunk 'Greeter'")
	}

	// Verify class and method symbols
	foundClassSym := false
	foundMethodSym := false
	for _, s := range result.Symbols {
		if s.Name == "Greeter" && s.Kind == "class" {
			foundClassSym = true
		}
		if s.Name == "greet" && s.Kind == "function" {
			foundMethodSym = true
		}
	}
	if !foundClassSym {
		t.Error("expected class symbol 'Greeter'")
	}
	if !foundMethodSym {
		t.Error("expected method symbol 'greet'")
	}
}

func TestParse_PythonImports(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import os
import sys
from pathlib import Path
from collections import defaultdict

def process_files(directory, pattern, recursive=True):
    """Walk a directory and process all matching files."""
    results = defaultdict(list)
    base = Path(directory)
    if not base.exists():
        raise FileNotFoundError(f"Directory not found: {directory}")
    if recursive:
        entries = base.rglob(pattern)
    else:
        entries = base.glob(pattern)
    for entry in entries:
        size = os.path.getsize(entry)
        results[entry.suffix].append({"path": str(entry), "size": size})
    return dict(results)
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundHeader := false
	foundFunc := false
	for _, c := range result.Chunks {
		if c.Kind == "header" {
			foundHeader = true
		}
		if c.SymbolName == "process_files" {
			foundFunc = true
		}
	}
	if !foundHeader {
		t.Error("expected header chunk for imports")
	}
	if !foundFunc {
		t.Error("expected function chunk 'process_files'")
	}
}

// ── Go parsing tests ──

func TestParse_GoFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

import "fmt"

func Hello(name string, greeting string, count int) string {
	if name == "" {
		return "anonymous"
	}
	result := ""
	for i := 0; i < count; i++ {
		result += fmt.Sprintf("%s %s (iteration %d)\n", greeting, name, i)
	}
	return result
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

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify function symbol with kind "function"
	found := false
	for _, s := range result.Symbols {
		if s.Name == "Hello" && s.Kind == "function" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a function symbol named 'Hello'")
	}
}

func TestParse_GoMethod(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

import (
	"fmt"
	"net"
)

type Server struct {
	addr     string
	listener net.Listener
	running  bool
}

func (s *Server) Start() error {
	if s.running {
		return fmt.Errorf("server already running on %s", s.addr)
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.addr, err)
	}
	s.listener = ln
	s.running = true
	fmt.Printf("Server started on %s\n", s.addr)
	return nil
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

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify method symbol with kind "method"
	found := false
	for _, s := range result.Symbols {
		if s.Name == "Start" && s.Kind == "method" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a method symbol named 'Start' with kind 'method'")
	}
}

func TestParse_GoTypeDecl(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

type Config struct {
	Host            string
	Port            int
	MaxConnections  int
	ReadTimeoutMs   int
	WriteTimeoutMs  int
	EnableTLS       bool
	CertFile        string
	KeyFile         string
	AllowedOrigins  []string
	DatabaseURL     string
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

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Verify type chunk exists
	foundChunk := false
	for _, c := range result.Chunks {
		if c.SymbolName == "Config" && c.Kind == "type" {
			foundChunk = true
			break
		}
	}
	if !foundChunk {
		t.Error("expected a type chunk named 'Config'")
	}

	// Verify type symbol
	foundSym := false
	for _, s := range result.Symbols {
		if s.Name == "Config" && s.Kind == "type" {
			foundSym = true
			break
		}
	}
	if !foundSym {
		t.Error("expected type symbol 'Config'")
	}
}

// ── Rust tests ──

func TestParse_RustFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`use std::collections::HashMap;

fn add(a: i32, b: i32) -> i32 {
    let result = a + b;
    println!("sum = {}", result);
    result
}

pub fn greet(name: &str) -> String {
    let greeting = format!("Hello, {}!", name);
    println!("{}", greeting);
    greeting
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	funcs := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			funcs[s.Name] = true
		}
	}
	for _, name := range []string{"add", "greet"} {
		if !funcs[name] {
			t.Errorf("expected function symbol %q", name)
		}
	}
}

func TestParse_RustStruct(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`pub struct Point {
    x: f64,
    y: f64,
}

impl Point {
    pub fn new(x: f64, y: f64) -> Self {
        Point { x, y }
    }

    pub fn distance(&self, other: &Point) -> f64 {
        let dx = self.x - other.x;
        let dy = self.y - other.y;
        (dx * dx + dy * dy).sqrt()
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(result.Chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Should have type symbol for Point
	foundType := false
	for _, s := range result.Symbols {
		if s.Name == "Point" && s.Kind == "type" {
			foundType = true
			break
		}
	}
	if !foundType {
		t.Error("expected type symbol 'Point'")
	}
}

// ── Python decorated definitions ──

func TestParse_PythonDecoratedFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Module-level decorated functions (not inside a class)
	src := []byte(`@staticmethod
def static_method(x, y, z):
    """A decorated static method."""
    result = x + y + z
    validated = result > 0
    return validated

@property
def name(self):
    """A decorated property."""
    value = self._name
    processed = value.strip()
    return processed
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have chunks for the decorated functions
	funcChunks := 0
	for _, c := range result.Chunks {
		if c.Kind == "function" {
			funcChunks++
		}
	}
	if funcChunks < 2 {
		t.Errorf("expected at least 2 function chunks for decorated definitions, got %d", funcChunks)
	}

	// Should have symbols for the decorated functions
	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			methods[s.Name] = true
		}
	}
	for _, name := range []string{"static_method", "name"} {
		if !methods[name] {
			t.Errorf("expected symbol %q for decorated definition", name)
		}
	}
}

func TestParse_PythonDecoratedClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`from dataclasses import dataclass

@dataclass
class Config:
    """Application configuration."""
    host: str = "localhost"
    port: int = 8080
    debug: bool = False
    max_connections: int = 100
    timeout_seconds: float = 30.0
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "test.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundClass := false
	for _, s := range result.Symbols {
		if s.Name == "Config" && s.Kind == "class" {
			foundClass = true
			break
		}
	}
	if !foundClass {
		t.Error("expected class symbol 'Config' for decorated class")
	}
}

// ── SupportedLanguage ──

func TestSupportedLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lang string
		want bool
	}{
		{"go", true},
		{"python", true},
		{"typescript", true},
		{"rust", true},
		{"java", true},
		{"groovy", false}, // TODO: dropped pending official tree-sitter-groovy Go bindings
		{"bash", true},
		{"javascript", true},
		{"ruby", true},
		{"erb", true},
		{"", false},
		{"c++", false},
		{"csharp", false},
	}

	for _, tc := range tests {
		t.Run(tc.lang, func(t *testing.T) {
			t.Parallel()
			got := SupportedLanguage(tc.lang)
			if got != tc.want {
				t.Errorf("SupportedLanguage(%q) = %v, want %v", tc.lang, got, tc.want)
			}
		})
	}
}

// ── Java tests ──

func TestParse_JavaClassWithMethods(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package com.example;

import java.util.List;

public class UserService {
    private final String name;

    public UserService(String name) {
        this.name = name;
    }

    public List<String> getUsers() {
        return List.of("alice", "bob");
    }

    public void deleteUser(String id) {
        System.out.println("deleting " + id);
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "UserService.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have a class symbol
	foundClass := false
	for _, s := range result.Symbols {
		if s.Name == "UserService" && s.Kind == "class" {
			foundClass = true
			break
		}
	}
	if !foundClass {
		t.Error("expected class symbol 'UserService'")
	}

	// Should have method symbols
	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "method" {
			methods[s.Name] = true
		}
	}
	for _, m := range []string{"UserService", "getUsers", "deleteUser"} {
		if !methods[m] {
			t.Errorf("expected method symbol %q", m)
		}
	}

	// Should have import edge with first symbol as source
	foundImport := false
	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "List" {
			foundImport = true
			if e.SrcSymbolName != "UserService" {
				t.Errorf("expected import edge SrcSymbolName='UserService', got %q", e.SrcSymbolName)
			}
			break
		}
	}
	if !foundImport {
		t.Error("expected import edge for 'List'")
	}
}

// ── Groovy tests ──

func TestParse_GroovyFunction(t *testing.T) {
	t.Skip("TODO: groovy support dropped pending official tree-sitter-groovy Go bindings")
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`def greet(String name) {
    println "Hello, ${name}!"
}

int add(int a, int b) {
    return a + b
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "utils.groovy",
		Content:  src,
		Language: "groovy",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	functions := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			functions[s.Name] = true
		}
	}
	for _, f := range []string{"greet", "add"} {
		if !functions[f] {
			t.Errorf("expected function symbol %q", f)
		}
	}
}

// ── Bash tests ──

func TestParse_BashFunctions(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`#!/bin/bash

NAME="world"

greet() {
    echo "Hello, $1!"
}

function cleanup {
    rm -rf /tmp/work
}

greet "$NAME"
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "script.sh",
		Content:  src,
		Language: "bash",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	functions := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			functions[s.Name] = true
		}
	}
	for _, f := range []string{"greet", "cleanup"} {
		if !functions[f] {
			t.Errorf("expected function symbol %q", f)
		}
	}
}

// ── JavaScript tests ──

func TestParse_JavaScriptClassWithMethods(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`import { EventEmitter } from 'events';

export class Server extends EventEmitter {
    constructor(port) {
        super();
        this.port = port;
    }

    start() {
        console.log("listening on " + this.port);
    }
}

export function createServer(port) {
    return new Server(port);
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "server.js",
		Content:  src,
		Language: "javascript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have class symbol
	foundClass := false
	for _, s := range result.Symbols {
		if s.Name == "Server" && s.Kind == "class" {
			foundClass = true
			break
		}
	}
	if !foundClass {
		t.Error("expected class symbol 'Server'")
	}

	// Should have function symbol
	foundFunc := false
	for _, s := range result.Symbols {
		if s.Name == "createServer" && s.Kind == "function" {
			foundFunc = true
			break
		}
	}
	if !foundFunc {
		t.Error("expected function symbol 'createServer'")
	}

	// Should have method symbols
	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "method" {
			methods[s.Name] = true
		}
	}
	for _, m := range []string{"constructor", "start"} {
		if !methods[m] {
			t.Errorf("expected method symbol %q", m)
		}
	}

	// Should have import edge
	foundImport := false
	for _, e := range result.Edges {
		if e.Kind == "imports" && e.DstSymbolName == "EventEmitter" {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Error("expected import edge for 'EventEmitter'")
	}

	// Should have inheritance edge
	foundInherits := false
	for _, e := range result.Edges {
		if e.Kind == "inherits" && e.DstSymbolName == "EventEmitter" {
			foundInherits = true
			break
		}
	}
	if !foundInherits {
		t.Error("expected inherits edge for 'EventEmitter'")
	}
}

func TestParse_LargeFunction_SplitsChunks(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Generate a Go function with >1024 tokens to trigger splitLargeChunks.
	// Each line is ~5 tokens, so ~250 lines should exceed the threshold.
	var src string
	src += "package main\n\nfunc BigFunc() {\n"
	for i := 0; i < 300; i++ {
		src += "\t_ = fmt.Sprintf(\"line %d value %d result %d output %d data %d\")\n"
	}
	src += "}\n"

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "big.go",
		Content:  []byte(src),
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// The large function should be split into multiple chunks
	funcChunks := 0
	for _, c := range result.Chunks {
		if c.Kind == "function" {
			funcChunks++
		}
	}
	if funcChunks < 2 {
		t.Errorf("expected large function to be split into >= 2 chunks, got %d", funcChunks)
	}

	// Each chunk should be ≤ maxChunkTokens
	for _, c := range result.Chunks {
		if c.TokenCount > 1200 { // allow some margin over 1024
			t.Errorf("chunk %q has %d tokens, expected ≤ ~1024", c.SymbolName, c.TokenCount)
		}
	}
}

// ── Migration validation tests ──

func TestParse_ContextCancellation(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err = p.Parse(ctx, ParseInput{
		FilePath: "test.go",
		Content:  []byte(`package main; func main() {}`),
		Language: "go",
	})
	if err == nil {
		// Some parsers may complete before checking cancellation on small inputs.
		// This is acceptable — the test verifies no panic occurs.
		return
	}
	if ctx.Err() == nil {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestParse_InvalidLanguageReturnsError(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	_, err = p.Parse(context.Background(), ParseInput{
		FilePath: "test.swift",
		Content:  []byte(`print("hello")`),
		Language: "swift",
	})
	if err == nil {
		t.Fatal("expected error for unsupported language, got nil")
	}
}

func TestParse_AllLanguagesSmoke(t *testing.T) {
	t.Parallel()

	langs := []struct {
		lang    string
		file    string
		content string
	}{
		{"go", "main.go", "package main\nfunc main() {}"},
		{"python", "main.py", "def hello():\n    pass"},
		{"typescript", "app.ts", "function greet(): void {}"},
		{"javascript", "app.js", "function greet() {}"},
		{"rust", "main.rs", "fn main() {}"},
		{"java", "Main.java", "public class Main { public void run() {} }"},
		{"bash", "run.sh", "#!/bin/bash\nrun() { echo ok; }"},
	}

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	for _, tc := range langs {
		t.Run(tc.lang, func(t *testing.T) {
			result, err := p.Parse(context.Background(), ParseInput{
				FilePath: tc.file,
				Content:  []byte(tc.content),
				Language: tc.lang,
			})
			if err != nil {
				t.Fatalf("Parse(%s): %v", tc.lang, err)
			}
			if len(result.Chunks) == 0 {
				t.Errorf("expected at least 1 chunk for %s, got 0", tc.lang)
			}
		})
	}
}

func TestParse_ByteRangesValid(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package main

import "fmt"

type Server struct {
	port int
}

func (s *Server) Start() {
	fmt.Println("starting")
}
`)
	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "server.go",
		Content:  src,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, c := range result.Chunks {
		if c.StartLine < 1 {
			t.Errorf("chunk %q has StartLine %d < 1", c.SymbolName, c.StartLine)
		}
		if c.EndLine < c.StartLine {
			t.Errorf("chunk %q has EndLine %d < StartLine %d", c.SymbolName, c.EndLine, c.StartLine)
		}
		if c.Content == "" {
			t.Errorf("chunk %q has empty content", c.SymbolName)
		}
	}
}

func TestParse_TypeScriptClassSignature(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`export class UserService {
    getUser(id: string): User {
        return this.db.find(id);
    }
    deleteUser(id: string): void {
        this.db.remove(id);
    }
}
`)
	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "service.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should have a class chunk with non-empty content (tests buildClassSignatureWithConfig byte slicing)
	found := false
	for _, c := range result.Chunks {
		if c.SymbolName == "UserService" && c.Kind == "class" {
			found = true
			if c.Content == "" {
				t.Error("UserService class chunk has empty content")
			}
		}
	}
	if !found {
		t.Error("UserService class chunk not found")
	}
}

// ── Ruby tests ──

func TestParse_RubyClassWithMethods(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`# frozen_string_literal: true

class UserService
  def initialize(repo)
    @repo = repo
  end

  def create_user(name:, email:)
    user = User.new(name: name, email: email)
    @repo.add(user)
    user
  end

  def get_user(user_id)
    @repo.get(user_id)
  end

  def self.create_admin(repo, name:, email:)
    user = User.new(name: name, email: email, role: 'admin')
    repo.add(user)
    user
  end
end
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "service.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Check for class
	foundClass := false
	for _, s := range result.Symbols {
		if s.Name == "UserService" && s.Kind == "class" {
			foundClass = true
			break
		}
	}
	if !foundClass {
		t.Error("expected class symbol UserService")
	}

	// Check for methods
	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "method" {
			methods[s.Name] = true
		}
	}
	for _, m := range []string{"initialize", "create_user", "get_user", "create_admin"} {
		if !methods[m] {
			t.Errorf("expected method symbol %q", m)
		}
	}
}

func TestParse_RubyModule(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`module Utils
  MAX_RETRIES = 3

  def self.hash_string(value)
    Digest::SHA256.hexdigest(value)
  end

  def self.format_user(user)
    JSON.generate(user.to_h)
  end
end
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "utils.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Check for module (treated as class)
	foundModule := false
	for _, s := range result.Symbols {
		if s.Name == "Utils" && s.Kind == "class" {
			foundModule = true
			break
		}
	}
	if !foundModule {
		t.Error("expected module symbol Utils (kind=class)")
	}

	// Check for singleton methods
	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "method" {
			methods[s.Name] = true
		}
	}
	for _, m := range []string{"hash_string", "format_user"} {
		if !methods[m] {
			t.Errorf("expected singleton method symbol %q", m)
		}
	}
}

// ── ERB tests ──

func TestParse_ERBTemplate(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`<!DOCTYPE html>
<html>
<head>
  <title><%= @page_title %></title>
</head>
<body>
  <% if current_user %>
    <h1>Welcome, <%= current_user.name %></h1>
  <% end %>
  <% @items.each do |item| %>
    <p><%= item.name %></p>
  <% end %>
</body>
</html>
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "index.html.erb",
		Content:  src,
		Language: "erb",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// ERB files should produce chunks (template content is indexed)
	if len(result.Chunks) == 0 {
		t.Error("expected at least one chunk from ERB template")
	}

	// Verify we can find the template content
	foundContent := false
	for _, c := range result.Chunks {
		if len(c.Content) > 0 {
			foundContent = true
			break
		}
	}
	if !foundContent {
		t.Error("expected chunks to have content")
	}
}

func TestParse_ERBSupportedLanguage(t *testing.T) {
	t.Parallel()

	if !SupportedLanguage("erb") {
		t.Error("expected erb to be a supported language")
	}
}

// ── Recursive chunking tests: nesting bugs ──

func TestParse_RubyNestedModuleClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`module Authentication
  class TokenValidator
    def initialize(secret)
      @secret = secret
    end

    def validate(token)
      decoded = decode(token)
      decoded && !expired?(decoded)
    end
  end

  class TokenGenerator
    def generate(payload)
      Base64.encode64(payload.to_json)
    end
  end
end
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "auth.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Should find module, both classes, and methods
	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	for _, want := range []struct{ name, kind string }{
		{"Authentication", "class"},
		{"TokenValidator", "class"},
		{"TokenGenerator", "class"},
		{"initialize", "method"},
		{"validate", "method"},
		{"generate", "method"},
	} {
		if syms[want.name] != want.kind {
			t.Errorf("expected symbol %q kind=%q, got %q", want.name, want.kind, syms[want.name])
		}
	}

	// Chunk count is not asserted: ADR-004 emits this small fixture as a
	// single Authentication module chunk. Symbol extraction (above) is the
	// real coverage for nested container recursion — walkForSymbols
	// descends regardless of chunk size.
}

func TestParse_RubySingletonClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class Configuration
  class << self
    def instance
      @instance ||= new
    end

    def reset!
      @instance = nil
    end
  end

  def initialize
    @host = "localhost"
  end
end
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "config.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "method" {
			methods[s.Name] = true
		}
	}
	for _, m := range []string{"instance", "reset!", "initialize"} {
		if !methods[m] {
			t.Errorf("expected method symbol %q", m)
		}
	}
}

func TestParse_PythonDecoratedMethodsInClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class CacheManager:
    def __init__(self, max_size=100):
        self._cache = {}
        self._max_size = max_size

    @property
    def size(self):
        return len(self._cache)

    @staticmethod
    def hash_key(key):
        import hashlib
        return hashlib.sha256(key.encode()).hexdigest()

    @classmethod
    def create_with_defaults(cls):
        return cls(max_size=256)

    def get(self, key):
        return self._cache.get(self.hash_key(key))
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "cache.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	methods := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			methods[s.Name] = true
		}
	}

	// All methods including decorated ones should be found
	for _, m := range []string{"__init__", "size", "hash_key", "create_with_defaults", "get"} {
		if !methods[m] {
			t.Errorf("expected function symbol %q (decorated methods inside class)", m)
		}
	}
}

func TestParse_PythonNestedClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`class Outer:
    class Inner:
        def __init__(self, value):
            self.value = value

        def process(self):
            return self.value * 2

    def __init__(self):
        self.inner = self.Inner(42)

    def run(self):
        return self.inner.process()
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "nested.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	for _, want := range []struct{ name, kind string }{
		{"Outer", "class"},
		{"Inner", "class"},
		{"__init__", "function"},
		{"process", "function"},
		{"run", "function"},
	} {
		if syms[want.name] != want.kind {
			t.Errorf("expected symbol %q kind=%q, got %q", want.name, want.kind, syms[want.name])
		}
	}
}

func TestParse_PythonTypeAlias(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`type Point = tuple[float, float]

def distance(a: Point, b: Point) -> float:
    return ((a[0] - b[0]) ** 2 + (a[1] - b[1]) ** 2) ** 0.5
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "types.py",
		Content:  src,
		Language: "python",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundType := false
	foundFunc := false
	for _, c := range result.Chunks {
		if c.Kind == "type" {
			foundType = true
		}
		if c.Kind == "function" {
			foundFunc = true
		}
	}
	if !foundType {
		t.Error("expected type chunk for type alias")
	}
	if !foundFunc {
		t.Error("expected function chunk for distance")
	}
}

func TestParse_RustTraitWithMethods(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`pub trait Serializer {
    fn serialize(&self) -> Vec<u8>;
    fn content_type(&self) -> &str;

    fn serialize_to_string(&self) -> String {
        String::from_utf8_lossy(&self.serialize()).to_string()
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "lib.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	if syms["Serializer"] != "interface" {
		t.Errorf("expected trait symbol 'Serializer' kind=interface, got %q", syms["Serializer"])
	}
	for _, m := range []string{"serialize", "content_type", "serialize_to_string"} {
		if syms[m] != "function" {
			t.Errorf("expected function symbol %q inside trait, got %q", m, syms[m])
		}
	}
}

func TestParse_RustModuleWithItems(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`mod internal {
    pub fn validate(input: &str) -> bool {
        !input.is_empty() && input.len() < 1024
    }

    pub struct Config {
        pub host: String,
        pub port: u16,
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "lib.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	if syms["internal"] != "module" {
		t.Errorf("expected module symbol 'internal', got kind=%q", syms["internal"])
	}
	if syms["validate"] != "function" {
		t.Errorf("expected function symbol 'validate' inside mod, got kind=%q", syms["validate"])
	}
	if syms["Config"] != "type" {
		t.Errorf("expected type symbol 'Config' inside mod, got kind=%q", syms["Config"])
	}
}

func TestParse_RustUnion(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`union FloatOrInt {
    f: f32,
    i: u32,
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "lib.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundUnion := false
	for _, s := range result.Symbols {
		if s.Name == "FloatOrInt" && s.Kind == "type" {
			foundUnion = true
			break
		}
	}
	if !foundUnion {
		t.Error("expected union symbol 'FloatOrInt' kind=type")
	}
}

func TestParse_RustExternBlock(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`extern "C" {
    fn abs(input: i32) -> i32;
    fn strlen(s: *const std::os::raw::c_char) -> usize;
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "lib.rs",
		Content:  src,
		Language: "rust",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	funcs := map[string]bool{}
	for _, s := range result.Symbols {
		if s.Kind == "function" {
			funcs[s.Name] = true
		}
	}
	for _, f := range []string{"abs", "strlen"} {
		if !funcs[f] {
			t.Errorf("expected function symbol %q inside extern block", f)
		}
	}
}

func TestParse_JavaInnerClass(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`public class Outer {
    private String name;

    public Outer(String name) {
        this.name = name;
    }

    public String getName() {
        return name;
    }

    public class Inner {
        private int value;

        public Inner(int value) {
            this.value = value;
        }

        public int getValue() {
            return value;
        }
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "Outer.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Use a set of name+kind pairs since constructors share names with classes
	type symKey struct{ name, kind string }
	symSet := map[symKey]bool{}
	for _, s := range result.Symbols {
		symSet[symKey{s.Name, s.Kind}] = true
	}

	for _, want := range []symKey{
		{"Outer", "class"},
		{"Inner", "class"},
		{"getName", "method"},
		{"getValue", "method"},
	} {
		if !symSet[want] {
			t.Errorf("expected symbol %q kind=%q", want.name, want.kind)
		}
	}
}

func TestParse_JavaInterfaceWithDefaults(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`public interface Cacheable {
    String cacheKey();

    default long ttl() {
        return 3600;
    }

    static String buildKey(String id) {
        return "cache:" + id;
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "Cacheable.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	if syms["Cacheable"] != "interface" {
		t.Errorf("expected interface symbol 'Cacheable', got kind=%q", syms["Cacheable"])
	}
	for _, m := range []string{"cacheKey", "ttl", "buildKey"} {
		if syms[m] != "method" {
			t.Errorf("expected method symbol %q inside interface, got kind=%q", m, syms[m])
		}
	}
}

func TestParse_TypeScriptNamespace(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`namespace Validators {
  export function isEmail(s: string): boolean {
    return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(s);
  }

  export class RegexValidator {
    constructor(private pattern: RegExp) {}

    isValid(s: string): boolean {
      return this.pattern.test(s);
    }
  }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "validators.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	if syms["Validators"] != "namespace" {
		t.Errorf("expected namespace symbol 'Validators', got kind=%q", syms["Validators"])
	}
	if syms["isEmail"] != "function" {
		t.Errorf("expected function symbol 'isEmail' inside namespace, got kind=%q", syms["isEmail"])
	}
	if syms["RegexValidator"] != "class" {
		t.Errorf("expected class symbol 'RegexValidator' inside namespace, got kind=%q", syms["RegexValidator"])
	}
}

func TestParse_TypeScriptGeneratorFunction(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`function* range(start: number, end: number): Generator<number> {
  for (let i = start; i < end; i++) {
    yield i;
  }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "gen.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	foundFunc := false
	for _, s := range result.Symbols {
		if s.Name == "range" && s.Kind == "function" {
			foundFunc = true
			break
		}
	}
	if !foundFunc {
		t.Error("expected function symbol 'range' for generator function")
	}
}

func TestParse_JavaRecordCompactConstructor(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package com.example;

public record Point(double x, double y) {
    public Point {
        if (Double.isNaN(x) || Double.isNaN(y)) {
            throw new IllegalArgumentException("Coordinates must not be NaN");
        }
    }

    public double distanceTo(Point other) {
        double dx = this.x - other.x;
        double dy = this.y - other.y;
        return Math.sqrt(dx * dx + dy * dy);
    }

    static {
        System.out.println("Point record loaded");
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "Point.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	type symKey struct{ name, kind string }
	symSet := map[symKey]bool{}
	for _, s := range result.Symbols {
		symSet[symKey{s.Name, s.Kind}] = true
	}

	// Record itself is indexed as class
	if !symSet[symKey{"Point", "class"}] {
		t.Error("expected record symbol 'Point' kind=class")
	}
	// Compact constructor is a "method" symbol named Point (same as enclosing record).
	// Regular methods inside the record must also be extracted.
	if !symSet[symKey{"distanceTo", "method"}] {
		t.Error("expected method symbol 'distanceTo' inside record")
	}
	// Compact constructor: tree-sitter exposes compact_constructor_declaration;
	// verify at least one "method" symbol with name "Point" exists.
	foundCompact := false
	for _, s := range result.Symbols {
		if s.Name == "Point" && s.Kind == "method" {
			foundCompact = true
			break
		}
	}
	if !foundCompact {
		t.Error("expected compact constructor method symbol 'Point'")
	}
}

func TestParse_JavaStaticInitializer(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`package com.example;

public class Config {
    private static final java.util.Map<String, String> DEFAULTS = new java.util.HashMap<>();

    static {
        DEFAULTS.put("host", "localhost");
        DEFAULTS.put("port", "8080");
        DEFAULTS.put("timeout", "30");
    }

    public static String get(String key) {
        return DEFAULTS.get(key);
    }
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "Config.java",
		Content:  src,
		Language: "java",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Symbol for enclosing class + the static method must both be found.
	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}
	if syms["Config"] != "class" {
		t.Errorf("expected class symbol 'Config', got kind=%q", syms["Config"])
	}
	if syms["get"] != "method" {
		t.Errorf("expected method symbol 'get', got kind=%q", syms["get"])
	}

	// The static initializer body must survive chunking — under ADR-004 this
	// small Config class fits in a single class chunk, so the initializer
	// source lives inside that chunk's content. Verify the body is preserved.
	foundInitBody := false
	for _, c := range result.Chunks {
		if strings.Contains(c.Content, `DEFAULTS.put("host"`) &&
			strings.Contains(c.Content, `DEFAULTS.put("timeout"`) {
			foundInitBody = true
			break
		}
	}
	if !foundInitBody {
		t.Error("expected a chunk containing the static initializer body (DEFAULTS.put lines)")
	}
}

func TestParse_TypeScriptAmbientDeclarations(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`declare function fetch(url: string): Promise<Response>;

declare class EventEmitter {
  on(event: string, listener: (...args: any[]) => void): this;
  emit(event: string, ...args: any[]): boolean;
}

declare namespace NodeJS {
  interface ProcessEnv {
    NODE_ENV: string;
    PORT?: string;
  }
}

declare const VERSION: string;
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "ambient.d.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	syms := map[string]string{}
	for _, s := range result.Symbols {
		syms[s.Name] = s.Kind
	}

	// declare function → function_signature symbol
	if syms["fetch"] != "function" {
		t.Errorf("expected ambient function 'fetch' kind=function, got %q", syms["fetch"])
	}
	// declare class → class symbol
	if syms["EventEmitter"] != "class" {
		t.Errorf("expected ambient class 'EventEmitter' kind=class, got %q", syms["EventEmitter"])
	}
	// declare namespace → namespace symbol
	if syms["NodeJS"] != "namespace" {
		t.Errorf("expected ambient namespace 'NodeJS' kind=namespace, got %q", syms["NodeJS"])
	}
	// declare const → variable symbol
	if syms["VERSION"] != "variable" && syms["VERSION"] != "constant" {
		t.Errorf("expected ambient const 'VERSION' kind=variable|constant, got %q", syms["VERSION"])
	}
}

func TestParse_TypeScriptOverloadSignatures(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	src := []byte(`function parse(input: string): number;
function parse(input: number): string;
function parse(input: string | number): string | number {
  if (typeof input === "string") {
    return parseInt(input, 10);
  }
  return input.toString();
}
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "overloads.ts",
		Content:  src,
		Language: "typescript",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Both overload signatures AND the implementation should be extracted.
	// Overload signatures are function_signature (no body); the implementation
	// is function_declaration. All three resolve to symbol name "parse".
	parseCount := 0
	for _, s := range result.Symbols {
		if s.Name == "parse" && s.Kind == "function" {
			parseCount++
		}
	}
	if parseCount < 3 {
		t.Errorf("expected 3 'parse' function symbols (2 overload signatures + 1 implementation), got %d", parseCount)
	}
}

// TestParse_DepthGuardBoundary is the RED test for bug #12 in
// docs/review-findings/parser-bugs-from-recursive-chunking.md.
//
// Current buggy code at chunker.go:139 uses `depth >= maxChunkDepth` which
// caps recursion at depth 9 — at depth 10 the chunker falls back to
// splitNodeByLines and never extracts the children. ADR-004 §6 specifies
// `depth > maxChunkDepth`, meaning depth 10 should still recurse and only
// depth 11+ should hit the guard.
//
// This test constructs 11 levels of nested Ruby modules with a method at
// the bottom. Under the buggy `>=` the innermost module at depth 10 gets
// line-split and the method is never emitted as its own chunk. Under the
// fixed `>` the method is extracted as a function chunk.
func TestParse_DepthGuardBoundary(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Build 11 levels of nested Ruby modules. The innermost module L11
	// contains a large FILLER_DATA array that alone exceeds maxChunkTokens
	// (1024). Since the filler is inside L11, every outer level (L1..L11)
	// inherits its token count, so the size gate at chunker.go
	// (`tokens <= maxChunkTokens → emit as single chunk`) does NOT fire at
	// any level, forcing real recursion through all 11 depths to reach
	// deeply_nested_method. Without this bulk the bug #11 fix would collapse
	// the whole tree into a single L1 chunk and there would be no depth-11
	// recursion to guard.
	//
	// Token math: each FILLER_DATA line like "entry_000_alpha_beta_..." is
	// ~15 tokens under tiktoken cl100k_base. 120 lines ≈ 1800 tokens, safely
	// above 1024 at every nesting level.
	var fill strings.Builder
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&fill, "                        \"entry_%03d_alpha_beta_gamma_delta_epsilon\",\n", i)
	}

	// deeply_nested_method's body must exceed minChunkTokens (20) so the
	// emitted function chunk survives mergeSmallChunks — otherwise it is
	// folded into the parent signature chunk and the test can't observe it.
	src := []byte(fmt.Sprintf(`module L1
  module L2
    module L3
      module L4
        module L5
          module L6
            module L7
              module L8
                module L9
                  module L10
                    module L11
                      FILLER_DATA = [
%s                      ]

                      def deeply_nested_method
                        accumulator = 0
                        values = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
                        values.each do |v|
                          accumulator += v * v
                        end
                        accumulator * 2
                      end
                    end
                  end
                end
              end
            end
          end
        end
      end
    end
  end
end
`, fill.String()))

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "deep.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// The method at AST depth 11 must be extracted as its own function
	// chunk. Under the buggy depth guard (bug #12), L11 at depth 10 is
	// line-split and the method is swallowed into the L11 body. Under the
	// fixed `depth > maxChunkDepth`, L11 recurses and the method is emitted
	// as a function chunk. Each outer level exceeds maxChunkTokens due to
	// FILLER_DATA, so the bug #11 size gate does not short-circuit recursion.
	foundMethod := false
	for _, c := range result.Chunks {
		if c.SymbolName == "deeply_nested_method" && c.Kind == "function" {
			foundMethod = true
			break
		}
	}
	if !foundMethod {
		t.Errorf("expected 'deeply_nested_method' function chunk at AST depth 11 — " +
			"bug #12 regression: depth guard at chunker.go:149 must use > not >=")
	}
}

// TestChunk_SmallContainerEmittedAsSingleChunk is the RED test for bug #11
// in docs/review-findings/parser-bugs-from-recursive-chunking.md.
//
// Bug #11: chunkNode calls findChunkableChildren unconditionally BEFORE
// checking tokens <= maxChunkTokens. Any container with chunkable
// descendants is decomposed into a signature chunk + child chunks,
// regardless of size. ADR-004 §"Core Algorithm" and §"Key behaviors"
// specify the opposite: "A 20-line module with a 15-line class doesn't
// decompose — the whole thing fits in one chunk."
//
// This test uses a small Ruby class (~60 tokens) with multiple methods
// and asserts that:
//  1. Exactly one chunk is produced (the whole class).
//  2. The chunk kind is "class", not a signature summary.
//  3. The chunk content contains the FULL source of every method body —
//     not a "{ ... }" summary.
//
// Under the buggy eager decomposition the test fails: the parser emits
// a class signature chunk plus one method chunk per definition, losing
// the full method source from the class chunk.
func TestChunk_SmallContainerEmittedAsSingleChunk(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	// Small Ruby class — well under maxChunkTokens (1024). Each method
	// body is sized to exceed minChunkTokens (20) so extracted child
	// chunks survive mergeSmallChunks — otherwise the post-processing
	// pass merges tiny fragments and masks the eager-decomposition bug.
	src := []byte(`class Greeter
  def initialize(name, greeting, punctuation)
    @name = name
    @greeting = greeting
    @punctuation = punctuation
    @greeted_at = Time.now
  end

  def hello
    prefix = @greeting.upcase
    body = @name.capitalize
    suffix = @punctuation
    "#{prefix}, #{body}#{suffix}"
  end

  def goodbye
    prefix = "goodbye"
    body = @name.capitalize
    suffix = @punctuation
    "#{prefix}, #{body}#{suffix}"
  end
end
`)

	result, err := p.Parse(context.Background(), ParseInput{
		FilePath: "greeter.rb",
		Content:  src,
		Language: "ruby",
	})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Exactly one chunk: the whole class emitted as-is.
	if len(result.Chunks) != 1 {
		var kinds []string
		for _, c := range result.Chunks {
			kinds = append(kinds, fmt.Sprintf("%s(%q)", c.Kind, c.SymbolName))
		}
		t.Fatalf("expected 1 chunk for small class (~60 tokens, well under maxChunkTokens=1024), "+
			"got %d: %v — bug #11: chunkNode decomposes containers eagerly regardless of size",
			len(result.Chunks), kinds)
	}

	chunk := result.Chunks[0]
	if chunk.Kind != "class" {
		t.Errorf("expected chunk kind=class, got %q", chunk.Kind)
	}
	if chunk.SymbolName != "Greeter" {
		t.Errorf("expected chunk SymbolName=Greeter, got %q", chunk.SymbolName)
	}

	// Content must contain the FULL class source, not a signature summary.
	// Under the bug, methods are replaced with "initialize { ... }" placeholders
	// and the actual method bodies are moved to separate chunks.
	for _, want := range []string{
		"def initialize",
		"@greeted_at = Time.now",
		"def hello",
		"prefix = @greeting.upcase",
		"def goodbye",
		`prefix = "goodbye"`,
	} {
		if !strings.Contains(chunk.Content, want) {
			t.Errorf("chunk content missing %q — bug #11: method bodies extracted into separate chunks, "+
				"parent chunk only holds a signature summary", want)
		}
	}
}

// findFirstNamed walks a tree-sitter node breadth-first and returns the
// first named descendant whose Kind() matches `kind`. Test-only helper.
func findFirstNamed(root *tree_sitter.Node, kind string) *tree_sitter.Node {
	queue := []*tree_sitter.Node{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.Kind() == kind {
			return n
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			queue = append(queue, n.NamedChild(uint(i)))
		}
	}
	return nil
}

// TestExtractName_JavaField is the RED test for bug #1 in
// docs/review-findings/parser-bugs-from-recursive-chunking.md.
//
// Bug #1: extractName walks a node's named children looking for an
// identifier. For a Java field_declaration like
// `private final ExecutorService executor;`, the AST is:
//
//	field_declaration
//	  modifiers (private, final)
//	  type_identifier  (ExecutorService)   ← walked first, wins
//	  variable_declarator
//	    identifier     (executor)          ← actually wanted
//
// Because type_identifier appears before variable_declarator in the named
// children list, extractName returns "ExecutorService" instead of
// "executor". The result is that any language relying on extractName to
// identify a variable-name-after-type-identifier field loses the
// variable's name — symbols for such fields would be indexed under the
// type name.
//
// Java's SymbolKindMap currently omits field_declaration as a workaround
// (see languages.go) so the bug is dormant in production, but the underlying
// extractName bug remains and must be fixed before field symbol
// extraction can be re-enabled. This test calls extractName directly on
// a parsed field_declaration node.
func TestExtractName_JavaField(t *testing.T) {
	t.Parallel()

	p, err := NewParser()
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	defer p.Close()

	cfg, err := p.getConfig("java")
	if err != nil {
		t.Fatalf("getConfig java: %v", err)
	}
	if err := p.ts.SetLanguage(cfg.Grammar); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}

	src := []byte(`class Pool {
    private final ExecutorService executor;
}
`)

	tree := p.ts.Parse(src, nil)
	if tree == nil {
		t.Fatal("tree-sitter returned nil tree")
	}
	defer tree.Close()

	field := findFirstNamed(tree.RootNode(), "field_declaration")
	if field == nil {
		t.Fatal("no field_declaration node found in parsed Java tree")
	}

	name := extractName(field, src)
	if name != "executor" {
		t.Errorf("extractName(field_declaration) = %q, want %q — "+
			"bug #1: type_identifier walked before variable_declarator, so type name wins",
			name, "executor")
	}
}
