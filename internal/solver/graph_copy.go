package solver

import (
	"fmt"
	"reflect"
	"unsafe"
)

// graphCopier deep-copies an object graph. Every pointer, map, and slice it reaches
// gets a fresh copy, unexported fields included. A pointer or map reached twice is
// copied once, so two fields that referred to one object in the source refer to one
// object in the copy.
//
// That holds only for a pointer to a whole object. A pointer to a field of a struct
// or to an element of a slice is copied apart from the struct or slice it points
// into. Two slices over one backing array each get their own array too. A write
// through one copy then no longer reaches the other.
//
// It refuses a non-nil channel, unsafe pointer, or func, since none of them can be
// copied without knowing what they refer to. A caller that has to carry a func
// clears it before the copy and sets it on the copy afterwards.
//
// The copy walks memory through unsafe pointers rather than reflect.Value, since a
// reflect.Value assignment checks the types on every write and dominates the cost.
// reflect still describes each type once, in the typePlan built for it.
type graphCopier struct {
	// shared reports whether a pointer of the given type is left pointing at the
	// source object rather than copied.
	shared func(reflect.Type) bool

	plans map[reflect.Type]*typePlan

	// dynamicPlans maps the first word of an interface value to the plan for its
	// dynamic type. That word is the type itself for an empty interface and the
	// method table for any other, and either one names the dynamic type.
	dynamicPlans map[unsafe.Pointer]*typePlan

	// pointers maps a source address and the plan for the type stored there to its
	// copy. The plan is half the key because a struct and its first field share an
	// address.
	pointers map[pointerKey]unsafe.Pointer

	// maps maps a source map to its copy.
	maps map[unsafe.Pointer]reflect.Value
}

type pointerKey struct {
	addr unsafe.Pointer
	plan *typePlan
}

// copyKind is how copyInto copies one value.
type copyKind int

const (
	// copyBytes is a value with no pointer in it, copied byte for byte.
	copyBytes copyKind = iota
	// copyString is a string. Its bytes are never written, so the copy shares them.
	copyString
	copyPointer
	copyMap
	copySlice
	copyInterface
	copyStruct
	copyArray
	// copyRefuse is a func, channel, or unsafe pointer, which only copies when nil.
	copyRefuse
)

// typePlan describes how to copy a value of one type.
type typePlan struct {
	typ  reflect.Type
	kind copyKind
	// elem is the plan for what a pointer points at, or for a slice's or an
	// array's element. key is the plan for a map's key, and value for its value.
	//
	// For a pointer, a slice, or a map, these are built on first copy rather than
	// with this plan. A type can refer to itself only through one of those three
	// or an interface. Building them with this plan would read the kind of a plan
	// still being built, and that kind is not settled yet.
	elem       *typePlan
	key, value *typePlan
	// bytes holds the runs of a struct's memory that hold no pointer, and fields
	// holds the fields that do.
	bytes  []byteRun
	fields []fieldPlan
	// shared is set for a pointer type the copier leaves pointing at the source.
	shared bool
}

type fieldPlan struct {
	offset uintptr
	plan   *typePlan
}

type byteRun struct {
	offset, size uintptr
}

func newGraphCopier(shared func(reflect.Type) bool) *graphCopier {
	return &graphCopier{
		shared:       shared,
		plans:        map[reflect.Type]*typePlan{},
		dynamicPlans: map[unsafe.Pointer]*typePlan{},
		pointers:     map[pointerKey]unsafe.Pointer{},
		maps:         map[unsafe.Pointer]reflect.Value{},
	}
}

// copyPtr returns a deep copy of the object p points at.
func copyPtr[T any](g *graphCopier, p *T) *T {
	plan := g.planFor(reflect.TypeFor[*T]())
	return (*T)(g.copyPointer(plan, unsafe.Pointer(p)))
}

// planFor returns the plan for t, building it on first use.
func (g *graphCopier) planFor(t reflect.Type) *typePlan {
	if plan, done := g.plans[t]; done {
		return plan
	}
	plan := &typePlan{typ: t}
	// Recorded before the fields are planned, so a struct that refers to itself
	// through a field finds this plan.
	g.plans[t] = plan
	switch t.Kind() {
	case reflect.Pointer:
		plan.kind = copyPointer
		plan.shared = g.shared(t)
	case reflect.Map:
		plan.kind = copyMap
	case reflect.Slice:
		plan.kind = copySlice
	case reflect.Interface:
		plan.kind = copyInterface
	case reflect.String:
		plan.kind = copyString
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		plan.kind = copyRefuse
	case reflect.Struct:
		plan.kind = copyBytes
		for i := range t.NumField() {
			field := t.Field(i)
			fieldType := g.planFor(field.Type)
			if fieldType.kind == copyBytes {
				plan.addBytes(field.Offset, field.Type.Size())
				continue
			}
			plan.kind = copyStruct
			plan.fields = append(plan.fields, fieldPlan{offset: field.Offset, plan: fieldType})
		}
	case reflect.Array:
		plan.elem = g.planFor(t.Elem())
		plan.kind = copyArray
		if plan.elem.kind == copyBytes {
			plan.kind = copyBytes
		}
	default:
		plan.kind = copyBytes
	}
	return plan
}

// addBytes extends the last byte run when the field starts where it ends, and
// starts a new run otherwise.
func (p *typePlan) addBytes(offset, size uintptr) {
	if size == 0 {
		return
	}
	if n := len(p.bytes); n > 0 && p.bytes[n-1].offset+p.bytes[n-1].size == offset {
		p.bytes[n-1].size += size
		return
	}
	p.bytes = append(p.bytes, byteRun{offset, size})
}

// copyInto writes a deep copy of the value at src into dst. Both hold plan's type,
// and dst holds its zero value.
func (g *graphCopier) copyInto(plan *typePlan, dst, src unsafe.Pointer) {
	switch plan.kind {
	case copyBytes:
		size := plan.typ.Size()
		if size > 0 {
			copy(unsafe.Slice((*byte)(dst), size), unsafe.Slice((*byte)(src), size))
		}
	case copyString:
		*(*string)(dst) = *(*string)(src)
	case copyPointer:
		*(*unsafe.Pointer)(dst) = g.copyPointer(plan, *(*unsafe.Pointer)(src))
	case copyMap:
		g.copyMap(plan, dst, src)
	case copySlice:
		g.copySlice(plan, dst, src)
	case copyInterface:
		g.copyInterface(plan, dst, src)
	case copyStruct:
		// A byte copy is safe only where no pointer sits, since a pointer written
		// that way skips the garbage collector's write barrier.
		for _, run := range plan.bytes {
			copy(unsafe.Slice((*byte)(unsafe.Add(dst, run.offset)), run.size),
				unsafe.Slice((*byte)(unsafe.Add(src, run.offset)), run.size))
		}
		for _, field := range plan.fields {
			g.copyInto(field.plan, unsafe.Add(dst, field.offset), unsafe.Add(src, field.offset))
		}
	case copyArray:
		size := plan.elem.typ.Size()
		for i := range plan.typ.Len() {
			offset := uintptr(i) * size
			g.copyInto(plan.elem, unsafe.Add(dst, offset), unsafe.Add(src, offset))
		}
	case copyRefuse:
		if *(*unsafe.Pointer)(src) != nil {
			panic(fmt.Sprintf("graphCopier: cannot copy a non-nil %s", plan.typ))
		}
	}
}

// copyPointer returns the copy of the object src points at, where plan is the
// plan for src's pointer type.
func (g *graphCopier) copyPointer(plan *typePlan, src unsafe.Pointer) unsafe.Pointer {
	if src == nil || plan.shared {
		return src
	}
	if plan.elem == nil {
		plan.elem = g.planFor(plan.typ.Elem())
	}
	key := pointerKey{src, plan.elem}
	if dst, done := g.pointers[key]; done {
		return dst
	}
	dst := reflect.New(plan.typ.Elem()).UnsafePointer()
	// Recorded before the pointee is copied, so a cycle back to this object finds
	// the copy in progress.
	g.pointers[key] = dst
	g.copyInto(plan.elem, dst, src)
	return dst
}

func (g *graphCopier) copyMap(plan *typePlan, dst, src unsafe.Pointer) {
	srcMap := reflect.NewAt(plan.typ, src).Elem()
	if srcMap.IsNil() {
		return
	}
	dstSlot := reflect.NewAt(plan.typ, dst).Elem()
	if copied, done := g.maps[srcMap.UnsafePointer()]; done {
		dstSlot.Set(copied)
		return
	}
	copied := reflect.MakeMapWithSize(plan.typ, srcMap.Len())
	g.maps[srcMap.UnsafePointer()] = copied
	if plan.key == nil {
		plan.key = g.planFor(plan.typ.Key())
		plan.value = g.planFor(plan.typ.Elem())
	}

	// SetMapIndex copies its arguments, so one set of slots serves every entry.
	srcKey := reflect.New(plan.key.typ).Elem()
	srcValue := reflect.New(plan.value.typ).Elem()
	dstKey := reflect.New(plan.key.typ).Elem()
	dstValue := reflect.New(plan.value.typ).Elem()
	iter := srcMap.MapRange()
	for iter.Next() {
		srcKey.SetIterKey(iter)
		srcValue.SetIterValue(iter)
		dstKey.SetZero()
		dstValue.SetZero()
		g.copyInto(plan.key, dstKey.Addr().UnsafePointer(), srcKey.Addr().UnsafePointer())
		g.copyInto(plan.value, dstValue.Addr().UnsafePointer(), srcValue.Addr().UnsafePointer())
		copied.SetMapIndex(dstKey, dstValue)
	}
	dstSlot.Set(copied)
}

func (g *graphCopier) copySlice(plan *typePlan, dst, src unsafe.Pointer) {
	srcSlice := reflect.NewAt(plan.typ, src).Elem()
	if srcSlice.IsNil() {
		return
	}
	if plan.elem == nil {
		plan.elem = g.planFor(plan.typ.Elem())
	}
	n := srcSlice.Len()
	copied := reflect.MakeSlice(plan.typ, n, n)
	if n > 0 {
		size := plan.elem.typ.Size()
		dstBase, srcBase := copied.UnsafePointer(), srcSlice.UnsafePointer()
		for i := range n {
			offset := uintptr(i) * size
			g.copyInto(plan.elem, unsafe.Add(dstBase, offset), unsafe.Add(srcBase, offset))
		}
	}
	reflect.NewAt(plan.typ, dst).Elem().Set(copied)
}

// copyInterface copies an interface value. Its two words are the dynamic type or
// method table, then the data. A pointer dynamic value is the data word itself, so
// its copy is the same first word over the copied pointer. Any other dynamic value
// goes through reflect.
func (g *graphCopier) copyInterface(plan *typePlan, dst, src unsafe.Pointer) {
	words := *(*[2]unsafe.Pointer)(src)
	if words[0] == nil {
		return
	}
	dynamic, known := g.dynamicPlans[words[0]]
	if !known {
		dynamic = g.planFor(reflect.NewAt(plan.typ, src).Elem().Elem().Type())
		g.dynamicPlans[words[0]] = dynamic
	}
	if dynamic.kind == copyPointer {
		*(*[2]unsafe.Pointer)(dst) = [2]unsafe.Pointer{words[0], g.copyPointer(dynamic, words[1])}
		return
	}
	value := reflect.New(dynamic.typ).Elem()
	value.Set(reflect.NewAt(plan.typ, src).Elem().Elem())
	copied := reflect.New(dynamic.typ).Elem()
	g.copyInto(dynamic, copied.Addr().UnsafePointer(), value.Addr().UnsafePointer())
	reflect.NewAt(plan.typ, dst).Elem().Set(copied)
}
