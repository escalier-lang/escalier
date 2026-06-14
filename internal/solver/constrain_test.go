package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Messages adapts a slice of SolverError to a slice of their rendered messages
// so table-driven tests read naturally.
func Messages(errs []SolverError) []string {
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	return msgs
}

func num() *soltype.PrimType   { return &soltype.PrimType{Prim: soltype.NumPrim} }
func str() *soltype.PrimType   { return &soltype.PrimType{Prim: soltype.StrPrim} }
func boolT() *soltype.PrimType { return &soltype.PrimType{Prim: soltype.BoolPrim} }

func numLit(v float64) *soltype.LitType { return &soltype.LitType{Lit: &soltype.NumLit{Value: v}} }
func strLit(v string) *soltype.LitType  { return &soltype.LitType{Lit: &soltype.StrLit{Value: v}} }

func identParam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t}
}

func optParam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t, Optional: true}
}

// restParam builds a typed rest param (`...name: t`), which must be the last param.
func restParam(name string, t soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: t, Rest: true}
}

// exactFn / inexactFn build function types so the accept-set tests read at a glance
// which arm they exercise. Exact is the zero value of Inexact, so exactFn sets no
// flag; inexactFn sets Inexact.
func exactFn(ret soltype.Type, params ...*soltype.FuncParam) *soltype.FuncType {
	return &soltype.FuncType{Params: params, Ret: ret}
}

func inexactFn(ret soltype.Type, params ...*soltype.FuncParam) *soltype.FuncType {
	return &soltype.FuncType{Params: params, Ret: ret, Inexact: true}
}

func TestConstrainPrim(t *testing.T) {
	tests := []struct {
		name  string
		sub   soltype.Type
		super soltype.Type
		want  []string
	}{
		{"number <: number", num(), num(), nil},
		{"string <: string", str(), str(), nil},
		{"number <: string", num(), str(), []string{"cannot constrain number <: string"}},
		{"boolean <: number", boolT(), num(), []string{"cannot constrain boolean <: number"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.sub, tt.super)
			require.Equal(t, tt.want, Messages(errs))
		})
	}
}

func TestConstrainPrimMismatchStructure(t *testing.T) {
	c := &Context{}
	sub, super := num(), str()
	errs := c.Constrain(sub, super)
	require.Len(t, errs, 1)
	cc, ok := errs[0].(*CannotConstrainError)
	require.True(t, ok)
	require.Same(t, soltype.Type(sub), cc.Sub)
	require.Same(t, soltype.Type(super), cc.Super)
}

func TestConstrainLiteral(t *testing.T) {
	tests := []struct {
		name  string
		sub   soltype.Type
		super soltype.Type
		want  []string
	}{
		{"5 <: number", numLit(5), num(), nil},
		{`"hello" <: string`, strLit("hello"), str(), nil},
		{"5 <: 5", numLit(5), numLit(5), nil},
		{"5 <: string", numLit(5), str(), []string{"cannot constrain 5 <: string"}},
		{"5 <: 6", numLit(5), numLit(6), []string{"cannot constrain 5 <: 6"}},
		// Regression: float64 literals must render at 64-bit precision, not
		// float32 (which would garble these to 0.12345679 / 16777216).
		{"high-precision decimal", numLit(0.123456789), str(),
			[]string{"cannot constrain 0.123456789 <: string"}},
		{"large integer past float32 mantissa", numLit(16777217), str(),
			[]string{"cannot constrain 16777217 <: string"}},
		{`"a" <: "b"`, strLit("a"), strLit("b"), []string{`cannot constrain "a" <: "b"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.sub, tt.super)
			require.Equal(t, tt.want, Messages(errs))
		})
	}
}

func TestConstrainFunctionVariance(t *testing.T) {
	// (number) -> number  <:  (number) -> number
	t.Run("same shape", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", num()))
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (number) -> number <: (5) -> number requires
	// 5 <: number on the param (super param <: sub param), which holds.
	t.Run("contravariant params (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", numLit(5)))
		require.Empty(t, c.Constrain(f1, f2))
	})

	// Contravariant params: (5) -> number <: (number) -> number requires
	// number <: 5, which fails.
	t.Run("contravariant params (fail)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(num(), identParam("x", numLit(5)))
		f2 := exactFn(num(), identParam("x", num()))
		require.Equal(t, []string{"cannot constrain number <: 5"}, Messages(c.Constrain(f1, f2)))
	})

	// Covariant return: (number) -> 5 <: (number) -> number requires 5 <: number.
	t.Run("covariant return (ok)", func(t *testing.T) {
		c := &Context{}
		f1 := exactFn(numLit(5), identParam("x", num()))
		f2 := exactFn(num(), identParam("x", num()))
		require.Empty(t, c.Constrain(f1, f2))
	})
}

// TestConstrainFunctionAcceptSet exercises the PR4 accept-set rule (#677 §4.2.1):
// G <: F iff accept(G) ⊇ accept(F), with params contravariant and return covariant.
// Read F (the super) as the callback slot.
func TestConstrainFunctionAcceptSet(t *testing.T) {
	// Two EXACT functions relate by the old same-arity rule: accept is [n, n] on both
	// sides, so ⊇ forces equal arity.
	t.Run("exact same arity ok", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			exactFn(num(), identParam("x", num())),
			exactFn(num(), identParam("x", num())),
		))
	})

	// An exact supplier cannot fill a WIDER exact slot: accept [1,1] does not contain
	// the slot's count 2 (upper-bound failure, hiG < hiF).
	t.Run("exact fn(x) rejected by fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		g := exactFn(num(), identParam("x", num()))
		f := exactFn(num(), identParam("x", num()), identParam("y", num()))
		errs := c.Constrain(g, f)
		require.Equal(t, []string{"cannot constrain function of arity 1 <: function of arity 2"}, Messages(errs))
		fa, ok := errs[0].(*FuncArityMismatchError)
		require.True(t, ok)
		require.Same(t, g, fa.Sub)
		require.Same(t, f, fa.Super)
	})

	// The #677 callback matrix for an exact fn(x, y) slot: fn(x, y), fn(x, ...), and
	// fn(...) are all accepted; exact fn(x) (too narrow upper bound) and a 3-param
	// function (demands more than the slot supplies) are rejected.
	slot := func() *soltype.FuncType { return exactFn(num(), identParam("x", num()), identParam("y", num())) }
	t.Run("exact fn(x, y) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(exactFn(num(), identParam("x", num()), identParam("y", num())), slot()))
	})
	t.Run("inexact fn(x, ...) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		// accept(G) = [1, ∞) ⊇ accept(slot) = [2, 2]; the slot's arg 2 is tolerated,
		// the single shared param checked contravariantly.
		require.Empty(t, c.Constrain(inexactFn(num(), identParam("x", num())), slot()))
	})
	t.Run("inexact fn(...) fills fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		// accept(G) = [0, ∞) ⊇ [2, 2]: a zero-param inexact function fills any exact
		// slot whose required count it meets. This is the case exactness exists to permit.
		require.Empty(t, c.Constrain(inexactFn(num()), slot()))
	})
	t.Run("exact fn(x) rejected by fn(x, y) slot (matrix)", func(t *testing.T) {
		c := &Context{}
		require.Equal(t,
			[]string{"cannot constrain function of arity 1 <: function of arity 2"},
			Messages(c.Constrain(exactFn(num(), identParam("x", num())), slot())))
	})
	t.Run("3-param fn rejected by fn(x, y) slot", func(t *testing.T) {
		c := &Context{}
		g := exactFn(num(), identParam("x", num()), identParam("y", num()), identParam("z", num()))
		// accept(G) = [3, 3]; the slot supplies 2, below G's required 3 → lower-bound
		// failure (loG > loF).
		require.Equal(t,
			[]string{"cannot constrain function of arity 3 <: function of arity 2"},
			Messages(c.Constrain(g, slot())))
	})

	// Exact </: inexact: an exact function's finite upper bound cannot cover an
	// inexact slot's ∞ (the asymmetry §4.2.1.1 explains).
	t.Run("exact fn(x) rejected by inexact fn(x, ...) slot", func(t *testing.T) {
		c := &Context{}
		require.Equal(t,
			[]string{"cannot constrain function of arity 1 <: function of arity 1"},
			Messages(c.Constrain(exactFn(num(), identParam("x", num())), inexactFn(num(), identParam("x", num())))))
	})

	// Inexact <: inexact: the classic "fewer params is okay" rule — both upper bounds
	// are ∞, the supplier just needs to handle the consumer's required params.
	t.Run("inexact fn(x, ...) fills inexact fn(x, y, ...) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			inexactFn(num(), identParam("x", num())),
			inexactFn(num(), identParam("x", num()), identParam("y", num())),
		))
	})

	// Optional params lower `required` without changing arity: exact fn(a, b?)
	// (accept [1, 2]) fills the narrower exact fn(a) slot (accept [1, 1]) — the slot
	// never supplies the optional argument.
	t.Run("exact fn(a, b?) fills fn(a) slot", func(t *testing.T) {
		c := &Context{}
		require.Empty(t, c.Constrain(
			exactFn(num(), identParam("a", num()), optParam("b", num())),
			exactFn(num(), identParam("a", num())),
		))
	})
}

// TestAcceptSetRestParam pins the arity arithmetic for a typed rest param: it lifts
// the upper bound to ∞ and never counts toward the required floor (#677 §4.2.3).
func TestAcceptSetRestParam(t *testing.T) {
	t.Run("fn(a, b, ...rest): required 2, upper unbounded", func(t *testing.T) {
		f := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("a", num()), identParam("b", num()), restParam("rest", num())}, Ret: num()}
		require.Equal(t, 2, requiredCount(f))
		lo, hi := acceptSet(f)
		require.Equal(t, 2, lo)
		require.Equal(t, unboundedArity, hi)
	})

	t.Run("fn(...rest): required 0, upper unbounded", func(t *testing.T) {
		f := &soltype.FuncType{Params: []*soltype.FuncParam{restParam("rest", num())}, Ret: num()}
		require.Equal(t, 0, requiredCount(f))
		lo, hi := acceptSet(f)
		require.Equal(t, 0, lo)
		require.Equal(t, unboundedArity, hi)
	})

	t.Run("fn(a, b?, ...rest): required 1 (rest and trailing optional both drop out)", func(t *testing.T) {
		f := &soltype.FuncType{Params: []*soltype.FuncParam{identParam("a", num()), optParam("b", num()), restParam("rest", num())}, Ret: num()}
		require.Equal(t, 1, requiredCount(f))
	})
}

// TestConstrainFunctionRestParam exercises the accept-set subtyping rule for a typed
// rest param, whose ∞ upper bound mirrors the inexact `...` marker.
func TestConstrainFunctionRestParam(t *testing.T) {
	restFn := func(ret soltype.Type, params ...*soltype.FuncParam) *soltype.FuncType {
		return &soltype.FuncType{Params: params, Ret: ret}
	}

	// fn(a, ...rest) (accept [1, ∞)) fills a WIDER exact slot fn(a, b, c) (accept
	// [3, 3]): the rest absorbs the slot's extra arguments — the case rest exists for.
	t.Run("rest fn fills a wider exact slot", func(t *testing.T) {
		c := &Context{}
		g := restFn(num(), identParam("a", num()), restParam("rest", num()))
		f := exactFn(num(), identParam("a", num()), identParam("b", num()), identParam("c", num()))
		require.Empty(t, c.Constrain(g, f))
	})

	// A rest fn and an inexact fn have the same ∞ upper bound, so a rest fn fills an
	// inexact slot of matching required arity.
	t.Run("rest fn fills an inexact slot", func(t *testing.T) {
		c := &Context{}
		g := restFn(num(), identParam("a", num()), restParam("rest", num()))
		f := inexactFn(num(), identParam("a", num()))
		require.Empty(t, c.Constrain(g, f))
	})

	// An exact fn(a, b) (accept [2, 2]) is REJECTED by a `fn(...rest)` slot (accept
	// [0, ∞)): the slot may invoke with zero args, but g demands two — a lower-bound
	// failure (loG=2 > loF=0), exactness aside.
	t.Run("exact fn rejected by a zero-required rest slot", func(t *testing.T) {
		c := &Context{}
		g := exactFn(num(), identParam("a", num()), identParam("b", num()))
		f := restFn(num(), restParam("rest", num()))
		require.Equal(t,
			[]string{"cannot constrain function of arity 2 <: function of arity 1"},
			Messages(c.Constrain(g, f)))
	})
}

func TestConstrainTuple(t *testing.T) {
	// [number, string] <: [number, string]
	t.Run("same length covariant", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Empty(t, c.Constrain(t1, t2))
	})

	// [5, "a"] <: [number, string] (element-wise covariant, literals widen)
	t.Run("covariant elements", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{numLit(5), strLit("a")}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Empty(t, c.Constrain(t1, t2))
	})

	t.Run("element mismatch", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), num()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		require.Equal(t, []string{"cannot constrain number <: string"}, Messages(c.Constrain(t1, t2)))
	})

	t.Run("length mismatch", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num()}}
		errs := c.Constrain(t1, t2)
		require.Equal(t, []string{"cannot constrain tuple of length 2 <: tuple of length 1"}, Messages(errs))
		tl, ok := errs[0].(*TupleLengthMismatchError)
		require.True(t, ok)
		require.Same(t, t1, tl.Sub)
		require.Same(t, t2, tl.Super)
	})

	// [number, string, boolean] <: [number, ...]: a longer tuple satisfies an
	// inexact super, matching the shared prefix element-wise (the A2 width arm).
	t.Run("longer fills inexact (width)", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), str(), boolT()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num()}, Inexact: true}
		require.Empty(t, c.Constrain(t1, t2))
	})

	// [5] <: [number, ...]: same length against an inexact super still checks; the
	// prefix is covariant (5 <: number).
	t.Run("equal length fills inexact covariant", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{numLit(5)}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num()}, Inexact: true}
		require.Empty(t, c.Constrain(t1, t2))
	})

	// [number] <: [number, string, ...]: too SHORT for the inexact super's declared
	// prefix is still a length mismatch — inexactness widens only on the long side.
	t.Run("shorter than inexact prefix rejects", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{num(), str()}, Inexact: true}
		errs := c.Constrain(t1, t2)
		require.Equal(t, []string{"cannot constrain tuple of length 1 <: tuple of length 2"}, Messages(errs))
		require.IsType(t, &TupleLengthMismatchError{}, errs[0])
	})

	// The shared prefix stays covariant against an inexact super: a prefix mismatch
	// surfaces the inner failure rather than being masked by width tolerance.
	t.Run("inexact prefix mismatch surfaces", func(t *testing.T) {
		c := &Context{}
		t1 := &soltype.TupleType{Elems: []soltype.Type{num(), num()}}
		t2 := &soltype.TupleType{Elems: []soltype.Type{str()}, Inexact: true}
		require.Equal(t, []string{"cannot constrain number <: string"}, Messages(c.Constrain(t1, t2)))
	})
}

// propElem builds a PropertyElem so the object accept-set tests read at a glance.
func propElem(name string, t soltype.Type) *soltype.PropertyElem {
	return &soltype.PropertyElem{Name: name, Type: t}
}

// mutRef builds an owned-mutable borrow for the RefType constrain tests (C2). Lt is
// always nil in C2 — the lifetime sort lands in D1 — so the owned-mutable wrapper is
// the only meaningful borrow constructible here. A real immutable borrow needs a
// lifetime (`Mut: false, Lt: 'a`), so its helper arrives in D2; the bare <: RefType
// arm mints the degenerate `Mut: false, Lt: nil` view internally with a struct
// literal, not through a helper.
func mutRef(inner soltype.RefInner) *soltype.RefType {
	return &soltype.RefType{Mut: true, Inner: inner}
}

// TestConstrainDescribesRefOperand pins review finding 2: describe must NAME a
// RefType operand in a constraint failure, not render it as `?`. A non-borrowable
// source (a primitive) against a mut-borrow target is not wrappable by the
// bare <: RefType arm, so it falls through to CannotConstrainError carrying the
// borrow as its Super — exactly the path describe was missing an arm for.
func TestConstrainDescribesRefOperand(t *testing.T) {
	c := &Context{}
	errs := c.Constrain(num(), mutRef(exactObj(propElem("x", num()))))
	require.Equal(t, []string{"cannot constrain number <: mut object"}, Messages(errs))
}

// exactObj / inexactObj build object types so the tests show which arm they
// exercise. Exact is the zero value of Inexact, so exactObj sets no flag.
func exactObj(elems ...soltype.ObjTypeElem) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: elems}
}

func inexactObj(elems ...soltype.ObjTypeElem) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: elems, Inexact: true}
}

// TestConstrainObject exercises the one-way object exactness rule (A1): width
// tolerance is inexactness on the super, and an exact super fixes its member set.
//
// want is the expected rendered messages (nil for a clean constraint); check, when
// set, runs extra structural assertions (the specific error type, the operand
// pointers) that a message comparison can't express.
func TestConstrainObject(t *testing.T) {
	optProp := func(name string, ty soltype.Type) *soltype.PropertyElem {
		return &soltype.PropertyElem{Name: name, Type: ty, Optional: true}
	}
	tests := []struct {
		name       string
		sub, super soltype.Type
		want       []string
		check      func(t *testing.T, sub, super soltype.Type, errs []SolverError)
	}{
		{
			// {x: 5} <: {x: number}
			// Depth is covariant: checks 5 <: number on the shared property. Same
			// member set on both sides, so the exact gate passes.
			name:  "exact same member set, covariant depth",
			sub:   exactObj(propElem("x", numLit(5))),
			super: exactObj(propElem("x", num())),
		},
		{
			// {x: number, y: number} <: {x: number, ...}
			// Width is the inexact-target case: the sub drops the extra y because the
			// super only requires "has at least x".
			name:  "exact fills inexact (width)",
			sub:   exactObj(propElem("x", num()), propElem("y", num())),
			super: inexactObj(propElem("x", num())),
		},
		{
			// {x: number, y: number, ...} <: {x: number, ...}
			// Width again with an inexact source: an inexact sub still satisfies an
			// inexact super, dropping the extra y.
			name:  "inexact fills inexact (width)",
			sub:   inexactObj(propElem("x", num()), propElem("y", num())),
			super: inexactObj(propElem("x", num())),
		},
		{
			// {x: number} <: {y: number, ...}
			// A required property the sub lacks is a MissingPropertyError, regardless
			// of the super's exactness.
			name:  "missing required property",
			sub:   exactObj(propElem("x", num())),
			super: inexactObj(propElem("y", num())),
			want:  []string{"object is missing property: y"},
			check: func(t *testing.T, sub, super soltype.Type, errs []SolverError) {
				mp, ok := errs[0].(*MissingPropertyError)
				require.True(t, ok)
				require.Same(t, sub, mp.Sub)
				require.Same(t, super, mp.Super)
			},
		},
		{
			// {x: number, y: number} <: {x: number}
			// An extra property on the sub is rejected against an exact super, one error
			// per extra property.
			name:  "extra property rejected by exact target",
			sub:   exactObj(propElem("x", num()), propElem("y", num())),
			super: exactObj(propElem("x", num())),
			want:  []string{"object has extra property: y"},
			check: func(t *testing.T, sub, super soltype.Type, errs []SolverError) {
				ep, ok := errs[0].(*ExtraPropertyError)
				require.True(t, ok)
				require.Same(t, sub, ep.Sub)
				require.Same(t, super, ep.Super)
				require.Equal(t, "y", ep.Name)
			},
		},
		{
			// {x: number, ...} <: {x: number}
			// An inexact source cannot satisfy an exact target — the open `...` tail
			// may carry properties the target does not declare.
			name:  "inexact rejected by exact target",
			sub:   inexactObj(propElem("x", num())),
			super: exactObj(propElem("x", num())),
			want:  []string{"cannot constrain inexact object <: exact object"},
			check: func(t *testing.T, sub, super soltype.Type, errs []SolverError) {
				ie, ok := errs[0].(*InexactIntoExactError)
				require.True(t, ok)
				require.Same(t, sub, ie.Sub)
				require.Same(t, super, ie.Super)
			},
		},
		{
			// {x: {a: number}} <: {x: {a: string}}
			// Depth is recursive: a shared property whose types don't relate surfaces
			// the inner failure, threaded through the path-scoped seen set.
			name:  "nested depth mismatch surfaces inner error",
			sub:   exactObj(propElem("x", exactObj(propElem("a", num())))),
			super: exactObj(propElem("x", exactObj(propElem("a", str())))),
			want:  []string{"cannot constrain number <: string"},
		},
		{
			// {x: {a: 5}} <: {x: {a: number}}
			// Recursive depth that checks: the inner 5 <: number holds.
			name:  "nested depth covariant ok",
			sub:   exactObj(propElem("x", exactObj(propElem("a", numLit(5))))),
			super: exactObj(propElem("x", exactObj(propElem("a", num())))),
		},
		{
			// {a: number, b: number, c: number} <: {a: number}
			// Every extra property on the source against an exact target fires its own
			// ExtraPropertyError, in source-property order.
			name:  "multiple extra properties each report",
			sub:   exactObj(propElem("a", num()), propElem("b", num()), propElem("c", num())),
			super: exactObj(propElem("a", num())),
			want:  []string{"object has extra property: b", "object has extra property: c"},
		},
		{
			// {a: number, c: number} <: {a: number, b: number}
			// A missing required property and an extra property both fire in one
			// constraint: the super-required loop reports the missing one first, then the
			// sub-extra loop reports the extra.
			name:  "missing and extra combined against exact target",
			sub:   exactObj(propElem("a", num()), propElem("c", num())),
			super: exactObj(propElem("a", num()), propElem("b", num())),
			want:  []string{"object is missing property: b", "object has extra property: c"},
		},
		// Boundary member sets against the exactness gate.
		{
			// {} <: {a: number}
			name:  "empty exact missing required",
			sub:   exactObj(),
			super: exactObj(propElem("a", num())),
			want:  []string{"object is missing property: a"},
		},
		{
			// {a: number} <: {}
			name:  "extra against empty exact",
			sub:   exactObj(propElem("a", num())),
			super: exactObj(),
			want:  []string{"object has extra property: a"},
		},
		{
			// {} <: {}
			name:  "empty exact <: empty exact ok",
			sub:   exactObj(),
			super: exactObj(),
		},
		{
			// {a: number} <: {...}
			name:  "exact fills empty inexact (width)",
			sub:   exactObj(propElem("a", num())),
			super: inexactObj(),
		},
		// PropertyElem.Optional is part of the object shape, so subtyping is
		// presence-aware: an optional target property may be absent on the source,
		// and a required source property fills an optional target slot, but an
		// optional source property cannot fill a required target slot.
		{
			// {} <: {x?: number}: an optional target property may be absent.
			name:  "absent source satisfies optional target property",
			sub:   exactObj(),
			super: exactObj(optProp("x", num())),
		},
		{
			// {x?: number} <: {x: number}: the source may omit x, so it cannot fill a
			// required slot.
			name:  "optional source rejected by required target",
			sub:   exactObj(optProp("x", num())),
			super: exactObj(propElem("x", num())),
			want:  []string{"object property is optional but required: x"},
			check: func(t *testing.T, sub, super soltype.Type, errs []SolverError) {
				op, ok := errs[0].(*OptionalPropertyError)
				require.True(t, ok)
				require.Same(t, sub, op.Sub)
				require.Same(t, super, op.Super)
				require.Equal(t, "x", op.Name)
			},
		},
		{
			// {x: number} <: {x?: number}: a required property fills an optional slot.
			name:  "required source fills optional target property",
			sub:   exactObj(propElem("x", num())),
			super: exactObj(optProp("x", num())),
		},
		{
			// {x?: number} <: {x?: number}: optional on both, types covariant.
			name:  "optional on both sides ok",
			sub:   exactObj(optProp("x", numLit(5))),
			super: exactObj(optProp("x", num())),
		},
		{
			// An optional source property still recurses covariantly into an optional
			// target property, so an incompatible type is caught.
			name:  "optional on both sides depth mismatch",
			sub:   exactObj(optProp("x", num())),
			super: exactObj(optProp("x", str())),
			want:  []string{"cannot constrain number <: string"},
		},
		{
			// {a: 5} <: number
			// A non-object super falls through the object arm to the generic
			// CannotConstrainError, whose message renders the object via describe.
			name:  "object <: non-object renders via describe",
			sub:   exactObj(propElem("a", numLit(5))),
			super: num(),
			want:  []string{"cannot constrain object <: number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.sub, tt.super)
			require.Equal(t, tt.want, Messages(errs))
			if tt.check != nil {
				tt.check(t, tt.sub, tt.super, errs)
			}
		})
	}
}

// TestConstrainRef exercises the single RefType <: RefType rule — THE GATE (C2).
// The headline property is mut-driven inner invariance: a mutable target takes both
// a covariant read view and a contravariant write view, so the inner is invariant.
func TestConstrainRef(t *testing.T) {
	tests := []struct {
		name       string
		sub, super soltype.Type
		want       []string
		check      func(t *testing.T, sub, super soltype.Type, errs []SolverError)
	}{
		{
			// mut {x} <: mut {x}: identical inner satisfies both the read and write
			// view, so invariance holds.
			name:  "mut <: mut, identical inner",
			sub:   mutRef(exactObj(propElem("x", num()))),
			super: mutRef(exactObj(propElem("x", num()))),
		},
		{
			// mut {x: 5} <: mut {x: number}: the read view 5 <: number holds, but the
			// write view requires number <: 5, which fails — invariance in one message.
			name:  "mut inner is invariant: literal depth rejected on the write view",
			sub:   mutRef(exactObj(propElem("x", numLit(5)))),
			super: mutRef(exactObj(propElem("x", num()))),
			want:  []string{"cannot constrain number <: 5"},
		},
		{
			// mut {x, y} <: mut {x, ...}: the read view width-succeeds (inexact super),
			// but the write view {x, ...} <: {x, y} is missing y and is inexact-into-
			// exact — the plan's headline invariance rejection.
			name:  "mut wider <: mut inexact rejects on the write view",
			sub:   mutRef(exactObj(propElem("x", num()), propElem("y", num()))),
			super: mutRef(inexactObj(propElem("x", num()))),
			want: []string{
				"object is missing property: y",
				"cannot constrain inexact object <: exact object",
			},
		},
		{
			// The same two object inners as bare (immutable) values width-succeed: an
			// immutable borrow is covariant, so the missing-on-write-view problem never
			// arises. This is the contrast the plan draws against the mut case above.
			name:  "immutable width succeeds where mut invariance rejects",
			sub:   exactObj(propElem("x", num()), propElem("y", num())),
			super: inexactObj(propElem("x", num())),
		},
		{
			// mut {x} <: {x}: mut-decay. A mutable source satisfies a bare (owned,
			// immutable) target; the borrow peels and the inner is checked covariantly.
			name:  "mut-decay: mut <: bare allowed",
			sub:   mutRef(exactObj(propElem("x", num()))),
			super: exactObj(propElem("x", num())),
		},
		{
			// {x} <: mut {x}: the reverse is rejected. The bare source is wrapped as an
			// immutable view, and an immutable source cannot fill a mutable slot.
			name:  "bare <: mut rejected (mutability)",
			sub:   exactObj(propElem("x", num())),
			super: mutRef(exactObj(propElem("x", num()))),
			want:  []string{"cannot constrain immutable object <: mutable object"},
			check: func(t *testing.T, sub, super soltype.Type, errs []SolverError) {
				mm, ok := errs[0].(*MutabilityMismatchError)
				require.True(t, ok)
				// The sub is the wrapped immutable view; its inner is the bare source.
				require.Same(t, sub, mm.Sub.Inner)
				require.Same(t, super, soltype.Type(mm.Super))
			},
		},
		{
			// mut {x} <: number: an owned borrow peels and the inner object is checked
			// against the non-object super, falling through to CannotConstrainError.
			name:  "mut <: non-borrow peels to the inner",
			sub:   mutRef(exactObj(propElem("x", num()))),
			super: num(),
			want:  []string{"cannot constrain object <: number"},
		},
		{
			// number <: mut {x}: a non-borrowable source cannot be wrapped, so it falls
			// through to CannotConstrainError naming the borrow (review finding 2).
			name:  "non-borrowable source <: borrow names the borrow",
			sub:   num(),
			super: mutRef(exactObj(propElem("x", num()))),
			want:  []string{"cannot constrain number <: mut object"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			errs := c.Constrain(tt.sub, tt.super)
			require.Equal(t, tt.want, Messages(errs))
			if tt.check != nil {
				tt.check(t, tt.sub, tt.super, errs)
			}
		})
	}
}

// A borrow on either side of a constraint against a type VARIABLE records the WHOLE
// borrow as a bound (peeling would drop its mutability), so the variable coalesces
// back to the borrow. This pins the var-arm fall-through both directions.
func TestConstrainRefAgainstVar(t *testing.T) {
	t.Run("RefType <: var records the borrow as a lower bound", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		ref := mutRef(exactObj(propElem("x", num())))
		require.Empty(t, c.Constrain(ref, a))
		require.True(t, equalType(ref, coalesce(a, soltype.Positive)))
	})

	t.Run("var <: RefType records the borrow as an upper bound", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		ref := mutRef(exactObj(propElem("x", num())))
		require.Empty(t, c.Constrain(a, ref))
		require.True(t, equalType(ref, coalesce(a, soltype.Negative)))
	})
}

// TestConstrainRefInnerInvariantViaBounds is the gate's load-bearing property: a
// variable INSIDE a mutable inner is invariant, pinned from BOTH directions through
// the ordinary bound machinery with no special journaling. `mut {x: β} <: mut {x:
// number}` adds number as β's upper bound (the covariant read view) AND as its lower
// bound (the contravariant write view), so β coalesces to number in either polarity.
// This is what "encodes cleanly against the journal" means for the gate.
func TestConstrainRefInnerInvariantViaBounds(t *testing.T) {
	c := &Context{}
	b := c.freshVar(0)
	require.Empty(t, c.Constrain(
		mutRef(exactObj(propElem("x", b))),
		mutRef(exactObj(propElem("x", num()))),
	))
	require.True(t, equalType(num(), coalesce(b, soltype.Positive)), "lower bound (write view)")
	require.True(t, equalType(num(), coalesce(b, soltype.Negative)), "upper bound (read view)")
}

func TestConstrainVoid(t *testing.T) {
	c := &Context{}
	require.Empty(t, c.Constrain(&soltype.Void{}, &soltype.Void{}))
	require.Equal(t, []string{"cannot constrain void <: number"},
		Messages(c.Constrain(&soltype.Void{}, num())))
}

// PR8: the ErrorType recovery sentinel ABSORBS in both directions — a constraint
// with an ErrorType operand trivially succeeds, so a reported diagnostic's
// placeholder never cascades. Pinned against every concrete shape, on both sides.
func TestConstrainErrorTypeAbsorbs(t *testing.T) {
	errT := func() soltype.Type { return &soltype.ErrorType{} }
	concretes := []struct {
		name string
		t    soltype.Type
	}{
		{"number", num()},
		{"literal", numLit(5)},
		{"function", exactFn(num(), identParam("x", num()))},
		{"tuple", &soltype.TupleType{Elems: []soltype.Type{num()}}},
		{"void", &soltype.Void{}},
		{"error", errT()},
	}
	for _, tc := range concretes {
		t.Run("error <: "+tc.name, func(t *testing.T) {
			c := &Context{}
			require.Empty(t, c.Constrain(errT(), tc.t))
		})
		t.Run(tc.name+" <: error", func(t *testing.T) {
			c := &Context{}
			require.Empty(t, c.Constrain(tc.t, errT()))
		})
	}
}

// PR8: ErrorType short-circuits ABOVE the variable arms, so it never enters a
// var's bound list — coalesce / extrude / freshenAbove then never see it
// propagated through bounds.
func TestConstrainErrorTypeNeverEntersVarBounds(t *testing.T) {
	c := &Context{}
	a := c.freshVar(0)
	require.Empty(t, c.Constrain(a, &soltype.ErrorType{}))
	require.Empty(t, a.UpperBounds, "ErrorType must not be recorded as an upper bound")
	require.Empty(t, c.Constrain(&soltype.ErrorType{}, a))
	require.Empty(t, a.LowerBounds, "ErrorType must not be recorded as a lower bound")
}

func TestConstrainVariableBinding(t *testing.T) {
	// α <: number records number as an upper bound of α.
	c := &Context{}
	a := c.freshVar(0)
	n := num()
	require.Empty(t, c.Constrain(a, n))
	require.Len(t, a.UpperBounds, 1)
	require.Same(t, soltype.Type(n), a.UpperBounds[0])

	// 5 <: α records 5 as a lower bound of α.
	five := numLit(5)
	require.Empty(t, c.Constrain(five, a))
	require.Len(t, a.LowerBounds, 1)
	require.Same(t, soltype.Type(five), a.LowerBounds[0])
}

func TestConstrainTransitivePropagation(t *testing.T) {
	// Constrain α <: number, then 5 <: α; the second constraint must propagate
	// the existing upper bound (number), checking 5 <: number — which holds.
	c := &Context{}
	a := c.freshVar(0)
	require.Empty(t, c.Constrain(a, num()))
	require.Empty(t, c.Constrain(numLit(5), a))

	// Now make the propagation fail: β <: number, then "hello" <: β must
	// propagate "hello" <: number and fail.
	c2 := &Context{}
	b := c2.freshVar(0)
	require.Empty(t, c2.Constrain(b, num()))
	require.Equal(t, []string{`cannot constrain "hello" <: number`},
		Messages(c2.Constrain(strLit("hello"), b)))
}

func TestConstrainExtrusion(t *testing.T) {
	// When a lower-level variable is constrained against a higher-level type,
	// that type must be extruded down to the variable's level: the lower var's
	// bound list must capture a FRESH level-0 variable, not the original
	// higher-level variable (no cross-level leakage).
	c := &Context{}
	low := c.freshVar(0)  // level 0
	high := c.freshVar(1) // level 1

	// low <: high. low (level 0) is the sub variable; high (level 1) lives above
	// low's level, so it is extruded down to a fresh level-0 variable before
	// being recorded as low's upper bound.
	require.Empty(t, c.Constrain(low, high))

	require.Len(t, low.UpperBounds, 1)
	bound, ok := low.UpperBounds[0].(*soltype.TypeVarType)
	require.True(t, ok)
	require.NotSame(t, high, bound)       // the original high var did not leak in
	require.Equal(t, 0, bound.Level)      // it was copied down to low's level
	require.Greater(t, bound.ID, high.ID) // and it is a freshly-allocated var

	// The extruded var is wired back to the original through high's bound list.
	require.Len(t, high.LowerBounds, 1)
	require.Same(t, soltype.Type(bound), high.LowerBounds[0])
}

func TestConstrainExtrusionBothPolarities(t *testing.T) {
	// Extruding a function that mentions the same higher-level variable in both
	// a contravariant (param) and a covariant (return) position must produce
	// TWO distinct fresh vars — one per polarity, with opposite bound wiring.
	// A cache keyed by var ID alone would reuse the first-seen polarity's copy
	// in the other position; the (id, polarity) key keeps them distinct.
	c := &Context{}
	low := c.freshVar(0) // level 0
	a := c.freshVar(1)   // level 1, used in both positions of fn
	fn := &soltype.FuncType{
		Params: []*soltype.FuncParam{identParam("x", a)},
		Ret:    a,
	}

	// low <: fn. fn lives above low's level, so it is extruded down; `a` is
	// reached at Positive (return) and Negative (param) polarity.
	require.Empty(t, c.Constrain(low, fn))

	require.Len(t, low.UpperBounds, 1)
	extruded, ok := low.UpperBounds[0].(*soltype.FuncType)
	require.True(t, ok)

	paramVar, ok := extruded.Params[0].Type.(*soltype.TypeVarType)
	require.True(t, ok)
	retVar, ok := extruded.Ret.(*soltype.TypeVarType)
	require.True(t, ok)

	// Distinct fresh vars, both copied down to low's level.
	require.NotSame(t, paramVar, retVar)
	require.Equal(t, 0, paramVar.Level)
	require.Equal(t, 0, retVar.Level)
}
