package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPrintAliasType renders an alias reference under its name, bare or with a type
// argument list, stripping the dep_graph namespace prefix the way a class name is.
func TestPrintAliasType(t *testing.T) {
	require.Equal(t, "Point", Print(&AliasType{Name: "Point"}))
	require.Equal(t, "Point", Print(&AliasType{Name: "Geometry.Point"}))
	require.Equal(t, "Box<number>", Print(&AliasType{Name: "Box", TypeArgs: []Type{numP()}}))
	require.Equal(t, "Pair<number, string>", Print(&AliasType{Name: "Pair", TypeArgs: []Type{numP(), strP()}}))
}

// TestPrintAliasBorrow renders a borrow whose inner is an alias, since AliasType is a
// RefInner that flows through the borrow machinery like a class instance.
func TestPrintAliasBorrow(t *testing.T) {
	require.Equal(t, "&Point", Print(&RefType{Lt: Anon, Inner: &AliasType{Name: "Point"}}))
	require.Equal(t, "mut Point", Print(&RefType{Mut: true, Inner: &AliasType{Name: "Point"}}))
}

// TestLevelOfAliasType takes the max level over the alias's type arguments, and is 0
// for a bare reference whose name carries no variables.
func TestLevelOfAliasType(t *testing.T) {
	require.Equal(t, 0, LevelOf(&AliasType{Name: "Point"}), "no arguments ⇒ level 0")
	alias := &AliasType{Name: "Box", TypeArgs: []Type{
		&TypeVarType{ID: 1, Level: 3},
		&TypeVarType{ID: 2, Level: 6},
	}}
	require.Equal(t, 6, LevelOf(alias), "the level is the max over the type arguments")
}

// skipAlias replaces an AliasType with repl and skips its children, exercising the
// SkipChildren arm of AliasType.Accept.
type skipAlias struct{ repl Type }

func (v skipAlias) EnterType(t Type, _ Polarity) EnterResult {
	if _, ok := t.(*AliasType); ok {
		return EnterResult{Type: v.repl, SkipChildren: true}
	}
	return EnterResult{}
}
func (skipAlias) ExitType(t Type, _ Polarity) Type { return t }

// TestAcceptAliasType rebuilds an AliasType only when a type argument changes, reuses
// the pointer when nothing changes, and honors a SkipChildren replacement.
func TestAcceptAliasType(t *testing.T) {
	a := &TypeVarType{ID: 1}
	str := &PrimType{Prim: StrPrim}
	alias := &AliasType{Name: "Box", TypeArgs: []Type{a}}

	got := alias.Accept(&replaceVar{target: a, repl: str}, Positive).(*AliasType)
	require.NotSame(t, alias, got, "a changed argument forces a new token")
	require.Equal(t, "Box", got.Name, "the name carries through")
	require.Same(t, str, got.TypeArgs[0], "the changed argument took the replacement")

	require.Same(t, alias, alias.Accept(identityVisitor{}, Positive), "an unchanged AliasType keeps its pointer")

	num := numP()
	require.Same(t, num, alias.Accept(skipAlias{repl: num}, Positive), "SkipChildren hands the replacement straight through")
}

// TestPrintAsSchemeAliasArg names a type variable that appears inside an alias
// reference's arguments, so freeTypeVars descends into AliasType.TypeArgs.
func TestPrintAsSchemeAliasArg(t *testing.T) {
	a := &TypeVarType{ID: 1}
	fn := &FuncType{
		Params: []*FuncParam{identP("x", &AliasType{Name: "Box", TypeArgs: []Type{a}})},
		Ret:    &AliasType{Name: "Box", TypeArgs: []Type{a}},
	}
	require.Equal(t, "fn <T0>(x: Box<T0>) -> Box<T0>", PrintAsScheme(fn))
}

// TestAliasTypeInterfaces asserts AliasType satisfies the sealed Type and RefInner
// interfaces, so it is a first-class type and a borrowable inner.
func TestAliasTypeInterfaces(t *testing.T) {
	var _ Type = &AliasType{}
	var _ RefInner = &AliasType{}
	a := &AliasType{}
	a.isType()
	a.isRefInner()
}
