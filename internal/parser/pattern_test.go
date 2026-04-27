package parser

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePatternNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringLiteral": {
			input: "\"hello\"",
		},
		"NumberLiteral": {
			input: "5",
		},
		"BooleanLiteralTrue": {
			input: "true",
		},
		"BooleanLiteralFalse": {
			input: "false",
		},
		"NullLiteral": {
			input: "null",
		},
		"UndefinedLiteral": {
			input: "undefined",
		},
		"RegexLiteral": {
			input: "/hello/gi",
		},
		"Identifier": {
			input: "x",
		},
		"IdentifierWithTypeAnnotation": {
			input: "x:number",
		},
		"IdentifierWithTypeAnnotationAndDefault": {
			input: "x:number = 5",
		},
		"Wildcard": {
			input: "_",
		},
		"TuplePatternWithRest": {
			input: "[a, b = 5, ...rest]",
		},
		"TuplePatternWithTypeAnnotations": {
			input: "[x:number, y:string = 5]",
		},
		"ObjectPatternWithRest": {
			input: "{a, b: c, ...rest}",
		},
		"ObjectPatternWithDefaults": {
			input: "{a = 5, b: c = \"hello\"}",
		},
		"ObjectPatternWithInlineTypeAnnotations": {
			input: "{x::number, y::string}",
		},
		"ObjectPatternWithInlineTypeAnnotationsAndDefaults": {
			input: "{x::number = 0, y::string = \"hello\"}",
		},
		"ObjectPatternWithKeyValueAndInlineTypeAnnotations": {
			input: "{x: a:number, y: b:string}",
		},
		"ObjectPatternWithKeyValueInlineTypeAnnotationsAndDefaults": {
			input: "{x: a:number = 0, y: b:string = \"hello\"}",
		},
		"ExtractPattern": {
			input: "Foo(a, b)",
		},
		"NamespacedExtractPattern": {
			input: "MyNamespace.Foo(a, b)",
		},
		"QualifiedExtractPatternNoArgs": {
			input: "Option.None",
		},
		"InstancePattern": {
			input: "Point {x, y}",
		},
		"NamespacedInstancePattern": {
			input: "MyNamespace.Point {x, y}",
		},
		"WildcardPattern": {
			input: "_",
		},
		"MutIdent": {
			input: "mut x",
		},
		"MutIdentWithTypeAnnotation": {
			input: "mut x: number",
		},
		"ObjectPatternWithMutShorthand": {
			input: "{mut x, y}",
		},
		"ObjectPatternWithMutKeyValue": {
			input: "{x: mut a, y: b}",
		},
		"ObjectPatternMixedMutShorthandAndKeyValue": {
			input: "{mut x, y: mut a, z}",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			expr := parser.pattern(true, true)

			snaps.MatchSnapshot(t, expr)
			assert.Equal(t, parser.errors, []*Error{})
		})
	}
}

// TestParseMutPatternRejected verifies that `mut` in pattern position
// only attaches to identifier leaves. Applying it to a destructuring
// pattern (tuple/object) or wildcard is rejected — per-leaf control
// inside destructuring is expressed by putting `mut` on each leaf.
func TestParseMutPatternRejected(t *testing.T) {
	tests := map[string]string{
		"OnTuplePat":         "mut [a, b]",
		"OnObjectPat":        "mut {a, b}",
		"OnWildcard":         "mut _",
		"OnLitPat":           "mut 5",
		"OnRestPat":          "mut ...rest",
		"InObjShortNonIdent": "{mut 5}",
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := NewParser(ctx, source)
			_ = p.pattern(true, true)
			assert.NotEmpty(t, p.errors,
				"expected parse error for %q, got none", input)
		})
	}
}

// TestParseMutOnNonIdentDoesNotDoubleReport verifies that recovery
// from `mut <non-ident>` reports the error exactly once and does not
// re-enter the pattern parser to produce a second "Expected a pattern"
// error from literalPat's default case.
func TestParseMutOnNonIdentDoesNotDoubleReport(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		// Tokens that fall through pattern()'s switch to literalPat
		// and aren't recognized as literals — these are the cases that
		// could produce a second "Expected a pattern" diagnostic.
		"MutCloseParen": "mut )",
		"MutEOF":        "mut",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := NewParser(ctx, source)
			_ = p.pattern(true, true)
			require.Lenf(t, p.errors, 1,
				"expected exactly one error for %q, got %d: %v",
				input, len(p.errors), p.errors)
		})
	}
}

// TestParseMutOnDestructuringSuggestsPerLeaf verifies that `mut [a, b]`
// and `mut {a, b}` report errors hinting the per-leaf form
// (`[mut a, b]` / `{mut a, b}`) instead of just saying "must be followed
// by an identifier", which doesn't tell the user what to do instead.
func TestParseMutOnDestructuringSuggestsPerLeaf(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectContains string
	}{
		"OnTuplePat":  {"mut [a, b]", "[mut a, b]"},
		"OnObjectPat": {"mut {a, b}", "{mut a, b}"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := NewParser(ctx, source)
			_ = p.pattern(true, true)
			require.NotEmpty(t, p.errors, "expected an error for %q", test.input)
			found := false
			for _, err := range p.errors {
				if strings.Contains(err.Message, test.expectContains) {
					found = true
					break
				}
			}
			assert.Truef(t, found,
				"expected error containing %q for input %q, got: %v",
				test.expectContains, test.input, p.errors)
		})
	}
}

// TestParseMutOnObjKeyValueErrorSpan verifies that `{ mut x: a }` —
// using `mut` on a key-value form rather than a shorthand — reports the
// error pointed at the `mut` keyword, not at the key.
func TestParseMutOnObjKeyValueErrorSpan(t *testing.T) {
	t.Parallel()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: "{mut x: a}"}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := NewParser(ctx, source)
	_ = p.pattern(true, true)
	require.NotEmpty(t, p.errors, "expected an error")
	for _, err := range p.errors {
		// `mut` starts at column 2 in `{mut x: a}` (after `{`).
		// The error's span should cover the `mut` keyword (column 2..5),
		// not the key `x` (column 6..7).
		assert.Equalf(t, 2, int(err.Span.Start.Column),
			"error span should start at the `mut` keyword (col 2), got col %d (msg: %s)",
			err.Span.Start.Column, err.Message)
	}
}

// TestParseMutSelfWithMutParam verifies that pattern-level `mut p` and
// the dedicated `mut self` parsing path coexist on the same method
// signature without interfering. The parser handles `mut self` before
// recursing into per-param patterns, so a subsequent `mut p` parameter
// must still parse cleanly.
func TestParseMutSelfWithMutParam(t *testing.T) {
	tests := map[string]struct {
		input          string
		wantParamCount int
		wantMutSelf    bool
		wantParamMut   []bool // by index, parameter pattern's IdentPat.Mutable
	}{
		"mut self followed by mut p": {
			input: `class Counter(c: number) {
				c,
				bump(mut self, mut p: number) -> number { return self.c + p },
			}`,
			wantParamCount: 1,
			wantMutSelf:    true,
			wantParamMut:   []bool{true},
		},
		"mut self followed by plain p": {
			input: `class Counter(c: number) {
				c,
				bump(mut self, p: number) -> number { return self.c + p },
			}`,
			wantParamCount: 1,
			wantMutSelf:    true,
			wantParamMut:   []bool{false},
		},
		"plain self followed by mut p": {
			input: `class Counter(c: number) {
				c,
				peek(self, mut p: number) -> number { return self.c + p },
			}`,
			wantParamCount: 1,
			wantMutSelf:    false,
			wantParamMut:   []bool{true},
		},
		"mut self followed by mut and plain mix": {
			input: `class Counter(c: number) {
				c,
				bump(mut self, mut p: number, q: number) -> number { return self.c + p + q },
			}`,
			wantParamCount: 2,
			wantMutSelf:    true,
			wantParamMut:   []bool{true, false},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := NewParser(ctx, source)
			script, errs := p.ParseScript()
			assert.Empty(t, errs, "expected no parse errors for %q", test.input)
			assert.NotNil(t, script, "expected a parsed script")
			method := findFirstMethodInScript(script)
			if method == nil {
				t.Fatalf("expected to find a MethodElem")
			}
			if test.wantMutSelf {
				assert.NotNil(t, method.MutSelf, "expected MutSelf to be set")
				if method.MutSelf != nil {
					assert.True(t, *method.MutSelf, "expected MutSelf to be true")
				}
			} else {
				if method.MutSelf != nil {
					assert.False(t, *method.MutSelf, "expected MutSelf to be false (plain self)")
				}
			}
			assert.Len(t, method.Fn.Params, test.wantParamCount,
				"unexpected param count")
			for i, want := range test.wantParamMut {
				if i >= len(method.Fn.Params) {
					break
				}
				ip, ok := method.Fn.Params[i].Pattern.(*ast.IdentPat)
				if !ok {
					t.Errorf("param[%d] is not IdentPat: %T", i, method.Fn.Params[i].Pattern)
					continue
				}
				assert.Equalf(t, want, ip.Mutable,
					"param[%d] (%s) Mutable mismatch", i, ip.Name)
			}
		})
	}
}

func findFirstMethodInScript(script *ast.Script) *ast.MethodElem {
	for _, stmt := range script.Stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		cd, ok := ds.Decl.(*ast.ClassDecl)
		if !ok {
			continue
		}
		for _, elem := range cd.Body {
			if me, ok := elem.(*ast.MethodElem); ok {
				return me
			}
		}
	}
	return nil
}
