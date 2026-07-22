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

// TestPrintQualifiedAliasType renders an alias and class under their full dep_graph
// namespace prefix rather than the stripped display name, so a solver identity key formed
// from PrintQualified keeps two same-local-name nominal types in different namespaces
// distinct. A nested class argument is qualified too, so `Box<A.Point>` and `Box<B.Point>`
// never collide on one key.
func TestPrintQualifiedAliasType(t *testing.T) {
	require.Equal(t, "Geometry.Point", PrintQualified(&AliasType{Name: "Geometry.Point"}))
	require.NotEqual(t,
		PrintQualified(&AliasType{Name: "Box", TypeArgs: []Type{&ClassType{Name: "A.Point"}}}),
		PrintQualified(&AliasType{Name: "Box", TypeArgs: []Type{&ClassType{Name: "B.Point"}}}),
		"a nested class argument keeps its namespace so cross-namespace types do not collide",
	)
	require.Equal(t,
		"Box<A.Point>",
		PrintQualified(&AliasType{Name: "Box", TypeArgs: []Type{&ClassType{Name: "A.Point"}}}),
	)
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
	require.NotSame(t, alias, got, "a changed argument forces a new type")
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

// TestPrintAliasLifetimeArgs renders a lifetime-generic alias reference: the lifetime
// argument 'a renders before any type argument, so `Foo<'a>` and `Foo<'a, number>` show
// their lifetime first. PrintAsScheme names the free lifetime argument 'a.
func TestPrintAliasLifetimeArgs(t *testing.T) {
	lx := &LifetimeVar{ID: 0, Level: 1}
	require.Equal(t, "Foo<'a>", PrintAsScheme(&AliasType{Name: "Foo", LifetimeArgs: []Lifetime{lx}}))
	require.Equal(t, "Foo<'a, number>", PrintAsScheme(&AliasType{
		Name:         "Foo",
		LifetimeArgs: []Lifetime{lx},
		TypeArgs:     []Type{numP()},
	}))
}

// TestPrintQualifiedAliasLifetimeArgsDistinct is the distinctness property the LifetimeArgs
// field exists for: two references to one alias at different lifetimes serialize to different
// identity keys, so `Foo<'a>` and `Foo<'b>` never collide on the intern-cache key the
// recursion guard uses. Keying without the lifetime arguments would let two borrows of one
// alias at different lifetimes share a cache entry.
func TestPrintQualifiedAliasLifetimeArgsDistinct(t *testing.T) {
	la := &LifetimeVar{ID: 0, Level: 1}
	lb := &LifetimeVar{ID: 1, Level: 1}
	require.NotEqual(t,
		PrintQualified(&AliasType{Name: "Foo", LifetimeArgs: []Lifetime{la}}),
		PrintQualified(&AliasType{Name: "Foo", LifetimeArgs: []Lifetime{lb}}),
		"two lifetimes render to distinct identity keys",
	)
}

// TestLevelOfAliasLifetimeArgs folds an alias reference's lifetime argument into its level,
// so a free lifetime argument lifts the level the same way a type argument does.
func TestLevelOfAliasLifetimeArgs(t *testing.T) {
	alias := &AliasType{
		Name:         "Foo",
		LifetimeArgs: []Lifetime{&LifetimeVar{ID: 0, Level: 7}},
		TypeArgs:     []Type{&TypeVarType{ID: 1, Level: 3}},
	}
	require.Equal(t, 7, LevelOf(alias), "the level is the max over the lifetime and type arguments")
}

// TestAcceptAliasLifetimeArgsCopyOnWrite rewrites a type argument inside an alias reference,
// rebuilds the AliasType, carries the lifetime argument through unchanged, and replaces the
// type argument. Lifetimes are not Types, so Accept never walks the lifetime argument.
func TestAcceptAliasLifetimeArgsCopyOnWrite(t *testing.T) {
	str := &PrimType{Prim: StrPrim}
	a := &TypeVarType{ID: 1}
	lx := &LifetimeVar{ID: 0, Level: 1}
	alias := &AliasType{Name: "Foo", LifetimeArgs: []Lifetime{lx}, TypeArgs: []Type{a}}

	got := alias.Accept(&replaceVar{target: a, repl: str}, Positive).(*AliasType)

	require.NotSame(t, alias, got, "a changed type argument forces a new AliasType")
	require.Same(t, lx, got.LifetimeArgs[0], "the lifetime argument carries through unchanged")
	require.Same(t, str, got.TypeArgs[0], "the type argument took the replacement")
}

// TestFreeLifetimeVarsAliasLifetimeArg collects a lifetime-generic alias reference's lifetime
// argument, so `Foo<'a>` surfaces 'a as a free lifetime the scheme quantifies.
func TestFreeLifetimeVarsAliasLifetimeArg(t *testing.T) {
	lx := &LifetimeVar{ID: 0, Level: 1}
	alias := &AliasType{Name: "Foo", LifetimeArgs: []Lifetime{lx}, TypeArgs: []Type{numP()}}
	require.Equal(t, []*LifetimeVar{lx}, freeLifetimeVars(alias))
}
