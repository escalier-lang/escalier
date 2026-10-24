package solver

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

func TestGraphCopier(t *testing.T) {
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
			dst := copyPtr(newGraphCopier(tt.shared), src)
			require.NotSame(t, src, dst)
			tt.check(t, src, dst)
		})
	}
}

func TestGraphCopierCopiesValueRecursiveType(t *testing.T) {
	src := &copyOuter{inners: map[string]copyInner{
		"a": {outer: copyOuter{leaf: &copyLeaf{label: "inner"}}},
	}}
	dst := copyPtr(newGraphCopier(copyNoLeaves), src)
	require.NotSame(t, src.inners["a"].outer.leaf, dst.inners["a"].outer.leaf)
	require.Equal(t, "inner", dst.inners["a"].outer.leaf.label)
}

func TestGraphCopierRefusesFunc(t *testing.T) {
	src := &copyNode{callback: func() {}}
	require.PanicsWithValue(t, "graphCopier: cannot copy a non-nil func()", func() {
		copyPtr(newGraphCopier(copyNoLeaves), src)
	})
}
