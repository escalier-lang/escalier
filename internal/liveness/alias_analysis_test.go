package liveness

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestDetermineAliasSource_Identifier(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = 1

	source := DetermineAliasSource(ident)

	require.Equal(t, AliasSourceVariable, source.Kind)
	require.Equal(t, []VarID{1}, source.VarIDs)
}

func TestDetermineAliasSource_NonLocalIdentifier(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = -1

	source := DetermineAliasSource(ident)

	require.Equal(t, AliasSourceUnknown, source.Kind)
}

func TestDetermineAliasSource_UnsetIdentifier(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	// VarID defaults to 0

	source := DetermineAliasSource(ident)

	require.Equal(t, AliasSourceUnknown, source.Kind)
}

func TestDetermineAliasSource_Literal(t *testing.T) {
	lit := ast.NewLitExpr(ast.NewNumber(42, ast.Span{}))

	source := DetermineAliasSource(lit)

	require.Equal(t, AliasSourceFresh, source.Kind)
	require.Empty(t, source.VarIDs)
}

func TestDetermineAliasSource_ObjectExpr(t *testing.T) {
	obj := ast.NewObject(nil, ast.Span{})

	source := DetermineAliasSource(obj)

	require.Equal(t, AliasSourceFresh, source.Kind)
}

func TestDetermineAliasSource_TupleExpr(t *testing.T) {
	tuple := ast.NewArray(nil, ast.Span{})

	source := DetermineAliasSource(tuple)

	require.Equal(t, AliasSourceFresh, source.Kind)
}

func TestDetermineAliasSource_CallExpr(t *testing.T) {
	call := ast.NewCall(ast.NewIdent("foo", ast.Span{}), nil, false, ast.Span{})

	source := DetermineAliasSource(call)

	require.Equal(t, AliasSourceFresh, source.Kind)
}

func TestDetermineAliasSource_BinaryExpr(t *testing.T) {
	bin := ast.NewBinary(
		ast.NewLitExpr(ast.NewNumber(1, ast.Span{})),
		ast.NewLitExpr(ast.NewNumber(2, ast.Span{})),
		ast.Plus,
		ast.Span{},
	)

	source := DetermineAliasSource(bin)

	require.Equal(t, AliasSourceFresh, source.Kind)
}

func TestDetermineAliasSource_MemberExpr(t *testing.T) {
	member := ast.NewMember(
		ast.NewIdent("obj", ast.Span{}),
		ast.NewIdentifier("field", ast.Span{}),
		false,
		ast.Span{},
	)

	source := DetermineAliasSource(member)

	require.Equal(t, AliasSourceUnknown, source.Kind)
}

func TestDetermineAliasSource_TypeCast(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = 3
	cast := ast.NewTypeCast(ident, nil, ast.Span{})

	source := DetermineAliasSource(cast)

	require.Equal(t, AliasSourceVariable, source.Kind)
	require.Equal(t, []VarID{3}, source.VarIDs)
}

// Integration tests: alias tracking with DetermineAliasSource

func TestAliasTracking_ValBEqualsA(t *testing.T) {
	// val a = {x: 1}; val b = a → a and b in same alias set
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)

	aSets := tracker.GetAliasSets(a)
	bSets := tracker.GetAliasSets(b)
	require.Len(t, aSets, 1)
	require.Len(t, bSets, 1)
	require.Equal(t, aSets[0].ID, bSets[0].ID)
	require.Len(t, aSets[0].Members, 2)
}

func TestAliasTracking_FreshValue(t *testing.T) {
	// val b = {x: 1} → b gets a fresh alias set
	tracker := NewAliasTracker()
	var b VarID = 1

	tracker.NewValue(b, AliasImmutable)

	sets := tracker.GetAliasSets(b)
	require.Len(t, sets, 1)
	require.Equal(t, b, sets[0].Origin)
	require.Len(t, sets[0].Members, 1)
}

func TestAliasTracking_ReassignToFresh(t *testing.T) {
	// var b = a; b = {x: 1} → b leaves a's set after reassignment
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)

	// Verify they start in the same set
	require.Equal(t,
		tracker.GetAliasSets(a)[0].ID,
		tracker.GetAliasSets(b)[0].ID,
	)

	// Reassign b to a fresh value
	tracker.Reassign(b, nil, AliasImmutable)

	// b should have its own set now
	aSets := tracker.GetAliasSets(a)
	bSets := tracker.GetAliasSets(b)
	require.Len(t, aSets, 1)
	require.Len(t, bSets, 1)
	require.NotEqual(t, aSets[0].ID, bSets[0].ID)
	require.NotContains(t, aSets[0].Members, b)
}

func TestAliasTracking_ReassignToOtherVar(t *testing.T) {
	// var b = a; b = c → b leaves a's set and joins c's set
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.NewValue(c, AliasImmutable)

	// Reassign b to c
	tracker.Reassign(b, &c, AliasImmutable)

	aSets := tracker.GetAliasSets(a)
	bSets := tracker.GetAliasSets(b)
	cSets := tracker.GetAliasSets(c)

	require.NotContains(t, aSets[0].Members, b, "b should not be in a's set")
	require.Equal(t, bSets[0].ID, cSets[0].ID, "b should be in c's set")
}

func TestAliasTracking_MultipleAliases(t *testing.T) {
	// val b = a; val c = a → a, b, c all in same set
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, a, AliasImmutable)

	sets := tracker.GetAliasSets(a)
	require.Len(t, sets, 1)
	require.Len(t, sets[0].Members, 3)
	require.Contains(t, sets[0].Members, a)
	require.Contains(t, sets[0].Members, b)
	require.Contains(t, sets[0].Members, c)
}

func TestAliasTracking_Chain(t *testing.T) {
	// val b = a; val c = b → a, b, c all in same set
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, b, AliasImmutable)

	aSets := tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.Len(t, aSets[0].Members, 3)
	require.Contains(t, aSets[0].Members, a)
	require.Contains(t, aSets[0].Members, b)
	require.Contains(t, aSets[0].Members, c)
}

func TestAliasTracking_Shadowing(t *testing.T) {
	// val x = a; val x = {y: 1}
	// With distinct VarIDs: first x (VarID=2) stays in a's set,
	// second x (VarID=3) gets a fresh set.
	tracker := NewAliasTracker()
	var a VarID = 1
	var x1 VarID = 2 // first x
	var x2 VarID = 3 // second x (shadow)

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(x1, a, AliasImmutable)
	tracker.NewValue(x2, AliasImmutable)

	// x1 should still be in a's set
	aSets := tracker.GetAliasSets(a)
	require.Len(t, aSets, 1)
	require.Contains(t, aSets[0].Members, x1)
	require.NotContains(t, aSets[0].Members, x2)

	// x2 should have its own set
	x2Sets := tracker.GetAliasSets(x2)
	require.Len(t, x2Sets, 1)
	require.Equal(t, x2, x2Sets[0].Origin)
	require.NotContains(t, x2Sets[0].Members, x1)
}
