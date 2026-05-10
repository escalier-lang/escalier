package interop

import (
	"strings"
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

// TestLoadSource_GetterMutSelf verifies that a getter declared with `mut self`
// is recorded as mutating (e.g. lazy-initialization patterns).
func TestLoadSource_GetterMutSelf(t *testing.T) {
	src := `
override declare global {
    declare class Foo {
        get cached(mut self) -> number,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(src, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	key := overrideKey{Module: "", ClassName: "Foo", Member: "cached", Kind: kindGetter}
	entry, _, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected getter entry")
	}
	if !entry.Mut {
		t.Error("getter declared with `mut self` should be classified as mutating")
	}
}

// TestLoadSource_SetterPlainSelf verifies that a setter declared with plain
// `self` (no `mut`) is recorded as non-mutating (e.g. proxies / no-ops).
func TestLoadSource_SetterPlainSelf(t *testing.T) {
	src := `
override declare global {
    declare class Foo {
        set passthrough(self, v: number),
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(src, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}
	key := overrideKey{Module: "", ClassName: "Foo", Member: "passthrough", Kind: kindSetter}
	entry, _, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected setter entry")
	}
	if entry.Mut {
		t.Error("setter declared with non-mut `self` should be classified as non-mutating")
	}
}

// TestLoadSource_FieldMutability verifies that a writable field is classified
// as mutating and a `readonly` field is classified as non-mutating.
func TestLoadSource_FieldMutability(t *testing.T) {
	src := `
override declare global {
    declare class Foo {
        x: number,
        readonly y: number,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(src, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	x := overrideKey{Module: "", ClassName: "Foo", Member: "x", Kind: kindField}
	if entry, _, ok := r.lookup(x); !ok {
		t.Fatal("expected entry for x")
	} else if !entry.Mut {
		t.Error("non-readonly field should be mutating")
	}

	y := overrideKey{Module: "", ClassName: "Foo", Member: "y", Kind: kindField}
	if entry, _, ok := r.lookup(y); !ok {
		t.Fatal("expected entry for y")
	} else if entry.Mut {
		t.Error("readonly field should be non-mutating")
	}
}

// TestLoadSource_DuplicateKeyError verifies that loading two overrides with
// the same (module, class, member, kind) within a single tier surfaces an
// error instead of silently overwriting.
func TestLoadSource_DuplicateKeyError(t *testing.T) {
	first := `
override declare global {
    declare class Foo {
        bar(mut self) -> void,
    }
}
`
	second := `
override declare global {
    declare class Foo {
        bar(self) -> void,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(first, "first.esc", false); err != nil {
		t.Fatalf("loadSource first: %v", err)
	}
	err := r.loadSource(second, "second.esc", false)
	if err == nil {
		t.Fatal("expected error on duplicate override key, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate override") {
		t.Errorf("expected duplicate-override error, got: %v", err)
	}
}

// TestLoadSource_ModuleScopedDoesNotLeakToGlobal verifies that an override
// declared inside `override declare module "x"` does not match a lookup
// against the global scope (Module="") for the same class name.
func TestLoadSource_ModuleScopedDoesNotLeakToGlobal(t *testing.T) {
	src := `
override declare module "my-lib" {
    declare class Foo {
        bar(self) -> void,
    }
}
`
	r := newOverrideRegistry()
	if err := r.loadSource(src, "test.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	moduleKey := overrideKey{Module: "my-lib", ClassName: "Foo", Member: "bar", Kind: kindMethod}
	if _, _, ok := r.lookup(moduleKey); !ok {
		t.Error("expected module-scoped lookup to find entry")
	}

	globalKey := overrideKey{Module: "", ClassName: "Foo", Member: "bar", Kind: kindMethod}
	if _, _, ok := r.lookup(globalKey); ok {
		t.Error("module-scoped override should not be visible to a global lookup")
	}
}

// TestPureModuleEndToEnd verifies that addPureModule routes through Classify:
// a module member with no specific override resolves as non-mutating at tier 4.
func TestPureModuleEndToEnd(t *testing.T) {
	r := newOverrideRegistry()
	r.addPureModule("ramda")

	// No specific entry for ramda#anything; the pure-module rule should
	// classify any class-method lookup against it as non-mutating.
	key := overrideKey{Module: "ramda", ClassName: "List", Member: "push", Kind: kindMethod}
	entry, fromUser, ok := r.lookup(key)
	if !ok {
		t.Fatal("expected pure-module fallback to produce an entry")
	}
	if fromUser {
		t.Error("pure-module fallback should not be reported as a user override")
	}
	if entry.Mut {
		t.Error("pure-module fallback should classify as non-mutating")
	}
}
