package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMethodBinderBoundReadsAtTheInstance covers a method binder bounded by its class's
// type parameter, as `U` is in `m<U: T>`, reached through an instance. The bound reads at
// the instance's argument, so on a `C<number>` it is `U: number`.
func TestAMethodBinderBoundReadsAtTheInstance(t *testing.T) {
	// C is invariant in T, because `sink` takes a T and `v` returns one.
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

// TestAMethodBinderBoundIsAnInputPosition covers how a method binder's bound counts toward
// its class's variance. A bound limits what a caller may pass for the binder, so the class
// parameter it names sits in an input position.
func TestAMethodBinderBoundIsAnInputPosition(t *testing.T) {
	tests := []struct {
		name string
		src  string
		errs []string
	}{
		{
			// The bound is the only place T appears, so C is contravariant and does not
			// widen.
			name: "ABoundAloneBlocksWidening",
			src: `
				class C<T> {
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
			`,
			errs: []string{"cannot constrain string <: number"},
		},
		{
			name: "ABoundAloneAllowsNarrowing",
			src: `
				class C<T> {
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				fn narrow(c: &C<number | string>) -> &C<number> { return c }
			`,
		},
		{
			// The field returns T and the bound takes it, so C is invariant and does not widen.
			name: "ABoundBesideAFieldBlocksWidening",
			src: `
				class C<T> {
					v: Array<T>,
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
			`,
			errs: []string{"cannot constrain string <: number"},
		},
		{
			// `C<number>` does not widen, so the override is checked at `U: number`, the bound
			// the `extends` clause gives the inherited method.
			name: "AnOverrideAtTheExtendsBoundIsCompatible",
			src: `
				class C<T> {
					v: Array<T>,
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				class Sub extends C<number> {
					constructor(&mut self) { super([1]) },
					m<U: number>(&self, x: U) -> boolean { return x > 1 },
				}
			`,
		},
		{
			name: "AnOverrideWithAWiderBoundIsCompatible",
			src: `
				class C<T> {
					v: Array<T>,
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				class Sub extends C<number> {
					constructor(&mut self) { super([1]) },
					m<U>(&self, x: U) -> boolean { return false },
				}
			`,
		},
		{
			// The return gives T an output position and the bound an input position, so C is
			// invariant.
			name: "ABoundBesideAReturnBlocksWidening",
			src: `
				class C<T> {
					m<U: T>(&self, x: U) -> T { return x },
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
			`,
			errs: []string{"cannot constrain string <: number"},
		},
		{
			// A held function's binder bound is an input position the way a method's is.
			name: "AHeldFunctionsBoundBlocksWidening",
			src: `
				class C<T> {
					readonly f: fn <U: T>(x: U) -> T,
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
			`,
			errs: []string{"cannot constrain string <: number"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.errs, errorMessagesOf(errs))
		})
	}
}

// TestABodyUseWithinTheDeclaredBoundReachesTheCaller asserts that a body passing its own
// type parameter on within the declared bound is accepted, and that a call's argument flows
// through to the result. `f(u)` links `U` to the variable `f` instantiates `A` to. The link
// is how `g("a")` returns `"a"`.
func TestABodyUseWithinTheDeclaredBoundReachesTheCaller(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f<A: string>(a: A) -> A { return a }
		fn g<U: string>(u: U) { return f(u) }
		val r = g("a")
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "fn <U: string>(u: U) -> U", values["g"])
	require.Equal(t, `"a"`, values["r"])
}

// TestACallBreakingALinkedBoundReportsOnce asserts that a call failing the same bound along two
// paths reports it once. `g(5)` checks `5` against U's declared `string` and again against
// `f`'s `A`, which `f(u)` linked U to and which is bounded by `string` too.
func TestACallBreakingALinkedBoundReportsOnce(t *testing.T) {
	_, _, errs := inferSource(t, `fn f<A: string>(a: A) -> A { return a }
fn g<U: string>(u: U) { return f(u) }
val r = g(5)`)
	require.Equal(t, []string{"3:11-3:12: cannot constrain 5 <: string"}, messagesWithSpan(t, errs))
}
