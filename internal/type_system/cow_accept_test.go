package type_system

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// countingSubstitutionVisitor mimics TypeParamSubstitutionVisitor but also
// counts how many times EnterType is called. This lets us detect double
// traversal of TypeArgs when ExitType redundantly calls CowAcceptTypes.
type countingSubstitutionVisitor struct {
	substitutions map[string]Type
	enterCount    int
}

func (v *countingSubstitutionVisitor) EnterType(t Type) EnterResult {
	v.enterCount++
	return EnterResult{}
}

func (v *countingSubstitutionVisitor) ExitType(t Type) Type {
	if tref, ok := t.(*TypeRefType); ok {
		typeName := QualIdentToString(tref.Name)
		if sub, found := v.substitutions[typeName]; found {
			return sub
		}
		// TypeArgs are already visited by TypeRefType.Accept via CowAcceptTypes
		// before ExitType is called, so no need to re-traverse them here.
	}
	return nil
}

// TestSubstitutionVisitorDoubleTraversal verifies that a substitution visitor
// does not re-traverse TypeArgs in ExitType when Accept already visited them.
// With the redundant CowAcceptTypes call in ExitType, Array<T> with T→number
// causes 3 EnterType calls (Array<T>, T, then number from the re-traversal).
// Without the redundant call, it should be only 2 (Array<T>, T).
func TestSubstitutionVisitorDoubleTraversal(t *testing.T) {
	// Array<T> — one TypeRefType with one type arg
	typeRef := NewTypeRefType(nil, "Array", nil, NewTypeRefType(nil, "T", nil))

	v := &countingSubstitutionVisitor{
		substitutions: map[string]Type{
			"T": NewNumPrimType(nil),
		},
	}

	result := typeRef.Accept(v)

	// Correctness: substitution should work regardless
	assert.Equal(t, "Array<number>", result.String())

	// Performance: TypeArgs should be visited exactly once.
	// Accept visits: Array<T> (1) + T (2) = 2 EnterType calls.
	// With the redundant CowAcceptTypes in ExitType, there's an extra
	// visit of the already-substituted 'number', making it 3.
	assert.Equal(t, 2, v.enterCount,
		"ExitType should not re-traverse TypeArgs that Accept already visited")
}

func TestCowAcceptTypes(t *testing.T) {
	t.Run("returns same slice when nothing changes", func(t *testing.T) {
		items := []Type{
			NewNumPrimType(nil),
			NewStrPrimType(nil),
		}

		result, changed := CowAcceptTypes(items, &IdentityVisitor{})

		assert.False(t, changed)
		assert.Same(t, &items[0], &result[0], "should return the same slice, not a copy")
	})

	t.Run("returns new slice when an element changes", func(t *testing.T) {
		numType := NewNumPrimType(nil)
		strType := NewStrPrimType(nil)
		boolType := NewBoolPrimType(nil)
		items := []Type{numType, strType}

		visitor := NewTypeReplacementVisitor(map[Type]Type{
			strType: boolType,
		})
		result, changed := CowAcceptTypes(items, visitor)

		assert.True(t, changed)
		assert.NotSame(t, &items[0], &result[0], "should return a new slice")
		// Unchanged element is preserved
		assert.Same(t, items[0], result[0])
		// Changed element is the replacement
		assert.Same(t, boolType, result[1])
	})

	t.Run("handles empty slice", func(t *testing.T) {
		items := []Type{}

		result, changed := CowAcceptTypes(items, &IdentityVisitor{})

		assert.False(t, changed)
		assert.Equal(t, 0, len(result))
	})

	t.Run("preserves elements before the first change", func(t *testing.T) {
		num := NewNumPrimType(nil)
		str := NewStrPrimType(nil)
		boolType := NewBoolPrimType(nil)
		replacement := NewNumPrimType(nil)

		// Only the last element changes
		items := []Type{num, str, boolType}
		visitor := NewTypeReplacementVisitor(map[Type]Type{
			boolType: replacement,
		})

		result, changed := CowAcceptTypes(items, visitor)

		assert.True(t, changed)
		// First two elements are copied from original
		assert.Same(t, num, result[0])
		assert.Same(t, str, result[1])
		assert.Same(t, replacement, result[2])
	})
}

func TestCowAcceptElems(t *testing.T) {
	t.Run("returns same slice when nothing changes", func(t *testing.T) {
		items := []ObjTypeElem{
			&PropertyElem{Name: NewStrKey("x"), Value: NewNumPrimType(nil)},
			&PropertyElem{Name: NewStrKey("y"), Value: NewStrPrimType(nil)},
		}

		result, changed := CowAcceptElems(items, &IdentityVisitor{})

		assert.False(t, changed)
		assert.Same(t, &items[0], &result[0])
	})

	t.Run("returns new slice when an element changes", func(t *testing.T) {
		numType := NewNumPrimType(nil)
		strType := NewStrPrimType(nil)
		boolType := NewBoolPrimType(nil)

		items := []ObjTypeElem{
			&PropertyElem{Name: NewStrKey("x"), Value: numType},
			&PropertyElem{Name: NewStrKey("y"), Value: strType},
		}

		// Replace strType with boolType — this will cause the second PropertyElem to change
		visitor := NewTypeReplacementVisitor(map[Type]Type{
			strType: boolType,
		})
		result, changed := CowAcceptElems(items, visitor)

		assert.True(t, changed)
		// First element unchanged
		assert.Same(t, items[0], result[0])
		// Second element changed (new PropertyElem with boolType)
		prop := result[1].(*PropertyElem)
		assert.Same(t, boolType, prop.Value)
	})
}

func TestCowAcceptTypeRefs(t *testing.T) {
	t.Run("returns same slice when nothing changes", func(t *testing.T) {
		items := []*TypeRefType{
			NewTypeRefType(nil, "Foo", nil),
			NewTypeRefType(nil, "Bar", nil),
		}

		result, changed := CowAcceptTypeRefs(items, &IdentityVisitor{})

		assert.False(t, changed)
		assert.Same(t, &items[0], &result[0])
	})

	t.Run("preserves nil entries", func(t *testing.T) {
		ref := NewTypeRefType(nil, "Foo", nil)
		items := []*TypeRefType{nil, ref, nil}

		result, changed := CowAcceptTypeRefs(items, &IdentityVisitor{})

		assert.False(t, changed)
		// Same slice returned
		assert.Same(t, &items[0], &result[0])
	})

	t.Run("preserves nil entries when another element changes", func(t *testing.T) {
		numType := NewNumPrimType(nil)
		strType := NewStrPrimType(nil)

		refWithArg := NewTypeRefType(nil, "Foo", nil, numType)
		items := []*TypeRefType{nil, refWithArg}

		// Replace numType with strType — this changes the TypeArg inside refWithArg
		visitor := NewTypeReplacementVisitor(map[Type]Type{
			numType: strType,
		})
		result, changed := CowAcceptTypeRefs(items, visitor)

		assert.True(t, changed)
		assert.Nil(t, result[0])
		assert.Equal(t, "Foo<string>", result[1].String())
	})
}
