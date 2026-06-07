package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// identityVisitor rewrites nothing: every node is kept and every child walked.
type identityVisitor struct{}

func (identityVisitor) EnterType(Type, Polarity) EnterResult { return EnterResult{} }
func (identityVisitor) ExitType(t Type, _ Polarity) Type     { return t }

// recorder logs the (node, polarity) of every EnterType, rewriting nothing.
type recorder struct{ seen map[Type]Polarity }

func (r *recorder) EnterType(t Type, pol Polarity) EnterResult {
	r.seen[t] = pol
	return EnterResult{}
}
func (r *recorder) ExitType(t Type, _ Polarity) Type { return t }

// Accept flips polarity on (contravariant) function parameters and keeps it on
// the (covariant) return — and the flip composes through nesting, so a parameter
// of a parameter is back to the outer polarity.
func TestAcceptThreadsPolarity(t *testing.T) {
	c := &PrimType{Prim: NumPrim}
	d := &PrimType{Prim: StrPrim}
	e := &PrimType{Prim: BoolPrim}
	a := &TypeVarType{ID: 1}

	// fn(p: fn(q: C) -> D, a) -> fn() -> E, walked from Positive.
	inner := &FuncType{
		Params: []*FuncParam{{Pattern: &IdentPat{Name: "q"}, Type: c}},
		Ret:    d,
	}
	retFn := &FuncType{Ret: e}
	outer := &FuncType{
		Params: []*FuncParam{
			{Pattern: &IdentPat{Name: "p"}, Type: inner},
			{Pattern: &IdentPat{Name: "a"}, Type: a},
		},
		Ret: retFn,
	}

	r := &recorder{seen: map[Type]Polarity{}}
	outer.Accept(r, Positive)

	require.Equal(t, Positive, r.seen[outer], "root keeps the start polarity")
	require.Equal(t, Negative, r.seen[inner], "a parameter is contravariant")
	require.Equal(t, Positive, r.seen[c], "a parameter of a parameter flips back")
	require.Equal(t, Negative, r.seen[d], "the return of a contravariant fn stays flipped")
	require.Equal(t, Negative, r.seen[a], "the second parameter is contravariant")
	require.Equal(t, Positive, r.seen[retFn], "the return is covariant")
	require.Equal(t, Positive, r.seen[e], "the return of a covariant fn stays positive")
}

// An atom is handed straight to the visitor and passes through unchanged: a no-op
// rewrite returns the SAME pointer.
func TestAcceptAtomPassThrough(t *testing.T) {
	atoms := []Type{
		&PrimType{Prim: NumPrim},
		&LitType{Lit: &NumLit{Value: 5}},
		&Void{},
		&NeverType{},
		&UnknownType{},
		&TypeVarType{ID: 1},
	}
	for _, a := range atoms {
		require.Same(t, a, a.Accept(identityVisitor{}, Positive))
		require.Same(t, a, a.Accept(identityVisitor{}, Negative))
	}
}

// A no-op rewrite over a compound node preserves identity all the way down: the
// FuncType, its *FuncParams, and its return are the SAME pointers.
func TestAcceptIdentityPreservation(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	fn := &FuncType{
		Params:  []*FuncParam{{Pattern: &IdentPat{Name: "x"}, Type: num, Optional: true}},
		Ret:     num,
		Inexact: true,
	}
	got := fn.Accept(identityVisitor{}, Positive)
	require.Same(t, fn, got, "an unchanged FuncType keeps its pointer")
}

// replaceVar swaps one specific variable for repl; vars are leaves so SkipChildren
// is set (there are no tree children to walk).
type replaceVar struct {
	target *TypeVarType
	repl   Type
}

func (v *replaceVar) EnterType(t Type, _ Polarity) EnterResult {
	if tv, ok := t.(*TypeVarType); ok && tv == v.target {
		return EnterResult{Type: v.repl, SkipChildren: true}
	}
	return EnterResult{}
}
func (v *replaceVar) ExitType(t Type, _ Polarity) Type { return t }

// Changing one child allocates a new parent but REUSES every unchanged sibling
// (copy-on-write), and carries the surface markers (Optional/Rest/Inexact) through.
func TestAcceptCopyOnWrite(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	p0 := &FuncParam{Pattern: &IdentPat{Name: "x"}, Type: a, Optional: true}
	p1 := &FuncParam{Pattern: &IdentPat{Name: "y"}, Type: num, Rest: true}
	fn := &FuncType{Params: []*FuncParam{p0, p1}, Ret: num, Inexact: true}

	got := fn.Accept(&replaceVar{target: a, repl: str}, Positive).(*FuncType)

	require.NotSame(t, fn, got, "a changed child forces a new parent")
	require.Same(t, str, got.Params[0].Type, "the changed param took the replacement")
	require.NotSame(t, p0, got.Params[0], "the changed param is a fresh *FuncParam")
	require.True(t, got.Params[0].Optional, "the changed param keeps its Optional marker")
	require.Same(t, p1, got.Params[1], "the unchanged param keeps its *FuncParam pointer")
	require.Same(t, num, got.Ret, "the unchanged return keeps its pointer")
	require.True(t, got.Inexact, "the FuncType keeps its Inexact marker")
}

// recordEntered logs every node EnterType reaches, replacing target with repl and
// SKIPPING its children — so a skipped subtree is never entered.
type recordEntered struct {
	target  Type
	repl    Type
	entered map[Type]bool
}

func (v *recordEntered) EnterType(t Type, _ Polarity) EnterResult {
	v.entered[t] = true
	if t == v.target {
		return EnterResult{Type: v.repl, SkipChildren: true}
	}
	return EnterResult{}
}
func (v *recordEntered) ExitType(t Type, _ Polarity) Type { return t }

// EnterType's replacement is honored and SkipChildren prunes the subtree: the
// replaced node's former children are never visited.
func TestAcceptReplaceAndSkipChildren(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	leaf := &PrimType{Prim: StrPrim}
	inner := &TupleType{Elems: []Type{leaf}}
	outer := &TupleType{Elems: []Type{inner}}

	v := &recordEntered{target: inner, repl: num, entered: map[Type]bool{}}
	got := outer.Accept(v, Positive).(*TupleType)

	require.Same(t, num, got.Elems[0], "inner was replaced by the substitute")
	require.True(t, v.entered[inner], "the replaced node was itself entered")
	require.False(t, v.entered[leaf], "a skipped subtree is never entered")
}

// replaceDescend replaces the node matching target with repl and DESCENDS
// (SkipChildren=false), recording every node EnterType reaches.
type replaceDescend struct {
	target  Type
	repl    Type
	entered map[Type]bool
}

func (v *replaceDescend) EnterType(t Type, _ Polarity) EnterResult {
	v.entered[t] = true
	if t == v.target {
		return EnterResult{Type: v.repl} // SkipChildren=false: rebuild from repl's children
	}
	return EnterResult{}
}
func (v *replaceDescend) ExitType(t Type, _ Polarity) Type { return t }

// A same-kind replacement with SkipChildren=false rebuilds from the REPLACEMENT's
// children, not the original's, and preserves identity when nothing under it changed.
func TestAcceptDescendsIntoSameKindReplacement(t *testing.T) {
	origElem := &PrimType{Prim: NumPrim}
	replElem := &PrimType{Prim: StrPrim}
	orig := &TupleType{Elems: []Type{origElem}}
	repl := &TupleType{Elems: []Type{replElem}}

	v := &replaceDescend{target: orig, repl: repl, entered: map[Type]bool{}}
	got := orig.Accept(v, Positive).(*TupleType)

	require.True(t, v.entered[replElem], "the replacement's child is walked")
	require.False(t, v.entered[origElem], "the original's child is not walked")
	require.Same(t, repl, got, "nothing changed within repl, so identity is preserved")
}

// A different-kind replacement with SkipChildren=false violates the descend
// contract and panics with a clear message (not a bare type-assertion fault).
func TestAcceptDescendDifferentKindPanics(t *testing.T) {
	orig := &TupleType{Elems: []Type{&PrimType{Prim: NumPrim}}}
	v := &replaceDescend{target: orig, repl: &PrimType{Prim: StrPrim}, entered: map[Type]bool{}}
	require.PanicsWithValue(t,
		"soltype.Accept: EnterType replaced *soltype.TupleType with *soltype.PrimType under "+
			"SkipChildren=false; a same-kind replacement is required to descend "+
			"(set SkipChildren=true to replace with a different kind)",
		func() { orig.Accept(v, Positive) })
}
