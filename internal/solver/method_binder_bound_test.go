package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMethodBinderBoundReadsAtTheInstance covers a method binder bounded by its class's
// type parameter, as `U` is in `m<U: T>`, reached through an instance. The bound reads at
// the instance's argument, so on a `C<number>` it is `U: number`.
func TestAMethodBinderBoundReadsAtTheInstance(t *testing.T) {
	// C is invariant in T, because `sink` takes a T and `v` returns one. That keeps the
	// widest-instance override check out of these cases.
	const class = `
		class C<T> {
			v: Array<T>,
			sink: fn (x: T) -> undefined,
			m<U: T>(&self, x: U) -> boolean { return false },
		}
		val b: C<number> = C([1], fn (x: number) { return undefined })
	`
	const sub = `
		class Sub extends C<number> {
			constructor(&mut self) { super([1], fn (x: number) { return undefined }) },
			m<U: number>(&self, x: U) -> boolean { return x > 1 },
		}
	`
	tests := []struct {
		name string
		src  string
		// binding names a value whose rendered type the case asserts. An empty binding
		// asserts no type.
		binding string
		want    string
		errs    []string
	}{
		{
			name: "ACallOutsideTheBoundIsRejected",
			src:  class + `val r = b.m("s")`,
			errs: []string{`cannot constrain "s" <: number`},
		},
		{
			name:    "ACallInsideTheBoundIsAccepted",
			src:     class + `val r = b.m(1)`,
			binding: "r",
			want:    "boolean",
		},
		{
			name:    "TheMethodValueNamesNoClassVariable",
			src:     class + `val t = b.m`,
			binding: "t",
			want:    "fn <U: number>(x: U) -> boolean",
		},
		{
			name: "AnOverrideAfterARejectedCallIsCompatible",
			src:  class + `val r = b.m("s")` + sub,
			errs: []string{`cannot constrain "s" <: number`},
		},
		{
			name: "AnOverrideAfterAnAcceptedCallIsCompatible",
			src:  class + `val r = b.m(1)` + sub,
		},
		{
			name: "AnOverrideWithNoCallIsCompatible",
			src:  class + sub,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, errorMessagesOf(errs))
			} else {
				require.Equal(t, tt.errs, errorMessagesOf(errs))
			}
			if tt.binding != "" {
				require.Equal(t, tt.want, values[tt.binding])
			}
		})
	}
}

// TestABodyForcedBinderBoundIsEnforcedAtACall asserts that a call honors a bound the body
// forced on a binder beside the one it declares. `f(u)` forces `U` below `f`'s `string`, so
// `g(5)` has to pass `5` as a string too.
//
// The body should not be accepted in the first place, since `u` may be a `number`. #1898
// tracks reporting `cannot constrain number <: string` at `f(u)`. Once it lands, that error
// replaces the one at `g(5)`, and this test's assertion moves with it.
func TestABodyForcedBinderBoundIsEnforcedAtACall(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f<A: string>(a: A) -> A { return a }
		fn g<U: number | string>(u: U) { return f(u) }
		val r = g(5)
	`)
	require.Equal(t, []string{"cannot constrain 5 <: string"}, errorMessagesOf(errs))
}
