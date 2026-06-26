package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// TestMultiBoundRenders is the end-to-end demo that the engine→coalesce→print
// pipeline is wired up correctly: a variable with two distinct lower bounds,
// coalesced in positive position, renders as a union of those bounds.
//
// It lives in package solver (not soltype) because it drives the engine's
// unexported Context/freshVar and coalesce, then reaches soltype.Print across
// the package boundary. soltype must not import solver, so the pipeline can only
// be exercised from this side.
func TestMultiBoundRenders(t *testing.T) {
	c := &Context{}
	a := c.freshVar(1)
	a.LowerBounds = []soltype.Type{num(), str()}
	got := soltype.Print(coalesce(a, soltype.Positive))
	require.Equal(t, "number | string", got)
}
