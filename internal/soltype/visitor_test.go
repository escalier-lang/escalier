package soltype

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
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
	entered set.Set[Type]
}

func (v *recordEntered) EnterType(t Type, _ Polarity) EnterResult {
	v.entered.Add(t)
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

	v := &recordEntered{target: inner, repl: num, entered: set.NewSet[Type]()}
	got := outer.Accept(v, Positive).(*TupleType)

	require.Same(t, num, got.Elems[0], "inner was replaced by the substitute")
	require.True(t, v.entered.Contains(inner), "the replaced node was itself entered")
	require.False(t, v.entered.Contains(leaf), "a skipped subtree is never entered")
}

// replaceDescend replaces the node matching target with repl and DESCENDS
// (SkipChildren=false), recording every node EnterType reaches.
type replaceDescend struct {
	target  Type
	repl    Type
	entered set.Set[Type]
}

func (v *replaceDescend) EnterType(t Type, _ Polarity) EnterResult {
	v.entered.Add(t)
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

	v := &replaceDescend{target: orig, repl: repl, entered: set.NewSet[Type]()}
	got := orig.Accept(v, Positive).(*TupleType)

	require.True(t, v.entered.Contains(replElem), "the replacement's child is walked")
	require.False(t, v.entered.Contains(origElem), "the original's child is not walked")
	require.Same(t, repl, got, "nothing changed within repl, so identity is preserved")
}

// A different-kind replacement with SkipChildren=false violates the descend
// contract and panics with a clear message (not a bare type-assertion fault).
func TestAcceptDescendDifferentKindPanics(t *testing.T) {
	orig := &TupleType{Elems: []Type{&PrimType{Prim: NumPrim}}}
	v := &replaceDescend{target: orig, repl: &PrimType{Prim: StrPrim}, entered: set.NewSet[Type]()}
	require.PanicsWithValue(t,
		"soltype.Accept: EnterType replaced *soltype.TupleType with *soltype.PrimType under "+
			"SkipChildren=false; a same-kind replacement is required to descend "+
			"(set SkipChildren=true to replace with a different kind)",
		func() { orig.Accept(v, Positive) })
}

// An Accept rewrite over an inexact ObjectType carries the Inexact flag onto the
// rebuilt object. The old RecordType rebuild had no flag to carry; the M4
// ObjectType.Accept must copy it (visitor.go), or a coalesce/extrude/freshenAbove
// pass would silently turn an inexact object exact. This pins the property the A1
// plan flagged as a latent bug the new field exposes.
func TestAcceptObjectPreservesInexact(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	p0 := &PropertyElem{Name: "x", Type: a, Optional: true}
	p1 := &PropertyElem{Name: "y", Type: num}
	obj := &ObjectType{Elems: []ObjTypeElem{p0, p1}, Inexact: true}

	got := obj.Accept(&replaceVar{target: a, repl: str}, Positive).(*ObjectType)

	require.NotSame(t, obj, got, "a changed property forces a new object")
	require.True(t, got.Inexact, "the rebuilt object keeps its Inexact marker")
	gp0 := got.Elems[0].(*PropertyElem)
	require.Same(t, str, gp0.Type, "the changed property took the replacement")
	require.NotSame(t, p0, gp0, "the changed property is a fresh *PropertyElem")
	require.True(t, gp0.Optional, "the changed property keeps its Optional marker")
	require.Same(t, p1, got.Elems[1], "the unchanged property keeps its *PropertyElem pointer")
}

// UnionType.Accept threads the Inexact flag through a rewrite. Without the
// fix at visitor.go's UnionType arm, every coalesce, extrude, and
// freshenAbove pass would silently drop the flag. The fix is load-bearing,
// not cosmetic: those passes run on every type the solver touches.
func TestAcceptUnionPreservesInexact(t *testing.T) {
	a := &TypeVarType{ID: 1}
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	u := &UnionType{Types: []Type{a, num}, Inexact: true}

	got := u.Accept(&replaceVar{target: a, repl: str}, Positive).(*UnionType)

	require.NotSame(t, u, got, "a changed member forces a new union")
	require.True(t, got.Inexact, "the rebuilt union keeps its Inexact marker")
	require.Same(t, str, got.Types[0], "the changed member took the replacement")
	require.Same(t, num, got.Types[1], "the unchanged member keeps its pointer")
}

// An unchanged inexact UnionType keeps its pointer (copy-on-write).
func TestAcceptUnionIdentityPreservation(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	u := &UnionType{Types: []Type{num, str}, Inexact: true}
	require.Same(t, u, u.Accept(identityVisitor{}, Positive), "an unchanged UnionType keeps its pointer")
}

// An unchanged inexact ObjectType keeps its pointer (copy-on-write): a no-op
// rewrite allocates nothing.
func TestAcceptObjectIdentityPreservation(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	obj := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: num}}, Inexact: true}
	require.Same(t, obj, obj.Accept(identityVisitor{}, Positive), "an unchanged ObjectType keeps its pointer")
}

// A no-op rewrite over a RefType keeps its pointer all the way down (copy-on-write).
func TestAcceptRefIdentityPreservation(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	ref := &RefType{Mut: true, Inner: &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: num}}}}
	require.Same(t, ref, ref.Accept(identityVisitor{}, Positive), "an unchanged RefType keeps its pointer")
}

// Rewriting a variable inside a borrow rebuilds the RefType, carries the Mut marker
// through, and keeps the result a well-formed RefInner inner.
func TestAcceptRefCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	inner := &ObjectType{Elems: []ObjTypeElem{&PropertyElem{Name: "x", Type: a}}}
	ref := &RefType{Mut: true, Inner: inner}

	got := ref.Accept(&replaceVar{target: a, repl: str}, Positive).(*RefType)

	require.NotSame(t, ref, got, "a changed inner forces a new RefType")
	require.True(t, got.Mut, "the rebuilt RefType keeps its Mut marker")
	require.Nil(t, got.Lt, "the lifetime carries through unchanged")
	gotInner := got.Inner.(*ObjectType)
	require.NotSame(t, inner, gotInner, "the changed inner is a fresh object")
	require.Same(t, str, gotInner.Elems[0].(*PropertyElem).Type, "the variable took the replacement")
}
