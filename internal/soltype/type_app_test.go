package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// skipNode replaces the node identical to target with repl and walks none of its children.
type skipNode struct {
	target Type
	repl   Type
}

func (v *skipNode) EnterType(t Type, _ Polarity) EnterResult {
	if t == v.target {
		return EnterResult{Type: v.repl, SkipChildren: true}
	}
	return EnterResult{}
}
func (v *skipNode) ExitType(t Type, _ Polarity) Type { return t }

// TestTypeCtorApply covers a constructor's arity, its name, and filling its holes. A partial
// constructor keeps its fixed arguments in place, and an argument count other than its arity or
// a base that is neither a class nor an alias applies to nothing.
func TestTypeCtorApply(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	box := &TypeCtorType{Base: &ClassType{Name: "Box"}, Fixed: []Type{nil}}
	tagged := &TypeCtorType{Base: &AliasType{Name: "Tagged"}, Fixed: []Type{str, nil}}

	require.Equal(t, 1, box.Arity())
	require.Equal(t, 1, tagged.Arity())
	require.Equal(t, "Box", box.Name())
	require.Equal(t, "Tagged", tagged.Name())
	require.Equal(t, "", (&TypeCtorType{Base: num}).Name())

	inst, ok := box.Apply([]Type{num})
	require.True(t, ok)
	require.Equal(t, "Box<number>", Print(inst))
	inst, ok = tagged.Apply([]Type{num})
	require.True(t, ok)
	require.Equal(t, "Tagged<string, number>", Print(inst))

	_, ok = box.Apply([]Type{num, str})
	require.False(t, ok)
	_, ok = (&TypeCtorType{Base: num, Fixed: []Type{nil}}).Apply([]Type{num})
	require.False(t, ok)
}

// TestTypeAppPrint covers how an application and a constructor render. A skolem head renders
// under its name, a whole constructor renders bare, and a partial one renders its holes as `_`.
// An application binds like an atom inside a union.
func TestTypeAppPrint(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	f := &SkolemType{ID: 1, Name: "F"}
	app := &TypeAppType{Head: f, Args: []Type{num}}

	require.Equal(t, "F<number>", Print(app))
	require.Equal(t, "F<number> | string", Print(&UnionType{Types: []Type{app, str}}))
	require.Equal(t, "Box", Print(&TypeCtorType{Base: &ClassType{Name: "Box"}, Fixed: []Type{nil}}))
	require.Equal(t, "Tagged<string, _>",
		Print(&TypeCtorType{Base: &ClassType{Name: "Tagged"}, Fixed: []Type{str, nil}}))
}

// TestTypeAppAccept covers how the visitor rebuilds an application and a constructor. A head
// substituted with a constructor gives the instance the constructor names, a rewritten fixed
// argument gives a new constructor, and a replacement under SkipChildren is returned as is.
func TestTypeAppAccept(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	str := &PrimType{Prim: StrPrim}
	f := &TypeVarType{ID: 1}
	x := &TypeVarType{ID: 2}
	box := &TypeCtorType{Base: &ClassType{Name: "Box"}, Fixed: []Type{nil}}
	app := &TypeAppType{Head: f, Args: []Type{num}}

	applied := app.Accept(&replaceVar{target: f, repl: box}, Positive)
	require.Equal(t, "Box<number>", Print(applied))

	// A walk that changes nothing returns the same node.
	require.Same(t, app, app.Accept(identityVisitor{}, Positive))

	partial := &TypeCtorType{Base: &ClassType{Name: "Tagged"}, Fixed: []Type{x, nil}}
	rewritten := partial.Accept(&replaceVar{target: x, repl: str}, Positive)
	require.Equal(t, "Tagged<string, _>", Print(rewritten))
	require.Same(t, partial, partial.Accept(identityVisitor{}, Positive))

	require.Same(t, num, Type(app).Accept(&skipNode{target: app, repl: num}, Positive))
	require.Same(t, num, Type(partial).Accept(&skipNode{target: partial, repl: num}, Positive))
}

// TestTypeAppLevel covers the level of an application and a constructor, which is the highest
// level among the variables they hold.
func TestTypeAppLevel(t *testing.T) {
	f := &TypeVarType{ID: 1, Level: 3}
	a := &TypeVarType{ID: 2, Level: 5}
	require.Equal(t, 5, LevelOf(&TypeAppType{Head: f, Args: []Type{a}}))
	require.Equal(t, 3, LevelOf(&TypeAppType{Head: f, Args: []Type{&PrimType{Prim: NumPrim}}}))
	require.Equal(t, 5, LevelOf(&TypeCtorType{Base: &ClassType{Name: "Tagged"}, Fixed: []Type{a, nil}}))
}

// TestNewTypeAppLatticeHead covers a head that coalesced to a union or an intersection. The
// constructors among its members are applied and every other member is dropped. A head with no
// constructor among its members stays an application.
func TestNewTypeAppLatticeHead(t *testing.T) {
	num := &PrimType{Prim: NumPrim}
	v := &TypeVarType{ID: 1}
	box := &TypeCtorType{Base: &ClassType{Name: "Box"}, Fixed: []Type{nil}}
	arr := &TypeCtorType{Base: &ClassType{Name: "Array"}, Fixed: []Type{nil}}
	args := []Type{num}

	require.Equal(t, "Box<number>", Print(NewTypeApp(&UnionType{Types: []Type{v, box}}, args, nil)))
	require.Equal(t, "Box<number> | Array<number>",
		Print(NewTypeApp(&UnionType{Types: []Type{box, v, arr}}, args, nil)))
	require.Equal(t, "Box<number>", Print(NewTypeApp(&IntersectionType{Types: []Type{v, box}}, args, nil)))
	require.Equal(t, "Box<number> & Array<number>",
		Print(NewTypeApp(&IntersectionType{Types: []Type{box, arr}}, args, nil)))
	_, stays := NewTypeApp(&UnionType{Types: []Type{v, &TypeVarType{ID: 2}}}, args, nil).(*TypeAppType)
	require.True(t, stays)
}

// TestPrintSchemeConstructorParam asserts that a declared type-constructor parameter keeps its
// `<_>` in a scheme's binder list.
func TestPrintSchemeConstructorParam(t *testing.T) {
	f := &TypeVarType{ID: 1, Level: 2}
	app := &TypeAppType{Head: f, Args: []Type{&PrimType{Prim: NumPrim}}}
	quantified := func(v *TypeVarType) bool { return v == f }
	got := PrintAsSchemeWith(app, quantified, nil, []*TypeParam{{Name: "F", Var: f, Slots: []SlotVariance{SlotInvariant}}}, nil)
	require.Equal(t, "<F<_>> F<number>", got)
}
