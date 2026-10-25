package graphcopy

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

type copyNode struct {
	name     string
	count    int
	next     *copyNode
	children []*copyNode
	byNode   map[*copyNode]string
	value    any
	shape    copyShape
	pair     [2]*copyNode
	leaf     *copyLeaf
	callback func()
}

type copyShape interface{ area() int }

type copySquare struct{ side int }

func (s copySquare) area() int { return s.side * s.side }

type copyCircle struct{ radius int }

func (c *copyCircle) area() int { return 3 * c.radius * c.radius }

type copyLeaf struct{ label string }

// copyOuter reaches itself by value through a map, so planning copyInner meets
// copyOuter while copyOuter's own plan is still being built.
type copyOuter struct {
	inners map[string]copyInner
	leaf   *copyLeaf
}

type copyInner struct{ outer copyOuter }

func copyNoLeaves(reflect.Type) bool { return false }

func copySharedLeaves(t reflect.Type) bool { return t == reflect.TypeFor[*copyLeaf]() }

func TestCopy(t *testing.T) {
	cases := []struct {
		name   string
		shared func(reflect.Type) bool
		build  func() *copyNode
		check  func(t *testing.T, src, dst *copyNode)
	}{
		{
			name:   "copies unexported scalars",
			shared: copyNoLeaves,
			build:  func() *copyNode { return &copyNode{name: "a", count: 3} },
			check: func(t *testing.T, src, dst *copyNode) {
				require.Equal(t, "a", dst.name)
				require.Equal(t, 3, dst.count)
			},
		},
		{
			name:   "keeps a cycle a cycle",
			shared: copyNoLeaves,
			build: func() *copyNode {
				n := &copyNode{name: "self"}
				n.next = n
				return n
			},
			check: func(t *testing.T, src, dst *copyNode) {
				require.Same(t, dst, dst.next)
			},
		},
		{
			name:   "copies a node reached twice once",
			shared: copyNoLeaves,
			build: func() *copyNode {
				child := &copyNode{name: "child"}
				return &copyNode{next: child, children: []*copyNode{child}, pair: [2]*copyNode{child, nil}}
			},
			check: func(t *testing.T, src, dst *copyNode) {
				require.NotSame(t, src.next, dst.next)
				require.Same(t, dst.next, dst.children[0])
				require.Same(t, dst.next, dst.pair[0])
			},
		},
		{
			name:   "rewrites map keys to their copies",
			shared: copyNoLeaves,
			build: func() *copyNode {
				child := &copyNode{name: "child"}
				return &copyNode{next: child, byNode: map[*copyNode]string{child: "c"}}
			},
			check: func(t *testing.T, src, dst *copyNode) {
				require.Equal(t, map[*copyNode]string{dst.next: "c"}, dst.byNode)
			},
		},
		{
			name:   "copies a pointer held in an interface",
			shared: copyNoLeaves,
			build: func() *copyNode {
				return &copyNode{shape: &copyCircle{radius: 2}, value: &copyLeaf{label: "boxed"}}
			},
			check: func(t *testing.T, src, dst *copyNode) {
				require.NotSame(t, src.shape, dst.shape)
				require.Equal(t, 12, dst.shape.area())
				require.NotSame(t, src.value, dst.value)
				require.Equal(t, &copyLeaf{label: "boxed"}, dst.value)
			},
		},
		{
			name:   "copies a value held in an interface",
			shared: copyNoLeaves,
			build:  func() *copyNode { return &copyNode{shape: copySquare{side: 3}, value: "text"} },
			check: func(t *testing.T, src, dst *copyNode) {
				require.Equal(t, copySquare{side: 3}, dst.shape)
				require.Equal(t, "text", dst.value)
			},
		},
		{
			name:   "leaves a shared pointer pointing at the source",
			shared: copySharedLeaves,
			build:  func() *copyNode { return &copyNode{leaf: &copyLeaf{label: "kept"}} },
			check: func(t *testing.T, src, dst *copyNode) {
				require.Same(t, src.leaf, dst.leaf)
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			src := tt.build()
			dst := Copy(New(tt.shared), src)
			require.NotSame(t, src, dst)
			tt.check(t, src, dst)
		})
	}
}

func TestCopyCopiesValueRecursiveType(t *testing.T) {
	src := &copyOuter{inners: map[string]copyInner{
		"a": {outer: copyOuter{leaf: &copyLeaf{label: "inner"}}},
	}}
	dst := Copy(New(copyNoLeaves), src)
	require.NotSame(t, src.inners["a"].outer.leaf, dst.inners["a"].outer.leaf)
	require.Equal(t, "inner", dst.inners["a"].outer.leaf.label)
}

func TestCopyRefusesFunc(t *testing.T) {
	src := &copyNode{callback: func() {}}
	require.PanicsWithValue(t, "graphcopy: cannot copy a non-nil func()", func() {
		Copy(New(copyNoLeaves), src)
	})
}

// TestCopies asserts that Copies returns the copy of each object of the type it names,
// and of no other type.
func TestCopies(t *testing.T) {
	src := &copyNode{name: "a", next: &copyNode{name: "b"}, leaf: &copyLeaf{label: "l"}}
	g := New(copyNoLeaves)
	dst := Copy(g, src)

	nodes := Copies[copyNode](g)
	require.ElementsMatch(t, []*copyNode{dst, dst.next}, nodes)
	require.Equal(t, []*copyLeaf{dst.leaf}, Copies[copyLeaf](g))
	require.Empty(t, Copies[copyOuter](g))
}

// TestDiff covers the changes Diff reports for a graph written to after it was copied.
// Each row changes one field of the source and expects the key and the description
// Diff gives it.
func TestDiff(t *testing.T) {
	cases := []struct {
		name   string
		change func(src *copyNode)
		want   map[string]string
	}{
		{
			name:   "unchanged",
			change: func(*copyNode) {},
			want:   map[string]string{},
		},
		{
			name:   "scalar",
			change: func(src *copyNode) { src.count = 4 },
			want:   map[string]string{"graphcopy.copyNode.count": "3 became 4"},
		},
		{
			name:   "string",
			change: func(src *copyNode) { src.name = "b" },
			want:   map[string]string{"graphcopy.copyNode.name": `"a" became "b"`},
		},
		{
			name:   "pointer",
			change: func(src *copyNode) { src.next = &copyNode{} },
			want:   map[string]string{"graphcopy.copyNode.next": "points elsewhere"},
		},
		{
			name:   "slice length",
			change: func(src *copyNode) { src.children = append(src.children, src) },
			want:   map[string]string{"graphcopy.copyNode.children": "length 1 became 2"},
		},
		{
			name:   "map length",
			change: func(src *copyNode) { src.byNode[&copyNode{}] = "x" },
			want:   map[string]string{"graphcopy.copyNode.byNode": "length 1 became 2"},
		},
		{
			name:   "map value",
			change: func(src *copyNode) { src.byNode[src] = "other" },
			want:   map[string]string{"graphcopy.copyNode.byNode": `"self" became "other"`},
		},
		{
			name:   "interface holding a value",
			change: func(src *copyNode) { src.value = 6 },
			want:   map[string]string{"graphcopy.copyNode.value": "5 became 6"},
		},
		{
			name:   "interface holding another type",
			change: func(src *copyNode) { src.shape = copySquare{side: 1} },
			want:   map[string]string{"graphcopy.copyNode.shape": "holds another type"},
		},
		{
			name:   "interface holding another pointer",
			change: func(src *copyNode) { src.shape = &copyCircle{radius: 1} },
			want:   map[string]string{"graphcopy.copyNode.shape": "points elsewhere"},
		},
		{
			name:   "array element",
			change: func(src *copyNode) { src.pair[1] = src },
			want:   map[string]string{"graphcopy.copyNode.pair": "points elsewhere"},
		},
		{
			name:   "func",
			change: func(src *copyNode) { src.callback = func() {} },
			want:   map[string]string{"graphcopy.copyNode.callback": "was set"},
		},
		{
			name:   "nested object",
			change: func(src *copyNode) { src.leaf.label = "m" },
			want:   map[string]string{"graphcopy.copyLeaf.label": `"l" became "m"`},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			src := &copyNode{name: "a", count: 3, leaf: &copyLeaf{label: "l"}, value: 5, shape: &copyCircle{radius: 1}}
			src.children = []*copyNode{src}
			src.pair = [2]*copyNode{src, nil}
			src.byNode = map[*copyNode]string{src: "self"}
			g := New(copyNoLeaves)
			Copy(g, src)
			tt.change(src)
			require.Equal(t, tt.want, g.Diff())
		})
	}
}

type diffPoint struct{ x, y int }

type diffPtrKey struct{ p *int }

type diffInner struct{ leaf *copyLeaf }

type diffOther struct{ n int }

// diffRecord holds the map keys, nested structs, and field layouts copyNode lacks.
type diffRecord struct {
	byName  map[string]int
	byPoint map[diffPoint]int
	byShape map[copyShape]int
	alias   map[string]int
	created map[string]int
	value   any
	other   *diffOther
	inner   diffInner
	cells   [2]diffInner
	grid    [3]int
	empty   []int
	nothing struct{}
	a, b    int
}

// TestDiffKeysAndLayouts covers what Diff reports for map keys of each kind, for nested
// structs and arrays of them, and for values the snapshot never saw. Each row changes
// one field of the source and expects the key and the description Diff gives it.
func TestDiffKeysAndLayouts(t *testing.T) {
	cases := []struct {
		name   string
		change func(src *diffRecord)
		want   map[string]string
	}{
		{
			name:   "unchanged",
			change: func(*diffRecord) {},
			want:   map[string]string{},
		},
		{
			name:   "string key replaced",
			change: func(src *diffRecord) { delete(src.byName, "a"); src.byName["b"] = 1 },
			want: map[string]string{
				"graphcopy.diffRecord.alias":  "holds a key the snapshot lacks",
				"graphcopy.diffRecord.byName": "holds a key the snapshot lacks",
			},
		},
		{
			name:   "struct key value",
			change: func(src *diffRecord) { src.byPoint[diffPoint{1, 2}] = 9 },
			want:   map[string]string{"graphcopy.diffRecord.byPoint": "3 became 9"},
		},
		{
			name: "interface key holding a new pointer",
			change: func(src *diffRecord) {
				for k := range src.byShape {
					if _, ok := k.(*copyCircle); ok {
						delete(src.byShape, k)
					}
				}
				src.byShape[&copyCircle{radius: 2}] = 1
			},
			want: map[string]string{"graphcopy.diffRecord.byShape": "holds a key the snapshot lacks"},
		},
		{
			name:   "interface key holding a value",
			change: func(src *diffRecord) { src.byShape[copySquare{side: 1}] = 7 },
			want:   map[string]string{"graphcopy.diffRecord.byShape": "5 became 7"},
		},
		{
			name:   "nil interface key",
			change: func(src *diffRecord) { src.byShape[nil] = 8 },
			want:   map[string]string{"graphcopy.diffRecord.byShape": "6 became 8"},
		},
		{
			name:   "aliased map",
			change: func(src *diffRecord) { src.alias["a"] = 4 },
			want: map[string]string{
				"graphcopy.diffRecord.alias":  "1 became 4",
				"graphcopy.diffRecord.byName": "1 became 4",
			},
		},
		{
			name:   "map created",
			change: func(src *diffRecord) { src.created = map[string]int{} },
			want:   map[string]string{"graphcopy.diffRecord.created": "was created or dropped"},
		},
		{
			name:   "nil interface set",
			change: func(src *diffRecord) { src.value = diffPoint{} },
			want:   map[string]string{"graphcopy.diffRecord.value": "holds another type"},
		},
		{
			name:   "pointer of a type never copied",
			change: func(src *diffRecord) { src.other = &diffOther{} },
			want:   map[string]string{"graphcopy.diffRecord.other": "points elsewhere"},
		},
		{
			name:   "nested struct",
			change: func(src *diffRecord) { src.inner.leaf = &copyLeaf{} },
			want:   map[string]string{"graphcopy.diffRecord.inner": "points elsewhere"},
		},
		{
			name:   "array of structs",
			change: func(src *diffRecord) { src.cells[1].leaf = &copyLeaf{} },
			want:   map[string]string{"graphcopy.diffRecord.cells": "points elsewhere"},
		},
		{
			name:   "array of bytes",
			change: func(src *diffRecord) { src.grid[2] = 1 },
			want:   map[string]string{"graphcopy.diffRecord.grid": "[0 0 0] became [0 0 1]"},
		},
		{
			name:   "adjacent scalars",
			change: func(src *diffRecord) { src.b = 1 },
			want:   map[string]string{"graphcopy.diffRecord.b": "0 became 1"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			byName := map[string]int{"a": 1}
			src := &diffRecord{
				byName:  byName,
				byPoint: map[diffPoint]int{{1, 2}: 3},
				byShape: map[copyShape]int{&copyCircle{radius: 1}: 4, copySquare{side: 1}: 5, nil: 6},
				alias:   byName,
				empty:   []int{},
			}
			g := New(copyNoLeaves)
			Copy(g, src)
			tt.change(src)
			require.Equal(t, tt.want, g.Diff())
		})
	}
}

type diffKeyed struct{ byKey map[diffPtrKey]int }

// TestDiffReportsAKeyHoldingAPointerInAStruct asserts that Diff reports a map keyed by a
// struct holding a pointer even when nothing changed. Diff cannot name the snapshot's
// counterpart of such a key.
func TestDiffReportsAKeyHoldingAPointerInAStruct(t *testing.T) {
	src := &diffKeyed{byKey: map[diffPtrKey]int{{p: new(int)}: 1}}
	g := New(copyNoLeaves)
	Copy(g, src)
	require.Equal(t, map[string]string{"graphcopy.diffKeyed.byKey": "holds a key the snapshot lacks"}, g.Diff())
}
