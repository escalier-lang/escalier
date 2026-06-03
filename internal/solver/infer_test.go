package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// inferSource parses a single in-memory .esc source, runs InferModule, and
// returns the rendered top-level value/type bindings plus any SolverErrors. This
// is the PR-2 single-file table harness (§3.6) — fast, no on-disk fixtures.
// Bindings are read straight off the module scope's own maps (not the prelude
// parent), so operators and the stdlib-type placeholders are excluded. Parse
// errors fail the test outright; only inference errors flow back to the caller so
// a case can assert on them (e.g. the forward-reference limitation).
func inferSource(t *testing.T, src string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors, "expected no parse errors")

	scope, _, errs := InferModule(module)
	return renderValueBindings(scope.values), renderTypeBindings(scope.types), errs
}

// renderValueBindings renders each value binding to its soltype string.
func renderValueBindings(m map[string]ValueBinding) map[string]string {
	out := make(map[string]string, len(m))
	for name, b := range m {
		out[name] = soltype.Print(b.Type)
	}
	return out
}

// renderTypeBindings renders each type binding to its soltype string.
func renderTypeBindings(m map[string]TypeBinding) map[string]string {
	out := make(map[string]string, len(m))
	for name, b := range m {
		out[name] = soltype.Print(b.Type)
	}
	return out
}

func TestInferModuleValDecls(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "NumberLiteral",
			src:  `val x = 5`,
			want: map[string]string{"x": "5"},
		},
		{
			name: "StringLiteral",
			src:  `val s = "hi"`,
			want: map[string]string{"s": `"hi"`},
		},
		{
			name: "BoolLiteral",
			src:  `val b = true`,
			want: map[string]string{"b": "true"},
		},
		{
			name: "MultipleDecls",
			src: `
				val x = 5
				val s = "hi"
			`,
			want: map[string]string{"x": "5", "s": `"hi"`},
		},
		{
			name: "IdentifierInitializerReferencesEarlierDecl",
			src: `
				val x = 5
				val y = x
			`,
			want: map[string]string{"x": "5", "y": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// A forward reference — a decl that uses a name defined later in the source —
// fails in PR-2 because the module driver walks decls in source order with no
// dep-graph ordering. PR-5 adds SCC ordering and this case flips to success.
func TestInferModuleForwardReferenceIsError(t *testing.T) {
	values, _, errs := inferSource(t, `
		val y = x
		val x = 5
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: x", errs[0].Message())
	// x is bound by the time the loop reaches it; only the forward use of x in y
	// failed (y therefore resolves to the never placeholder report() returns).
	require.Equal(t, map[string]string{"x": "5", "y": "never"}, values)
}

// A top-level declaration outside PR-2's VarDecl coverage reports a clean
// UnsupportedNodeError rather than panicking. (FuncDecl support lands in PR-3.)
func TestInferModuleUnsupportedDecl(t *testing.T) {
	_, _, errs := inferSource(t, `fn f() {}`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: FuncDecl", errs[0].Message())
}

// A `val` with no initializer is outside the PR-2 subset (annotation-driven
// binding needs TypeAnn support that lands later) and reports cleanly.
func TestInferModuleVarDeclWithoutInitializer(t *testing.T) {
	_, _, errs := inferSource(t, `declare val x: number`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: VarDecl without initializer", errs[0].Message())
}

// A destructuring pattern is IdentPat-only-gated in M2 (M4 adds tuple/record
// binding); the binding reports UnsupportedNodeError and introduces no value.
func TestInferModuleDestructuringPatternUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val [a, b] = [1, 2]`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TuplePat", errs[0].Message())
	require.Empty(t, values)
}
