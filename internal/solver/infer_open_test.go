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
		values, _, errs := inferSource(t, `fn dist(open p) { p.x
 p.y }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: unknown, y: unknown, ...}) -> void", values["dist"])
	})

	t.Run("un-open peer renders exact", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn dist(p) { p.x
 p.y }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {x: unknown, y: unknown}) -> void", values["dist"])
	})

	t.Run("passing extra fields to an open param checks", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(open p) { p.x
 p.y }
val r = foo({x: 1, y: 2, z: 3})`)
		require.Empty(t, errs)
	})

	// The operative seal (B2): a closed param's requirement is sealed to exact at
	// generalization, so a call passing an object with extra fields is rejected.
	t.Run("passing extra fields to a closed param rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(p) { p.x
 p.y }
val r = foo({x: 1, y: 2, z: 3})`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:29-3:30: object has extra property: z", msgWithSpan(errs[0]))
	})

	// An exact argument still checks against a closed param.
	t.Run("passing the exact shape to a closed param checks", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(p) { p.x
 p.y }
val r = foo({x: 1, y: 2})`)
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

// TestInferOpenParamNested pins the DEEP semantics of `open`: it makes the param's
// whole inferred shape row-polymorphic, every nested object included — not just the
// top object. The un-`open` peer seals every level to exact. So a closed param
// rejects an extra field at ANY depth, while an open param accepts one at any depth.
func TestInferOpenParamNested(t *testing.T) {
	t.Run("open renders inexact at every level", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn foo(open p) { p.a.b }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {a: {b: unknown, ...}, ...}) -> void", values["foo"])
	})

	t.Run("closed seals every level to exact", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn foo(p) { p.a.b }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: {a: {b: unknown}}) -> void", values["foo"])
	})

	t.Run("open accepts an extra field on the nested object", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(open p) { p.a.b }
val r = foo({a: {b: 1, c: 2}})`)
		require.Empty(t, errs)
	})

	t.Run("open accepts an extra field on the outer object", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(open p) { p.a.b }
val r = foo({a: {b: 1}, d: 2})`)
		require.Empty(t, errs)
	})

	t.Run("closed rejects an extra field on the nested object", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(p) { p.a.b }
val r = foo({a: {b: 1, c: 2}})`)
		require.Len(t, errs, 1)
		require.Equal(t, "2:27-2:28: object has extra property: c", msgWithSpan(errs[0]))
	})

	t.Run("closed rejects an extra field on the outer object", func(t *testing.T) {
		_, _, errs := inferSource(t, `fn foo(p) { p.a.b }
val r = foo({a: {b: 1}, d: 2})`)
		require.Len(t, errs, 1)
		require.Equal(t, "2:28-2:29: object has extra property: d", msgWithSpan(errs[0]))
	})
}
