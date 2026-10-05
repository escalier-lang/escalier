package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// newBorrowedRead builds a read of the property `n` through an immutable borrow at lvl.
func newBorrowedRead(c *checker, lvl int) borrowedRead {
	req := &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "n", Type: c.freshAt(lvl)}},
		Inexact: true,
	}
	shape := &soltype.ObjectType{
		Elems:   []soltype.ObjTypeElem{&soltype.PropertyElem{Name: "n", Type: c.freshAt(lvl)}},
		Inexact: true,
	}
	return borrowedRead{
		borrow: &soltype.RefType{Lt: c.ctx.freshLifetime(lvl), Inner: req},
		req:    req,
		shape:  shape,
	}
}

// TestProbeDiscardDropsBorrowedReads asserts that a borrowed read recorded under a probe is
// removed when the probe is discarded and kept when it is committed.
func TestProbeDiscardDropsBorrowedReads(t *testing.T) {
	c := newTestChecker()
	v := c.freshAt(0)
	c.ctx.addBorrowedRead(v, newBorrowedRead(c, 0))

	p := c.openProbe()
	c.ctx.addBorrowedRead(v, newBorrowedRead(c, 0))
	require.Len(t, c.ctx.borrowedReads[v], 2)
	c.closeProbe(p, false)
	require.Len(t, c.ctx.borrowedReads[v], 1)

	p = c.openProbe()
	c.ctx.addBorrowedRead(v, newBorrowedRead(c, 0))
	c.closeProbe(p, true)
	require.Len(t, c.ctx.borrowedReads[v], 2)
}

// TestNegativeExtrusionCopiesBorrowedReads asserts that extruding a variable in negative
// position gives the fresh variable the original's borrowed reads. A concrete bound that
// reaches the fresh variable does not reach the original, so the reads have to travel with it.
func TestNegativeExtrusionCopiesBorrowedReads(t *testing.T) {
	c := newTestChecker()
	v := c.freshAt(1)
	c.ctx.addBorrowedRead(v, newBorrowedRead(c, 1))

	extruded := c.ctx.extrude(v, soltype.Negative, 0, map[extrudeKey]*soltype.TypeVarType{})
	nv, ok := extruded.(*soltype.TypeVarType)
	require.True(t, ok)
	require.NotSame(t, v, nv)
	require.Len(t, c.ctx.borrowedReads[nv], 1)
	require.Equal(t, 0, soltype.LevelOf(c.ctx.borrowedReads[nv][0].req))
}

// TestBorrowedReadSkipsAVariableLowerBound asserts that reading through a pointee whose only
// lower bound is a variable records the read and reports nothing.
func TestBorrowedReadSkipsAVariableLowerBound(t *testing.T) {
	c := newTestChecker()
	v := c.freshAt(0)
	c.ctx.addLowerBound(v, c.freshAt(0))
	read := newBorrowedRead(c, 0)

	errs := c.ctx.constrainBorrowedVarFieldRead(read.borrow, v, read.req, newSeenPairs())
	require.Empty(t, errs)
	require.Len(t, c.ctx.borrowedReads[v], 1)
}
