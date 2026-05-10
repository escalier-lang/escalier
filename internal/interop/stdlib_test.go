package interop

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStdlibFilesParse verifies that every shipped stdlib override file under
// stdlib/ can be parsed without error and produces at least one override entry.
// Uses WalkDir to match loadStdlib's recursive traversal.
func TestStdlibFilesParse(t *testing.T) {
	found := 0
	err := filepath.WalkDir("stdlib", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(d.Name()) != ".esc" {
			return nil
		}
		found++
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			r := newOverrideRegistry()
			if err := r.loadSource(string(data), path, false); err != nil {
				t.Fatalf("loadSource: %v", err)
			}
			if len(r.shipped) == 0 {
				t.Errorf("expected at least one override entry, got none")
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir stdlib: %v", err)
	}
	if found == 0 {
		t.Fatal("no .esc files found under stdlib/")
	}
}

// TestStdlibDateMutability spot-checks key Date entries in es5.esc.
func TestStdlibDateMutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "es5.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "es5.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		method  string
		wantMut bool
	}{
		{"setHours", true},
		{"setFullYear", true},
		{"setUTCSeconds", true},
		{"getFullYear", false},
		{"getTime", false},
		{"toISOString", false},
	}
	for _, tt := range tests {
		t.Run("Date."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: "Date", Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("no entry for Date.%s", tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("Date.%s: got Mut=%v, want %v", tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

// TestStdlibRegExpMutability spot-checks RegExp entries in es5.esc.
func TestStdlibRegExpMutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "es5.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "es5.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		method  string
		wantMut bool
	}{
		{"exec", false},
		{"test", false},
		{"compile", true},
	}
	for _, tt := range tests {
		t.Run("RegExp."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: "RegExp", Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("no entry for RegExp.%s", tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("RegExp.%s: got Mut=%v, want %v", tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

// TestStdlibES2015Mutability spot-checks Promise/WeakMap/WeakSet in es2015.esc.
func TestStdlibES2015Mutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "es2015.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "es2015.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		class   string
		method  string
		wantMut bool
	}{
		{"Promise", "then", false},
		{"Promise", "catch", false},
		{"Promise", "finally", false},
		{"WeakMap", "set", true},
		{"WeakMap", "delete", true},
		{"WeakMap", "get", false},
		{"WeakMap", "has", false},
		{"WeakSet", "add", true},
		{"WeakSet", "delete", true},
		{"WeakSet", "has", false},
	}
	for _, tt := range tests {
		t.Run(tt.class+"."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: tt.class, Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("no entry for %s.%s", tt.class, tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("%s.%s: got Mut=%v, want %v", tt.class, tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

// TestStdlibTypedArrayMutability spot-checks Int8Array and Float64Array in es2017.esc.
func TestStdlibTypedArrayMutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "es2017.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "es2017.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		class   string
		method  string
		wantMut bool
	}{
		{"Int8Array", "fill", true},
		{"Int8Array", "reverse", true},
		{"Int8Array", "sort", true},
		{"Int8Array", "map", false},
		{"Int8Array", "slice", false},
		{"Float64Array", "copyWithin", true},
		{"Float64Array", "forEach", false},
		{"Uint8ClampedArray", "set", true},
		{"Uint8ClampedArray", "values", false},
	}
	for _, tt := range tests {
		t.Run(tt.class+"."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: tt.class, Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("no entry for %s.%s", tt.class, tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("%s.%s: got Mut=%v, want %v", tt.class, tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}

// TestStdlibWeakRefMutability checks WeakRef.deref in es2021.esc.
func TestStdlibWeakRefMutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "es2021.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "es2021.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	key := overrideKey{Module: "", ClassName: "WeakRef", Member: "deref", Kind: kindMethod}
	entry, _, ok := r.lookup(key)
	if !ok {
		t.Fatal("no entry for WeakRef.deref")
	}
	if entry.Mut {
		t.Error("WeakRef.deref should be non-mutating")
	}
}

// TestStdlibURLSearchParamsMutability spot-checks URLSearchParams in dom.esc.
func TestStdlibURLSearchParamsMutability(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("stdlib", "dom.esc"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	r := newOverrideRegistry()
	if err := r.loadSource(string(data), "dom.esc", false); err != nil {
		t.Fatalf("loadSource: %v", err)
	}

	tests := []struct {
		method  string
		wantMut bool
	}{
		{"append", true},
		{"set", true},
		{"delete", true},
		{"sort", true},
		{"get", false},
		{"getAll", false},
		{"has", false},
		{"keys", false},
		{"values", false},
		{"entries", false},
		{"forEach", false},
	}
	for _, tt := range tests {
		t.Run("URLSearchParams."+tt.method, func(t *testing.T) {
			key := overrideKey{Module: "", ClassName: "URLSearchParams", Member: tt.method, Kind: kindMethod}
			entry, _, ok := r.lookup(key)
			if !ok {
				t.Fatalf("no entry for URLSearchParams.%s", tt.method)
			}
			if entry.Mut != tt.wantMut {
				t.Errorf("URLSearchParams.%s: got Mut=%v, want %v", tt.method, entry.Mut, tt.wantMut)
			}
		})
	}
}
