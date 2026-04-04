package parser

import (
	"context"
	"testing"
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

	// Should have chunks for class and methods
	if len(result.Chunks) < 2 {
		t.Fatalf("expected at least 2 chunks (class + methods), got %d", len(result.Chunks))
	}

	foundClass := false
	foundMethod := false
	for _, c := range result.Chunks {
		if c.SymbolName == "UserService" {
			foundClass = true
		}
		if c.Kind == "method" {
			foundMethod = true
		}
	}
	if !foundClass {
		t.Error("expected class chunk 'UserService'")
	}
	if !foundMethod {
		t.Error("expected method chunks")
	}

	// Should have class symbol
	foundSym := false
	for _, s := range result.Symbols {
		if s.Name == "UserService" && s.Kind == "class" {
			foundSym = true
			break
		}
	}
	if !foundSym {
		t.Error("expected class symbol 'UserService'")
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
