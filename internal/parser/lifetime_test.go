package parser

import (
	"context"
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
		"InterfaceMethodLifetimeParam": {
			input: `interface Borrower { borrow<'a>(self, p: 'a Point) -> 'a Point }`,
		},
		"ClassMethodLifetimeParam": {
			input: `class Box { borrow<'a>(self, p: 'a Point) -> 'a Point { return p } }`,
		},
		"ClassWithLifetimeParam": {
			input: `class Container<'a> { p: 'a Point }`,
		},
		"InterfaceWithLifetimeParam": {
			input: `interface Holder<'a> { p: 'a Point }`,
		},
		"TypeRefBareLifetimeArg": {
			input: `val v: View<'a> = ref`,
		},
		"TypeRefStaticLifetimeArg": {
			input: `val v: Container<'static> = ref`,
		},
		"TypeRefMixedLifetimeAndTypeArgs": {
			input: `val v: Pair<'a, T> = ref`,
		},
		"ClassImplementsLifetimeArg": {
			input: `class Forwarder<'a> implements View<'a> { value: 'a Point }`,
		},
		"FnReturnsTypeRefWithLifetimeArg": {
			input: `fn borrow<'a>(c: Container<'a>) -> 'a Point { return c.p }`,
		},
		"TypeRefLifetimeUnionArg": {
			input: `val v: View<('a | 'b)> = ref`,
		},
		"TypeRefLifetimeUnionArgMixed": {
			input: `val v: Pair<('a | 'b), T> = ref`,
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
			assert.Empty(t, errors, "unexpected errors: %#v", errors)
		})
	}
}

// TestParseLifetimeInUnsupportedContextErrors verifies that lifetime
// parameters on declaration kinds that still don't support them
// (type aliases, enums, object/class-field method shorthands) produce
// a parse-time diagnostic rather than being silently dropped.
// Functions, `fn`-type annotations, classes, and interfaces all
// support `<'a, ...>` clauses — see TestParseLifetimeAnnotations.
func TestParseLifetimeInUnsupportedContextErrors(t *testing.T) {
	const expected = "lifetime parameters are not supported in this context"

	tests := map[string]struct {
		input string
	}{
		"TypeAliasWithLifetimeParam":  {input: `type Box<'a> = 'a Point`},
		"EnumWithLifetimeParam":       {input: `enum Maybe<'a> { Some, None }`},
		"ClassFieldWithLifetimeParam":   {input: `class Box { p<'a>: Point }`},
		"ObjectPropertyWithLifetimeParam": {input: `val x: { p<'a>: Point } = ref`},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			_, errors := parser.ParseScript()

			if assert.Len(t, errors, 1,
				"expected exactly one parse error for lifetime in unsupported context: %#v", errors) {
				assert.Equal(t, expected, errors[0].Message)
			}
		})
	}
}

