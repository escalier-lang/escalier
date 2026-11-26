package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferOpenParam covers the `open` parameter marker (M4 B2). On a parameter with a
// written type the marker changes nothing, so a written inexact object stays inexact and
// accepts extra fields, and an exact one stays exact. A parameter named `open` is an
// ordinary binding.
func TestInferOpenParam(t *testing.T) {
	t.Run("an open param keeps its inexact type", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn dist(open p: &{x: unknown, y: unknown, ...}) { p.x
 p.y }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: &{x: unknown, y: unknown, ...}) -> undefined", values["dist"])
	})

	t.Run("a param without the marker keeps its exact type", func(t *testing.T) {
		values, _, errs := inferSource(t, `fn dist(p: &{x: unknown, y: unknown}) { p.x
 p.y }`)
		require.Empty(t, errs)
		require.Equal(t, "fn (p: &{x: unknown, y: unknown}) -> undefined", values["dist"])
	})

	// `open` is provisional and context-sensitive: a param literally named `open`
	// (no following pattern) is an ordinary binding, not a marker.
	t.Run("open as a plain param name is not a marker", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn f<T0>(open: T0) { return open }")
		require.Empty(t, errs)
		require.Equal(t, "fn <T0>(open: T0) -> T0", values["f"])
	})

	// Each call below goes to a callee declared with the signature "an open param keeps its
	// inexact type" or "a param without the marker keeps its exact type" asserts.
	calls := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "passing extra fields to an open param checks",
			src: `declare fn foo(p: &{x: unknown, y: unknown, ...}) -> undefined
val r = foo({x: 1, y: 2, z: 3})`,
		},
		{
			// B2's operative seal closes a closed param's requirement to exact at
			// generalization, so its signature rejects a call passing extra fields.
			name: "passing extra fields to a closed param rejects",
			src: `declare fn foo(p: &{x: unknown, y: unknown}) -> undefined
val r = foo({x: 1, y: 2, z: 3})`,
			want: []string{"2:29-2:30: object has extra property: z"},
		},
		{
			// An exact argument still checks against a closed param.
			name: "passing the exact shape to a closed param checks",
			src: `declare fn foo(p: &{x: unknown, y: unknown}) -> undefined
val r = foo({x: 1, y: 2})`,
		},
	}
	for _, tt := range calls {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestInferOpenParamNested asserts the DEEP semantics of `open`: it makes the param's
// whole inferred shape row-polymorphic, every nested object included — not just the
// top object. The un-`open` peer seals every level to exact. So a closed param
// rejects an extra field at ANY depth, while an open param accepts one at any depth.
func TestInferOpenParamNested(t *testing.T) {
	t.Run("open renders inexact at every level", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn foo(open p: &{a: {b: unknown, ...}, ...}) { p.a.b }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: &{a: {b: unknown, ...}, ...}) -> undefined", values["foo"])
	})

	t.Run("closed seals every level to exact", func(t *testing.T) {
		values, _, errs := inferSource(t, "fn foo(p: &{a: {b: unknown}}) { p.a.b }")
		require.Empty(t, errs)
		require.Equal(t, "fn (p: &{a: {b: unknown}}) -> undefined", values["foo"])
	})

	// Each call below goes to a callee declared with the signature "open renders inexact at
	// every level" or "closed seals every level to exact" asserts.
	calls := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "open accepts an extra field on the nested object",
			src: `declare fn foo(p: &{a: {b: unknown, ...}, ...}) -> undefined
val r = foo({a: {b: 1, c: 2}})`,
		},
		{
			name: "open accepts an extra field on the outer object",
			src: `declare fn foo(p: &{a: {b: unknown, ...}, ...}) -> undefined
val r = foo({a: {b: 1}, d: 2})`,
		},
		{
			name: "closed rejects an extra field on the nested object",
			src: `declare fn foo(p: &{a: {b: unknown}}) -> undefined
val r = foo({a: {b: 1, c: 2}})`,
			want: []string{"2:27-2:28: object has extra property: c"},
		},
		{
			name: "closed rejects an extra field on the outer object",
			src: `declare fn foo(p: &{a: {b: unknown}}) -> undefined
val r = foo({a: {b: 1}, d: 2})`,
			want: []string{"2:28-2:29: object has extra property: d"},
		},
	}
	for _, tt := range calls {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
