package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFreshVarSequencing(t *testing.T) {
	c := &Context{}

	a := c.freshVar(0)
	require.Equal(t, 0, a.ID)
	require.Equal(t, 0, a.Level)

	b := c.freshVar(1)
	require.Equal(t, 1, b.ID)
	require.Equal(t, 1, b.Level)

	d := c.freshVar(1)
	require.Equal(t, 2, d.ID)
	require.Equal(t, 1, d.Level)

	// Fresh variables start with no bounds.
	require.Empty(t, a.LowerBounds)
	require.Empty(t, a.UpperBounds)

	// Each call yields a distinct pointer.
	require.NotSame(t, a, b)
	require.NotSame(t, b, d)
}
