package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// bagSrc declares a class that stays covariant in T while `contains` takes a T, so a
// `Bag<number>` can be read as a `Bag<number | string>` and an override of `contains`
// has to accept what that wider view passes.
const bagSrc = `
	class Bag<T> {
		readonly items: Array<T>,
		contains(&self, x: T) -> boolean { return false },
	}
`

// numBagSrc overrides `contains` with a narrow signature for its own callers and an
// implementation that accepts anything a widened view passes.
const numBagSrc = `
	class NumBag extends Bag<number> {
		constructor(&mut self) { super([1]) },
		contains(&self, x: number) -> boolean,
		contains(&self, x: unknown) -> boolean {
			return match x {
				n: number => n > 1,
				_ => false,
			}
		},
	}
`

// TestAMethodImplementationIsCheckedApartFromItsSignatures covers a method declared as
// bodiless signatures followed by one implementation. A caller is checked against the
// signatures, the body against the implementation, and the override check reads the
// implementation where a widened view of an ancestor reaches it.
func TestAMethodImplementationIsCheckedApartFromItsSignatures(t *testing.T) {
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
			name:    "ADirectCallReadsTheSignature",
			src:     bagSrc + numBagSrc + `val r = NumBag().contains(1)`,
			binding: "r",
			want:    "boolean",
		},
		{
			name: "ADirectCallOutsideTheSignatureIsRejected",
			src:  bagSrc + numBagSrc + `val r = NumBag().contains("s")`,
			errs: []string{`cannot constrain "s" <: number`},
		},
		{
			name:    "TheMethodValueOmitsTheImplementation",
			src:     bagSrc + numBagSrc + `val m = NumBag().contains`,
			binding: "m",
			want:    "fn (x: number) -> boolean",
		},
		{
			// The widened view reaches the implementation, which accepts a string.
			name: "AWidenedViewPassesWhatItsArgumentAdmits",
			src: bagSrc + numBagSrc + `
				val wide: &Bag<number | string> = NumBag()
				val r = wide.contains("s")
			`,
		},
		{
			// The body reads `x` at the implementation's `unknown`, not at the
			// signature's `number`.
			name: "TheBodyIsCheckedAgainstTheImplementation",
			src: bagSrc + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean,
					contains(&self, x: unknown) -> boolean { return x > 1 },
				}
			`,
			errs: []string{"cannot constrain unknown <: number"},
		},
		{
			name: "ALoneNarrowOverrideIsRejected",
			src: bagSrc + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean { return x > 1 },
				}
			`,
			errs: []string{
				"class `NumBag` redeclares inherited member `contains` with type `fn (x: number) -> boolean`, " +
					"which is not compatible with `fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			name: "AnImplementationNarrowerThanTheWidenedViewIsRejected",
			src: bagSrc + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean,
					contains(&self, x: number | string) -> boolean { return false },
				}
			`,
			errs: []string{
				"class `NumBag` redeclares inherited member `contains` with type `fn (x: number | string) -> boolean`, " +
					"which is not compatible with `fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			name: "AnImplementationThatRejectsASignatureIsReported",
			src: `
				class Box {
					get(&self, x: number) -> boolean,
					get(&self, x: string) -> boolean { return false },
				}
			`,
			errs: []string{
				"the implementation of `get` in class `Box` has type `fn (x: string) -> boolean`, " +
					"which is not compatible with its signature `fn (x: number) -> boolean`",
			},
		},
		{
			name: "AnImplementationReturningMoreThanASignatureIsReported",
			src: `
				class Box {
					get(&self, x: number) -> number,
					get(&self, x: number) -> number | string { return x },
				}
			`,
			errs: []string{
				"the implementation of `get` in class `Box` has type `fn (x: number) -> number | string`, " +
					"which is not compatible with its signature `fn (x: number) -> number`",
			},
		},
		{
			name: "AnImplementationTakingADifferentReceiverIsReported",
			src: `
				class Box {
					get(&self, x: number) -> boolean,
					get(&mut self, x: unknown) -> boolean { return false },
				}
			`,
			errs: []string{"Overloaded method 'get' must use the same `self` receiver in every arm."},
		},
		{
			// Each bodiless arm is a signature callers see, and the implementation has to
			// stand in for both.
			name: "SeveralSignaturesShareOneImplementation",
			src: `
				class Box {
					get(&self, x: number) -> boolean,
					get(&self, x: string) -> boolean,
					get(&self, x: number | string) -> boolean { return false },
				}
				val a = Box().get(1)
				val b = Box().get("s")
			`,
			binding: "b",
			want:    "boolean",
		},
		{
			name: "AStaticMethodCanDeclareAnImplementation",
			src: `
				class Box {
					static of(x: number) -> boolean,
					static of(x: unknown) -> boolean { return false },
				}
				val r = Box.of("s")
			`,
			errs: []string{`cannot constrain "s" <: number`},
		},
		{
			// A subclass whose own parameter fills Bag's is generic in it. The bound lets a
			// caller pass only a number, while a widened view passes anything.
			name: "AGenericOverride",
			src: bagSrc + `
				class NumLike<T: number> extends Bag<T> {
					constructor(&mut self, items: Array<T>) { super(items) },
					contains(&self, x: T) -> boolean,
					contains(&self, x: unknown) -> boolean { return false },
				}
				val r = NumLike([1, 2]).contains(1)
			`,
			binding: "r",
			want:    "boolean",
		},
		{
			name: "ALoneGenericOverrideIsRejected",
			src: bagSrc + `
				class NumLike<T: number> extends Bag<T> {
					constructor(&mut self, items: Array<T>) { super(items) },
					contains(&self, x: T) -> boolean { return false },
				}
			`,
			errs: []string{
				"class `NumLike` redeclares inherited member `contains` with type `fn (x: T) -> boolean`, " +
					"which is not compatible with `fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// Bag widens through NumBag, so a class two levels down still owes the widened
			// view an implementation that takes anything.
			name: "AnOverrideTwoLevelsDown",
			src: bagSrc + numBagSrc + `
				class SmallBag extends NumBag {
					constructor(&mut self) { super() },
					contains(&self, x: number) -> boolean,
					contains(&self, x: unknown) -> boolean { return false },
				}
				val r = SmallBag().contains(1)
			`,
			binding: "r",
			want:    "boolean",
		},
		{
			name: "ALoneOverrideTwoLevelsDownIsRejected",
			src: bagSrc + numBagSrc + `
				class SmallBag extends NumBag {
					constructor(&mut self) { super() },
					contains(&self, x: number) -> boolean { return false },
				}
			`,
			errs: []string{
				"class `SmallBag` redeclares inherited member `contains` with type `fn (x: number) -> boolean`, " +
					"which is not compatible with `fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			// A subclass's signature still has to fit the one NumBag's own callers see.
			name: "ASignatureTwoLevelsDownNarrowerThanItsParentsIsRejected",
			src: bagSrc + numBagSrc + `
				class SmallBag extends NumBag {
					constructor(&mut self) { super() },
					contains(&self, x: 1) -> boolean,
					contains(&self, x: unknown) -> boolean { return false },
				}
			`,
			errs: []string{
				"class `SmallBag` redeclares inherited member `contains` with type `fn (x: 1) -> boolean`, " +
					"which is not compatible with `fn (x: number) -> boolean` declared by `NumBag`",
			},
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

// TestAnOverrideDiagnosticPointsAtTheArmItRejects asserts that a rejection of what a
// widened view runs points at the implementation, and a rejection of what callers see
// points at a signature, whichever order the arms are written in.
func TestAnOverrideDiagnosticPointsAtTheArmItRejects(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "TheImplementationWrittenFirst",
			src: bagSrc + `class NumBag extends Bag<number> {
constructor(&mut self) { super([1]) },
contains(&self, x: number | string) -> boolean { return false },
contains(&self, x: number) -> boolean,
}`,
			want: []string{
				"8:1-8:64: class `NumBag` redeclares inherited member `contains` with type " +
					"`fn (x: number | string) -> boolean`, which is not compatible with " +
					"`fn (x: unknown) -> boolean` declared by `Bag`",
			},
		},
		{
			name: "ASignatureWrittenBeforeTheImplementation",
			src: bagSrc + `class NumBag extends Bag<number> {
constructor(&mut self) { super([1]) },
contains(&self, x: 1) -> boolean,
contains(&self, x: unknown) -> boolean { return false },
}`,
			want: []string{
				"8:1-8:33: class `NumBag` redeclares inherited member `contains` with type " +
					"`fn (x: 1) -> boolean`, which is not compatible with " +
					"`fn (x: number) -> boolean` declared by `Bag`",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
