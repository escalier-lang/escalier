package type_system

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetLifetimeTypeRef(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	ty := &TypeRefType{
		Name:     NewIdent("Point"),
		Lifetime: lt,
	}
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimeObjectType(t *testing.T) {
	lt := &LifetimeValue{ID: 1, Name: "obj"}
	ty := NewObjectType(nil, nil)
	ty.Lifetime = lt
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimeTupleType(t *testing.T) {
	lt := &LifetimeVar{ID: 2, Name: "b"}
	ty := NewTupleType(nil)
	ty.Lifetime = lt
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimeMutType(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	inner := &TypeRefType{
		Name:     NewIdent("Point"),
		Lifetime: lt,
	}
	ty := &MutType{
		Type: inner,
	}
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimePrimitive(t *testing.T) {
	ty := NewNumPrimType(nil)
	require.Nil(t, GetLifetime(ty))
}

func TestGetLifetimeNilLifetime(t *testing.T) {
	ty := &TypeRefType{
		Name: NewIdent("Point"),
	}
	require.Nil(t, GetLifetime(ty))
}

func TestGetLifetimeUnionSame(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	ty := NewUnionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt},
	)
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimeUnionDifferent(t *testing.T) {
	lt1 := &LifetimeVar{ID: 1, Name: "a"}
	lt2 := &LifetimeVar{ID: 2, Name: "b"}
	ty := NewUnionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt1},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt2},
	)
	require.Nil(t, GetLifetime(ty))
}

func TestGetLifetimeIntersectionSame(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	ty := NewIntersectionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt},
	)
	require.Equal(t, lt, GetLifetime(ty))
}

func TestGetLifetimeIntersectionDifferent(t *testing.T) {
	lt1 := &LifetimeVar{ID: 1, Name: "a"}
	lt2 := &LifetimeVar{ID: 2, Name: "b"}
	ty := NewIntersectionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt1},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt2},
	)
	require.Nil(t, GetLifetime(ty))
}

func TestLifetimeVarConstruction(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}
	require.Equal(t, 1, lv.ID)
	require.Equal(t, "a", lv.Name)
	require.Nil(t, lv.Instance)
}

func TestLifetimeValueConstruction(t *testing.T) {
	val := &LifetimeValue{ID: 42, Name: "items", IsStatic: false}
	require.Equal(t, 42, val.ID)
	require.False(t, val.IsStatic)

	static := &LifetimeValue{ID: 1, Name: "global", IsStatic: true}
	require.True(t, static.IsStatic)
}

func TestLifetimeVarBinding(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}
	val := &LifetimeValue{ID: 10, Name: "items"}
	lv.Instance = val

	require.Equal(t, val, lv.Instance)
	require.Equal(t, 10, lv.Instance.ID)
}

func TestPruneLifetimeBoundVar(t *testing.T) {
	val := &LifetimeValue{ID: 10, Name: "items"}
	lv := &LifetimeVar{ID: 1, Name: "a", Instance: val}
	require.Equal(t, val, PruneLifetime(lv))
}

func TestPruneLifetimeUnboundVar(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}
	require.Equal(t, lv, PruneLifetime(lv))
}

func TestPruneLifetimeValue(t *testing.T) {
	val := &LifetimeValue{ID: 5, Name: "x"}
	require.Equal(t, val, PruneLifetime(val))
}

func TestPruneLifetimeNil(t *testing.T) {
	require.Nil(t, PruneLifetime(nil))
}
