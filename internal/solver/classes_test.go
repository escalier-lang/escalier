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

	t.Run("non-final class into exact object rejects", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Equal(t,
			[]string{"cannot constrain class Point <: exact object"},
			Messages(c.Constrain(cls("Point", false), exactObj(propElem("x", num()), propElem("y", num())))))
	})

	t.Run("final class into matching exact object succeeds", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Empty(t, Messages(c.Constrain(cls("Point", true), exactObj(propElem("x", num()), propElem("y", num())))))
	})

	t.Run("final class into exact object with an extra member rejects", func(t *testing.T) {
		c := &Context{}
		registerPoint(c, "Point")
		require.Equal(t,
			[]string{"object has extra property: y"},
			Messages(c.Constrain(cls("Point", true), exactObj(propElem("x", num())))))
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
