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
			// The issue's example. The field returns T, so C stays covariant and widens. The
			// bound marks T as a covariant input, so an override has to accept what the
			// widest view passes, and Sub's narrower bound is rejected.
			name: "ANarrowOverrideOfABoundedReaderIsRejected",
			src: `
				class C<T> {
					v: Array<T>,
					m<U: T>(&self, x: U) -> boolean { return false },
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
				class Sub extends C<number> {
					constructor(&mut self) { super([1]) },
					m<U: number>(&self, x: U) -> boolean { return x > 1 },
				}
			`,
			errs: []string{
				"class `Sub` redeclares inherited member `m` with type `fn <U: number>(x: U) -> boolean`, " +
					"which is not compatible with `fn <U: unknown>(x: U) -> boolean` declared by `C`",
			},
		},
		{
			name: "AnOverrideAcceptingTheWidestBoundIsCompatible",
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
			// The shape `Array.filter<S: T>` has. The return gives T an output position, so a
			// `&self` method's bound leaves C covariant.
			name: "ABoundBesideAReturnWidens",
			src: `
				class C<T> {
					m<U: T>(&self, x: U) -> T { return x },
				}
				fn widen(c: &C<number>) -> &C<number | string> { return c }
			`,
		},
		{
			// Unlike the method in ABoundBesideAReturnWidens, the held function is whatever the
			// constructor was given. It can close over state fixed to the instance's argument,
			// such as a `fn (x) { log.push(x) return x }` over a `log: Array<number>`. Read
			// through a widened `C<number | string>`, `f("s")` would push a string into
			// `log`. Nothing checks the stored function the way the override check checks a
			// method, so the field's input position counts and C is invariant.
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
