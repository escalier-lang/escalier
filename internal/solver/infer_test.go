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
	values = renderBindings(scope.values, func(b ValueBinding) soltype.Type { return b.Type })
	types = renderBindings(scope.types, func(b TypeBinding) soltype.Type { return b.Type })
	return values, types, errs
}

// renderBindings renders each binding in m to its soltype string, using typeOf
// to pull the soltype.Type out of the binding. One helper serves both the value
// and type sorts.
func renderBindings[B any](m map[string]B, typeOf func(B) soltype.Type) map[string]string {
	out := make(map[string]string, len(m))
	for name, b := range m {
		out[name] = soltype.Print(typeOf(b))
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

// PR-4: object literals, tuple literals, and member access infer end-to-end
// through the real parser pipeline. Field reads resolve through a record-typed
// binding (constrain's record <: record arm lowers the result from the matching
// field), and a read of an absent field surfaces a MissingPropertyError.
func TestInferModuleObjectsAndTuples(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "RecordLiteral",
			src:  `val o = {a: 5, b: "hi"}`,
			want: map[string]string{"o": `{a: 5, b: "hi"}`},
		},
		{
			name: "EmptyRecord",
			src:  `val o = {}`,
			want: map[string]string{"o": "{}"},
		},
		{
			name: "TupleLiteral",
			src:  `val t = [1, "hi"]`,
			want: map[string]string{"t": `[1, "hi"]`},
		},
		{
			name: "NestedRecordInTuple",
			src:  `val t = [{a: 1}, 2]`,
			want: map[string]string{"t": `[{a: 1}, 2]`},
		},
		{
			name: "FieldRead",
			src: `
				val o = {a: 5, b: "hi"}
				val x = o.a
			`,
			want: map[string]string{"o": `{a: 5, b: "hi"}`, "x": "5"},
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

// Reading a field the receiver lacks is a constraint failure (MissingProperty);
// the binding for the failed read resolves to the never placeholder.
func TestInferModuleFieldReadMissingProperty(t *testing.T) {
	values, _, errs := inferSource(t, `
		val o = {a: 5}
		val x = o.b
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: b", errs[0].Message())
	require.Equal(t, map[string]string{"o": "{a: 5}", "x": "never"}, values)
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

// A `val` with no initializer can't be inferred in M2 (annotation-driven binding
// needs TypeAnn support that lands later); it reports MissingInitializerError and
// binds NOTHING, so a later reference still fails as an unknown identifier rather
// than silently resolving to a placeholder.
func TestInferModuleVarDeclWithoutInitializer(t *testing.T) {
	values, _, errs := inferSource(t, `declare val x: number`)
	require.Len(t, errs, 1)
	require.Equal(t, "Variable declaration requires an initializer: x", errs[0].Message())
	require.Empty(t, values)
}

// A no-initializer decl must not leak a binding: a later use of the name is a
// genuine unknown-identifier error, not a silent resolution to a placeholder.
func TestInferModuleNoInitializerDoesNotLeakBinding(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare val x: number
		val y = x
	`)
	require.Len(t, errs, 2)
	require.Equal(t, "Variable declaration requires an initializer: x", errs[0].Message())
	require.Equal(t, "Unknown identifier: x", errs[1].Message())
	require.Equal(t, map[string]string{"y": "never"}, values)
}

// A destructuring pattern is IdentPat-only-gated in M2 (M4 adds tuple/record
// binding); the binding reports UnsupportedNodeError and introduces no value.
// The initializer `[1, 2]` is a tuple expression, which PR-4 now infers, so the
// only remaining error is the destructuring pattern on the binding side.
func TestInferModuleDestructuringPatternUnsupported(t *testing.T) {
	values, _, errs := inferSource(t, `val [a, b] = [1, 2]`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TuplePat", errs[0].Message())
	require.Empty(t, values)
}

// A duplicate top-level `val` is a redeclaration error (unlike FuncDecl
// overloads); the first binding is kept and the second reports cleanly.
func TestInferModuleDuplicateTopLevelValIsError(t *testing.T) {
	values, _, errs := inferSource(t, `
		val x = 5
		val x = "hi"
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Duplicate declaration: x", errs[0].Message())
	require.Equal(t, map[string]string{"x": "5"}, values)
}
