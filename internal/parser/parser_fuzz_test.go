package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
)

// FuzzParseScript tests that ParseScript never panics on arbitrary input
func FuzzParseScript(f *testing.F) {
	// Seed corpus with valid and interesting test cases
	seeds := []string{
		// Valid programs
		"val x = 5",
		"fn add(a, b) { return a + b }",
		"if (x > 0) { return x } else { return -x }",
		"val arr = [1, 2, 3]",
		"val obj = {x: 1, y: 2}",
		"class Point(x: number, y: number) { x, y }",
		"enum Color { Red, Green, Blue }",
		"interface Person { name: string, age: number }",
		"export fn foo() { return 42 }",
		"declare fn bar() -> void",
		"async fn fetchData() { return await fetch() }",

		// Edge cases and potentially problematic inputs
		"",
		" ",
		"\n",
		"\t",
		"//",
		"/**/",
		"/* unclosed comment",
		"\"unclosed string",
		"'unclosed string",
		"`unclosed template",
		"(",
		")",
		"{",
		"}",
		"[",
		"]",
		"...",
		"<",
		">",
		">>>",
		"=>",
		"fn",
		"fn(",
		"fn()",
		"fn() {",
		"fn() {}",
		"val",
		"val x",
		"val x =",
		"val x = ",
		"class",
		"class Foo",
		"class Foo(",
		"class Foo()",
		"class Foo() {",
		"enum",
		"enum E",
		"enum E {",
		"interface",
		"interface I",
		"interface I {",
		"if",
		"if (",
		"if ()",
		"if () {",
		"for",
		"while",
		"match",
		"return",
		"throw",
		"try",
		"catch",
		"await",
		"async",
		"import",
		"export",
		"declare",

		// Nested structures
		"fn f() { fn g() { fn h() { return 1 } } }",
		"[[[[[]]]]]",
		"{{{{{}}}}}",
		"((((()))))",

		// Unicode and special characters
		"val 日本語 = 42",
		"val emoji = \"😀\"",
		"val \u0000 = 0",
		"val \x00 = 0",

		// Long inputs
		"val x = 1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10",
		"fn f(a, b, c, d, e, f, g, h, i, j, k, l, m, n, o, p) {}",

		// Mixed valid/invalid syntax
		"val x = 5; fn foo()",
		"class Foo { fn bar() { val x = }",
		"if (true) { val x = 5 } else { val y = ",
		"enum Color { Red, Green, }",
		"interface I { x: number, y: }",

		// Operators and expressions
		"x + y - z * w / v % u",
		"a && b || c",
		"!x",
		"~x",
		"++x",
		"--x",
		"x++",
		"x--",
		"x ** y",
		"x << y",
		"x >> y",
		"x >>> y",
		"x & y",
		"x | y",
		"x ^ y",
		"x ?? y",
		"x ?. y",
		"x?.[y]",

		// Type annotations
		"val x: number = 5",
		"fn foo(x: string): number { return 0 }",
		"val x: Array<number> = []",
		"val x: {x: number, y: string} = {x: 1, y: \"a\"}",
		"val x: (a: number) => string = fn(a) { return \"\" }",

		// Pattern matching
		"match x { Some(y) -> y, None -> 0 }",
		"val {x, y} = point",
		"val [a, b, ...rest] = arr",

		// JSX-like syntax (if supported)
		"<div></div>",
		"<Component />",
		"<Parent><Child /></Parent>",

		// Async/await
		"await promise",
		"await foo()",
		"async fn foo() { await bar() }",

		// Generics
		"fn identity<T>(x: T): T { return x }",
		"class Box<T>(value: T) { value }",
		"interface Pair<T, U> { first: T, second: U }",

		// Error handling
		"fn foo() throws Error { throw new Error() }",
		"try { foo() } catch (e) { handle(e) }",

		// Multiple statements
		"val a = 1\nval b = 2\nval c = 3",
		"fn f() {}\nfn g() {}\nfn h() {}",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Create a source with the fuzzed input
		source := &ast.Source{
			ID:       0,
			Path:     "fuzz.esc",
			Contents: input,
		}

		// Use a timeout to prevent infinite loops
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// Create parser and parse
		parser := NewParser(ctx, source)

		// The test passes if ParseScript doesn't panic
		// We don't care about errors - those are expected for invalid input
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseScript panicked on input %q: %v", input, r)
			}
		}()

		_, _ = parser.ParseScript()

		// Fail if the context timed out
		if ctx.Err() == context.DeadlineExceeded {
			// Print debug info about where we are
			token := parser.lexer.peek()
			t.Errorf("ParseScript timed out on input %q\nCurrent offset: %d/%d\nCurrent token: %v at line %d col %d",
				input, parser.lexer.currentOffset, len(parser.lexer.source.Contents),
				token.Type, token.Span.Start.Line, token.Span.Start.Column)
		}
	})
}

// FuzzParseLibFiles tests that ParseLibFiles never panics on arbitrary input
func FuzzParseLibFiles(f *testing.F) {
	// Seed corpus with valid module declarations
	seeds := []string{
		// Valid declarations
		"fn add(a, b) { return a + b }",
		"val PI = 3.14159",
		"class Circle(radius: number) { radius }",
		"enum Status { Success, Failure }",
		"interface User { name: string, email: string }",
		"export fn foo() { return 42 }",
		"export val x = 5",
		"export class Bar {}",
		"export enum E { A, B }",
		"export interface I { x: number }",
		"declare fn external() -> void",
		"declare val global: any",
		"declare class DOMElement { }",

		// Edge cases
		"",
		" ",
		"\n",
		"//",
		"/**/",
		"/* unclosed",
		"fn",
		"val",
		"class",
		"enum",
		"interface",
		"export",
		"declare",

		// Multiple declarations
		"fn f() {}\nfn g() {}",
		"val a = 1\nval b = 2",
		"class A {}\nclass B {}",

		// Generic declarations
		"fn identity<T>(x: T): T { return x }",
		"class Box<T>(value: T) { value }",
		"interface Pair<T, U> { first: T, second: U }",
		"enum Result<T, E> { Ok(value: T), Err(error: E) }",

		// Complex type annotations
		"fn complex(x: Array<{a: number, b: string}>): Map<string, number> { return new Map() }",
		"interface Nested { outer: { inner: { deep: number } } }",

		// Invalid syntax that should be handled gracefully
		"fn incomplete(",
		"class Missing {",
		"enum Broken { A,",
		"interface Bad { x:",
		"export fn",
		"declare class",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Create a single source to avoid resource exhaustion
		// Test different paths on different runs to test namespace derivation
		sources := []*ast.Source{
			{
				ID:       0,
				Path:     "main.esc",
				Contents: input,
			},
		}

		// Use a shorter timeout to prevent resource exhaustion
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// The test passes if ParseLibFiles doesn't panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseLibFiles panicked on input %q: %v", input, r)
			}
		}()

		_, _ = ParseLibFiles(ctx, sources)

		// Fail if the context timed out
		if ctx.Err() == context.DeadlineExceeded {
			t.Errorf("ParseLibFiles timed out on input %q", input)
		}
	})
}

// FuzzParseTypeAnn tests that ParseTypeAnn never panics on arbitrary input
func FuzzParseTypeAnn(f *testing.F) {
	// Seed corpus with valid type annotations
	seeds := []string{
		// Primitive types
		"number",
		"string",
		"boolean",
		"void",
		"any",
		"unknown",
		"never",
		"bigint",
		"symbol",

		// Array types
		"number[]",
		"string[]",
		"Array<number>",
		"Array<string>",
		"ReadonlyArray<number>",

		// Tuple types
		"[number, string]",
		"[number, string, boolean]",
		"[number, ...string[]]",

		// Object types
		"{x: number}",
		"{x: number, y: string}",
		"{x: number, y: string, z: boolean}",
		"{readonly x: number}",
		"{x?: number}",

		// Function types
		"() => void",
		"(x: number) => string",
		"(x: number, y: string) => boolean",
		"<T>(x: T) => T",

		// Union types
		"number | string",
		"number | string | boolean",
		"null | undefined",

		// Intersection types
		"A & B",
		"A & B & C",

		// Generic types
		"Box<number>",
		"Map<string, number>",
		"Promise<string>",
		"Array<Box<number>>",

		// Qualified types
		"Foo.Bar",
		"Foo.Bar.Baz",
		"Foo.Bar<number>",

		// Complex types
		"Array<{x: number, y: string}>",
		"Map<string, Array<number>>",
		"{x: number, y: (a: string) => boolean}",
		"((x: number) => string) | ((y: string) => number)",

		// Edge cases
		"",
		" ",
		"<",
		">",
		"|",
		"&",
		"(",
		")",
		"[",
		"]",
		"{",
		"}",
		":",
		",",
		"=>",
		"...",
		"?",
		"readonly",

		// Incomplete types
		"Array<",
		"Map<string,",
		"{x:",
		"(x:",
		"number |",
		"A &",
		"Foo.",

		// Nested types
		"Array<Array<Array<number>>>",
		"{a: {b: {c: number}}}",
		"((x: (y: number) => string) => boolean) => void",

		// Keywords as type names
		"String",
		"Number",
		"Boolean",
		"Object",
		"Function",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Use a timeout to prevent infinite loops
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		// The test passes if ParseTypeAnn doesn't panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ParseTypeAnn panicked on input %q: %v", input, r)
			}
		}()

		_, _ = ParseTypeAnn(ctx, input)

		// Fail if the context timed out
		if ctx.Err() == context.DeadlineExceeded {
			t.Errorf("ParseTypeAnn timed out on input %q", input)
		}
	})
}

// FuzzParseCombination tests parsing with various combinations of valid and invalid syntax
func FuzzParseCombination(f *testing.F) {
	// Seed with combinations of different constructs
	seeds := [][]string{
		{"val x = ", "5", ""},
		{"fn foo(", "a", ") { return a }"},
		{"class ", "Foo", "(x: number) { x }"},
		{"enum ", "E", " { A, B }"},
		{"interface ", "I", " { x: number }"},
		{"export ", "fn bar() {}", ""},
		{"declare ", "val x: number", ""},
		{"if (", "true", ") { return 1 }"},
		{"match ", "x", " { A -> 1 }"},
		{"async ", "fn foo() {}", ""},
		{"", "// comment", "\nval x = 5"},
		{"", "/* block */", "\nval x = 5"},
	}

	for _, seed := range seeds {
		// Combine the parts
		combined := seed[0] + seed[1] + seed[2]
		f.Add(combined)
	}

	f.Fuzz(func(t *testing.T, input string) {
		source := &ast.Source{
			ID:       0,
			Path:     "fuzz.esc",
			Contents: input,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		parser := NewParser(ctx, source)

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Parser panicked on combined input %q: %v", input, r)
			}
		}()

		_, _ = parser.ParseScript()

		// Fail if the context timed out
		if ctx.Err() == context.DeadlineExceeded {
			t.Errorf("Parser timed out on combined input %q", input)
		}
	})
}
