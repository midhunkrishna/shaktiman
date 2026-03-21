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
