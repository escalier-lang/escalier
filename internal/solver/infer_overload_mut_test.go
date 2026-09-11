package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// #1519. A uniquely-owned argument fills an owned-mutable parameter of an OVERLOAD ARM,
// the same grant inferCall makes for a callee it resolved to a single signature. Each arm
// below is character-for-character a signature that accepts the call on its own, so
// without the grant adding an unrelated second arm would break a call that compiled.
//
// Both halves of the upgrade are covered. A syntactically fresh literal is uniquely owned
// because nothing else refers to it. A named binding that is dead after the call is
// uniquely owned because the call moves it.
func TestInferOverloadOwnedMutArgument(t *testing.T) {
	t.Run("function, fresh literal", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn take(a: mut {x: number}) -> number { return 1 }
			fn take(a: mut {x: number}, b: number) -> number { return 2 }
			val r = take({x: 1})
		`)
		require.Empty(t, errs)
	})
	t.Run("function, moved variable", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn take(a: mut {x: number}) -> number { return 1 }
			fn take(a: mut {x: number}, b: number) -> number { return 2 }
			fn g() -> number {
				val p = {x: 1}
				return take(p)
			}
		`)
		require.Empty(t, errs)
	})
	t.Run("method", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			class C {
				n: number,
				m(self, a: mut {x: number}) -> number { return 1 },
				m(self, a: mut {x: number}, b: number) -> number { return 2 },
			}
			val c = C(0)
			val r = c.m({x: 1})
		`)
		require.Empty(t, errs)
	})
	t.Run("constructor", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			declare class Box {
				constructor(mut self, a: mut {x: number}),
				constructor(mut self, a: mut {x: number}, b: number),
			}
			val r = Box({x: 1})
		`)
		require.Empty(t, errs)
	})
}

// A live source is not uniquely owned, so it takes no upgrade and the call finds no arm.
// This is the boundary the grant must not cross: `p` is read after the call, so a second
// owner would be observing writes made through the mutable view the arm asks for.
func TestInferOverloadOwnedMutArgumentLiveSourceRejected(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn take(a: mut {x: number}) -> number { return 1 }
		fn take(a: mut {x: number}, b: number) -> number { return 2 }
		fn g() -> number {
			val p = {x: 1}
			val n = take(p)
			return p.x
		}
	`)
	require.Equal(t, []string{"7:11-7:14: use of moved value 'p'"}, messagesWithSpan(t, errs))
}

// The upgrade runs inside a per-arm trial, so an arm that takes it and then fails on a
// LATER argument has to roll back with the rest of the trial. The first arm below upgrades
// `{x: 1}` and is rejected on `5 <: string`; the second arm then wins and gives the call
// its own return type. A leaked bound or a leaked diagnostic from the losing trial would
// show up as an error here or as the wrong type for `r`.
func TestInferOverloadOwnedMutArgumentLosingArmRollsBack(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn take(a: mut {x: number}, b: string) -> string { return b }
		fn take(a: mut {x: number}, b: number) -> number { return b }
		val r = take({x: 1}, 5)
	`)
	require.Empty(t, errs)
	require.Equal(t, "number", values["r"])
}

// A rest slot takes no upgrade: an argument there fills one ELEMENT of the array the slot
// gathers rather than the slot itself, so it is checked against the element type. inferCall
// skips a rest slot for the same reason. The accepted call and the rejected one differ only
// in an element, which is what shows the element is being read.
//
// Neither arm carries a return annotation, which keeps the set on the group-var path.
// TestInferOverloadPathsAgreeOnAnArrayParameter covers the fully-annotated path, where
// the same rest slot reaches the same element type.
func TestInferOverloadRestSlotChecksElement(t *testing.T) {
	t.Run("matching elements accept", func(t *testing.T) {
		values, _, errs := inferSource(t, `
			fn g(...xs: mut Array<number>) { return 1 }
			fn g(a: string, b: string) { return a }
			val r = g(1, 2)
		`)
		require.Empty(t, messagesWithSpan(t, errs))
		require.Equal(t, "1", values["r"])
	})
	t.Run("a mismatched element rejects", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn g(...xs: mut Array<number>) { return 1 }
			fn g(a: string, b: string) { return a }
			val r = g(1, true)
		`)
		require.Equal(t,
			[]string{"4:12-4:22: No matching overload for this call\n" +
				"  fn (...xs: mut Array<number>) -> 1\n" +
				"  fn (a: string, b: string) -> string"},
			messagesWithSpan(t, errs))
	})
}
