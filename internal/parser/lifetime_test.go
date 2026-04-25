package parser

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

// TestLexLifetimeTokens snapshots the token stream for inputs that
// exercise the lifetime-token lexer path (`'ident`).
func TestLexLifetimeTokens(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"SingleLifetime":              {input: "'a"},
		"MultipleLifetimes":           {input: "'a 'static 'b1"},
		"LifetimeAdjacentToType":      {input: "mut 'a Point"},
		"LifetimeUnionInsideParens":   {input: "('a | 'b)"},
		"LifetimeInTypeParameterList": {input: "<'a, 'b, T>"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			lexer := NewLexer(source)
			tokens := lexer.Lex()
			snaps.MatchSnapshot(t, tokens)
		})
	}
}

// TestParseLifetimeAnnotations snapshots the parsed AST for declarations
// involving lifetime parameters and lifetime annotations on types.
func TestParseLifetimeAnnotations(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"FnSingleLifetimeParam": {
			input: `fn identity<'a>(p: mut 'a Point) -> mut 'a Point { return p }`,
		},
		"FnTwoLifetimeParams": {
			input: `fn first<'a, 'b>(a: mut 'a Point, b: mut 'b Point) -> mut 'a Point { return a }`,
		},
		"FnLifetimeAndTypeParams": {
			input: `fn identity<'a, T>(p: mut 'a T) -> mut 'a T { return p }`,
		},
		"FnReturnLifetimeUnion": {
			input: `fn pick<'a, 'b>(a: 'a Point, b: 'b Point, cond: boolean) -> ('a | 'b) Point { return a }`,
		},
		"FnImmutableLifetimeOnRef": {
			input: `fn ref<'a>(p: 'a Point) -> 'a Point { return p }`,
		},
		"FnTypeAnnLifetimeParams": {
			input: `val f: fn<'a>(p: 'a Point) -> 'a Point = ref`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			module, errors := parser.ParseScript()

			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)
		})
	}
}

// TestParseLifetimeInUnsupportedContextErrors verifies that lifetime
// parameters appearing on declaration kinds that don't yet support them
// (class/type/interface/enum/methods) produce a parse-time diagnostic
// rather than being silently dropped. Functions and `fn`-type
// annotations remain the only supported sites in Phase 8.1.
func TestParseLifetimeInUnsupportedContextErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"ClassWithLifetimeParam":     {input: `class Container<'a>(p: 'a Point) { p, }`},
		"TypeAliasWithLifetimeParam": {input: `type Box<'a> = 'a Point`},
		"InterfaceWithLifetimeParam": {input: `interface Holder<'a> { p: 'a Point }`},
		"EnumWithLifetimeParam":      {input: `enum Maybe<'a> { Some('a Point), None }`},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			_, errors := parser.ParseScript()

			assert.NotEmpty(t, errors,
				"expected a parse error for lifetime in unsupported context")
		})
	}
}

// TestParseDataClass snapshots class declarations with the
// `data` modifier and a regular class declaration for contrast.
func TestParseDataClass(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"DataClass": {
			input: `data class Config(host: string) { host, }`,
		},
		"DataClassWithMutSelfMethod": {
			input: `
				data class Config(host: string) {
					host,
					setHost(mut self, h: string) -> void {}
				}
			`,
		},
		"RegularClass": {
			input: `class Point(x: number, y: number) { x, y, }`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			module, errors := parser.ParseScript()

			for _, stmt := range module.Stmts {
				snaps.MatchSnapshot(t, stmt)
			}
			if len(errors) > 0 {
				for i, err := range errors {
					fmt.Printf("Error[%d]: %#v\n", i, err)
				}
			}
			assert.Len(t, errors, 0)
		})
	}
}
