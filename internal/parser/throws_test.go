package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// parseThrowsSrc parses one in-memory module and returns its errors, rendered with spans.
func parseThrowsSrc(t *testing.T, src string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, errs := ParseLibFiles(ctx, []*ast.Source{{ID: 0, Path: "input.esc", Contents: src}})
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.String()
	}
	return out
}

// Every signature form that can carry a `throws` clause parses one, independently of
// whether it also writes a `-> R` return annotation. The clause routes through one
// helper, so a form that parses it at all parses it the same way.
func TestParseThrowsClauseOnEverySignatureForm(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"FnDeclWithReturnType", `fn f() -> number throws string { return 1 }`},
		{"FnDeclWithoutReturnType", `fn f() throws string { throw "x" }`},
		{"FnExprWithoutReturnType", `val f = fn () throws string { throw "x" }`},
		{"DeclareFn", `declare fn f() -> number throws string`},
		{"Method", `class C { m(self) -> number throws string { throw "x" } }`},
		{"Getter", `class C { v: number, get x(self) -> number throws string { return self.v } }`},
		{"Setter", `class C { v: number, set x(mut self, v: number) throws string { self.v = v } }`},
		{"Constructor", `class C { constructor(mut self) throws string { throw "x" } }`},
		{"FuncTypeAnn", `type F = fn(x: number) -> number throws string`},
		{"ObjectMethodSignature", `type T = {parse(self) -> number throws SyntaxError}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Empty(t, parseThrowsSrc(t, test.src))
		})
	}
}

// A `throws` with no type after it is one error, and the rest of the signature still
// parses. The `{` case is the one that matters: handing the block to the type parser
// would read it as an object type and consume the function body, so the clause has to
// end before it.
func TestParseThrowsClauseMissingType(t *testing.T) {
	t.Run("FollowedByABody", func(t *testing.T) {
		require.Equal(t,
			[]string{"1:8-1:14: Expected type annotation after 'throws'"},
			parseThrowsSrc(t, `fn f() throws { return 1 }`))
	})
	t.Run("FollowedByTheNextDeclaration", func(t *testing.T) {
		errs := parseThrowsSrc(t, "fn f() -> number throws\nval y = 1")
		require.NotEmpty(t, errs)
		require.Equal(t, "1:18-1:24: Expected type annotation after 'throws'", errs[0])
	})
}

// A constructor accepts `throws` on either side of the `->` it may not declare. Writing
// one on each side is a single error, the return type, and the body still parses: the
// second clause is consumed and discarded, and the first is the one that survives.
func TestParseThrowsClauseOnBothSidesOfConstructorArrow(t *testing.T) {
	require.Equal(t,
		[]string{"1:47-1:49: constructors cannot declare a return type"},
		parseThrowsSrc(t, `class C { constructor(mut self) throws string -> number throws boolean { } }`))
}
