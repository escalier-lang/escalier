package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// spanText returns the source substring covered by s. Columns are 1-indexed and
// the end is exclusive (the lexer's convention), so a token at columns [c, c+n)
// renders n characters. M2.5's error spans are single-line; a multi-line span is
// not expected here and yields "".
func spanText(src string, s ast.Span) string {
	lines := strings.Split(src, "\n")
	if s.Start.Line < 1 || s.Start.Line > len(lines) || s.Start.Line != s.End.Line {
		return ""
	}
	line := lines[s.Start.Line-1]
	if s.Start.Column < 1 || s.End.Column-1 > len(line) || s.Start.Column > s.End.Column {
		return ""
	}
	return line[s.Start.Column-1 : s.End.Column-1]
}

// requireBlame asserts the sole error's span-prefixed message ("line:col-line:col:
// message"), the source text its primary span covers, and the source text each
// related span covers (in order). The golden span fixtures (§3.10) use it to pin
// exact blame against real-parser spans.
func requireBlame(t *testing.T, src string, errs []SolverError, msg, primary string, related ...string) {
	t.Helper()
	require.Len(t, errs, 1)
	require.Equal(t, msg, msgWithSpan(errs[0]))
	require.Equal(t, primary, spanText(src, errs[0].Span()), "primary blame")
	got := []string{}
	for _, s := range errs[0].Related() {
		got = append(got, spanText(src, s))
	}
	want := related
	if want == nil {
		want = []string{}
	}
	require.Equal(t, want, got, "related blame")
}

// --- Golden span fixtures (§3.10): exact blame against real-parser spans ---

// A val-annotation mismatch blames the offending literal, with the annotation as
// the related expected-source — the milestone's headline fixture (§3.7).
func TestBlameValAnnotationLiteral(t *testing.T) {
	src := `val x: number = "hi"`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, `1:17-1:21: cannot constrain "hi" <: number`, `"hi"`, "number")
}

// A call-arg mismatch blames the offending argument, with the callee's param
// annotation as the related source.
func TestBlameCallArgument(t *testing.T) {
	src := `fn f(x: number) -> number { return x }
val r = f("hi")`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, `2:11-2:15: cannot constrain "hi" <: number`, `"hi"`, "number")
}

// A too-many-args direct call is the PR4 extra-arg lint (TooManyArgsError), a
// BRIDGE error that self-blames the whole call and relates the callee expression.
// (It supersedes the FuncArityMismatch this fixture asserted before PR4 — too-many
// is now the lint's uniform message, while FuncArityMismatch keeps the too-few /
// callback-arity failures. The related span is the callee `f` here, derived from
// the AST, not the callee's definition span resolved through Prov.)
func TestBlameCallTooManyArgs(t *testing.T) {
	src := `fn f(x: number) -> number { return x }
val r = f(1, 2)`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"2:9-2:16: Too many arguments: expected at most 1, but got 2", "f(1, 2)", "f")
}

// A too-few-args direct call is the PR4 too-few lint (NotEnoughArgsError), a BRIDGE
// error symmetric to TooManyArgsError: it self-blames the whole call and relates the
// callee expression (`f` here, from the AST — not the callee's definition resolved
// through Prov). `f` requires two params; the call supplies one.
func TestBlameCallTooFewArgs(t *testing.T) {
	src := `fn f(x: number, y: number) -> number { return x }
val r = f(1)`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"2:9-2:13: Not enough arguments: expected at least 2, but got 1", "f(1)", "f")
}

// A missing-property read blames the member's prop (.foo), not the receiver, with
// the receiver's definition as the related span. Like TestBlameCallArity, M2.5
// resolved that related span only for an inline receiver (a named receiver was
// coalesced to a fresh ObjectType with no Prov entry); M3's generalize-and-share
// instantiation keeps a VAR-FREE receiver's original object — here `{a: 5}` is
// monomorphic, so it is shared unchanged through instantiation (recorded
// ObjectField against the literal) — so the named receiver's related span now
// resolves. A receiver whose value flowed through an instantiation that REBUILT it
// (e.g. the result of a polymorphic call) does not keep the entry; see
// TestBlameMissingPropertyPolymorphicReceiverLosesRelated.
func TestBlameMissingProperty(t *testing.T) {
	src := `val o = {a: 5}
val x = o.b`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "2:11-2:12: object is missing property: b", "b", "{a: 5}")
}

// The bound of M3's related-span improvement: a receiver derived from a
// POLYMORPHIC call loses its related "defined here" span. Instantiating `mk`
// freshens its body, rebuilding the record into a fresh pointer with no Prov
// entry, so the missing-property error carries no related receiver span — unlike
// the direct var-free literal in TestBlameMissingProperty. Documents that the
// improvement holds for var-free callees/receivers, not values that flowed through
// instantiation.
func TestBlameMissingPropertyPolymorphicReceiverLosesRelated(t *testing.T) {
	src := `val mk = fn (v) { return {a: v} }
val o = mk(5)
val x = o.b`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "3:11-3:12: object is missing property: b", "b")
}

// An identifier-flow mismatch blames the USE, not the definition: x's type traces
// to its definition, which is not inside the use's constraint node, so the
// containment guard falls back to the use (§3.8). The annotation is the related
// expected-source.
func TestBlameIdentifierUseNotDefinition(t *testing.T) {
	src := `val x = "hi"
val a: number = x`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, `2:17-2:18: cannot constrain "hi" <: number`, "x", "number")
}

// An inline receiver DOES resolve its related span: {a: 5} is used directly (not
// coalesced through a binding), so its Prov entry survives and surfaces as the
// receiver-related span.
func TestBlameMissingPropertyInlineReceiverRelated(t *testing.T) {
	src := `val x = {a: 5}.b`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "1:16-1:17: object is missing property: b", "b", "{a: 5}")
}

// --- Bridge-error span fixtures (self-blaming, no Prov) ---

// An unknown identifier blames the ident itself.
func TestBlameUnknownIdentifier(t *testing.T) {
	src := `val y = missing`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "1:9-1:16: Unknown identifier: missing", "missing")
}

// A duplicate top-level `val` blames the second decl and exposes the first as a
// related "previously declared here" span.
func TestBlameDuplicateDeclarationRelatesPrevious(t *testing.T) {
	src := `val x = 5
val x = "hi"`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "2:1-2:13: Duplicate declaration: x", `val x = "hi"`, "val x = 5")
}

// An unsupported feature (optional chaining) blames the member, not a separate
// node, and reports the feature name.
func TestBlameUnsupportedFeatureOptionalChain(t *testing.T) {
	src := `val o = {a: 5}
val x = o?.a`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "2:9-2:13: Unsupported: OptionalChain", "o?.a")
}

// --- Site-fallback fixtures ---

// A void result has no Prov entry — it is minted without an AST node — so a
// constraint with void as its subject falls back to the constraint site, the use.
// `fn f() {}` returns void, and `val x: number = f()` blames the `f()` call with
// the annotation as the related expected-source. (Replaces the hand-built
// CannotConstrain "unrecorded operand → site" unit test; its operand-within-site
// and operand-outside-site branches are already covered by TestBlameCallArgument
// and TestBlameIdentifierUseNotDefinition above.)
func TestBlameVoidSubjectFallsBackToCallSite(t *testing.T) {
	src := `fn f() {}
val x: number = f()`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "2:17-2:20: cannot constrain void <: number", "f()", "number")
}

// The too-many-args lint on an immediately-invoked function blames the call and
// relates the inline callee expression. TooManyArgsError is a bridge error, so the
// related span is the callee node directly (Call.Callee.Span()), not a Prov
// resolution — for an inline FuncExpr that is the function expression itself. (The
// primary is the call expression; for a parenthesized callee the parser's span
// begins at the inner `fn`, so the leading `(` is not part of it.)
func TestBlameCallTooManyArgsInlineCalleeRelated(t *testing.T) {
	src := `val r = (fn (x: number) -> number { return x })(1, 2)`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"1:10-1:54: Too many arguments: expected at most 1, but got 2",
		`fn (x: number) -> number { return x })(1, 2)`,
		`fn (x: number) -> number { return x }`)
}

// tspan builds a single-SourceID span from 1-indexed line/column ints — only the
// hand-built degrade-path test below needs it.
func tspan(sl, sc, el, ec int) ast.Span {
	return ast.NewSpan(ast.Location{Line: sl, Column: sc}, ast.Location{Line: el, Column: ec}, 0)
}

// The three site-carrying constraint kinds degrade to the constraint site when
// NEITHER operand resolves through Prov, rather than returning the zero span
// (which a consumer would mis-render as 0:0). Unlike every fixture above, this path
// is UNREACHABLE from M2.5 source — FuncArity always records the call-shape,
// MissingProperty always records the field var, and tuple <: tuple cannot fire
// without a tuple sink — so it must be exercised with hand-built errors and a
// seeded Prov here; it becomes live with M4 record/tuple sinks.
func TestConstraintKindsFallBackToSiteWhenUnrecorded(t *testing.T) {
	site := ast.NewIdent("site", tspan(7, 3, 7, 12))

	t.Run("FuncArity", func(t *testing.T) {
		e := &FuncArityMismatchError{
			Sub:   &soltype.FuncType{Params: make([]*soltype.FuncParam, 2)},
			Super: &soltype.FuncType{Params: make([]*soltype.FuncParam, 1)},
			prov:  Prov{}, site: site,
		}
		require.Equal(t, "cannot constrain function of arity 2 <: function of arity 1", e.Message())
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("TupleLength", func(t *testing.T) {
		e := &TupleLengthMismatchError{
			Sub:   &soltype.TupleType{Elems: []soltype.Type{num(), num()}},
			Super: &soltype.TupleType{Elems: []soltype.Type{num()}},
			prov:  Prov{}, site: site,
		}
		require.Equal(t, "cannot constrain tuple of length 2 <: tuple of length 1", e.Message())
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("MissingProperty", func(t *testing.T) {
		e := &MissingPropertyError{
			Sub:   &soltype.ObjectType{},
			Super: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "b", Type: &soltype.TypeVarType{ID: 9}}}},
			Name:  "b", prov: Prov{}, site: site,
		}
		require.Equal(t, "object is missing property: b", e.Message())
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("InexactIntoExact", func(t *testing.T) {
		e := &InexactIntoExactError{
			Sub: &soltype.ObjectType{Inexact: true}, Super: &soltype.ObjectType{}, prov: Prov{}, site: site,
		}
		require.Equal(t, "cannot constrain inexact object <: exact object", e.Message())
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("ExtraProperty", func(t *testing.T) {
		e := &ExtraPropertyError{
			Sub:   &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "b", Type: &soltype.TypeVarType{ID: 9}}}},
			Super: &soltype.ObjectType{},
			Name:  "b", prov: Prov{}, site: site,
		}
		require.Equal(t, "object has extra property: b", e.Message())
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("OptionalProperty", func(t *testing.T) {
		e := &OptionalPropertyError{
			Sub:   &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "b", Type: &soltype.TypeVarType{ID: 9}, Optional: true}}},
			Super: &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "b", Type: &soltype.TypeVarType{ID: 9}}}},
			Name:  "b", prov: Prov{}, site: site,
		}
		require.Equal(t, site.Span(), e.Span())
	})
}

// checker.constrain stamps prov + the constraint node onto EVERY constraint-error
// kind it forwards, so each error's Span() resolves to a real source span instead
// of the zero span. The object errors (InexactIntoExactError, ExtraPropertyError,
// OptionalPropertyError) were added in M4 A1; this pins that their switch arms
// exist — a missing arm leaves prov/site nil and Span() degrades to 0:0. Exercised
// directly through c.constrain because an exact-object sink is not reachable from
// source until object annotations land (A3).
func TestConstrainStampsObjectExactnessErrors(t *testing.T) {
	node := ast.NewIdent("site", tspan(4, 2, 4, 6))

	t.Run("ExtraPropertyError", func(t *testing.T) {
		c := newChecker()
		// exact {x, y} <: exact {x}: y is an extra property on the source.
		c.constrain(node, exactObj(propElem("x", num()), propElem("y", num())), exactObj(propElem("x", num())))
		require.Len(t, c.errs, 1)
		require.IsType(t, &ExtraPropertyError{}, c.errs[0])
		require.Equal(t, "object has extra property: y", c.errs[0].Message())
		require.Equal(t, node.Span(), c.errs[0].Span())
	})

	t.Run("InexactIntoExactError", func(t *testing.T) {
		c := newChecker()
		// inexact {x, ...} <: exact {x}: an inexact source cannot fill an exact sink.
		c.constrain(node, inexactObj(propElem("x", num())), exactObj(propElem("x", num())))
		require.Len(t, c.errs, 1)
		require.IsType(t, &InexactIntoExactError{}, c.errs[0])
		require.Equal(t, "cannot constrain inexact object <: exact object", c.errs[0].Message())
		require.Equal(t, node.Span(), c.errs[0].Span())
	})

	t.Run("OptionalPropertyError", func(t *testing.T) {
		c := newChecker()
		// {x?: number} <: {x: number}: an optional source cannot fill a required property.
		c.constrain(node,
			exactObj(&soltype.PropertyElem{Name: "x", Type: num(), Optional: true}),
			exactObj(propElem("x", num())))
		require.Len(t, c.errs, 1)
		require.IsType(t, &OptionalPropertyError{}, c.errs[0])
		require.Equal(t, node.Span(), c.errs[0].Span())
	})
}

// --- M2.5 finding #1: unsupported-annotation recovery (no spurious `<: never`) ---

// An unsupported annotation reports ITS OWN error once and the binding recovers to
// the type it would otherwise infer — no cascade `cannot constrain e <: never`, no
// `never`-poisoned binding. Covers each annotation position:
//   - val: keeps the initializer's inferred type;
//   - function return: keeps the inferred body return type;
//   - param: recovers to a fresh var (rendering `unknown` in contravariant position).
func TestUnsupportedAnnotationRecovers(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{"val", `val x: Foo = 5`, map[string]string{"x": "5"}},
		{"return", `fn f() -> Foo { return 5 }`, map[string]string{"f": "fn () -> 5"}},
		{"param", `fn g(x: Foo) -> number { return 5 }`, map[string]string{"g": "fn (x: unknown) -> number"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, "Unsupported: TypeRefTypeAnn", errs[0].Message())
			require.Equal(t, tt.want, values)
		})
	}
}

// A VarDecl with a nil pattern (not produced by the parser, which synthesizes a
// placeholder, but possible in a hand-built AST) must blame the decl without
// panicking — honoring M2's "never a panic" guarantee now that Span() is lazy
// (it derefs the stored node on demand). Mirrors inferFunc's nil-param fallback.
func TestNilVarDeclPatternBlamesDeclWithoutPanic(t *testing.T) {
	c := newChecker()
	d := ast.NewVarDecl(ast.ValKind, nil, nil, numExpr(5), false, false, testSpan())
	require.NotPanics(t, func() {
		_, _, ok := c.inferDeclDef(NewScope(), 0, d, "")
		require.False(t, ok)
	})
	require.Len(t, c.errs, 1)
	require.Equal(t, "Unsupported: VarDecl", c.errs[0].Message())
	require.Equal(t, testSpan(), c.errs[0].Span()) // derefs the decl node, not a nil pattern
}
