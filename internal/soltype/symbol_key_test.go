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

// A unique symbol's reserved name reads back as the id and the name it was built from,
// and two ids never share a reserved name.
func TestUniqueSymbolMemberNameRoundTrips(t *testing.T) {
	tests := []struct {
		sym  *UniqueSymbolType
		want string
	}{
		{sym: &UniqueSymbolType{ID: 0}, want: "@@#0"},
		{sym: &UniqueSymbolType{ID: 7, Name: "sym"}, want: "@@#7:sym"},
		{sym: &UniqueSymbolType{ID: 1 << 20, Name: "Keys.C.key"}, want: "@@#1048576:Keys.C.key"},
	}
	for _, tt := range tests {
		name := UniqueSymbolMemberName(tt.sym)
		require.Equal(t, tt.want, name)
		back, isUnique := UniqueSymbolOfMemberName(name)
		require.True(t, isUnique, "%s should read back as a unique symbol", name)
		require.Equal(t, tt.sym, back)
		require.True(t, IsSymbolMemberName(name))

		_, isWellKnown := SymbolOfMemberName(name)
		require.False(t, isWellKnown, "%s should not read as a well-known symbol", name)
	}
	require.NotEqual(t,
		UniqueSymbolMemberName(&UniqueSymbolType{ID: 1}),
		UniqueSymbolMemberName(&UniqueSymbolType{ID: 10}))
}

// Only the canonical spelling of an id names a unique symbol, so one id has one name.
func TestUniqueSymbolMemberNameRejectsOtherSpellings(t *testing.T) {
	for _, name := range []string{
		"@@#", "@@#01", "@@#-1", "@@#+1", "@@#x", "@@ #1", "#1", "1", "@@#0:", "@@#:sym", "@@#01:sym",
	} {
		_, isUnique := UniqueSymbolOfMemberName(name)
		require.False(t, isUnique, "%q should not read as a unique symbol member", name)
		require.False(t, IsSymbolMemberName(name), "%q should not read as a symbol member", name)
	}
}

// A symbol-keyed member renders as a computed key, where an ordinary name renders bare
// and a name that is no identifier is quoted. A unique symbol's key holds the name of
// the symbol's declaration, or the symbol's id when it has no name.
func TestPrintObjectKeyNameRendersASymbolKey(t *testing.T) {
	named := UniqueSymbolMemberName(&UniqueSymbolType{ID: 3, Name: "sym"})
	unnamed := UniqueSymbolMemberName(&UniqueSymbolType{ID: 3})
	tests := []struct {
		name string
		want string
	}{
		{name: IteratorSymbolMember, want: "[Symbol.iterator]"},
		{name: named, want: "[sym]"},
		{name: unnamed, want: "[unique symbol#3]"},
		{name: "length", want: "length"},
		{name: "a-b", want: `"a-b"`},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, printObjectKeyName(tt.name))
	}
	require.Equal(t, "[sym]", DisplayMemberName(named))
	require.Equal(t, "[unique symbol#3]", DisplayMemberName(unnamed))
}

// A unique symbol carries an id rather than a type, so a walk has nothing under it to
// visit and an unchanged one keeps its pointer.
func TestAcceptUniqueSymbolIsALeaf(t *testing.T) {
	sym := &UniqueSymbolType{ID: 3}
	require.Same(t, Type(sym), sym.Accept(identityVisitor{}, Positive))
	require.Equal(t, "unique symbol#3", Print(sym))
}

// Print shows a named symbol as the query TypeScript writes for it, in its own type and
// in a key off it. Equal and PrintQualified compare and key on the id, so two symbols
// that share a name stay apart and the name never makes two ids equal.
func TestUniqueSymbolNameIsDisplayOnly(t *testing.T) {
	first := &UniqueSymbolType{ID: 0, Name: "sym"}
	shadow := &UniqueSymbolType{ID: 1, Name: "sym"}
	keyed := func(sym *UniqueSymbolType) Type {
		return &ObjectType{Elems: []ObjTypeElem{
			&PropertyElem{Name: UniqueSymbolMemberName(sym), Type: &PrimType{Prim: NumPrim}},
		}}
	}

	require.Equal(t, "typeof sym", Print(first))
	require.Equal(t, "{[sym]: number}", Print(keyed(first)))
	description := &LitType{Lit: &StrLit{Value: "description"}}
	require.Equal(t, `(typeof sym)["description"]`, Print(&IndexType{Target: first, Index: description}))

	require.Equal(t, "unique symbol#0", PrintQualified(first))
	require.Equal(t, "{[unique symbol#0]: number}", PrintQualified(keyed(first)))
	require.False(t, Equal(first, shadow))
	require.False(t, Equal(keyed(first), keyed(shadow)))
	require.True(t, Equal(first, &UniqueSymbolType{ID: 0, Name: "sym"}))
}
