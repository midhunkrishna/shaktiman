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
