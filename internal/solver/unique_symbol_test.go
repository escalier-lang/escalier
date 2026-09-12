package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Each written `unique symbol` names one particular symbol. Two of them are the same type
// only when they came from the same declaration, and every one is a subtype of `symbol`.
func TestUniqueSymbol(t *testing.T) {
	const decl = `
		declare class C {
			readonly a: unique symbol,
			readonly b: unique symbol,
		}
	`
	t.Run("EveryOneIsASymbol", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare fn take(s: symbol) -> number
			fn use(c: C) { return take(c.a) }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn (c: C) -> number", values["use"])
	})

	t.Run("OneReachesItself", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare fn takeA(s: C["a"]) -> number
			fn use(c: C) { return takeA(c.a) }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn (c: C) -> number", values["use"])
	})

	// Two declarations are two values however they read, so nothing weaker than identity
	// relates them. The report names each under the id that tells them apart.
	t.Run("TwoDistinctOnesAreUnrelated", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			declare fn takeA(s: C["a"]) -> number
			fn use(c: C) { return takeA(c.b) }
		`)
		require.Equal(t,
			[]string{"cannot constrain unique symbol#1 <: unique symbol#0"},
			errorMessagesOf(errs))
	})

	// `symbol` is the whole primitive and a unique symbol is one of its values, so the
	// subtyping runs one way only.
	t.Run("ASymbolIsNotAParticularOne", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			declare fn takeA(s: C["a"]) -> number
			fn use(s: symbol) { return takeA(s) }
		`)
		require.Equal(t,
			[]string{"cannot constrain symbol <: unique symbol#0"},
			errorMessagesOf(errs))
	})
}

// The kind carries an id rather than a type, and the pieces that key on a type read that
// id: equality holds on a match and ordering separates a mismatch. Its leaf visit lives in
// the soltype package, where the identity visitor does.
func TestUniqueSymbolIsALeafKeyedOnItsID(t *testing.T) {
	first := &soltype.UniqueSymbolType{ID: 0}
	same := &soltype.UniqueSymbolType{ID: 0}
	other := &soltype.UniqueSymbolType{ID: 1}

	require.True(t, equalType(first, same))
	require.False(t, equalType(first, other))
	require.Zero(t, compareType(first, same))

	ab, ba := compareType(first, other), compareType(other, first)
	require.NotZero(t, ab)
	require.Equal(t, -ab, ba)

	// Its rank is its own, so the tie-breaker never asserts another kind to it.
	require.NotEqual(t, typeKindOrder(first), typeKindOrder(&soltype.PrimType{Prim: soltype.SymPrim}))
}

// `std:prelude` reaches the well-known symbols through `declare var Symbol:
// SymbolConstructor`, so a member off that binding is the particular symbol the
// constructor declares rather than the whole `symbol` primitive.
func TestAWellKnownSymbolReachesItsOwnType(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare class SymbolConstructor {
			readonly iterator: unique symbol,
			readonly asyncIterator: unique symbol,
		}
		declare var Symbol: SymbolConstructor
		val it = Symbol.iterator
		val asyncIt = Symbol.asyncIterator
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "unique symbol#0", values["it"])
	require.Equal(t, "unique symbol#1", values["asyncIt"])
}
