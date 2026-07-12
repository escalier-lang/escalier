package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// cls builds a non-generic class instance token for the nominal constrain tests.
func cls(name string, final bool) *soltype.ClassType {
	return &soltype.ClassType{Name: name, Final: final}
}

// TestConstrainNominal exercises the C1 nominal constrain rule directly on the Context,
// registering ClassDefs by hand so it can cover cases source cannot yet produce — most
// importantly a `final` class, whose surface syntax the parser does not accept.
func TestConstrainNominal(t *testing.T) {
	// registerPoint seeds a two-field class `{x: number, y: number}` under name.
	registerPoint := func(c *Context, name string) {
		c.registerClass(name, &ClassDef{Body: exactObj(propElem("x", num()), propElem("y", num()))})
	}

	t.Run("same class succeeds", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Empty(t, Messages(c.Constrain(cls("Point", false), cls("Point", false))))
	})

	t.Run("different unrelated classes reject", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		c.registerClass("Vec", &ClassDef{Body: exactObj(propElem("x", num()))})
		require.Equal(t,
			[]string{"cannot constrain Point <: Vec"},
			Messages(c.Constrain(cls("Point", false), cls("Vec", false))))
	})

	t.Run("subclass reaches superclass through the graph", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "A")
		c.registerClass("B", &ClassDef{
			Body:   exactObj(propElem("x", num()), propElem("y", num())),
			Supers: []*soltype.ClassType{cls("A", false)},
		})
		require.Empty(t, Messages(c.Constrain(cls("B", false), cls("A", false))))
		// The reverse never holds: a superclass instance is not one of its subclass.
		require.Equal(t,
			[]string{"cannot constrain A <: B"},
			Messages(c.Constrain(cls("A", false), cls("B", false))))
	})

	t.Run("transitive superclass across two edges", func(t *testing.T) {
		c := &Context{}
		c.registerClass("A", &ClassDef{Body: exactObj(propElem("x", num()))})
		c.registerClass("B", &ClassDef{Body: exactObj(propElem("x", num())), Supers: []*soltype.ClassType{cls("A", false)}})
		c.registerClass("C", &ClassDef{Body: exactObj(propElem("x", num())), Supers: []*soltype.ClassType{cls("B", false)}})
		require.Empty(t, Messages(c.Constrain(cls("C", false), cls("A", false))))
	})

	t.Run("class into inexact object projects the body", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Empty(t, Messages(c.Constrain(cls("Point", false), inexactObj(propElem("x", num())))))
	})

	t.Run("object into class rejects", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Equal(t,
			[]string{"cannot constrain object <: class Point"},
			Messages(c.Constrain(exactObj(propElem("x", num()), propElem("y", num())), cls("Point", false))))
	})
}

// TestConstrainNominalArgVariance covers the per-argument dispatch of the same-name
// rule. C1 treats every argument position as Invariant, so an argument must match in
// both directions; C2 replaces this with inferred variance.
func TestConstrainNominalArgVariance(t *testing.T) {
	newBox := func() *Context {
		c := &Context{}
		c.registerClass("Box", &ClassDef{
			TypeParams: []*soltype.TypeParam{{Name: "T", Var: &soltype.TypeVarType{ID: 100}}},
			Variance:   []Variance{Invariant},
			Body:       exactObj(propElem("value", &soltype.TypeVarType{ID: 100})),
		})
		return c
	}
	box := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Box", TypeArgs: []soltype.Type{arg}}
	}

	t.Run("equal arguments succeed", func(t *testing.T) {
		c := newBox()
		require.Empty(t, Messages(c.Constrain(box(numLit(5)), box(numLit(5)))))
	})

	t.Run("invariant argument rejects a widening", func(t *testing.T) {
		c := newBox()
		// Box<5> <: Box<number> fails: the invariant position also demands number <: 5.
		require.Equal(t,
			[]string{"cannot constrain number <: 5"},
			Messages(c.Constrain(box(numLit(5)), box(num()))))
	})
}

// TestInferBodyVariance covers C2's per-parameter variance measurement over a
// hand-built class body, so each occurrence shape is isolated: a field is an output
// position (covariant), a method value parameter is an input position (contravariant),
// both together are invariant, and a parameter used nowhere is bivariant. The method
// receiver `self` is excluded, so a method reading `self` does not drag its parameter to
// invariant.
func TestInferBodyVariance(t *testing.T) {
	// selfMethod builds a method whose receiver is the class instance at its own type
	// parameter, plus one value parameter and a return, so the walk sees a genuine `self`
	// it must exclude.
	selfMethod := func(name, cls string, tv *soltype.TypeVarType, param, ret soltype.Type) *soltype.MethodElem {
		self := &soltype.ClassType{Name: cls, TypeArgs: []soltype.Type{tv}}
		sig := &soltype.FuncType{
			SelfParam: &soltype.FuncParam{Pattern: &soltype.IdentPat{Name: "self"}, Type: self},
			Params:    []*soltype.FuncParam{{Pattern: &soltype.IdentPat{Name: "x"}, Type: param}},
			Ret:       ret,
		}
		return &soltype.MethodElem{Name: name, Signatures: []*soltype.FuncType{sig}}
	}
	// oneParam builds a single-type-parameter ClassDef so each case shares one var
	// pointer between the TypeParams entry and the body, matching how inferClassDecl
	// threads the same *TypeVarType through both.
	oneParam := func(build func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType)) *ClassDef {
		tv := &soltype.TypeVarType{ID: 1}
		body, supers := build(tv)
		return &ClassDef{
			TypeParams: []*soltype.TypeParam{{Name: "T", Var: tv}},
			Body:       body,
			Supers:     supers,
		}
	}
	tests := []struct {
		name string
		def  *ClassDef
		want []Variance
	}{
		{
			name: "field only is covariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("value", tv)), nil
			}),
			want: []Variance{Covariant},
		},
		{
			name: "method value parameter only is contravariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(selfMethod("accept", "Consumer", tv, tv, &soltype.Void{})), nil
			}),
			want: []Variance{Contravariant},
		},
		{
			name: "field and parameter together are invariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(
					propElem("value", tv),
					selfMethod("accept", "Cell", tv, tv, &soltype.Void{}),
				), nil
			}),
			want: []Variance{Invariant},
		},
		{
			name: "method returning the parameter is covariant despite the self receiver",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(selfMethod("get", "Box", tv, num(), tv)), nil
			}),
			want: []Variance{Covariant},
		},
		{
			name: "parameter used nowhere is bivariant",
			def: oneParam(func(_ *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("n", num())), nil
			}),
			want: []Variance{Bivariant},
		},
		{
			name: "parameter reaching a super is invariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("value", tv)),
					[]*soltype.ClassType{{Name: "Base", TypeArgs: []soltype.Type{tv}}}
			}),
			want: []Variance{Invariant},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, inferBodyVariance(tt.def))
		})
	}
}

// TestConstrainNominalVarianceDispatch drives the four variance lines the milestone
// pins Option 2 against `mut` with (§M5 Accept): a covariant Box widens, a contravariant
// Consumer does not, and either under a `mut` borrow is invariant because the RefType
// arm's bidirectional sweep forces both argument directions. The ClassDefs carry the
// variance directly, isolating constrain's per-position dispatch from the inference that
// TestInferBodyVariance covers.
func TestConstrainNominalVarianceDispatch(t *testing.T) {
	numOrStr := &soltype.UnionType{Types: []soltype.Type{num(), str()}}
	newCtx := func() *Context {
		c := &Context{}
		boxVar := &soltype.TypeVarType{ID: 100}
		c.registerClass("Box", &ClassDef{
			TypeParams: []*soltype.TypeParam{{Name: "T", Var: boxVar}},
			Variance:   []Variance{Covariant},
			Body:       exactObj(propElem("value", boxVar)),
		})
		consumerVar := &soltype.TypeVarType{ID: 101}
		c.registerClass("Consumer", &ClassDef{
			TypeParams: []*soltype.TypeParam{{Name: "T", Var: consumerVar}},
			Variance:   []Variance{Contravariant},
		})
		return c
	}
	box := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Box", TypeArgs: []soltype.Type{arg}}
	}
	consumer := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Consumer", TypeArgs: []soltype.Type{arg}}
	}

	t.Run("covariant Box widens", func(t *testing.T) {
		c := newCtx()
		require.Empty(t, Messages(c.Constrain(box(num()), box(numOrStr))))
	})
	t.Run("mut Box is invariant", func(t *testing.T) {
		c := newCtx()
		require.Equal(t,
			[]string{"cannot constrain string <: number"},
			Messages(c.Constrain(mutRef(box(num())), mutRef(box(numOrStr)))))
	})
	t.Run("contravariant Consumer rejects a widening", func(t *testing.T) {
		c := newCtx()
		require.Equal(t,
			[]string{"cannot constrain string <: number"},
			Messages(c.Constrain(consumer(num()), consumer(numOrStr))))
	})
	t.Run("contravariant Consumer accepts a narrowing", func(t *testing.T) {
		c := newCtx()
		require.Empty(t, Messages(c.Constrain(consumer(numOrStr), consumer(num()))))
	})
	t.Run("mut Consumer is invariant", func(t *testing.T) {
		c := newCtx()
		require.Equal(t,
			[]string{"cannot constrain string <: number"},
			Messages(c.Constrain(mutRef(consumer(numOrStr)), mutRef(consumer(num())))))
	})
}

// TestProjectClassBodyDoesNotMutateRegistry pins that projecting a class instance never
// writes back to the shared ClassDef.Body. A generic class whose body mentions none of
// its type parameters projects through the substitution path, where ObjectType.Accept
// returns the registry Body unchanged; setting the projected exactness must land on a
// fresh copy, not on that shared object.
func TestProjectClassBodyDoesNotMutateRegistry(t *testing.T) {
	c := &Context{}
	body := exactObj(propElem("n", num())) // the body does not mention the type parameter
	c.registerClass("Phantom", &ClassDef{
		TypeParams: []*soltype.TypeParam{{Name: "T", Var: &soltype.TypeVarType{ID: 200}}},
		Variance:   []Variance{Invariant},
		Body:       body,
	})

	proj, ok := c.projectClassBody(&soltype.ClassType{Name: "Phantom", TypeArgs: []soltype.Type{num()}})
	require.True(t, ok)
	require.NotSame(t, body, proj)  // a fresh wrapper, not the shared registry Body
	require.True(t, proj.Inexact)   // the non-final instance projects an inexact view
	require.False(t, body.Inexact)  // the registry Body stays exact — never mutated
}
