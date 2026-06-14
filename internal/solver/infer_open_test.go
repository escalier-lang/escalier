package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferOpenParam exercises the `open` parameter marker end to end (M4 B2): an
// `open` param's usage-inferred object renders row-polymorphic (inexact), while an
// un-`open` peer closes to exact (the B1 Policy-A close). Passing an object with
// extra fields to the open param checks.
func TestInferOpenParam(t *testing.T) {
	t.Run("open param renders inexact", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn dist(open p) { p.x\n p.y }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: unknown, y: unknown, ...}) -> void", values["dist"])
	})

	t.Run("un-open peer renders exact", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn dist(p) { p.x\n p.y }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: unknown, y: unknown}) -> void", values["dist"])
	})

	t.Run("passing extra fields to an open param checks", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn foo(open p) { p.x\n p.y }\nval r = foo({x: 1, y: 2, z: 3})")
		require.Empty(t, errs)
	})

	// The operative seal (B2): a closed param's requirement is sealed to exact at
	// generalization, so a call passing an object with extra fields is rejected.
	t.Run("passing extra fields to a closed param rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn foo(p) { p.x\n p.y }\nval r = foo({x: 1, y: 2, z: 3})")
		require.Len(t, errs, 1)
		require.Equal(t, "object has extra property: z", errs[0].Message())
	})

	// An exact argument still checks against a closed param.
	t.Run("passing the exact shape to a closed param checks", func(t *testing.T) {
		_, _, errs := inferSource(t, "fn foo(p) { p.x\n p.y }\nval r = foo({x: 1, y: 2})")
		require.Empty(t, errs)
	})

	// `open` is provisional and context-sensitive: a param literally named `open`
	// (no following pattern) is an ordinary binding, not a marker.
	t.Run("open as a plain param name is not a marker", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f(open) { return open }")
		require.Empty(t, errs)
		require.Equal(t, "fn <T0>(open: T0) -> T0", values["f"])
	})
}
