package soltype

import "strings"

// symbol_key.go names the members a declaration keys off a well-known symbol.
//
// `interface Iterable<T> { [Symbol.iterator]() -> Iterator<T> }` declares a member
// under a symbol rather than a string. An object element carries a plain `Name
// string`, so each such member is stored under a reserved spelling instead: the
// symbol's own name behind a `@@` prefix, which is how the ECMAScript spec writes
// one. `[Symbol.iterator]` is the member `@@iterator`, and the printer renders that
// back as it was written.
//
// This stands in for a `unique symbol` kind rather than being one. It is sound
// because WellKnownSymbols is fixed and closed and each member of it has a distinct
// name, so two declarations naming the same symbol agree on the spelling and no two
// symbols share one. Two things it does not cover. A user cannot mint a symbol yet,
// and once they can, a minted symbol needs an identity a name cannot carry. A
// string-literal key spelled `"@@iterator"` collides with the reserved name, which no
// declaration in the tree writes and nothing rejects. Both retire with the real
// `unique symbol` kind.
const wellKnownSymbolPrefix = "@@"

// WellKnownSymbols is every symbol `Symbol` exposes as a property, the closed set the
// reserved spelling assumes. A `Symbol.foo` outside it names no well-known symbol, so
// a member keyed off one stays unsupported rather than resolving to a name two
// different symbols could share.
var WellKnownSymbols = []string{
	"asyncDispose",
	"asyncIterator",
	"dispose",
	"hasInstance",
	"isConcatSpreadable",
	"iterator",
	"match",
	"matchAll",
	"metadata",
	"replace",
	"search",
	"species",
	"split",
	"toPrimitive",
	"toStringTag",
	"unscopables",
}

// wellKnownSet is WellKnownSymbols as a lookup, built once.
var wellKnownSet = func() map[string]struct{} {
	s := make(map[string]struct{}, len(WellKnownSymbols))
	for _, name := range WellKnownSymbols {
		s[name] = struct{}{}
	}
	return s
}()

// SymbolMemberName returns the reserved name a member keyed off the well-known
// symbol `Symbol.<sym>` is stored under, and false when sym names no well-known
// symbol.
func SymbolMemberName(sym string) (string, bool) {
	if _, known := wellKnownSet[sym]; !known {
		return "", false
	}
	return wellKnownSymbolPrefix + sym, true
}

// SymbolOfMemberName returns the well-known symbol a reserved member name stands
// for, and false for an ordinary member name.
func SymbolOfMemberName(name string) (string, bool) {
	sym, found := strings.CutPrefix(name, wellKnownSymbolPrefix)
	if !found {
		return "", false
	}
	if _, known := wellKnownSet[sym]; !known {
		return "", false
	}
	return sym, true
}

// IteratorSymbolMember and AsyncIteratorSymbolMember are the two members the
// iteration rules look up, named here so a rule reads the constant rather than
// rebuilding the spelling.
var (
	IteratorSymbolMember      = wellKnownSymbolPrefix + "iterator"
	AsyncIteratorSymbolMember = wellKnownSymbolPrefix + "asyncIterator"
)
