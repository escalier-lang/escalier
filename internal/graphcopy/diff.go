package graphcopy

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"unsafe"
)

// graphDiff compares a live object graph against a snapshot a Copier took of it. Each
// object the copier copied is compared field by field with its copy, and a field that
// differs is recorded under the object's type and the name of its field the difference
// sits in.
type graphDiff struct {
	g     *Copier
	diffs map[string]string
	// planMu guards g.plans, which planFor writes when it meets a type for the
	// first time. Every graphDiff of one Diff call shares it.
	planMu *sync.RWMutex
}

// Diff returns each change made since the copy to an object of the graph g copied. A key
// names the changed object's type, and for a struct, the field of it the change sits in,
// as `solver.Context.classes`. Its value describes the first change found there.
//
// The objects are split across one goroutine per available CPU. Each goroutine
// records its own differences, and the results are merged once all have finished.
func (g *Copier) Diff() map[string]string {
	type entry struct {
		key  pointerKey
		snap unsafe.Pointer
	}
	entries := make([]entry, 0, len(g.pointers))
	for key, snap := range g.pointers {
		entries = append(entries, entry{key, snap})
	}

	workers := min(runtime.GOMAXPROCS(0), max(1, len(entries)))
	chunk := (len(entries) + workers - 1) / workers
	planMu := &sync.RWMutex{}
	results := make([]map[string]string, workers)
	var wg sync.WaitGroup
	for w := range workers {
		part := entries[min(w*chunk, len(entries)):min((w+1)*chunk, len(entries))]
		wg.Go(func() {
			d := &graphDiff{g: g, diffs: map[string]string{}, planMu: planMu}
			for _, e := range part {
				d.compare(e.key.plan, e.key.addr, e.snap, diffPath{owner: e.key.plan})
			}
			results[w] = d.diffs
		})
	}
	wg.Wait()

	diffs := map[string]string{}
	for _, result := range results {
		for key, change := range result {
			if _, seen := diffs[key]; !seen {
				diffs[key] = change
			}
		}
	}
	return diffs
}

// planFor returns g's plan for t. It holds planMu, since the goroutines of one Diff
// call share g.plans.
func (d *graphDiff) planFor(t reflect.Type) *typePlan {
	d.planMu.RLock()
	plan, done := d.g.plans[t]
	d.planMu.RUnlock()
	if done {
		return plan
	}
	d.planMu.Lock()
	defer d.planMu.Unlock()
	return d.g.planFor(t)
}

// diffPath names where a difference sits: the object it was found in, and the field of
// that object it was found under. Its string is built only when a difference is noted.
type diffPath struct {
	owner *typePlan
	field uintptr
	// inField is set once the walk has descended into one of owner's fields.
	inField bool
}

func (p diffPath) into(offset uintptr) diffPath {
	if p.inField || p.owner.typ.Kind() != reflect.Struct {
		return p
	}
	return diffPath{owner: p.owner, field: offset, inField: true}
}

func (p diffPath) String() string {
	name := p.owner.typ.String()
	if p.inField {
		name += "." + fieldAt(p.owner.typ, p.field)
	}
	return name
}

func (d *graphDiff) note(path diffPath, change string) {
	key := path.String()
	if _, seen := d.diffs[key]; !seen {
		d.diffs[key] = change
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
func (d *graphDiff) compare(plan *typePlan, live, snap unsafe.Pointer, path diffPath) {
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
		dynamic := d.g.dynamicPlans[lw[0]]
		if dynamic == nil {
			d.note(path, "holds a type the snapshot never held")
			return
		}
		if dynamic.kind == copyPointer {
			want, ok := d.snapshotOf(dynamic, lw[1])
			if !ok || want != sw[1] {
				d.note(path, "points elsewhere")
			}
			return
		}
		// Any other dynamic value is compared through an addressable copy of each side.
		a := reflect.New(dynamic.typ).Elem()
		a.Set(reflect.NewAt(plan.typ, live).Elem().Elem())
		b := reflect.New(dynamic.typ).Elem()
		b.Set(reflect.NewAt(plan.typ, snap).Elem().Elem())
		d.compare(dynamic, a.Addr().UnsafePointer(), b.Addr().UnsafePointer(), path)
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
			d.compare(plan.elem, unsafe.Add(ls.UnsafePointer(), offset), unsafe.Add(ss.UnsafePointer(), offset), path)
		}
	case copyMap:
		d.compareMap(plan, live, snap, path)
	case copyStruct:
		for _, run := range plan.bytes {
			l := unsafe.Slice((*byte)(unsafe.Add(live, run.offset)), run.size)
			s := unsafe.Slice((*byte)(unsafe.Add(snap, run.offset)), run.size)
			if string(l) != string(s) {
				d.compareRun(plan, run, live, snap, path)
			}
		}
		for _, field := range plan.fields {
			d.compare(field.plan, unsafe.Add(live, field.offset), unsafe.Add(snap, field.offset), path.into(field.offset))
		}
	case copyArray:
		size := plan.elem.typ.Size()
		for i := range plan.typ.Len() {
			offset := uintptr(i) * size
			d.compare(plan.elem, unsafe.Add(live, offset), unsafe.Add(snap, offset), path)
		}
	case copyRefuse:
		// The copier copied no non-nil func, channel, or unsafe pointer, so the snapshot
		// holds nil and any other live value was set since.
		if *(*unsafe.Pointer)(live) != *(*unsafe.Pointer)(snap) {
			d.note(path, "was set")
		}
	}
}

// compareRun notes each field of plan's struct type inside run whose bytes differ
// between live and snap.
func (d *graphDiff) compareRun(plan *typePlan, run byteRun, live, snap unsafe.Pointer, path diffPath) {
	for i := range plan.typ.NumField() {
		field := plan.typ.Field(i)
		if field.Offset < run.offset || field.Offset >= run.offset+run.size {
			continue
		}
		d.compare(d.planFor(field.Type), unsafe.Add(live, field.Offset), unsafe.Add(snap, field.Offset), path.into(field.Offset))
	}
}

// fieldAt returns the name of the first field of t at offset.
func fieldAt(t reflect.Type, offset uintptr) string {
	for i := range t.NumField() {
		if t.Field(i).Offset == offset {
			return t.Field(i).Name
		}
	}
	return "?"
}

// compareMap compares two maps by length and, for each live key whose snapshot key it can
// name, by the value under it.
func (d *graphDiff) compareMap(plan *typePlan, live, snap unsafe.Pointer, path diffPath) {
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
	// The loop reuses one addressable value for each of the live key, the
	// snapshot key, and the two values, so an entry allocates nothing for them.
	liveKey := reflect.New(plan.key.typ).Elem()
	snapKey := reflect.New(plan.key.typ).Elem()
	a := reflect.New(plan.value.typ).Elem()
	b := reflect.New(plan.value.typ).Elem()
	iter := lm.MapRange()
	for iter.Next() {
		liveKey.SetIterKey(iter)
		key, ok := d.snapshotKey(plan.key, liveKey, snapKey)
		if !ok {
			d.note(path, "holds a key the snapshot lacks")
			continue
		}
		snapValue := sm.MapIndex(key)
		if !snapValue.IsValid() {
			d.note(path, "holds a key the snapshot lacks")
			continue
		}
		a.SetIterValue(iter)
		b.Set(snapValue)
		d.compare(plan.value, a.Addr().UnsafePointer(), b.Addr().UnsafePointer(), path)
	}
}

// snapshotKey returns the snapshot's counterpart of a live map key. A key holding no
// pointer is its own counterpart, and a pointer key's is its copy. ok is false for any
// other key, and for a pointer the snapshot did not copy.
//
// out is an addressable value of the key's type that a pointer key's counterpart is
// written into and returned as. It is overwritten by the next call.
func (d *graphDiff) snapshotKey(plan *typePlan, key, out reflect.Value) (reflect.Value, bool) {
	if d.pointerFree(plan) {
		return key, true
	}
	switch plan.kind {
	case copyPointer:
		snap, ok := d.snapshotOf(plan, key.UnsafePointer())
		if !ok {
			return reflect.Value{}, false
		}
		*(*unsafe.Pointer)(out.Addr().UnsafePointer()) = snap
		return out, true
	case copyInterface:
		if key.IsNil() {
			return key, true
		}
		dynamic := d.planFor(key.Elem().Type())
		if dynamic.kind != copyPointer {
			return key, d.pointerFree(dynamic)
		}
		snap, ok := d.snapshotOf(dynamic, key.Elem().UnsafePointer())
		if !ok {
			return reflect.Value{}, false
		}
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
