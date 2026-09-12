package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A class declaring `(…) -> T` is callable as well as constructible. The specification
// forbids `new Symbol()`, so a call signature is the only way to make a symbol, and the fused
// `Symbol` class in `std:prelude` declares one and no constructor.
func TestACallSignatureMakesAClassCallable(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name: "TheClassValueIsCalled",
			src: `
				declare class Sym {
					(desc?: string) -> symbol,
				}
				val s = Sym("x")
			`,
			binding: "s",
			want:    "symbol",
		},
		{
			// A static sits beside the call signature and still reads.
			name: "AStaticBesideItStillReads",
			src: `
				declare class Sym {
					(desc?: string) -> symbol,
					static readonly iterator: unique symbol,
				}
				val it = Sym.iterator
			`,
			binding: "it",
			want:    "unique symbol#0",
		},
		{
			// `BooleanConstructor` writes `<T>(value?: T): boolean`, so a call signature
			// quantifies type parameters of its own.
			name: "ItMayBeGeneric",
			src: `
				declare class B {
					<T>(v: T) -> boolean,
				}
				val b = B(1)
			`,
			binding: "b",
			want:    "boolean",
		},
		{
			// Several are the arms of one overloaded signature, and a call resolves one.
			name: "AnOverloadedSignatureResolvesAnArm",
			src: `
				declare class N {
					() -> number,
					(v: string) -> boolean,
				}
				val r = N("x")
			`,
			binding: "r",
			want:    "boolean",
		},
		{
			// The class value is a subtype of the function its signature names.
			name: "TheClassValueFillsAFunctionParameter",
			src: `
				declare class Sym { (desc: string) -> symbol }
				declare fn take(f: fn (d: string) -> symbol) -> number
				val r = take(Sym)
			`,
			binding: "r",
			want:    "number",
		},
		{
			// A constructor still answers construction when both are declared.
			name: "AConstructorBesideItStillConstructs",
			src: `
				declare class D {
					constructor(mut self, n: number),
					(n: number) -> string,
					x: number,
				}
				val d = D(1)
			`,
			binding: "d",
			want:    "D",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// A class with a body compiles to a JavaScript `class`, and a `class` is never callable, so a
// call signature there would describe something the output cannot be. Only an ambient
// declaration, which describes a value the runtime already provides, may carry one.
func TestACallSignatureNeedsAnAmbientClass(t *testing.T) {
	_, _, errs := inferSource(t, `
		class C {
			() -> number
		}
	`)
	require.Equal(t, []string{
		"Only a `declare class` may declare a call signature; a class with a body compiles to a JavaScript `class`, which is not callable.",
	}, errorMessagesOf(errs))
}

// A class's own type parameters are in scope in a call signature, so a parameter named only
// there is resolved and counts as used.
func TestACallSignatureSeesTheClassTypeParameters(t *testing.T) {
	_, types, errs := inferSource(t, `
		declare class Wrap<T> {
			(v: T) -> T,
		}
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "Wrap<T>", types["Wrap"])
}

// A bare `(…) -> T` target asks whether the class value is callable as that function, so it
// reads the member a call reads. An object target naming a call signature asks whether the
// value carries that member, which is structural. The two coincide for a class declaring only
// a call signature and part ways for one declaring both.
func TestAFunctionTargetAsksCallabilityAndAnObjectTargetAsksForTheMember(t *testing.T) {
	// No constructor, so the call signature is both the member and what a call reads.
	t.Run("WithOnlyACallSignature", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			declare class Sym { (n: number) -> string }
			declare fn take(f: fn (n: number) -> string) -> number
			declare fn takeObj(f: { (n: number) -> string }) -> number
			val a = take(Sym)
			val b = takeObj(Sym)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "number", values["a"])
		require.Equal(t, "number", values["b"])
	})

	// Both declared. `D(1)` constructs, so the function target names `D`; the object target
	// names the call signature the class carries, which returns `string`.
	t.Run("WithAConstructorBesideIt", func(t *testing.T) {
		const decl = `
			declare class D {
				constructor(mut self, n: number),
				(n: number) -> string,
				x: number,
			}
		`
		values, _, errs := inferSource(t, decl+`
			declare fn take(f: fn (n: number) -> D) -> number
			val a = take(D)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "number", values["a"])

		values, _, errs = inferSource(t, decl+`
			declare fn takeObj(f: { (n: number) -> string }) -> number
			val b = takeObj(D)
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "number", values["b"])
	})
}
