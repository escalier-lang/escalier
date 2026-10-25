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
