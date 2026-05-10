package interop

import (
	"testing"
)

const globalOverrideSrc = `
override declare global {
    declare class Date {
        setHours(mut self, hours: number) -> number,
        getFullYear(self) -> number,
    }
    declare class Array<T> {
        slice(self, start?: number, end?: number) -> Array<T>,
        push(mut self, item: T) -> number,
    }
}
`

const moduleOverrideSrc = `
override declare module "fp-ts" {
    declare fn map(f: number) -> number
}
`

func TestLoadSource_GlobalClass(t *testing.T) {
	r := newOverrideRegistry()
	if err := r.loadSource(globalOverrideSrc, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		method  string
		wantMut bool
	}{
		{"setHours", true},
		{"getFullYear", false},
	}
	for _, tt := range tests {
		t.Run("Date."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: "Date", Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("expected entry for %s, got none", tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("Date.%s: got Mut=%v, want %v", tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

func TestLoadSource_ArrayMethods(t *testing.T) {
	r := newOverrideRegistry()
	if err := r.loadSource(globalOverrideSrc, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		method  string
		wantMut bool
	}{
		{"slice", false},
		{"push", true},
	}
	for _, tt := range tests {
		t.Run("Array."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: "Array", Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("expected entry for Array.%s, got none", tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("Array.%s: got Mut=%v, want %v", tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

func TestLoadSource_ModuleFunction(t *testing.T) {
	r := newOverrideRegistry()
	if err := r.loadSource(moduleOverrideSrc, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	key := overrideKey{Module: "fp-ts", ClassName: "", Member: "map", Kind: kindMethod}
	entry, _, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected entry for fp-ts#map, got none")
	}
	if entry.Mut {
		t.Error("fp-ts#map should be non-mutating")
	}
}

func TestLookup_MissingEntry(t *testing.T) {
	r := newOverrideRegistry()
	_, _, ok := r.lookup(overrideKey{Module: "", ClassName: "Foo", Member: "bar", Kind: kindMethod})
	if ok {
		t.Error("expected no entry for unknown key")
	}
}

func TestLookup_UserOverrideWinsOverShipped(t *testing.T) {
	shipped := `
override declare global {
    declare class Foo {
        bar(mut self) -> void,
    }
}
`
	user := `
override declare global {
    declare class Foo {
        bar(self) -> void,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(shipped, "shipped.esc", false); err != nil {
		t.Fatalf("loadSource shipped: %v", err)
	}
	if err := r.loadSource(user, "user.esc", true); err != nil {
		t.Fatalf("loadSource user: %v", err)
	}

	key := overrideKey{Module: "", ClassName: "Foo", Member: "bar", Kind: kindMethod}
	entry, fromUser, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected entry for Foo.bar")
	}
	if !fromUser {
		t.Error("expected entry to come from user overrides")
	}
	if entry.Mut {
		t.Error("user override says non-mutating; shipped says mutating — user should win")
	}
}

func TestLookup_ShippedUsedWhenNoUserOverride(t *testing.T) {
	shipped := `
override declare global {
    declare class Foo {
        bar(mut self) -> void,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(shipped, "shipped.esc", false); err != nil {
		t.Fatalf("loadSource shipped: %v", err)
	}

	key := overrideKey{Module: "", ClassName: "Foo", Member: "bar", Kind: kindMethod}
	entry, fromUser, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected entry for Foo.bar")
	}
	if fromUser {
		t.Error("expected entry to come from shipped overrides, not user")
	}
	if !entry.Mut {
		t.Error("Foo.bar should be mutating per shipped override")
	}
}

const getterSetterSrc = `
override declare global {
    declare class Foo {
        get value(self) -> number,
        set value(mut self, v: number),
    }
}
`

func TestLoadSource_GetterSetter(t *testing.T) {
	r := newOverrideRegistry()
	if err := r.loadSource(getterSetterSrc, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	getter := overrideKey{Module: "", ClassName: "Foo", Member: "value", Kind: kindGetter}
	if entry, _, ok := r.lookup(getter); !ok {
		t.Fatal("expected getter entry")
	} else if entry.Mut {
		t.Error("getter should be non-mutating")
	}

	setter := overrideKey{Module: "", ClassName: "Foo", Member: "value", Kind: kindSetter}
	if entry, _, ok := r.lookup(setter); !ok {
		t.Fatal("expected setter entry")
	} else if !entry.Mut {
		t.Error("setter should be mutating")
	}
}
