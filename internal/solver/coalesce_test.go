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
		want := &soltype.IntersectionType{Types: []soltype.Type{
			exactObj(propElem("a", num())),
			num(),
		}}
		require.True(t, equalType(want, got), "got %s", soltype.Print(got))
	})
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
