package solver

import (
	"maps"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestProvTableChildReadsItsParent asserts that a child table returns the origin its
// parent recorded, and that a write to the child does not reach the parent.
func TestProvTableChildReadsItsParent(t *testing.T) {
	parent := newProvTable()
	inherited, added := num(), num()
	parent.set(inherited, FromAST{Node: &ast.IdentExpr{Name: "a"}})

	child := parent.child()
	child.set(added, FromAST{Node: &ast.IdentExpr{Name: "b"}})

	o, ok := child.get(inherited)
	require.True(t, ok)
	require.Equal(t, "a", o.(FromAST).Node.(*ast.IdentExpr).Name)
	_, ok = parent.get(added)
	require.False(t, ok)
}

// TestProvTableChildShadowsItsParent asserts that an origin a child records for a type
// its parent also holds is the one the child returns, and that the parent keeps its own.
func TestProvTableChildShadowsItsParent(t *testing.T) {
	parent := newProvTable()
	ty := num()
	parent.set(ty, FromInstantiation{})

	child := parent.child()
	child.set(ty, FromNormalization{})

	o, _ := child.get(ty)
	require.IsType(t, FromNormalization{}, o)
	o, _ = parent.get(ty)
	require.IsType(t, FromInstantiation{}, o)
}

// TestProvTableRefusesAWriteOnceFrozen asserts that set panics on a table a child was
// taken from.
func TestProvTableRefusesAWriteOnceFrozen(t *testing.T) {
	parent := newProvTable()
	parent.child()
	require.Panics(t, func() { parent.set(num(), FromInstantiation{}) })
}

// TestProvTableAllYieldsEachTypeOnce asserts that all yields each type once across
// three layers, with the origin of the nearest layer holding it.
func TestProvTableAllYieldsEachTypeOnce(t *testing.T) {
	a, b, c := num(), num(), num()
	root := newProvTable()
	root.set(a, FromInstantiation{From: a})
	root.set(b, FromInstantiation{From: b})
	middle := root.child()
	middle.set(b, FromInstantiation{From: c})
	top := middle.child()
	top.set(c, FromInstantiation{From: c})

	got := maps.Collect(top.all())

	require.Equal(t, map[soltype.Type]Origin{
		a: FromInstantiation{From: a},
		b: FromInstantiation{From: c},
		c: FromInstantiation{From: c},
	}, got)
	require.Equal(t, 3, top.size())
}

// TestProvTableDiscardedProbeLeavesTheParent asserts that a discarded probe on a forked
// checker removes what it recorded, including an origin that shadowed the parent's.
// The parent's origin is the one the fork returns afterwards.
func TestProvTableDiscardedProbeLeavesTheParent(t *testing.T) {
	c := newTestChecker()
	ty := num()
	c.recordProv(ty, &ast.IdentExpr{Name: "parent"}, LiteralInference)
	c.prov = c.prov.child()

	p := c.openProbe()
	c.snapshotProv(ty)
	c.prov.set(ty, FromNormalization{})
	c.recordInstantiation(&soltype.TypeVarType{}, ty)
	c.closeProbe(p, false)

	require.Zero(t, len(c.prov.own))
	node, ok := c.prov.NodeFor(ty)
	require.True(t, ok)
	require.Equal(t, "parent", node.(*ast.IdentExpr).Name)
}
