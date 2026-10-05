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

// A unique symbol's reserved name reads back as the id it was built from, and two ids
// never share a name.
func TestUniqueSymbolMemberNameRoundTrips(t *testing.T) {
	for _, id := range []int{0, 7, 1 << 20} {
		name := UniqueSymbolMemberName(id)
		back, isUnique := UniqueSymbolOfMemberName(name)
		require.True(t, isUnique, "%s should read back as a unique symbol", name)
		require.Equal(t, id, back)
		require.True(t, IsSymbolMemberName(name))

		_, isWellKnown := SymbolOfMemberName(name)
		require.False(t, isWellKnown, "%s should not read as a well-known symbol", name)
	}
	require.NotEqual(t, UniqueSymbolMemberName(1), UniqueSymbolMemberName(10))
}

// Only the canonical spelling of an id names a unique symbol, so one id has one name.
func TestUniqueSymbolMemberNameRejectsOtherSpellings(t *testing.T) {
	for _, name := range []string{"@@#", "@@#01", "@@#-1", "@@#+1", "@@#x", "@@ #1", "#1", "1"} {
		_, isUnique := UniqueSymbolOfMemberName(name)
		require.False(t, isUnique, "%q should not read as a unique symbol member", name)
		require.False(t, IsSymbolMemberName(name), "%q should not read as a symbol member", name)
	}
}

// A symbol-keyed member renders as a computed key, where an ordinary name renders bare
// and a name that is no identifier is quoted. A unique symbol's key holds the symbol's
// own rendering.
func TestPrintObjectKeyNameRendersASymbolKey(t *testing.T) {
	require.Equal(t, "[Symbol.iterator]", printObjectKeyName(IteratorSymbolMember))
	require.Equal(t, "[unique symbol#3]", printObjectKeyName(UniqueSymbolMemberName(3)))
	require.Equal(t, "[unique symbol#3]", DisplayMemberName(UniqueSymbolMemberName(3)))
	require.Equal(t, "length", printObjectKeyName("length"))
	require.Equal(t, `"a-b"`, printObjectKeyName("a-b"))
}

// A unique symbol carries an id rather than a type, so a walk has nothing under it to
// visit and an unchanged one keeps its pointer.
func TestAcceptUniqueSymbolIsALeaf(t *testing.T) {
	sym := &UniqueSymbolType{ID: 3}
	require.Same(t, Type(sym), sym.Accept(identityVisitor{}, Positive))
	require.Equal(t, "unique symbol#3", Print(sym))
}
