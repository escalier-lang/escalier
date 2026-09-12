package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The two directions agree, so a member stored under a reserved name reads back as
// the symbol it was keyed off.
func TestSymbolMemberNameRoundTrips(t *testing.T) {
	for _, sym := range WellKnownSymbols {
		name, known := SymbolMemberName(sym)
		require.True(t, known, "%s should be well known", sym)

		back, isSymbol := SymbolOfMemberName(name)
		require.True(t, isSymbol, "%s should read back as a symbol", name)
		require.Equal(t, sym, back)
	}
}

// A name outside the closed set is not a symbol member in either direction. The
// prefix alone does not make one, so an ordinary member that happens to start with
// it stays ordinary.
func TestSymbolMemberNameRejectsWhatIsNotWellKnown(t *testing.T) {
	_, known := SymbolMemberName("whatever")
	require.False(t, known)

	for _, name := range []string{"iterator", "@@whatever", "@@", "", "@iterator"} {
		_, isSymbol := SymbolOfMemberName(name)
		require.False(t, isSymbol, "%q should not read as a symbol member", name)
	}
}

// The two members the iteration rules look up are the reserved names for their
// symbols, so a rule reading the constant and a declaration writing the key agree.
func TestIterationMembersMatchTheirSymbols(t *testing.T) {
	sync, known := SymbolMemberName("iterator")
	require.True(t, known)
	require.Equal(t, sync, IteratorSymbolMember)

	async, known := SymbolMemberName("asyncIterator")
	require.True(t, known)
	require.Equal(t, async, AsyncIteratorSymbolMember)
}

// A symbol-keyed member renders as the computed key the source wrote, where an
// ordinary name renders bare and a name that is no identifier is quoted.
func TestPrintObjectKeyNameRendersASymbolKey(t *testing.T) {
	require.Equal(t, "[Symbol.iterator]", printObjectKeyName(IteratorSymbolMember))
	require.Equal(t, "length", printObjectKeyName("length"))
	require.Equal(t, `"a-b"`, printObjectKeyName("a-b"))
}
