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

// PR6 — function overloading for free functions. A name with more than one
// top-level FuncDecl is an overload set; a direct call resolves to the arm whose
// parameter accepts the argument, via resolveOverload (a phase distinct from
// constrain, driven by the PR5 probe).

// Per-argument-type resolution: f(5) picks the number arm, f("hi") the string arm,
// each call yielding that arm's return type.
func TestInferOverloadResolvesByArgType(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val r = f(5)
		val s = f("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
	require.Equal(t, "string", values["s"])
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> string)", values["f"])
}

// Unannotated overloads are allowed when NOT recursive — arms are inferred
// independently and resolution dispatches on arity: f(5) hits the 1-param arm,
// f(5, "hi") the 2-param arm.
func TestInferOverloadDispatchesOnArity(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x, y) { x }
		val a = f(5)
		val b = f(5, "hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["a"])
	require.Equal(t, "5", values["b"])
}

// Two arms with identical parameter types are REJECTED: an overload set compiles to a
// single runtime function that dispatches on argument types, so two arms accepting
// exactly the same arguments cannot be told apart at codegen. The error blames the
// later arm and relates the earlier one.
func TestInferOverloadDuplicateParamTypesRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x: number) -> string { "a" }
		fn f(x: number) -> boolean { true }
		val r = f(5)
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"Overload arms must have distinguishable parameter types: f",
		errs[0].Message())
}

// Cross-file declaration order is pinned to SOURCE POSITION (file path alphabetically,
// then line/column), NOT to the order the parser received the files. Two arms of f
// live in separate files with DISTINCT parameter types (a.esc takes number, b.esc
// takes string); the value-position intersection must list them in path-alphabetical
// order — a.esc's number arm first, b.esc's string arm second. The files are handed to
// the parser in REVERSE-alphabetical order (b.esc first, so it gets the lower
// SourceID); the path-alphabetical arm (a.esc) must still come first regardless.
func TestInferOverloadCrossFileDeclarationOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sources := []*ast.Source{
		{ID: 0, Path: "b.esc", Contents: `fn f(x: string) -> boolean { true }`},
		{ID: 1, Path: "a.esc", Contents: "fn f(x: number) -> string { \"s\" }\nval r = f(5)"},
	}
	module, parseErrs := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrs, "expected no parse errors")
	values, _, errs := inferModule(module)
	require.Empty(t, errs)
	require.Equal(t, "string", values["r"],
		"f(5) selects a.esc's number arm")
	require.Equal(t, "(fn (x: number) -> string) & (fn (x: string) -> boolean)", values["f"],
		"the value-position intersection lists arms in path-alphabetical order (a.esc, then b.esc)")
}

// Specificity beats declaration order: a concrete arm declared AFTER a generic one
// still wins for a matching concrete argument (most-specific-first). f("hi") picks
// the string arm even though the generic arm is declared first and would also match.
func TestInferOverloadSpecificityBeatsDeclarationOrder(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x: string) -> boolean { true }
		val r = f("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "boolean", values["r"], "the more specific (string) arm outranks the earlier generic arm")
}

// A call with an unconstrained argument (a still-unconstrained parameter variable)
// falls back to declaration-order first-match: f(y) inside `fn (y) {…}` resolves to
// the first arm and pins y to that arm's parameter type. This over-narrows the
// enclosing function (g then rejects a later g("hi")) — a documented MVP limitation
// whose real fix (deferred resolution) is tracked in #723.
func TestInferOverloadDeferredFallsBackToFirstMatch(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val g = fn (y) { f(y) }
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (y: number) -> number", values["g"],
		"an unconstrained argument defers to declaration-order first-match, pinning y to the first arm")
}

// No arm accepts the argument ⇒ NoMatchingOverloadError listing the candidates.
func TestInferOverloadNoMatch(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val r = f(true)
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"No matching overload for this call\n  fn (x: number) -> number\n  fn (x: string) -> string",
		errs[0].Message())
}

// A mutually-recursive group containing an overloaded function with un-annotated
// arms is rejected: the overload set must be ground before the group's bodies are
// inferred. The error blames the offending overloaded participant.
func TestInferOverloadMutualRecursionRequiresAnnotation(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(x) { g(x) }
		fn f(y) { g(y) }
		fn g(z) { f(z) }
	`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"Overloaded function in a recursive group must have fully-annotated signatures: f",
		errs[0].Message())
}

// A self-recursive fully-annotated overload type-checks: each arm's recursive call
// resolves against the whole pre-bound overload set (the number arm's f(x) hits the
// number arm, the string arm's hits the string arm), so neither arm is wrongly
// checked against the other. (Before the pre-binding fix this reported two spurious
// `cannot constrain` errors because the recursive reference saw only the first arm.)
func TestInferOverloadSelfRecursiveAnnotated(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { f(x) }
		fn f(x: string) -> string { f(x) }
	`)
	require.Empty(t, errs)
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> string)", values["f"])
}

// A fully-annotated overload that is MUTUALLY recursive with another binding which
// also CAPTURES it as a value resolves cleanly: the overload set is pre-bound before
// any body, so both the recursive calls and the value capture (`val h = f`) inside
// the component see the whole set, not a single first-arm var.
func TestInferOverloadMutualRecursionValueCapture(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { g(x) }
		fn f(x: string) -> number { g(5) }
		fn g(n: number) -> number {
			val h = f
			h(n)
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> number)", values["f"])
	require.Equal(t, "fn (n: number) -> number", values["g"])
}

// The gate is scoped to MUTUAL recursion: a non-recursive annotated overload whose
// arms merely call another (non-recursive) function is fine, since its component is
// a singleton — the gate never fires and the set binds normally.
func TestInferOverloadNonRecursiveAnnotatedAllowed(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn g(z: number) -> number { z }
		fn f(x: number) -> number { g(x) }
		fn f(x: string) -> string { "s" }
	`)
	require.Empty(t, errs)
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> string)", values["f"])
}

// Value-position use (PR6 scoped lattice exception): a let-bound overloaded name is
// the INTERSECTION of its arms, and calling THROUGH that binding resolves each call
// via constrain's function-intersection-LHS arm — g(5) ⇒ number, g("hi") ⇒ string.
func TestInferOverloadValuePosition(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val g = f
		val r = g(5)
		val s = g("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> string)", values["g"],
		"a let-bound overloaded name carries the intersection of its arms")
	require.Equal(t, "number", values["r"])
	require.Equal(t, "string", values["s"])
}

// A GENERIC overload arm used through a let-binding is freshened per use, not shared:
// g("hi") and g(true) resolve the generic 1-param arm independently, so they keep
// distinct types instead of cross-contaminating to "hi" | true. (Guards the
// soltype.LevelOf recursion into IntersectionType — without it freshenAbove prunes
// the level-0 intersection and aliases the arm's type variable across uses.)
func TestInferOverloadGenericArmValuePositionNoAlias(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x, y) { x }
		val g = f
		val a = g("hi")
		val b = g(true)
	`)
	require.Empty(t, errs)
	require.Equal(t, `"hi"`, values["a"], "the generic arm is freshened per use, not aliased")
	require.Equal(t, "true", values["b"])
}

// Value-position resolution uses the SAME specificity order as a direct call: a
// concrete arm declared after a generic one wins for a matching concrete argument,
// whether the callee is the overloaded name directly or a let-bound alias.
func TestInferOverloadValuePositionMatchesDirectOrder(t *testing.T) {
	direct, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x: string) -> boolean { true }
		val r = f("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "boolean", direct["r"], "direct call picks the more specific arm")

	binding, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x: string) -> boolean { true }
		val g = f
		val r = g("hi")
	`)
	require.Empty(t, errs)
	require.Equal(t, "boolean", binding["r"], "a call through a binding resolves to the same arm as the direct call")
}

// Three mixed arms (concrete-literal-ish, concrete-prim, generic) rank by specificity
// without relying on a non-transitive comparator: each call with a concrete argument
// selects the arm that accepts it, most-specific-first with declaration-order tiebreak.
func TestInferOverloadThreeArmSpecificity(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x) { x }
		fn f(x: number) -> boolean { true }
		fn f(x: string) -> string { x }
		val p = f(5)
		val q = f("hi")
		val r = f(true)
	`)
	require.Empty(t, errs)
	require.Equal(t, "boolean", values["p"], "number arm")
	require.Equal(t, "string", values["q"], "string arm")
	require.Equal(t, "true", values["r"], "falls back to the generic arm")
}

// Resolution rollback (exercising the PR5 probe): the first-tried arm (string) is a
// non-match for a number-bounded argument variable; its speculative `arg <: string`
// upper bound must be rolled back, so after resolution the argument var carries ONLY
// the winning (number) arm's bound — never the loser's. Built directly so the
// argument variable is inspectable.
func TestResolveOverloadRollsBackLosingArm(t *testing.T) {
	c := newChecker()

	str := func() soltype.Type { return &soltype.PrimType{Prim: soltype.StrPrim} }
	num := func() soltype.Type { return &soltype.PrimType{Prim: soltype.NumPrim} }
	strFn := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: str()}},
		Ret:    str(),
	}
	numFn := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: num()}},
		Ret:    num(),
	}
	// Overload set in declaration order: string arm first, number arm second.
	b := ValueBinding{Schemes: []TypeScheme{monoScheme(strFn), monoScheme(numFn)}}

	// The argument is a variable carrying a number-literal lower bound — constrained
	// enough to rank, but incompatible with the string arm (5 </: string).
	argVar := c.freshAt(0)
	argVar.LowerBounds = []soltype.Type{&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}}

	call := ast.NewCall(identExpr("f"), []ast.Expr{numExpr(5)}, false, testSpan())
	ret := c.resolveOverload(0, b, []soltype.Type{argVar}, call)

	require.Empty(t, c.errs, "the losing arm's trial error is rolled back, not accumulated")
	require.Equal(t, "number", soltype.Print(ret))
	require.Len(t, argVar.UpperBounds, 1, "the losing string arm's speculative upper bound was rolled back")
	require.Equal(t, num(), argVar.UpperBounds[0], "only the winning number arm's bound survives")
}

// An overload binding's Sources lines up one-to-one with its Schemes (each arm
// contributes one of each), so a multi-target go-to-definition can map Schemes[i] to
// the decl at Sources[i].
func TestInferOverloadBindingSourcesAlignWithSchemes(t *testing.T) {
	scope, _, errs := InferModule(parseModule(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
	`))
	require.Empty(t, errs)
	b, ok := scope.GetValue("f")
	require.True(t, ok)
	require.True(t, b.IsOverloaded())
	require.Len(t, b.Schemes, 2)
	require.Len(t, b.Sources, 2, "Sources lines up one-to-one with Schemes")
}

// A name bound by FuncDecls AND a `val` is not function-only, so it is not an overload
// set: the functions overload and the `val` is reported as a duplicate declaration. The
// shared funcOnlyDecls classifier keeps the gate and the binding consistent.
func TestInferOverloadMixedWithValIsDuplicate(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val f = 5
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Duplicate declaration: f", errs[0].Message())
	require.Equal(t, "(fn (x: number) -> number) & (fn (x: string) -> string)", values["f"],
		"the two functions still overload; only the val is rejected")
}

// The duplicate is rejected even when the val and the fn live in SEPARATE files of the
// same lib namespace: the dep graph keys by qualified name across all files, so a fn
// and a val sharing a name collide under one value binding regardless of file. The
// first-declared decl wins and the other is a duplicate; here a.esc's fn is primary and
// b.esc's val is reported.
func TestInferOverloadMixedWithValCrossFileIsDuplicate(t *testing.T) {
	values, _, errs := inferSources(t, map[string]string{
		"a.esc": `fn f(x: number) -> number { x }`,
		"b.esc": `val f = 5`,
	})
	require.Len(t, errs, 1)
	require.Equal(t, "Duplicate declaration: f", errs[0].Message())
	require.Equal(t, "fn (x: number) -> number", values["f"],
		"the cross-file val is rejected; the fn binding survives")
}
