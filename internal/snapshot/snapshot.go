// Package snapshot renders Go values into a compact, deterministic string
// suitable for use with go-snaps test snapshots. Compared to the default
// pretty-printer used by go-snaps, this:
//
//   - Omits zero-valued struct fields (nil pointers/interfaces/slices/maps,
//     false bools, zero numbers, empty strings, empty-but-non-nil
//     slices/maps, zero structs).
//   - Renders ast.Span on a single line (e.g. "1:6-1:7").
//   - Skips column alignment so diffs stay tight.
//   - Detects pointer cycles so cyclic AST/type graphs don't blow the stack.
package snapshot

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

const indent = "    "

// String renders v in the snapshot format described in the package doc.
func String(v any) string {
	if v == nil {
		return "nil"
	}
	var sb strings.Builder
	rv := reflect.ValueOf(v)
	// Make the root addressable so we can read unexported fields below.
	root := reflect.New(rv.Type()).Elem()
	root.Set(rv)
	s := &state{sb: &sb, seen: set.NewSet[uintptr]()}
	s.writeValue(root, 0)
	return sb.String()
}

type state struct {
	sb   *strings.Builder
	seen set.Set[uintptr]
}

func pad(depth int) string {
	return strings.Repeat(indent, depth)
}

func (s *state) writeValue(v reflect.Value, depth int) {
	if !v.IsValid() {
		s.sb.WriteString("nil")
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			s.sb.WriteString("nil")
			return
		}
		addr := v.Pointer()
		if s.seen.Contains(addr) {
			fmt.Fprintf(s.sb, "<cycle %s>", v.Type().String())
			return
		}
		s.seen.Add(addr)
		s.sb.WriteString("&")
		s.writeValue(v.Elem(), depth)
		s.seen.Remove(addr)
	case reflect.Interface:
		if v.IsNil() {
			s.sb.WriteString("nil")
			return
		}
		s.writeValue(v.Elem(), depth)
	case reflect.Struct:
		s.writeStruct(v, depth)
	case reflect.Slice, reflect.Array:
		s.writeSlice(v, depth)
	case reflect.Map:
		s.writeMap(v, depth)
	case reflect.String:
		fmt.Fprintf(s.sb, "%q", v.String())
	case reflect.Bool:
		fmt.Fprintf(s.sb, "%t", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fmt.Fprintf(s.sb, "%d", v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		fmt.Fprintf(s.sb, "%d", v.Uint())
	case reflect.Float32, reflect.Float64:
		fmt.Fprintf(s.sb, "%g", v.Float())
	default:
		// Channels, funcs, complex, etc. — fall back on %v.
		fmt.Fprintf(s.sb, "%v", safeInterface(v))
	}
}

var spanType = reflect.TypeFor[ast.Span]()

func (s *state) writeStruct(v reflect.Value, depth int) {
	t := v.Type()
	if t == spanType {
		span := v.Interface().(ast.Span)
		s.sb.WriteString(span.String())
		if span.SourceID != 0 {
			fmt.Fprintf(s.sb, "@%d", span.SourceID)
		}
		return
	}

	typeName := t.String()

	nonZero := make([]int, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := fieldValue(v, i)
		if isZero(f) {
			continue
		}
		nonZero = append(nonZero, i)
	}
	if len(nonZero) == 0 {
		fmt.Fprintf(s.sb, "%s{}", typeName)
		return
	}

	fmt.Fprintf(s.sb, "%s{\n", typeName)
	for _, i := range nonZero {
		f := fieldValue(v, i)
		s.sb.WriteString(pad(depth + 1))
		s.sb.WriteString(t.Field(i).Name)
		s.sb.WriteString(": ")
		s.writeValue(f, depth+1)
		s.sb.WriteString(",\n")
	}
	s.sb.WriteString(pad(depth))
	s.sb.WriteString("}")
}

func (s *state) writeSlice(v reflect.Value, depth int) {
	typeName := v.Type().String()
	n := v.Len()
	if n == 0 {
		fmt.Fprintf(s.sb, "%s{}", typeName)
		return
	}
	fmt.Fprintf(s.sb, "%s{\n", typeName)
	for i := range n {
		s.sb.WriteString(pad(depth + 1))
		s.writeValue(v.Index(i), depth+1)
		s.sb.WriteString(",\n")
	}
	s.sb.WriteString(pad(depth))
	s.sb.WriteString("}")
}

func (s *state) writeMap(v reflect.Value, depth int) {
	typeName := v.Type().String()
	if v.Len() == 0 {
		fmt.Fprintf(s.sb, "%s{}", typeName)
		return
	}
	keys := v.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return mapKeyString(keys[i]) < mapKeyString(keys[j])
	})
	fmt.Fprintf(s.sb, "%s{\n", typeName)
	for _, k := range keys {
		s.sb.WriteString(pad(depth + 1))
		s.writeValue(k, depth+1)
		s.sb.WriteString(": ")
		s.writeValue(v.MapIndex(k), depth+1)
		s.sb.WriteString(",\n")
	}
	s.sb.WriteString(pad(depth))
	s.sb.WriteString("}")
}

func mapKeyString(k reflect.Value) string {
	switch k.Kind() {
	case reflect.String:
		return k.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("%020d", k.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return fmt.Sprintf("%020d", k.Uint())
	default:
		return fmt.Sprint(safeInterface(k))
	}
}

// fieldValue returns the i'th field of struct v, transparently bypassing the
// CanInterface restriction for unexported fields so the writer can recurse.
func fieldValue(v reflect.Value, i int) reflect.Value {
	f := v.Field(i)
	if f.CanInterface() {
		return f
	}
	if !f.CanAddr() {
		cp := reflect.New(f.Type()).Elem()
		cp.Set(f)
		return cp
	}
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func safeInterface(v reflect.Value) any {
	if v.CanInterface() {
		return v.Interface()
	}
	if v.CanAddr() {
		return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem().Interface()
	}
	return "<unexported>"
}

func isZero(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.IsNil() || v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}
