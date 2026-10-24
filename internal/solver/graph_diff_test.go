package solver

import (
	"fmt"
	"reflect"
	"unsafe"
)

// graphDiff compares a live object graph against a snapshot graphCopier took of it. Each
// object the copier copied is compared field by field with its copy, and a field that
// differs is recorded under the object's type and the field's path.
type graphDiff struct {
	g     *graphCopier
	diffs map[string]string
}

// diffAgainstSnapshot returns, by type and field path, each change made to an object of
// the graph g copied since the copy. A path's value describes its first change.
func diffAgainstSnapshot(g *graphCopier) map[string]string {
	d := &graphDiff{g: g, diffs: map[string]string{}}
	for key, snap := range g.pointers {
		d.compare(key.plan, key.addr, snap, key.plan.typ.String())
	}
	return d.diffs
}

func (d *graphDiff) note(path, change string) {
	if _, seen := d.diffs[path]; !seen {
		d.diffs[path] = change
	}
}

// snapshotOf returns the snapshot's counterpart of a live pointer of plan's type, and
// whether the live pointer has one.
func (d *graphDiff) snapshotOf(plan *typePlan, live unsafe.Pointer) (unsafe.Pointer, bool) {
	if live == nil || plan.shared {
		return live, true
	}
	if plan.elem == nil {
		return nil, false
	}
	snap, ok := d.g.pointers[pointerKey{live, plan.elem}]
	return snap, ok
}

// compare records each difference between the live value at live and the snapshot value
// at snap, both of plan's type.
func (d *graphDiff) compare(plan *typePlan, live, snap unsafe.Pointer, path string) {
	switch plan.kind {
	case copyBytes:
		size := plan.typ.Size()
		if size > 0 && string(unsafe.Slice((*byte)(live), size)) != string(unsafe.Slice((*byte)(snap), size)) {
			d.note(path, fmt.Sprintf("%v became %v", reflect.NewAt(plan.typ, snap).Elem(), reflect.NewAt(plan.typ, live).Elem()))
		}
	case copyString:
		if *(*string)(live) != *(*string)(snap) {
			d.note(path, fmt.Sprintf("%q became %q", *(*string)(snap), *(*string)(live)))
		}
	case copyPointer:
		want, ok := d.snapshotOf(plan, *(*unsafe.Pointer)(live))
		if !ok || want != *(*unsafe.Pointer)(snap) {
			d.note(path, "points elsewhere")
		}
	case copyInterface:
		lw, sw := *(*[2]unsafe.Pointer)(live), *(*[2]unsafe.Pointer)(snap)
		if lw[0] != sw[0] {
			d.note(path, "holds another type")
			return
		}
		if lw[0] == nil {
			return
		}
		if dynamic := d.g.dynamicPlans[lw[0]]; dynamic != nil && dynamic.kind == copyPointer {
			want, ok := d.snapshotOf(dynamic, lw[1])
			if !ok || want != sw[1] {
				d.note(path, "points elsewhere")
			}
		}
	case copySlice:
		ls, ss := reflect.NewAt(plan.typ, live).Elem(), reflect.NewAt(plan.typ, snap).Elem()
		if ls.Len() != ss.Len() {
			d.note(path, fmt.Sprintf("length %d became %d", ss.Len(), ls.Len()))
			return
		}
		if ls.Len() == 0 || plan.elem == nil {
			return
		}
		size := plan.elem.typ.Size()
		for i := range ls.Len() {
			offset := uintptr(i) * size
			d.compare(plan.elem, unsafe.Add(ls.UnsafePointer(), offset), unsafe.Add(ss.UnsafePointer(), offset), path+"[]")
		}
	case copyMap:
		d.compareMap(plan, live, snap, path)
	case copyStruct:
		for i := range plan.typ.NumField() {
			field := plan.typ.Field(i)
			d.compare(d.g.planFor(field.Type), unsafe.Add(live, field.Offset), unsafe.Add(snap, field.Offset), path+"."+field.Name)
		}
	case copyArray:
		size := plan.elem.typ.Size()
		for i := range plan.typ.Len() {
			offset := uintptr(i) * size
			d.compare(plan.elem, unsafe.Add(live, offset), unsafe.Add(snap, offset), path+"[]")
		}
	}
}

// compareMap compares two maps by length and, for each live key whose snapshot key it can
// name, by the value under it.
func (d *graphDiff) compareMap(plan *typePlan, live, snap unsafe.Pointer, path string) {
	lm, sm := reflect.NewAt(plan.typ, live).Elem(), reflect.NewAt(plan.typ, snap).Elem()
	if lm.IsNil() != sm.IsNil() {
		d.note(path, "was created or dropped")
		return
	}
	if lm.IsNil() {
		return
	}
	if lm.Len() != sm.Len() {
		d.note(path, fmt.Sprintf("length %d became %d", sm.Len(), lm.Len()))
		return
	}
	if plan.key == nil {
		return
	}
	liveKey := reflect.New(plan.key.typ).Elem()
	iter := lm.MapRange()
	for iter.Next() {
		liveKey.SetIterKey(iter)
		snapKey, ok := d.snapshotKey(plan.key, liveKey)
		if !ok {
			d.note(path+"{}", "holds a key the snapshot lacks")
			continue
		}
		snapValue := sm.MapIndex(snapKey)
		if !snapValue.IsValid() {
			d.note(path+"{}", "holds a key the snapshot lacks")
			continue
		}
		a := reflect.New(plan.value.typ).Elem()
		a.Set(iter.Value())
		b := reflect.New(plan.value.typ).Elem()
		b.Set(snapValue)
		d.compare(plan.value, a.Addr().UnsafePointer(), b.Addr().UnsafePointer(), path+"{}")
	}
}

// snapshotKey returns the snapshot's counterpart of a live map key. A key holding no
// pointer is its own counterpart, and a pointer key's is its copy. ok is false for any
// other key, and for a pointer the snapshot did not copy.
func (d *graphDiff) snapshotKey(plan *typePlan, key reflect.Value) (reflect.Value, bool) {
	if d.pointerFree(plan) {
		return key, true
	}
	switch plan.kind {
	case copyPointer:
		snap, ok := d.snapshotOf(plan, key.UnsafePointer())
		if !ok {
			return reflect.Value{}, false
		}
		return reflect.NewAt(plan.typ.Elem(), snap), true
	case copyInterface:
		if key.IsNil() {
			return key, true
		}
		dynamic := d.g.planFor(key.Elem().Type())
		if dynamic.kind != copyPointer {
			return key, d.pointerFree(dynamic)
		}
		snap, ok := d.snapshotOf(dynamic, key.Elem().UnsafePointer())
		if !ok {
			return reflect.Value{}, false
		}
		out := reflect.New(plan.typ).Elem()
		out.Set(reflect.NewAt(dynamic.typ.Elem(), snap))
		return out, true
	default:
		return reflect.Value{}, false
	}
}

// pointerFree reports whether a value of plan's type holds nothing but bytes and
// strings, so the snapshot holds an equal value for it.
func (d *graphDiff) pointerFree(plan *typePlan) bool {
	switch plan.kind {
	case copyBytes, copyString:
		return true
	case copyStruct:
		for _, field := range plan.fields {
			if !d.pointerFree(field.plan) {
				return false
			}
		}
		return true
	case copyArray:
		return d.pointerFree(plan.elem)
	default:
		return false
	}
}
