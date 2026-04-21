package type_system

import (
	"testing"
)

func TestGetLifetimeTypeRef(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	ty := &TypeRefType{
		Name:     NewIdent("Point"),
		Lifetime: lt,
	}
	got := GetLifetime(ty)
	if got != lt {
		t.Errorf("expected lifetime var 'a, got %v", got)
	}
}

func TestGetLifetimeObjectType(t *testing.T) {
	lt := &LifetimeValue{ID: 1, Name: "obj"}
	ty := NewObjectType(nil, nil)
	ty.Lifetime = lt
	got := GetLifetime(ty)
	if got != lt {
		t.Errorf("expected lifetime value, got %v", got)
	}
}

func TestGetLifetimeTupleType(t *testing.T) {
	lt := &LifetimeVar{ID: 2, Name: "b"}
	ty := NewTupleType(nil)
	ty.Lifetime = lt
	got := GetLifetime(ty)
	if got != lt {
		t.Errorf("expected lifetime var 'b, got %v", got)
	}
}

func TestGetLifetimeMutabilityType(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	inner := &TypeRefType{
		Name:     NewIdent("Point"),
		Lifetime: lt,
	}
	ty := &MutabilityType{
		Type:       inner,
		Mutability: MutabilityMutable,
	}
	got := GetLifetime(ty)
	if got != lt {
		t.Errorf("expected lifetime from inner type, got %v", got)
	}
}

func TestGetLifetimePrimitive(t *testing.T) {
	ty := NewNumPrimType(nil)
	got := GetLifetime(ty)
	if got != nil {
		t.Errorf("expected nil lifetime for primitive, got %v", got)
	}
}

func TestGetLifetimeNilLifetime(t *testing.T) {
	ty := &TypeRefType{
		Name: NewIdent("Point"),
	}
	got := GetLifetime(ty)
	if got != nil {
		t.Errorf("expected nil lifetime, got %v", got)
	}
}

func TestGetLifetimeUnionSame(t *testing.T) {
	lt := &LifetimeVar{ID: 1, Name: "a"}
	ty := NewUnionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt},
	)
	got := GetLifetime(ty)
	if got != lt {
		t.Errorf("expected common lifetime, got %v", got)
	}
}

func TestGetLifetimeUnionDifferent(t *testing.T) {
	lt1 := &LifetimeVar{ID: 1, Name: "a"}
	lt2 := &LifetimeVar{ID: 2, Name: "b"}
	ty := NewUnionType(nil,
		&TypeRefType{Name: NewIdent("A"), Lifetime: lt1},
		&TypeRefType{Name: NewIdent("B"), Lifetime: lt2},
	)
	got := GetLifetime(ty)
	if got != nil {
		t.Errorf("expected nil for different lifetimes, got %v", got)
	}
}

func TestLifetimeVarConstruction(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}
	if lv.ID != 1 {
		t.Errorf("expected ID 1, got %d", lv.ID)
	}
	if lv.Name != "a" {
		t.Errorf("expected name 'a', got %s", lv.Name)
	}
	if lv.Instance != nil {
		t.Error("expected nil Instance")
	}
}

func TestLifetimeValueConstruction(t *testing.T) {
	val := &LifetimeValue{ID: 42, Name: "items", IsStatic: false}
	if val.ID != 42 {
		t.Errorf("expected ID 42, got %d", val.ID)
	}
	if val.IsStatic {
		t.Error("expected non-static")
	}

	static := &LifetimeValue{ID: 1, Name: "global", IsStatic: true}
	if !static.IsStatic {
		t.Error("expected static")
	}
}

func TestLifetimeVarBinding(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}
	val := &LifetimeValue{ID: 10, Name: "items"}
	lv.Instance = val

	if lv.Instance != val {
		t.Error("expected Instance to be bound")
	}
	if lv.Instance.ID != 10 {
		t.Errorf("expected bound ID 10, got %d", lv.Instance.ID)
	}
}

func TestPruneLifetimeBoundVar(t *testing.T) {
	val := &LifetimeValue{ID: 10, Name: "items"}
	lv := &LifetimeVar{ID: 1, Name: "a", Instance: val}

	got := PruneLifetime(lv)
	if got != val {
		t.Errorf("expected PruneLifetime to resolve to LifetimeValue, got %v", got)
	}
}

func TestPruneLifetimeUnboundVar(t *testing.T) {
	lv := &LifetimeVar{ID: 1, Name: "a"}

	got := PruneLifetime(lv)
	if got != lv {
		t.Errorf("expected PruneLifetime to return unbound var as-is, got %v", got)
	}
}

func TestPruneLifetimeValue(t *testing.T) {
	val := &LifetimeValue{ID: 5, Name: "x"}

	got := PruneLifetime(val)
	if got != val {
		t.Errorf("expected PruneLifetime to return LifetimeValue as-is, got %v", got)
	}
}

func TestPruneLifetimeNil(t *testing.T) {
	got := PruneLifetime(nil)
	if got != nil {
		t.Errorf("expected PruneLifetime(nil) to return nil, got %v", got)
	}
}
