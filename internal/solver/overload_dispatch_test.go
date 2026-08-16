package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Static resolution and the generated dispatcher must agree on which arm answers a
// call. The checker picks an arm in resolveOverload, driven by specificityOrder over
// the inferred arm types. The generated code picks one by running the if-else chain
// codegen.DispatchOrder lays out, driven by the arms' written parameter annotations.
// Two rankings derived from two representations can drift, and when they drift a call
// reaches an arm whose return type is not the one the checker gave it — which is a
// soundness problem, not a cosmetic one.
//
// The rows below are the corpus that measures the two against each other. Each row is
// an overload set whose arms are told apart by their return type, so naming the arm by
// its return type names it in both rankings at once. Both orders are computed for the
// whole set and compared position by position.
//
// One key is deliberately not compared. DispatchOrder puts arms with more parameters
// first, because a guard tests only the parameters its own arm declares, so a
// one-parameter arm placed first would answer a two-argument call. The checker has no
// counterpart: tryOverloadArm rejects an arm of the wrong arity before it is ranked at
// all. So each row's arms share an arity, and TestOverloadDispatchArityIsCheckerOnly
// covers the mixed-arity case on its own.

func TestOverloadDispatchAgreesWithResolution(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want names the arms in the order both rankings must produce, one entry per
		// arm, written as the arm's return annotation.
		want []string
	}{
		{
			// The disagreement this corpus exists to catch. A literal admits one value out
			// of its primitive's set, so the checker ranks `(x: 5)` ahead of `(x: number)`
			// however they are declared. A dispatcher that kept declaration order would
			// route f(5) to the number arm and hand back `string` where the checker said
			// `boolean`.
			name: "a literal arm outranks its primitive",
			src: `
				fn f(x: number) -> string { return "n" }
				fn f(x: 5) -> boolean { return true }
			`,
			want: []string{"boolean", "string"},
		},
		{
			name: "a literal arm declared first keeps its place",
			src: `
				fn f(x: 5) -> boolean { return true }
				fn f(x: number) -> string { return "n" }
			`,
			want: []string{"boolean", "string"},
		},
		{
			// Two primitives of different families accept disjoint arguments, so neither
			// outranks the other and both rankings fall back to declaration order.
			name: "disjoint primitives keep declaration order",
			src: `
				fn f(x: number) -> string { return "n" }
				fn f(x: string) -> boolean { return true }
			`,
			want: []string{"string", "boolean"},
		},
		{
			// An object with more required properties accepts fewer arguments, so it is
			// tested first. Every `{x, y}` is also an `{x}`, and testing `{x}` first would
			// swallow both.
			name: "more required properties outranks fewer",
			src: `
				fn f(p: {x: number}) -> string { return "n" }
				fn f(p: {x: number, y: number}) -> boolean { return true }
			`,
			want: []string{"boolean", "string"},
		},
		{
			// An optional property widens an arm rather than narrowing it: `{x, y?}`
			// accepts everything `{x}` accepts. Counting it would rank the wider arm first.
			name: "an optional property does not narrow an arm",
			src: `
				fn f(p: {x: number}) -> string { return "n" }
				fn f(p: {x: number, y?: number}) -> boolean { return true }
			`,
			want: []string{"string", "boolean"},
		},
		{
			// An exact object accepts no extra properties, so it is narrower than the
			// inexact one over the same fields. The two emit the SAME guard, which is
			// exactly why the ranking has to settle which of them answers the call.
			name: "an exact object outranks an inexact one over the same fields",
			src: `
				fn f(p: {x: number, ...}) -> string { return "n" }
				fn f(p: {x: number}) -> boolean { return true }
			`,
			want: []string{"boolean", "string"},
		},
		{
			// A type parameter is untestable at runtime, so its guard is a bare `true` and
			// the arm accepts every argument that reaches it. Both rankings treat it as the
			// catch-all and put it last.
			name: "an untestable arm sorts last",
			src: `
				fn f<T>(x: T) -> T { return x }
				fn f(x: string) -> boolean { return true }
			`,
			want: []string{"boolean", "T"},
		},
		{
			// Three arms whose ranking is only a partial order: the two concretes are
			// incomparable with each other and both beat the catch-all. Ranking by how many
			// arms dominate each one puts the concretes first, tied in declaration order,
			// and the catch-all last. Both sides rank this way.
			name: "two concretes and a catch-all",
			src: `
				fn f<T>(x: T) -> T { return x }
				fn f(x: number) -> string { return "n" }
				fn f(x: string) -> boolean { return true }
			`,
			want: []string{"string", "boolean", "T"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arms, armType := overloadArmsOf(t, tt.src, "f")
			require.Len(t, arms, len(tt.want))

			resolution := make([]string, len(arms))
			for i, idx := range specificityOrderOfArms(armType) {
				resolution[i] = returnAnnOf(t, arms[idx])
			}
			require.Equal(t, tt.want, resolution, "the order resolveOverload trials arms in")

			dispatch := make([]string, len(arms))
			for i, decl := range codegen.DispatchOrder(arms) {
				dispatch[i] = returnAnnOf(t, decl)
			}
			require.Equal(t, tt.want, dispatch, "the order the generated dispatcher tests arms in")
		})
	}
}

// Parameter count is the one ranking key the dispatcher has and the checker does not.
// The checker gates arity in tryOverloadArm, so a call only ever reaches arms of its
// own arity and their relative order is all that matters. The dispatcher has no arity
// gate, so it must test the arm with the most parameters first. Here that puts the two
// orders in opposite sequences without either being wrong.
func TestOverloadDispatchArityIsCheckerOnly(t *testing.T) {
	src := `
		fn f(x: string) -> string { return x }
		fn f(x: string, y: string) -> boolean { return true }
	`
	arms, armType := overloadArmsOf(t, src, "f")

	resolution := make([]string, len(arms))
	for i, idx := range specificityOrderOfArms(armType) {
		resolution[i] = returnAnnOf(t, arms[idx])
	}
	require.Equal(t, []string{"string", "boolean"}, resolution,
		"arity is not a specificity signal, so the arms stay in declaration order")

	dispatch := make([]string, len(arms))
	for i, decl := range codegen.DispatchOrder(arms) {
		dispatch[i] = returnAnnOf(t, decl)
	}
	require.Equal(t, []string{"boolean", "string"}, dispatch,
		"the two-parameter arm is tested first so it is not swallowed by the one-parameter arm")

	// The arity gate is what makes the two orders equivalent for any actual call: each
	// call reaches exactly one of these arms.
	values, _, errs := inferSource(t, src+"\nval a = f(\"hi\")\nval b = f(\"hi\", \"there\")")
	require.Empty(t, errs)
	require.Equal(t, "string", values["a"])
	require.Equal(t, "boolean", values["b"])
}

// overloadArmsOf infers src and returns the declarations of the overload set bound to
// name, in declaration order, alongside the inferred type of each arm. The two line up
// by index: arms[i] is the declaration the checker inferred armType[i] from.
func overloadArmsOf(t *testing.T, src, name string) ([]*ast.FuncDecl, []TypeScheme) {
	t.Helper()
	module := parseModule(t, src)
	c := newChecker()
	scope := sharedPrelude().Child()
	c.inferDepGraph(scope, 0, module, dep_graph.BuildDepGraph(module))
	require.Empty(t, c.errs)

	b, ok := scope.GetValue(name)
	require.True(t, ok, "no binding named %s", name)
	require.True(t, b.IsOverloaded(), "%s is not an overload set", name)
	require.Len(t, b.Sources, len(b.Schemes), "one source per arm")

	decls := make([]*ast.FuncDecl, len(b.Sources))
	for i, src := range b.Sources {
		nodeProv, ok := src.(*ast.NodeProvenance)
		require.True(t, ok, "arm %d has no declaration node", i)
		fd, ok := nodeProv.Node.(*ast.FuncDecl)
		require.True(t, ok, "arm %d is not a function declaration", i)
		decls[i] = fd
	}
	return decls, b.Schemes
}

// specificityOrderOfArms returns the arm indices in the order resolveOverload trials
// them for a call whose arguments all carry type information, which is what
// overloadOrder computes for such a call.
func specificityOrderOfArms(schemes []TypeScheme) []int {
	cands := make([]soltype.Type, len(schemes))
	for i, s := range schemes {
		// Leave a non-function arm's slot a nil interface, the way overloadOrder does, so
		// specificityOrder ranks it last instead of dereferencing a typed nil.
		if ft := schemeFunc(s); ft != nil {
			cands[i] = ft
		}
	}
	return specificityOrder(cands)
}

// returnAnnOf renders an arm's written return annotation, which is what each row names
// the arm by. Every arm in this corpus annotates its return.
func returnAnnOf(t *testing.T, decl *ast.FuncDecl) string {
	t.Helper()
	require.NotNil(t, decl.Return, "every arm in this corpus annotates its return")
	switch ret := decl.Return.(type) {
	case *ast.StringTypeAnn:
		return "string"
	case *ast.NumberTypeAnn:
		return "number"
	case *ast.BooleanTypeAnn:
		return "boolean"
	case *ast.TypeRefTypeAnn:
		return ast.QualIdentToString(ret.Name)
	}
	t.Fatalf("unhandled return annotation %T", decl.Return)
	return ""
}
