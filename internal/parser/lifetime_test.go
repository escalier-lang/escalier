package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/snapshot"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			snaps.MatchSnapshot(t, snapshot.String(tokens))
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
		"FnLifetimeParamSingleBound": {
			input: `fn pick<'a, 'b: 'a>(a: mut 'a Point, b: mut 'b Point) -> mut 'b Point { return b }`,
		},
		"FnLifetimeParamMultiBound": {
			input: `fn pick<'a: 'b & 'c, 'b, 'c>(a: mut 'a Point) -> mut 'a Point { return a }`,
		},
		"FnLifetimeParamStaticBound": {
			input: `fn keep<'a: 'static>(p: mut 'a Point) -> mut 'a Point { return p }`,
		},
		"FnTypeAnnLifetimeParamBound": {
			input: `val f: fn<'a, 'b: 'a>(a: 'a Point, b: 'b Point) -> 'b Point = pick`,
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
		"ClassMethodMutSelfLifetime": {
			input: `class Container { setItem<'a>(mut 'a self, p: mut 'a Point) -> void { } }`,
		},
		"ClassMethodSelfLifetime": {
			input: `class View { peek<'a>('a self) -> 'a Point { return self.p } }`,
		},
		"InterfaceMethodMutSelfLifetime": {
			input: `interface Mutator { setItem<'a>(mut 'a self, p: mut 'a Point) -> void }`,
		},
		"InterfaceMethodSelfLifetime": {
			input: `interface Viewer { peek<'a>('a self) -> 'a Point }`,
		},
		"ClassWithLifetimeParam": {
			input: `class Container<'a> { p: 'a Point }`,
		},
		"InterfaceWithLifetimeParam": {
			input: `interface Holder<'a> { p: 'a Point }`,
		},
		"TypeAliasWithLifetimeAndTypeParams": {
			input: `type Ref<'a, T> = &'a T`,
		},
		"TypeAliasWithLifetimeParamBound": {
			input: `type Pair<'a, 'b: 'a, T> = [&'a T, &'b T]`,
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
		// A `(` that opens a parenthesized type whose first token is a lifetime
		// is grouping, not the retired `('a | 'b)` union. The lifetime prefix
		// binds the inner type inside the parens, so these parse cleanly.
		"ParenLifetimePrefixType": {
			input: `val v: ('a Point) = ref`,
		},
		"ParenLifetimePrefixTypeArg": {
			input: `val v: View<('a Point)> = ref`,
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
				snaps.MatchSnapshot(t, snapshot.String(stmt))
			}
			assert.Empty(t, errors, "unexpected errors: %#v", errors)
		})
	}
}

// TestParseLifetimeInUnsupportedContextErrors verifies that lifetime
// parameters on declaration kinds that still don't support them
// (enums, object/class-field method shorthands) produce a parse-time
// diagnostic rather than being silently dropped. Functions, `fn`-type
// annotations, classes, interfaces, and type aliases all support
// `<'a, ...>` clauses — see TestParseLifetimeAnnotations.
func TestParseLifetimeInUnsupportedContextErrors(t *testing.T) {
	const expected = "lifetime parameters are not supported in this context"

	tests := map[string]struct {
		input string
	}{
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

// TestParseLifetimeBoundErrors verifies the disambiguations the bound syntax
// settles: 'static cannot bind on the left of a binder, and a bound's right-
// hand side accepts only lifetimes, so a non-lifetime after ':' or '&' is
// rejected. Each input recovers to exactly one diagnostic.
func TestParseLifetimeBoundErrors(t *testing.T) {
	tests := map[string]struct {
		input   string
		message string
	}{
		"StaticOnLeftOfBinder": {
			input:   `fn f<'static>(p: Point) -> Point { return p }`,
			message: "'static is not a bindable lifetime parameter name",
		},
		"NonLifetimeAfterColon": {
			input:   `fn f<'a: T>(p: 'a Point) -> 'a Point { return p }`,
			message: "expected a lifetime in a lifetime bound",
		},
		"NonLifetimeAfterAmpersand": {
			input:   `fn f<'a: 'b & T>(p: 'a Point) -> 'a Point { return p }`,
			message: "expected a lifetime in a lifetime bound",
		},
		"MissingLifetimeAfterColon": {
			input:   `fn f<'a:>(p: 'a Point) -> 'a Point { return p }`,
			message: "expected a lifetime in a lifetime bound",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			_, errors := parser.ParseScript()

			require.Len(t, errors, 1,
				"expected exactly one parse error: %#v", errors)
			require.Equal(t, test.message, errors[0].Message)
		})
	}
}

