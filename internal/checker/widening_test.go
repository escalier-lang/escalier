package checker

import (
	"testing"

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
