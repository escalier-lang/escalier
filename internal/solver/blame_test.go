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

// requireBlame asserts the sole error's message, the source text its primary span
// covers, and the source text each related span covers (in order). The golden
// span fixtures (§3.10) use it to pin exact blame against real-parser spans.
func requireBlame(t *testing.T, src string, errs []SolverError, msg, primary string, related ...string) {
	t.Helper()
	require.Len(t, errs, 1)
	require.Equal(t, msg, errs[0].Message())
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
	requireBlame(t, src, errs, `cannot constrain "hi" <: number`, `"hi"`, "number")
}

// A call-arg mismatch blames the offending argument, with the callee's param
// annotation as the related source.
func TestBlameCallArgument(t *testing.T) {
	src := "fn f(x: number) -> number { x }\nval r = f(\"hi\")"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, `cannot constrain "hi" <: number`, `"hi"`, "number")
}

// A call-arity mismatch points at the whole call. (The callee `f` is named, so its
// binding is coalesced to a fresh FuncType pointer that has no Prov entry — the
// "function defined here" related span resolves only for inline callees in M2.5
// and is made precise for named callees by M3's FromInstantiation.)
func TestBlameCallArity(t *testing.T) {
	src := "fn f(x: number) -> number { x }\nval r = f(1, 2)"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs,
		"cannot constrain function of arity 1 <: function of arity 2", "f(1, 2)")
}

// A missing-property read blames the member's prop (.foo), not the receiver. (The
// receiver `o` is named/coalesced, so its related span resolves only for an inline
// receiver in M2.5.)
func TestBlameMissingProperty(t *testing.T) {
	src := "val o = {a: 5}\nval x = o.b"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "object is missing property: b", "b")
}

// An identifier-flow mismatch blames the USE, not the definition: x's type traces
// to its definition, which is not inside the use's constraint node, so the
// containment guard falls back to the use (§3.8). The annotation is the related
// expected-source.
func TestBlameIdentifierUseNotDefinition(t *testing.T) {
	src := "val x = \"hi\"\nval a: number = x"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, `cannot constrain "hi" <: number`, "x", "number")
}

// An inline receiver DOES resolve its related span: {a: 5} is used directly (not
// coalesced through a binding), so its Prov entry survives and surfaces as the
// receiver-related span.
func TestBlameMissingPropertyInlineReceiverRelated(t *testing.T) {
	src := `val x = {a: 5}.b`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "object is missing property: b", "b", "{a: 5}")
}

// --- Bridge-error span fixtures (self-blaming, no Prov) ---

// An unknown identifier blames the ident itself.
func TestBlameUnknownIdentifier(t *testing.T) {
	src := `val y = missing`
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "Unknown identifier: missing", "missing")
}

// A duplicate top-level `val` blames the second decl and exposes the first as a
// related "previously declared here" span.
func TestBlameDuplicateDeclarationRelatesPrevious(t *testing.T) {
	src := "val x = 5\nval x = \"hi\""
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "Duplicate declaration: x", `val x = "hi"`, "val x = 5")
}

// An unsupported feature (optional chaining) blames the member, not a separate
// node, and reports the feature name.
func TestBlameUnsupportedFeatureOptionalChain(t *testing.T) {
	src := "val o = {a: 5}\nval x = o?.a"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, "Unsupported in M2: OptionalChain", "o?.a")
}

// --- PR-2 unit tests: Span()/Related() over hand-built errors + a seeded Prov ---

func tspan(sl, sc, el, ec int) ast.Span {
	return ast.NewSpan(ast.Location{Line: sl, Column: sc}, ast.Location{Line: el, Column: ec}, 0)
}

// CannotConstrainError blames the LHS operand's node when it lies WITHIN the
// constraint site (the f("hi") shape), and the related node follows the RHS.
func TestCannotConstrainBlameOperandWithinSite(t *testing.T) {
	lit := &soltype.LitType{Lit: &soltype.StrLit{Value: "hi"}}
	litNode := ast.NewLitExpr(ast.NewString("hi", tspan(1, 11, 1, 15)))
	prim := &soltype.PrimType{Prim: soltype.NumPrim}
	annNode := ast.NewNumberTypeAnn(tspan(1, 20, 1, 26))
	site := ast.NewIdent("call", tspan(1, 1, 1, 30)) // a node whose span contains litNode

	prov := Prov{lit: FromAST{Node: litNode, Kind: LiteralInference}, prim: FromAST{Node: annNode, Kind: AnnotationType}}
	e := &CannotConstrainError{LHS: lit, RHS: prim, prov: prov, site: site}

	require.Equal(t, litNode.Span(), e.Span(), "operand inside the site wins")
	require.Equal(t, []ast.Span{annNode.Span()}, e.Related(), "RHS is the related expected-source")
}

// When the LHS operand resolves OUTSIDE the constraint site (an ident's
// definition), the containment guard falls back to the site — the use.
func TestCannotConstrainBlameFallsBackToSiteWhenOperandOutside(t *testing.T) {
	lit := &soltype.LitType{Lit: &soltype.StrLit{Value: "hi"}}
	defNode := ast.NewLitExpr(ast.NewString("hi", tspan(1, 9, 1, 13))) // on line 1
	use := ast.NewIdent("x", tspan(2, 17, 2, 18))                      // the use, on line 2 — does NOT contain defNode

	prov := Prov{lit: FromAST{Node: defNode, Kind: LiteralInference}}
	e := &CannotConstrainError{LHS: lit, RHS: &soltype.PrimType{Prim: soltype.NumPrim}, prov: prov, site: use}

	require.Equal(t, use.Span(), e.Span(), "operand outside the site → blame the use")
}

// An unrecorded operand (no Prov entry) falls back to the site.
func TestCannotConstrainBlameFallsBackForUnrecordedOperand(t *testing.T) {
	site := ast.NewIdent("use", tspan(3, 1, 3, 10))
	e := &CannotConstrainError{
		LHS:  &soltype.Void{}, // never recorded
		RHS:  &soltype.PrimType{Prim: soltype.NumPrim},
		prov: Prov{},
		site: site,
	}
	require.Equal(t, site.Span(), e.Span())
	require.Empty(t, e.Related())
}

// FuncArityMismatchError blames the RHS call-shape (the call) and relates the LHS
// callee.
func TestFuncArityBlameCallAndRelatesCallee(t *testing.T) {
	callee := &soltype.FuncType{Ret: &soltype.Void{}}
	calleeNode := ast.NewIdent("f", tspan(1, 1, 1, 2))
	shape := &soltype.FuncType{Ret: &soltype.Void{}}
	callNode := ast.NewIdent("call", tspan(1, 1, 1, 8))

	prov := Prov{callee: FromAST{Node: calleeNode, Kind: FuncInference}, shape: FromAST{Node: callNode, Kind: CallShape}}
	e := &FuncArityMismatchError{LHS: callee, RHS: shape, prov: prov}

	require.Equal(t, callNode.Span(), e.Span())
	require.Equal(t, []ast.Span{calleeNode.Span()}, e.Related())
}

// MissingPropertyError blames the field's inner var (the .prop ident) and relates
// the receiver.
func TestMissingPropertyBlamePropAndRelatesReceiver(t *testing.T) {
	fieldVar := &soltype.TypeVarType{ID: 1}
	recv := &soltype.RecordType{Fields: []*soltype.RecordField{{Name: "a", Type: &soltype.PrimType{Prim: soltype.NumPrim}}}}
	req := &soltype.RecordType{Fields: []*soltype.RecordField{{Name: "b", Type: fieldVar}}}
	propNode := ast.NewIdentifier("b", tspan(2, 9, 2, 10))
	recvNode := ast.NewIdent("o", tspan(1, 9, 1, 15))

	prov := Prov{fieldVar: FromAST{Node: propNode, Kind: MemberAccess}, recv: FromAST{Node: recvNode, Kind: ObjectField}}
	e := &MissingPropertyError{LHS: recv, RHS: req, Name: "b", prov: prov}

	require.Equal(t, propNode.Span(), e.Span())
	require.Equal(t, []ast.Span{recvNode.Span()}, e.Related())
}

// Site fallback (M2.5 finding #2): when no operand resolves through Prov, the
// three site-carrying constraint kinds blame the constraint node rather than
// returning the zero span (which a consumer would mis-render as 0:0). This degrade
// path is unreachable in M2.5 but becomes live with M4 record/tuple sinks.
func TestConstraintKindsFallBackToSiteWhenUnrecorded(t *testing.T) {
	site := ast.NewIdent("site", tspan(7, 3, 7, 12))

	t.Run("FuncArity", func(t *testing.T) {
		e := &FuncArityMismatchError{
			LHS: &soltype.FuncType{}, RHS: &soltype.FuncType{}, prov: Prov{}, site: site,
		}
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("TupleLength", func(t *testing.T) {
		e := &TupleLengthMismatchError{
			LHS: &soltype.TupleType{}, RHS: &soltype.TupleType{}, prov: Prov{}, site: site,
		}
		require.Equal(t, site.Span(), e.Span())
	})
	t.Run("MissingProperty", func(t *testing.T) {
		e := &MissingPropertyError{
			LHS:  &soltype.RecordType{},
			RHS:  &soltype.RecordType{Fields: []*soltype.RecordField{{Name: "b", Type: &soltype.TypeVarType{ID: 9}}}},
			Name: "b", prov: Prov{}, site: site,
		}
		require.Equal(t, site.Span(), e.Span())
	})
}

// --- M2.5 finding #1: unsupported-annotation recovery (no spurious `<: never`) ---

// An unsupported val annotation reports ITS OWN error once and the binding
// recovers to the initializer's inferred type — no cascade `cannot constrain e <:
// never`, no `never`-poisoned binding.
func TestUnsupportedValAnnotationRecovers(t *testing.T) {
	values, _, errs := inferSource(t, `val x: Foo = 5`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TypeRefTypeAnn", errs[0].Message())
	require.Equal(t, map[string]string{"x": "5"}, values)
}

// An unsupported function-return annotation likewise recovers: one error, and the
// function keeps its inferred body return type instead of `never`.
func TestUnsupportedReturnAnnotationRecovers(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() -> Foo { 5 }`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TypeRefTypeAnn", errs[0].Message())
	require.Equal(t, map[string]string{"f": "fn () -> 5"}, values)
}

// An unsupported param annotation recovers the param to a fresh var (rendering as
// `unknown` in contravariant position), not `never`.
func TestUnsupportedParamAnnotationRecovers(t *testing.T) {
	values, _, errs := inferSource(t, `fn g(x: Foo) -> number { 5 }`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TypeRefTypeAnn", errs[0].Message())
	require.Equal(t, map[string]string{"g": "fn (x: unknown) -> number"}, values)
}
