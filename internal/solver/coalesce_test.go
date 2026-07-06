package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

func TestCoalesceAtomsPassThrough(t *testing.T) {
	tests := []struct {
		name string
		in   soltype.Type
	}{
		{"number", num()},
		{"literal 5", numLit(5)},
		{"void", &soltype.Void{}},
		{"never", &soltype.NeverType{}},
		{"unknown", &soltype.UnknownType{}},
		{"error", &soltype.ErrorType{}}, // PR8 recovery sentinel: a childless atom
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Same(t, tt.in, coalesce(tt.in, soltype.Positive))
			require.Same(t, tt.in, coalesce(tt.in, soltype.Negative))
		})
	}
}

func TestCoalesceSingleBoundInline(t *testing.T) {
	// A positive variable with a single lower bound (5) coalesces to that bound.
	t.Run("positive lower", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.LowerBounds = []soltype.Type{numLit(5)}
		got := coalesce(a, soltype.Positive)
		require.True(t, equalType(numLit(5), got))
	})

	// A negative variable with a single upper bound (number) coalesces to it.
	t.Run("negative upper", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.UpperBounds = []soltype.Type{num()}
		got := coalesce(a, soltype.Negative)
		require.True(t, equalType(num(), got))
	})
}

func TestCoalesceEmptyBoundCollapse(t *testing.T) {
	// An empty positive variable is the identity of `|` ⇒ never (⊥).
	t.Run("empty positive ⇒ never", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		require.IsType(t, &soltype.NeverType{}, coalesce(a, soltype.Positive))
	})

	// An empty negative variable is the identity of `&` ⇒ unknown (⊤).
	t.Run("empty negative ⇒ unknown", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		require.IsType(t, &soltype.UnknownType{}, coalesce(a, soltype.Negative))
	})
}

func TestCoalesceMultiBound(t *testing.T) {
	// A positive variable with two distinct lower bounds ⇒ union of the lowers.
	t.Run("positive ⇒ union", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.LowerBounds = []soltype.Type{num(), str()}
		got := coalesce(a, soltype.Positive)
		want := &soltype.UnionType{Types: []soltype.Type{num(), str()}}
		require.True(t, equalType(want, got))
	})

	// A negative variable with two distinct upper bounds ⇒ intersection.
	t.Run("negative ⇒ intersection", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.UpperBounds = []soltype.Type{num(), str()}
		got := coalesce(a, soltype.Negative)
		want := &soltype.IntersectionType{Types: []soltype.Type{num(), str()}}
		require.True(t, equalType(want, got))
	})

	// Duplicate bounds are deduplicated by structural equality, collapsing back
	// to the sole element (combine returns it directly).
	t.Run("duplicate lowers dedup", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.LowerBounds = []soltype.Type{num(), num()}
		got := coalesce(a, soltype.Positive)
		require.True(t, equalType(num(), got))
	})
}

// TestCoalesceNegativeObjectMerge pins B1: a negative variable carrying several
// member-access requirements as separate inexact one-property objects coalesces to
// a single EXACT object (Policy A), not an intersection of one-property objects.
// This is the receiver of `fn (p) { p.a; p.b }`, whose body lands `{a: …, ...}` and
// `{b: …, ...}` on p's upper bounds.
func TestCoalesceNegativeObjectMerge(t *testing.T) {
	// Two distinct one-property requirements fold into one exact two-property object.
	t.Run("distinct properties fold to one exact object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("a", num())),
			inexactObj(propElem("b", str())),
		}
		got := coalesce(p, soltype.Negative)
		want := exactObj(propElem("a", num()), propElem("b", str()))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	// A single member-access requirement closes to exact too — the row is sealed
	// once body inference completes, so `{a, ...}` becomes `{a}`.
	t.Run("single requirement closes to exact", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{inexactObj(propElem("a", num()))}
		got := coalesce(p, soltype.Negative)
		require.True(t, equalType(exactObj(propElem("a", num())), got), "got %s", soltype.Print(got))
	})

	// A property required on several of the merged objects becomes the intersection
	// of its per-requirement types: p <: {a: number} and p <: {a: string} ⇒ p.a is
	// number & string.
	t.Run("shared property becomes intersection", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("a", num())),
			inexactObj(propElem("a", str())),
		}
		got := coalesce(p, soltype.Negative)
		want := exactObj(&soltype.PropertyElem{
			Name: "a",
			Type: &soltype.IntersectionType{Types: []soltype.Type{num(), str()}},
		})
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	// Non-object upper bounds are left untouched alongside the folded object, so a
	// mixed bound list still renders as an intersection of the merged object and the
	// other parts.
	t.Run("non-object bounds survive the fold", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("a", num())),
			num(),
		}
		got := coalesce(p, soltype.Negative)
		// Members are in canonical order (M6 PR1): PrimType ranks before
		// ObjectType, so `number & {a: number}`.
		want := &soltype.IntersectionType{Types: []soltype.Type{
			num(),
			exactObj(propElem("a", num())),
		}}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

// TestCoalesceMutWriteFold pins the C3 whole-object mut merge: a field-write bound
// (a mut-wrapped inexact object) folds with the receiver's reads into ONE object,
// and the presence of any write wraps the merged object in `mut`. usageObject is the
// classifier that routes both read and write requirements into the fold.
func TestCoalesceMutWriteFold(t *testing.T) {
	// A lone write requirement folds and the result is wrapped in mut.
	t.Run("single write folds to a mut object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{mutRef(inexactObj(propElem("x", num())))}
		got := coalesce(p, soltype.Negative)
		want := mutRef(exactObj(propElem("x", num())))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	// A bare read and a mut write on the same receiver fold into ONE mut object —
	// not `{x} & mut {y}` — because any write makes the whole merged object mut.
	t.Run("read and write fold into one mut object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("x", num())),
			mutRef(inexactObj(propElem("y", str()))),
		}
		got := coalesce(p, soltype.Negative)
		want := mutRef(exactObj(propElem("x", num()), propElem("y", str())))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	// With no write, reads fold into a bare (immutable) object — the pre-C3 behavior,
	// confirming the mut wrap is gated on an actual write.
	t.Run("reads only fold to a bare object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("x", num())),
			inexactObj(propElem("y", str())),
		}
		got := coalesce(p, soltype.Negative)
		want := exactObj(propElem("x", num()), propElem("y", str()))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

// TestUsageObject pins the requirement classifier directly: a bare inexact object is
// a read, a mut-wrapped inexact object is a write, and an exact object, an immutable
// borrow, or a non-object is not a usage requirement.
func TestUsageObject(t *testing.T) {
	tests := []struct {
		name      string
		in        soltype.Type
		wantOK    bool
		wantWrite bool
	}{
		{"bare inexact object is a read", inexactObj(propElem("x", num())), true, false},
		{"mut inexact object is a write", mutRef(inexactObj(propElem("x", num()))), true, true},
		{"exact object is not a usage requirement", exactObj(propElem("x", num())), false, false},
		{"mut exact object is not a usage requirement", mutRef(exactObj(propElem("x", num()))), false, false},
		{"non-object is not a usage requirement", num(), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, write, ok := usageObject(tt.in)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantWrite, write)
		})
	}
}

// TestCoalesceOpenVarStaysInexact pins B2: an `open` parameter var's folded usage
// object stays inexact (row-polymorphic) instead of closing to exact. The Open flag
// on the var is the opt-out from B1's Policy-A close.
func TestCoalesceOpenVarStaysInexact(t *testing.T) {
	// An open var with two member-access requirements folds to one INEXACT object.
	t.Run("open var folds to inexact object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.Open = true
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("x", num())),
			inexactObj(propElem("y", str())),
		}
		got := coalesce(p, soltype.Negative)
		want := inexactObj(propElem("x", num()), propElem("y", str()))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})

	// A single requirement on an open var also stays inexact.
	t.Run("open var single requirement stays inexact", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0)
		p.Open = true
		p.UpperBounds = []soltype.Type{inexactObj(propElem("x", num()))}
		got := coalesce(p, soltype.Negative)
		require.True(t, equalType(inexactObj(propElem("x", num())), got), "got %s", soltype.Print(got))
	})

	// The un-open peer closes to exact (the B1 baseline), so the flag is what
	// distinguishes them.
	t.Run("closed peer folds to exact object", func(t *testing.T) {
		c := &Context{}
		p := c.freshVar(0) // Open defaults to false
		p.UpperBounds = []soltype.Type{
			inexactObj(propElem("x", num())),
			inexactObj(propElem("y", str())),
		}
		got := coalesce(p, soltype.Negative)
		want := exactObj(propElem("x", num()), propElem("y", str()))
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
}

func TestCoalesceStructuralRecursion(t *testing.T) {
	// The identity function `fn (x) -> x` is built with one variable used both as
	// the parameter type (negative) and the return type (positive). With empty
	// bounds, the uniform-inline coalescer renders the degenerate
	// `fn (x: unknown) -> never`: the param var is negative-empty ⇒ unknown, the
	// return var is positive-empty ⇒ never. (The named-`<T0>` rendering is M3.)
	c := &Context{}
	a := c.freshVar(0)
	fn := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: a}},
		Ret:    a,
	}
	got := coalesce(fn, soltype.Positive)

	want := &soltype.FuncType{
		Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: &soltype.UnknownType{}}},
		Ret:    &soltype.NeverType{},
	}
	require.True(t, equalType(want, got))
}

// TestCoalesceBorrowedVarInnerPeels pins review finding 1: coalescing a borrow whose
// inner is an inference variable inlines that variable to its bounds. RefInner admits
// *TypeVarType, so `mut β` is well-formed mid-inference. When β inlines to a
// non-borrowable type — a primitive bound, or never for empty bounds — the borrow
// wrapper must PEEL to the bare inner rather than panic: a `mut number` is a JS
// no-op, so the coalesced display is just the inner.
func TestCoalesceBorrowedVarInnerPeels(t *testing.T) {
	t.Run("inner var with a primitive bound peels to the primitive", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		a.LowerBounds = []soltype.Type{num()}
		ref := &soltype.RefType{Mut: true, Inner: a}
		got := coalesce(ref, soltype.Positive)
		require.True(t, equalType(num(), got))
	})

	t.Run("inner var with empty bounds peels to never", func(t *testing.T) {
		c := &Context{}
		a := c.freshVar(0)
		ref := &soltype.RefType{Mut: true, Inner: a}
		got := coalesce(ref, soltype.Positive)
		require.IsType(t, &soltype.NeverType{}, got)
	})
}

// TestCoalesceBorrowPreservesWrapper is the complement of TestCoalesceBorrowedVarInnerPeels:
// when the borrow's inner stays a RefInner after coalescing (here the inner is an
// OBJECT containing a variable, not a bare variable), the `mut` wrapper must SURVIVE.
// `mut {x: β}` with β bounded by number coalesces to `mut {x: number}`, not a peeled
// `{x: number}` — the realistic shape C3's field-write inference produces.
func TestCoalesceBorrowPreservesWrapper(t *testing.T) {
	c := &Context{}
	v := c.freshVar(0)
	v.UpperBounds = []soltype.Type{num()}
	ref := &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", v))}
	got := coalesce(ref, soltype.Negative)
	require.True(t, equalType(&soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))}, got))
}

// equalType discriminates a borrow on its Mut flag and its inner, mirroring the
// ObjectType arm's Inexact/Optional discriminators. This drives dedup in coalesce —
// without the Mut check `mut {x}` and an immutable `{x}` view would collapse.
func TestEqualTypeRef(t *testing.T) {
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "same mut and inner",
			a:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))},
			b:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))},
			want: true,
		},
		{
			name: "Mut differs",
			a:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))},
			b:    &soltype.RefType{Mut: false, Inner: exactObj(propElem("x", num()))},
			want: false,
		},
		{
			name: "inner differs",
			a:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))},
			b:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", str()))},
			want: false,
		},
		{
			name: "ref is not its bare inner",
			a:    &soltype.RefType{Mut: true, Inner: exactObj(propElem("x", num()))},
			b:    exactObj(propElem("x", num())),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// equalType on a ClassType is nominal: it compares the qualified Name and the Final
// exactness flag, then the type arguments positionally. Two instances of different
// classes, or of the same class with different arguments or exactness, are not equal.
func TestEqualTypeClass(t *testing.T) {
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "same name and arguments",
			a:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{num()}},
			b:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{num()}},
			want: true,
		},
		{
			name: "name differs",
			a:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{num()}},
			b:    &soltype.ClassType{Name: "Bag", Args: []soltype.Type{num()}},
			want: false,
		},
		{
			name: "type argument differs",
			a:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{num()}},
			b:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{str()}},
			want: false,
		},
		{
			name: "Final differs",
			a:    &soltype.ClassType{Name: "Box", Final: true},
			b:    &soltype.ClassType{Name: "Box", Final: false},
			want: false,
		},
		{
			name: "argument count differs",
			a:    &soltype.ClassType{Name: "Box", Args: []soltype.Type{num()}},
			b:    &soltype.ClassType{Name: "Box"},
			want: false,
		},
		{
			name: "a class is not its bare projected object",
			a:    &soltype.ClassType{Name: "Box"},
			b:    exactObj(),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// equalType compares two generic FuncTypes up to alpha-renaming of their positional
// type parameters: a parameter's identity is its position, not its variable id, so two
// signatures differing only in variable id compare equal, while a constraint, default,
// arity, or positional-use difference makes them unequal. A free variable is still keyed
// by pointer, so a bound parameter does not leak into the free-variable comparison.
func TestEqualTypeGenericFunc(t *testing.T) {
	// tv builds a fresh type variable at a quantifiable level.
	tv := func(id int) *soltype.TypeVarType { return &soltype.TypeVarType{ID: id, Level: 1} }
	// ident wraps a type as a named parameter.
	ident := func(name string, ty soltype.Type) *soltype.FuncParam {
		return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: name}, Type: ty}
	}
	// identityFn is `fn <U>(x: U) -> U` with U carrying the given id, constraint, and
	// default, so the table can vary one facet at a time.
	identityFn := func(id int, constraint, def soltype.Type) *soltype.FuncType {
		u := tv(id)
		if constraint != nil {
			u.UpperBounds = []soltype.Type{constraint}
		}
		return &soltype.FuncType{
			TypeParams: []*soltype.TypeParam{{Name: "U", Var: u, Default: def}},
			Params:     []*soltype.FuncParam{ident("x", u)},
			Ret:        u,
		}
	}
	// freeFn builds `fn <U>(x: U, y: free) -> U`, threading one U pointer through so
	// each side reuses its parameter variable the way real solver output does. The free
	// variable is passed in, so two sides can share it or differ.
	freeFn := func(u, free *soltype.TypeVarType) *soltype.FuncType {
		return &soltype.FuncType{
			TypeParams: []*soltype.TypeParam{{Name: "U", Var: u}},
			Params:     []*soltype.FuncParam{ident("x", u), ident("y", free)},
			Ret:        u,
		}
	}
	sharedFree := tv(99)

	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "differ only in parameter var id",
			a:    identityFn(10, nil, nil),
			b:    identityFn(20, nil, nil),
			want: true,
		},
		{
			name: "same constraint, different var id",
			a:    identityFn(10, num(), nil),
			b:    identityFn(20, num(), nil),
			want: true,
		},
		{
			name: "constraint differs",
			a:    identityFn(10, num(), nil),
			b:    identityFn(20, str(), nil),
			want: false,
		},
		{
			name: "one constrained, one not",
			a:    identityFn(10, num(), nil),
			b:    identityFn(20, nil, nil),
			want: false,
		},
		{
			name: "default differs",
			a:    identityFn(10, nil, num()),
			b:    identityFn(20, nil, str()),
			want: false,
		},
		{
			name: "one defaulted, one not",
			a:    identityFn(10, nil, num()),
			b:    identityFn(20, nil, nil),
			want: false,
		},
		{
			// Params match, so this reaches the TypeParams-length check rather than
			// short-circuiting on the earlier Params-length comparison.
			name: "type parameter arity differs",
			a:    identityFn(10, nil, nil),
			b:    &soltype.FuncType{Params: []*soltype.FuncParam{ident("x", num())}, Ret: num()},
			want: false,
		},
		{
			// The same free-var pointer beside an alpha-renamed parameter still matches.
			name: "same free var pointer matches",
			a:    freeFn(tv(10), sharedFree),
			b:    freeFn(tv(20), sharedFree),
			want: true,
		},
		{
			// A bound parameter does not leak into the free-var comparison: two distinct
			// free-var pointers stay unequal even though the parameters alpha-match.
			name: "different free var pointers do not match",
			a:    freeFn(tv(10), tv(98)),
			b:    freeFn(tv(20), tv(97)),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// equalType distinguishes methods by their receiver: presence marks an instance
// versus a static method, and the receiver type carries mutability, so (self),
// (mut self), and () are all distinct.
func TestEqualTypeFuncSelfParam(t *testing.T) {
	selfRecv := func(recv soltype.Type) *soltype.FuncParam {
		return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: recv}
	}
	owned := func() soltype.Type { return &soltype.ClassType{Name: "Counter"} }
	mutSelf := func() soltype.Type {
		return &soltype.RefType{Mut: true, Inner: &soltype.ClassType{Name: "Counter"}}
	}
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "same receiver",
			a:    &soltype.FuncType{SelfParam: selfRecv(owned()), Ret: num()},
			b:    &soltype.FuncType{SelfParam: selfRecv(owned()), Ret: num()},
			want: true,
		},
		{
			name: "receiver mutability differs",
			a:    &soltype.FuncType{SelfParam: selfRecv(owned()), Ret: num()},
			b:    &soltype.FuncType{SelfParam: selfRecv(mutSelf()), Ret: num()},
			want: false,
		},
		{
			name: "instance versus static",
			a:    &soltype.FuncType{SelfParam: selfRecv(owned()), Ret: num()},
			b:    &soltype.FuncType{Ret: num()},
			want: false,
		},
		{
			name: "both static",
			a:    &soltype.FuncType{Ret: num()},
			b:    &soltype.FuncType{Ret: num()},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// equalType distinguishes an instance getter or setter from a static one by receiver
// presence, and by receiver type.
func TestEqualTypeGetterSetterSelfParam(t *testing.T) {
	selfRecv := func(recv soltype.Type) *soltype.FuncParam {
		return &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: recv}
	}
	owned := func() soltype.Type { return &soltype.ClassType{Name: "List"} }
	mutSelf := func() soltype.Type {
		return &soltype.RefType{Mut: true, Inner: &soltype.ClassType{Name: "List"}}
	}
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "instance getter versus static getter",
			a:    exactObj(&soltype.GetterElem{Name: "g", SelfParam: selfRecv(owned()), Type: num()}),
			b:    exactObj(&soltype.GetterElem{Name: "g", Type: num()}),
			want: false,
		},
		{
			name: "same instance getter",
			a:    exactObj(&soltype.GetterElem{Name: "g", SelfParam: selfRecv(owned()), Type: num()}),
			b:    exactObj(&soltype.GetterElem{Name: "g", SelfParam: selfRecv(owned()), Type: num()}),
			want: true,
		},
		{
			name: "setter receiver mutability differs",
			a:    exactObj(&soltype.SetterElem{Name: "s", SelfParam: selfRecv(owned()), Param: num()}),
			b:    exactObj(&soltype.SetterElem{Name: "s", SelfParam: selfRecv(mutSelf()), Param: num()}),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// equalType over an object carrying method, getter, and setter members compares
// each member kind-for-kind and stays order-independent. A getter and a setter that
// share a name are disambiguated by kind, so a getter never matches a setter.
func TestEqualTypeObjectMembers(t *testing.T) {
	method := func(name string, sig *soltype.FuncType) *soltype.MethodElem {
		return &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}}
	}
	fn := func(param, ret soltype.Type) *soltype.FuncType {
		return &soltype.FuncType{Params: []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: param}}, Ret: ret}
	}
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "equal up to member order",
			a: exactObj(
				method("m", fn(num(), str())),
				&soltype.GetterElem{Name: "g", Type: num()},
			),
			b: exactObj(
				&soltype.GetterElem{Name: "g", Type: num()},
				method("m", fn(num(), str())),
			),
			want: true,
		},
		{
			name: "method signature differs",
			a:    exactObj(method("m", fn(num(), str()))),
			b:    exactObj(method("m", fn(num(), num()))),
			want: false,
		},
		{
			name: "getter and setter with the same name stay distinct",
			a:    exactObj(&soltype.GetterElem{Name: "x", Type: num()}),
			b:    exactObj(&soltype.SetterElem{Name: "x", Param: num()}),
			want: false,
		},
		{
			name: "setter param differs",
			a:    exactObj(&soltype.SetterElem{Name: "s", Param: num()}),
			b:    exactObj(&soltype.SetterElem{Name: "s", Param: str()}),
			want: false,
		},
		{
			name: "matching getter and setter sharing a name",
			a: exactObj(
				&soltype.GetterElem{Name: "x", Type: num()},
				&soltype.SetterElem{Name: "x", Param: num()},
			),
			b: exactObj(
				&soltype.SetterElem{Name: "x", Param: num()},
				&soltype.GetterElem{Name: "x", Type: num()},
			),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}

// A parameter's identity is its position: two two-parameter signatures that use their
// parameters in swapped positions are unequal even though each is well-formed.
func TestEqualTypeGenericFuncPositional(t *testing.T) {
	// mk builds `fn <U, V>(x: U, y: V) -> [retFirst, retSecond]`, choosing which
	// parameter each return element uses, so a swap changes only the body's positions.
	mk := func(idU, idV int, retFirst, retSecond int) *soltype.FuncType {
		u := &soltype.TypeVarType{ID: idU, Level: 1}
		v := &soltype.TypeVarType{ID: idV, Level: 1}
		pick := func(id int) *soltype.TypeVarType {
			if id == idU {
				return u
			}
			return v
		}
		return &soltype.FuncType{
			TypeParams: []*soltype.TypeParam{{Name: "U", Var: u}, {Name: "V", Var: v}},
			Params: []*soltype.FuncParam{
				{Pattern: &soltype.IdentPat{Name: "x"}, Type: u},
				{Pattern: &soltype.IdentPat{Name: "y"}, Type: v},
			},
			Ret: &soltype.TupleType{Elems: []soltype.Type{pick(retFirst), pick(retSecond)}},
		}
	}
	// Both return [first param, second param]: alpha-equal despite distinct var ids.
	require.True(t, equalType(mk(10, 11, 10, 11), mk(20, 21, 20, 21)))
	// The second swaps the return to [second param, first param]: positions differ.
	require.False(t, equalType(mk(10, 11, 10, 11), mk(20, 21, 21, 20)))
}

// equalType on ObjectType must discriminate on the Inexact flag and on each
// property's Optional marker (mirroring the FuncType arm's Inexact / param-Optional
// checks), and must be order-independent. Without the Optional check (M4 A1 review
// fix #2) {a: T} and {a?: T} would compare equal and coalesce/simplify would drop
// optionality.
func TestEqualTypeObject(t *testing.T) {
	optProp := func(name string, ty soltype.Type) *soltype.PropertyElem {
		return &soltype.PropertyElem{Name: name, Type: ty, Optional: true}
	}
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "equal up to property order",
			a:    exactObj(propElem("a", num()), propElem("b", str())),
			b:    exactObj(propElem("b", str()), propElem("a", num())),
			want: true,
		},
		{
			name: "Inexact differs",
			a:    exactObj(propElem("a", num())),
			b:    inexactObj(propElem("a", num())),
			want: false,
		},
		{
			name: "Optional differs",
			a:    exactObj(propElem("a", num())),
			b:    exactObj(optProp("a", num())),
			want: false,
		},
		{
			name: "property type differs",
			a:    exactObj(propElem("a", num())),
			b:    exactObj(propElem("a", str())),
			want: false,
		},
		{
			name: "property set size differs",
			a:    exactObj(propElem("a", num())),
			b:    exactObj(propElem("a", num()), propElem("b", str())),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
		})
	}
}
