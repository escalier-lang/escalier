package checker

import (
	"context"
	"testing"
	"time"

	ts "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestFlatUnionFlattensNestedUnions(t *testing.T) {
	strType := ts.NewStrPrimType(nil)
	numType := ts.NewNumPrimType(nil)
	boolType := ts.NewBoolPrimType(nil)

	// Simulate 3-step widening: string, then number, then boolean.
	// After step 1-2: Union(string, number)
	union2 := flatUnion(strType, numType)
	assertFlatUnionMembers(t, union2, []ts.Type{strType, numType})

	// After step 3: should be Union(string, number, boolean), NOT Union(Union(string, number), boolean)
	union3 := flatUnion(union2, boolType)
	assertFlatUnionMembers(t, union3, []ts.Type{strType, numType, boolType})
}

func TestFlatUnionFlattensNewTypeUnion(t *testing.T) {
	strType := ts.NewStrPrimType(nil)
	numType := ts.NewNumPrimType(nil)
	boolType := ts.NewBoolPrimType(nil)

	// newType is itself a union — flatUnion should flatten both sides.
	newUnion := ts.NewUnionType(nil, numType, boolType)
	result := flatUnion(strType, newUnion)
	assertFlatUnionMembers(t, result, []ts.Type{strType, numType, boolType})
}

func TestFlatUnionDeduplicatesSharedMembers(t *testing.T) {
	strType := ts.NewStrPrimType(nil)
	numType := ts.NewNumPrimType(nil)
	boolType := ts.NewBoolPrimType(nil)

	// oldType and newType share "number" — the result should not have duplicates.
	oldUnion := ts.NewUnionType(nil, strType, numType)
	newUnion := ts.NewUnionType(nil, numType, boolType)
	result := flatUnion(oldUnion, newUnion)
	assertFlatUnionMembers(t, result, []ts.Type{strType, numType, boolType})
}

func TestTypeContainsFindsNestedMembers(t *testing.T) {
	strType := ts.NewStrPrimType(nil)
	numType := ts.NewNumPrimType(nil)
	boolType := ts.NewBoolPrimType(nil)

	// Manually create a nested union: Union(Union(string, number), boolean)
	innerUnion := ts.NewUnionType(nil, strType, numType)
	nestedUnion := ts.NewUnionType(nil, innerUnion, boolType)

	assert.True(t, typeContains(nestedUnion, strType), "should find string in nested union")
	assert.True(t, typeContains(nestedUnion, numType), "should find number in nested union")
	assert.True(t, typeContains(nestedUnion, boolType), "should find boolean in nested union")
	assert.False(t, typeContains(nestedUnion, ts.NewVoidType(nil)), "should not find void in nested union")
}

func TestTypeContainsUnionNeedle(t *testing.T) {
	strType := ts.NewStrPrimType(nil)
	numType := ts.NewNumPrimType(nil)
	boolType := ts.NewBoolPrimType(nil)

	haystack := ts.NewUnionType(nil, strType, numType, boolType)

	// Union needle where all members are present.
	assert.True(t, typeContains(haystack, ts.NewUnionType(nil, strType, numType)),
		"should find union(string, number) in union(string, number, boolean)")

	// Union needle where one member is missing.
	assert.False(t, typeContains(haystack, ts.NewUnionType(nil, strType, ts.NewVoidType(nil))),
		"should not find union(string, void) in union(string, number, boolean)")
}

// TestWideningWithAliasedTypeVars verifies that when two Widenable TypeVars are
// aliased (tvA.Instance = tvB) and then widened via tvA, reading through tvB
// also observes the widened type. This simulates the case where two open objects
// have the same property, bind aliases their property TypeVars, and a subsequent
// conflicting write widens one of them.
func TestWideningWithAliasedTypeVars(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := NewChecker(ctx)
	inferCtx := Context{} // minimal context, enough for Unify

	numType := ts.NewNumPrimType(nil)

	// Create two Widenable TypeVars simulating property TypeVars from
	// two open objects that share a property.
	tvA := c.FreshVar(nil)
	tvA.Widenable = true
	tvB := c.FreshVar(nil)
	tvB.Widenable = true

	// Simulate bind aliasing: tvA -> tvB (as if bind(tvA, tvB) was called
	// when unifying the two open objects' property types).
	tvA.Instance = tvB

	// Simulate first write binding tvB to number (as if Unify(1, tvB)
	// went through bind and widened the literal to number).
	tvB.Instance = numType

	// Sanity: tvB should resolve to number. Do NOT Prune tvA here — that
	// would path-compress tvA.Instance from tvB to numType, destroying the
	// alias chain before the widening test.
	assert.Equal(t, "number", ts.Prune(tvB).String())

	// Conflicting write through tvA: Unify("hello", tvA).
	// This should trigger widening to number | string.
	strLit := ts.NewStrLitType(nil, "hello")
	errors := c.Unify(inferCtx, strLit, tvA)
	assert.Empty(t, errors, "widening should suppress the error")

	// Both tvA and tvB should resolve to number | string.
	assert.Equal(t, "number | string", ts.Prune(tvA).String(),
		"tvA should see the widened type")
	assert.Equal(t, "number | string", ts.Prune(tvB).String(),
		"tvB should also see the widened type (alias consistency)")
}

// TestRebuildContainersNormalizesUnionAfterTypeVarBound verifies that
// rebuildContainers re-runs container normalization when a TypeVar inside a
// union has been bound to a concrete type. The setup mirrors a binding site:
// a union `number | tv` is built before tv is resolved; once tv is bound to
// the literal 10, calling rebuildContainers must collapse the union to
// `number` via NewUnionType's literal-absorption pass.
//
// This locks in the contract that TypeVarType.Accept prunes through the
// bound Instance and that UnionType.Accept rebuilds via NewUnionType when
// children change.
func TestRebuildContainersNormalizesUnionAfterTypeVarBound(t *testing.T) {
	numType := ts.NewNumPrimType(nil)

	tv := ts.NewTypeVarType(nil, 1)
	tv.FromBinding = true

	union := ts.NewUnionType(nil, numType, tv)
	// Before binding, the union should still have two members.
	if u, ok := union.(*ts.UnionType); ok {
		assert.Len(t, u.Types, 2, "union should have 2 members before tv is bound")
	} else {
		t.Fatalf("expected UnionType before binding, got %T", union)
	}

	// Bind tv to a literal 10. NewUnionType has already run, so the union
	// still references the unbound tv.
	tv.Instance = ts.NewNumLitType(nil, 10)

	rebuilt := rebuildContainers(union)
	assert.Equal(t, "number", rebuilt.String(),
		"rebuildContainers should re-apply NewUnionType normalization, absorbing 10 into number")
}

// assertFlatUnionMembers verifies that t is a UnionType with exactly the
// expected members at the top level (no nested unions).
func assertFlatUnionMembers(t *testing.T, typ ts.Type, expected []ts.Type) {
	t.Helper()
	union, ok := typ.(*ts.UnionType)
	if !ok {
		t.Fatalf("expected UnionType, got %T", typ)
	}
	assert.Equal(t, len(expected), len(union.Types), "wrong number of union members")
	for i, member := range union.Types {
		if _, isUnion := member.(*ts.UnionType); isUnion {
			t.Errorf("member %d is a nested UnionType, expected flat union", i)
		}
		if i < len(expected) {
			assert.True(t, ts.Equals(member, expected[i]), "member %d: expected %s, got %s", i, expected[i], member)
		}
	}
}
