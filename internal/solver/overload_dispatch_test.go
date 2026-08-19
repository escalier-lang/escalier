package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// An overload set is a name carrying more than one top-level function declaration, and
// each of those declarations is an ARM of the set. A call reaches exactly one arm.
//
// Static resolution and the generated dispatcher must agree on which arm answers a
// call. The checker picks an arm in resolveOverload, driven by specificityOrder over
// the inferred arm types. The generated code picks one by running the if-else chain
// codegen.DispatchOrder lays out, driven by the arms' written parameter annotations.
// The two rankings read different things, so they can drift apart. When they do, a call
// reaches an arm whose return type is not the one the checker gave it. That is a
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
			// A union is untestable at runtime, so this arm's guard is a bare `true`. That
			// does NOT make it the catch-all a type parameter is. The checker cannot rank a
			// union against a primitive either, so both sides leave the pair tied and the
			// union arm keeps its declared place. Sorting it last on the strength of its
			// `true` guard would send f(5) to the number arm the checker did not resolve
			// it to.
			name: "an untestable union arm is not a catch-all",
			src: `
				fn f(x: number | string) -> string { return "u" }
				fn f(x: number) -> boolean { return true }
			`,
			want: []string{"string", "boolean"},
		},
		{
			// Required-property counting has to look past a property type neither side can
			// test. Both arms type `x` as the same union, so the `y` property is what
			// separates them and the two-property arm is tested first.
			name: "required properties rank past an untestable property type",
			src: `
				fn f(p: {x: number | string}) -> string { return "x" }
				fn f(p: {x: number | string, y: number}) -> boolean { return true }
			`,
			want: []string{"boolean", "string"},
		},
		{
			// A declare-only arm gets no branch in the generated chain, but it still takes
			// part in the ranking on both sides. `{y, w, ...}` outranks `{y, ...}`, which is
			// what leaves the later `{x, ...}` arm ahead of it.
			name: "a declare-only arm takes part in the ranking",
			src: `
				fn f(p: {y: number, ...}) -> boolean { return true }
				declare fn f(p: {y: number, w: number, ...}) -> number
				fn f(p: {x: number, ...}) -> string { return "x" }
			`,
			want: []string{"number", "string", "boolean"},
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

// Two arms the specificity ranking cannot separate fall back to declaration order, and
// both sides have to mean the same thing by that. The checker sorts its arms into
// source-position order before binding the set, so it never sees the order the dep
// graph happened to produce. DispatchOrder sorts too, for the same reason, and this
// test hands it the two arms back to front to prove it.
//
// The two arms live in separate files, since a single file's declarations are already
// in position order and would prove nothing. The parser gets them in path order, which
// is the order the compiler itself assigns source ids in. Reversing that would separate
// the two rankings rather than exercise them: armPosLess orders by file path while
// DispatchOrder orders by source id, the second gap the header of overload_order.go
// records.
func TestOverloadDispatchTiebreakIsSourcePosition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{
		{ID: 0, Path: "a.esc", Contents: `fn f(x: number) -> string { return "n" }`},
		{ID: 1, Path: "b.esc", Contents: `fn f(x: string) -> boolean { return true }`},
	})
	require.Empty(t, parseErrs, "expected no parse errors")

	arms, schemes := overloadArmsOfModule(t, module, "f")
	resolution := make([]string, len(arms))
	for i, idx := range specificityOrderOfArms(schemes) {
		resolution[i] = returnAnnOf(t, arms[idx])
	}
	require.Equal(t, []string{"string", "boolean"}, resolution,
		"the checker reads the arms in source-position order, a.esc before b.esc")

	// Hand DispatchOrder the arms back to front, the way an unsorted dep-graph walk
	// could. Sorting is what recovers the same order the checker read them in.
	reversed := []*ast.FuncDecl{arms[1], arms[0]}
	dispatch := make([]string, len(reversed))
	for i, decl := range codegen.DispatchOrder(reversed) {
		dispatch[i] = returnAnnOf(t, decl)
	}
	require.Equal(t, []string{"string", "boolean"}, dispatch,
		"the dispatcher recovers source-position order rather than keeping the order it was handed")
}

// overloadArmsOf parses and infers src, then returns the overload set bound to name.
func overloadArmsOf(t *testing.T, src, name string) ([]*ast.FuncDecl, []TypeScheme) {
	t.Helper()
	return overloadArmsOfModule(t, parseModule(t, src), name)
}

// overloadArmsOfModule infers module and returns the declarations of the overload set
// bound to name, in declaration order, alongside the inferred scheme of each arm. The
// two line up by index: arms[i] is the declaration the checker inferred schemes[i] from.
func overloadArmsOfModule(t *testing.T, module *ast.Module, name string) ([]*ast.FuncDecl, []TypeScheme) {
	t.Helper()
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
