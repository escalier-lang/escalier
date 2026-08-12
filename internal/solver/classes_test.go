package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// cls builds a non-generic class instance handle for the nominal constrain tests.
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
//
// Each case asserts both measured vectors. want is the immutable view and wantMut the
// mutable view, and they differ only where a non-`readonly` field's write view adds an
// input position the immutable view does not have.
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
	// readonlyProp builds a `readonly` field, which no reference can write, so its write
	// view never reaches the mutable-view vector.
	readonlyProp := func(name string, t soltype.Type) *soltype.PropertyElem {
		return &soltype.PropertyElem{Name: name, Type: t, Readonly: true}
	}
	tests := []struct {
		name    string
		def     *ClassDef
		want    []Variance
		wantMut []Variance
	}{
		{
			name: "field only is covariant, and invariant under mut",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("value", tv)), nil
			}),
			want:    []Variance{Covariant},
			wantMut: []Variance{Invariant},
		},
		{
			name: "readonly field is covariant in both views",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(readonlyProp("value", tv)), nil
			}),
			want:    []Variance{Covariant},
			wantMut: []Variance{Covariant},
		},
		{
			name: "method value parameter only is contravariant in both views",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(selfMethod("accept", "Consumer", tv, tv, &soltype.UndefinedType{})), nil
			}),
			want:    []Variance{Contravariant},
			wantMut: []Variance{Contravariant},
		},
		{
			name: "field and parameter together are invariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(
					propElem("value", tv),
					selfMethod("accept", "Cell", tv, tv, &soltype.UndefinedType{}),
				), nil
			}),
			want:    []Variance{Invariant},
			wantMut: []Variance{Invariant},
		},
		{
			name: "method returning the parameter is covariant despite the self receiver",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(selfMethod("get", "Box", tv, num(), tv)), nil
			}),
			want:    []Variance{Covariant},
			wantMut: []Variance{Covariant},
		},
		{
			name: "a field write drags a method's covariant return to invariant under mut",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(
					propElem("value", tv),
					selfMethod("read", "Box", tv, num(), tv),
				), nil
			}),
			want:    []Variance{Covariant},
			wantMut: []Variance{Invariant},
		},
		{
			// A `mut` borrow is a read-write window on its pointee, so a parameter behind
			// one is invariant even when the field holding the borrow is `readonly`.
			name: "readonly field holding a mut borrow is invariant in both views",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				inner := &soltype.ClassType{Name: "Box", TypeArgs: []soltype.Type{tv}}
				return exactObj(readonlyProp("inner", mutRef(inner))), nil
			}),
			want:    []Variance{Invariant},
			wantMut: []Variance{Invariant},
		},
		{
			name: "parameter used nowhere is bivariant",
			def: oneParam(func(_ *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("n", num())), nil
			}),
			want:    []Variance{Bivariant},
			wantMut: []Variance{Bivariant},
		},
		{
			name: "parameter reaching a super is invariant",
			def: oneParam(func(tv *soltype.TypeVarType) (*soltype.ObjectType, []*soltype.ClassType) {
				return exactObj(propElem("value", tv)),
					[]*soltype.ClassType{{Name: "Base", TypeArgs: []soltype.Type{tv}}}
			}),
			want:    []Variance{Invariant},
			wantMut: []Variance{Invariant},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			immut, mut := inferBodyVariance(tt.def)
			require.Equal(t, tt.want, immut)
			require.Equal(t, tt.wantMut, mut)
		})
	}
}

// TestConstrainNominalVarianceDispatch drives constrain's per-position variance dispatch
// against a `mut` borrow. A mutable borrow selects the class's mutable-view vector rather
// than pinning every argument, so it tightens only the parameters a write through the
// borrow can reach:
//
//   - Box's parameter is a writable field, invariant in the mutable view, so `mut Box`
//     rejects the widening `Box<number>` accepts;
//   - Consumer's is a method value parameter, contravariant in both views, so `mut
//     Consumer` accepts the same narrowing an immutable Consumer does;
//   - Reader's is a method return, covariant in both views, so `mut Reader` accepts the
//     same widening an immutable Reader does.
//
// The ClassDefs carry both vectors directly, isolating constrain's dispatch from the
// inference that TestInferBodyVariance covers.
func TestConstrainNominalVarianceDispatch(t *testing.T) {
	numOrStr := &soltype.UnionType{Types: []soltype.Type{num(), str()}}
	newCtx := func() *Context {
		c := &Context{}
		boxVar := &soltype.TypeVarType{ID: 100}
		c.registerClass("Box", &ClassDef{
			TypeParams:  []*soltype.TypeParam{{Name: "T", Var: boxVar}},
			Variance:    []Variance{Covariant},
			MutVariance: []Variance{Invariant},
			Body:        exactObj(propElem("value", boxVar)),
		})
		consumerVar := &soltype.TypeVarType{ID: 101}
		c.registerClass("Consumer", &ClassDef{
			TypeParams:  []*soltype.TypeParam{{Name: "T", Var: consumerVar}},
			Variance:    []Variance{Contravariant},
			MutVariance: []Variance{Contravariant},
		})
		readerVar := &soltype.TypeVarType{ID: 102}
		c.registerClass("Reader", &ClassDef{
			TypeParams:  []*soltype.TypeParam{{Name: "T", Var: readerVar}},
			Variance:    []Variance{Covariant},
			MutVariance: []Variance{Covariant},
		})
		return c
	}
	box := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Box", TypeArgs: []soltype.Type{arg}}
	}
	consumer := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Consumer", TypeArgs: []soltype.Type{arg}}
	}
	reader := func(arg soltype.Type) *soltype.ClassType {
		return &soltype.ClassType{Name: "Reader", TypeArgs: []soltype.Type{arg}}
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
	t.Run("mut Consumer accepts a narrowing", func(t *testing.T) {
		c := newCtx()
		require.Empty(t, Messages(c.Constrain(mutRef(consumer(numOrStr)), mutRef(consumer(num())))))
	})
	t.Run("mut Consumer rejects a widening", func(t *testing.T) {
		c := newCtx()
		require.Equal(t,
			[]string{"cannot constrain string <: number"},
			Messages(c.Constrain(mutRef(consumer(num())), mutRef(consumer(numOrStr)))))
	})
	t.Run("mut Reader accepts a widening", func(t *testing.T) {
		c := newCtx()
		require.Empty(t, Messages(c.Constrain(mutRef(reader(num())), mutRef(reader(numOrStr)))))
	})
	t.Run("mut Reader rejects a narrowing", func(t *testing.T) {
		c := newCtx()
		require.Equal(t,
			[]string{"cannot constrain string <: number"},
			Messages(c.Constrain(mutRef(reader(numOrStr)), mutRef(reader(num())))))
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
	require.NotSame(t, body, proj) // a fresh wrapper, not the shared registry Body
	require.True(t, proj.Inexact)  // the non-final instance projects an inexact view
	require.False(t, body.Inexact) // the registry Body stays exact — never mutated
}

// nominalGraph registers the class hierarchy the nominal-meet cases share.
//
//	Shape           Vec     Printable      Loop ──┐
//	├── Point                   ▲           ▲     │
//	│   └── Pixel               │           └─────┘
//	├── Line                    │
//	└── Doc ── implements ──────┘
//
// It also registers one generic class per variance: covariant Reader, mutable-view
// invariant Box, contravariant Consumer, invariant Cell, and bivariant Ghost. Two
// further classes extend a generic one. `Wrapper<T> extends Reader<T>` substitutes
// its own argument into the edge, and `NumReader extends Reader<number>` fixes the
// argument at the declaration.
func nominalGraph() *Context {
	c := &Context{}
	for _, name := range []string{"Shape", "Vec", "Printable"} {
		c.registerClass(name, &ClassDef{})
	}
	c.registerClass("Point", &ClassDef{Supers: []*soltype.ClassType{cls("Shape", false)}})
	c.registerClass("Pixel", &ClassDef{Supers: []*soltype.ClassType{cls("Point", false)}})
	c.registerClass("Line", &ClassDef{Supers: []*soltype.ClassType{cls("Shape", false)}})
	c.registerClass("Doc", &ClassDef{
		Supers:     []*soltype.ClassType{cls("Shape", false)},
		Implements: []*soltype.ClassType{cls("Printable", false)},
	})
	// A class extending itself is not something the checker builds. Registering one here
	// pins that the walk stops on a cyclic edge instead of recurring forever.
	c.registerClass("Loop", &ClassDef{Supers: []*soltype.ClassType{cls("Loop", false)}})
	// Pending is the shell the SCC pre-pass registers for a class in a mutually recursive
	// component, before its `extends` clause is resolved. BelowPending sits under it, so a
	// walk from there runs into the unresolved edge one step up.
	c.registerClass("Pending", &ClassDef{EdgesPending: true})
	c.registerClass("BelowPending", &ClassDef{Supers: []*soltype.ClassType{cls("Pending", false)}})

	generic := func(name string, immut, mut Variance, id int) {
		c.registerClass(name, &ClassDef{
			TypeParams:  []*soltype.TypeParam{{Name: "T", Var: &soltype.TypeVarType{ID: id}}},
			Variance:    []Variance{immut},
			MutVariance: []Variance{mut},
		})
	}
	generic("Reader", Covariant, Covariant, 300)
	generic("Box", Covariant, Invariant, 301)
	generic("Consumer", Contravariant, Contravariant, 302)
	generic("Cell", Invariant, Invariant, 303)
	generic("Ghost", Bivariant, Bivariant, 304)

	wrapperVar := &soltype.TypeVarType{ID: 305}
	c.registerClass("Wrapper", &ClassDef{
		TypeParams:  []*soltype.TypeParam{{Name: "T", Var: wrapperVar}},
		Variance:    []Variance{Covariant},
		MutVariance: []Variance{Covariant},
		Supers:      []*soltype.ClassType{genericCls("Reader", wrapperVar)},
	})
	c.registerClass("NumReader", &ClassDef{
		Supers: []*soltype.ClassType{genericCls("Reader", num())},
	})
	return c
}

// genericCls builds an instance handle for a generic class at the given arguments.
func genericCls(name string, args ...soltype.Type) *soltype.ClassType {
	return &soltype.ClassType{Name: name, TypeArgs: args}
}

// TestGlbClass covers the nominal meet of two class tags. want is the annotation the
// fused tag renders under, and an empty want says the pair admits no exact fusion, so
// the intersection keeps both tags as separate atoms.
func TestGlbClass(t *testing.T) {
	numOrStr := &soltype.UnionType{Types: []soltype.Type{num(), str()}}

	tests := []struct {
		name string
		a, b *soltype.ClassType
		want string
	}{
		{
			name: "one class met with itself is that class",
			a:    cls("Point", false), b: cls("Point", false),
			want: "Point",
		},
		{
			name: "a subclass met with its superclass is the subclass",
			a:    cls("Point", false), b: cls("Shape", false),
			want: "Point",
		},
		{
			name: "the superclass may be written first",
			a:    cls("Shape", false), b: cls("Point", false),
			want: "Point",
		},
		{
			name: "a subclass two edges down still reaches",
			a:    cls("Pixel", false), b: cls("Shape", false),
			want: "Pixel",
		},
		{
			name: "two unrelated classes have no common instance",
			a:    cls("Point", false), b: cls("Vec", false),
			want: "never",
		},
		{
			name: "two siblings under one superclass are unrelated to each other",
			a:    cls("Point", false), b: cls("Line", false),
			want: "never",
		},
		{
			name: "a cyclic extends edge terminates and reports no relation",
			a:    cls("Loop", false), b: cls("Vec", false),
			want: "never",
		},
		{
			name: "an unregistered class settles nothing",
			a:    cls("Point", false), b: cls("Unregistered", false),
			want: "",
		},
		{
			name: "a class whose edges are still pending settles nothing",
			a:    cls("Pending", false), b: cls("Vec", false),
			want: "",
		},
		{
			name: "a pending class along the walk settles nothing for the class below it",
			a:    cls("BelowPending", false), b: cls("Vec", false),
			want: "",
		},
		{
			name: "an implemented interface is not above the class that implements it",
			a:    cls("Doc", false), b: cls("Printable", false),
			want: "never",
		},
		{
			name: "an interface is unordered against the superclass of an implementor",
			a:    cls("Shape", false), b: cls("Printable", false),
			want: "never",
		},
		{
			name: "two tags disagreeing on exactness stay separate",
			a:    cls("Point", true), b: cls("Point", false),
			want: "",
		},
		{
			name: "a covariant position meets its two arguments",
			a:    genericCls("Reader", num()), b: genericCls("Reader", numOrStr),
			want: "Reader<number>",
		},
		{
			name: "a covariant position may meet to never",
			a:    genericCls("Reader", num()), b: genericCls("Reader", str()),
			want: "Reader<never>",
		},
		{
			name: "a contravariant position joins its two arguments",
			a:    genericCls("Consumer", num()), b: genericCls("Consumer", str()),
			want: "Consumer<number | string>",
		},
		{
			name: "an invariant position fuses only equal arguments",
			a:    genericCls("Cell", num()), b: genericCls("Cell", num()),
			want: "Cell<number>",
		},
		{
			name: "an invariant position with differing arguments stays separate",
			a:    genericCls("Cell", num()), b: genericCls("Cell", str()),
			want: "",
		},
		{
			name: "a bivariant position imposes nothing on its arguments",
			a:    genericCls("Ghost", num()), b: genericCls("Ghost", str()),
			want: "Ghost<number>",
		},
		{
			name: "a position a write reaches is dispatched as invariant",
			a:    genericCls("Box", num()), b: genericCls("Box", numOrStr),
			want: "",
		},
		{
			name: "an inherited argument is substituted along the edge",
			a:    genericCls("Wrapper", numLit(5)), b: genericCls("Reader", num()),
			want: "Wrapper<5>",
		},
		{
			name: "an inherited argument the superclass rules out stays separate",
			a:    genericCls("Wrapper", str()), b: genericCls("Reader", num()),
			want: "",
		},
		{
			name: "an edge declared at a fixed argument reaches its superclass",
			a:    cls("NumReader", false), b: genericCls("Reader", numOrStr),
			want: "NumReader",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			met, ok := nominalGraph().glbClass(test.a, test.b)
			if test.want == "" {
				require.False(t, ok)
				require.Nil(t, met)
				return
			}
			require.True(t, ok)
			require.Equal(t, test.want, soltype.Print(met))
		})
	}
}

// TestGlbClassInNormalForm covers what the meet's three answers do to the conjunct
// holding the tags, which is the reason the meet exists.
func TestGlbClassInNormalForm(t *testing.T) {
	c := nominalGraph()
	meet := func(members ...soltype.Type) soltype.Type {
		return newIntersection(nil, members)
	}

	t.Run("unrelated tags drop the conjunct before any structural work", func(t *testing.T) {
		require.Equal(t, "never", normDNF(c, meet(cls("Point", false), cls("Vec", false))))
	})

	t.Run("a subclass absorbs its superclass tag", func(t *testing.T) {
		require.Equal(t, "Pixel", normDNF(c, meet(cls("Pixel", false), cls("Shape", false))))
	})

	t.Run("a tag and a structural atom fill both slots of one conjunct", func(t *testing.T) {
		mixed := meet(cls("Point", false), parseType(t, "fn (x: number) -> string"))
		d := c.mkDNF(mixed, soltype.Positive)
		require.Len(t, d.Conjuncts, 1)
		require.Len(t, d.Conjuncts[0].Lnf.Atoms, 2)
		base, ok := d.Conjuncts[0].Lnf.Base()
		require.True(t, ok)
		require.Equal(t, "Point", soltype.Print(base))
		require.Equal(t, "(fn (x: number) -> string) & Point", soltype.Print(d.toType()))
	})

	t.Run("two tags of one class survive when no argument stands for the meet", func(t *testing.T) {
		conflicting := meet(genericCls("Cell", num()), genericCls("Cell", str()))
		d := c.mkDNF(conflicting, soltype.Positive)
		require.Len(t, d.Conjuncts, 1)
		require.Len(t, d.Conjuncts[0].Lnf.Atoms, 2)
		_, ok := d.Conjuncts[0].Lnf.Base()
		require.False(t, ok)
	})

	t.Run("an unrelated tag drops one member of a union and keeps the rest", func(t *testing.T) {
		either := newUnion(nil, []soltype.Type{
			meet(cls("Point", false), cls("Vec", false)),
			cls("Line", false),
		}, false)
		require.Equal(t, "Line", normDNF(c, either))
	})
}

// TestNominalSubtype covers the pure subtype query the nominal meet asks, which
// decides the same declared graph constrainNominal does without recording a bound.
func TestNominalSubtype(t *testing.T) {
	c := nominalGraph()

	tests := []struct {
		name       string
		sub, super *soltype.ClassType
		want       bool
	}{
		{name: "a class is below itself", sub: cls("Point", false), super: cls("Point", false), want: true},
		{name: "a subclass is below its superclass", sub: cls("Point", false), super: cls("Shape", false), want: true},
		{name: "a superclass is not below its subclass", sub: cls("Shape", false), super: cls("Point", false), want: false},
		{name: "two edges still reach", sub: cls("Pixel", false), super: cls("Shape", false), want: true},
		{name: "siblings are unrelated", sub: cls("Point", false), super: cls("Line", false), want: false},
		{name: "an implemented interface is not a nominal supertype", sub: cls("Doc", false), super: cls("Printable", false), want: false},
		{name: "an implementor is still below its superclass", sub: cls("Doc", false), super: cls("Shape", false), want: true},
		{name: "a covariant argument may narrow", sub: genericCls("Reader", numLit(5)), super: genericCls("Reader", num()), want: true},
		{name: "a covariant argument may not widen", sub: genericCls("Reader", num()), super: genericCls("Reader", numLit(5)), want: false},
		{name: "an inherited argument is checked at the superclass", sub: genericCls("Wrapper", numLit(5)), super: genericCls("Reader", num()), want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, c.nominalSubtype(test.sub, test.super))
		})
	}
}
