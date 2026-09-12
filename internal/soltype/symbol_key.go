package soltype

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// symbol_key.go names the members a declaration keys off a well-known symbol.
//
// `interface Iterable<T> { [Symbol.iterator]() -> Iterator<T> }` declares a member
// under a symbol rather than a string. An object element carries a plain `Name
// string` and nothing else, so each such member is stored under a reserved spelling
// instead: the symbol's own name behind a `@@` prefix. `[Symbol.iterator]` is the
// member `@@iterator`.
//
// The prefix is an internal spelling and never reaches a reader. Every name that does
// goes through DisplayMemberName or printObjectKeyName, both of which render
// `[Symbol.iterator]`. The `@@` form is the ECMAScript spec's own editorial notation
// for a well-known symbol rather than anything a program writes, which is why it is
// safe here and would not be in output.
//
// A key kind beside the name is the better shape, and the rest of the tree already has
// it: `type_system.ObjTypeKey` carries `{Kind, Str, Num, Sym}`, and `ecma262.MemberKey`
// carries `{Kind, Name}` and says it mirrors the former. Encoding into the name instead
// is what costs the collision below. Giving an object element the same kind-plus-payload
// key retires this file, and #1246 has to touch the key anyway, since a user-minted
// symbol needs an id where a well-known one needs only a name.
//
// This stands in for a `unique symbol` kind rather than being one. It is sound because
// WellKnownSymbols is fixed and closed and each member of it has a distinct name, so two
// declarations naming the same symbol agree on the spelling and no two symbols share one.
//
// Two things it does not cover. A user cannot mint a symbol yet, and once they can, a
// minted symbol needs an identity a name cannot carry. And a string-literal key spelling
// the reserved name would resolve to the same member, so objKeyName declines one rather
// than storing it — a tax no reserved spelling avoids, since every spelling is a string a
// program can write. Both retire with the real `unique symbol` kind.
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
var wellKnownSet = set.FromSlice(WellKnownSymbols)

// SymbolMemberName returns the reserved name a member keyed off the well-known
// symbol `Symbol.<sym>` is stored under, and false when sym names no well-known
// symbol.
func SymbolMemberName(sym string) (string, bool) {
	if !wellKnownSet.Contains(sym) {
		return "", false
	}
	return wellKnownSymbolPrefix + sym, true
}

// SymbolOfMemberName returns the well-known symbol a reserved member name stands
// for, and false for an ordinary member name.
func SymbolOfMemberName(name string) (string, bool) {
	sym, found := strings.CutPrefix(name, wellKnownSymbolPrefix)
	if !found || !wellKnownSet.Contains(sym) {
		return "", false
	}
	return sym, true
}

// DisplayMemberName renders a member name the way the source writes it, so a message
// naming one matches what the reader wrote. A member keyed off a well-known symbol is
// stored under a reserved name and shown as the computed key, `[Symbol.iterator]`. Every
// other name is its own display form.
func DisplayMemberName(name string) string {
	if sym, isSymbol := SymbolOfMemberName(name); isSymbol {
		return "[Symbol." + sym + "]"
	}
	return name
}

// IteratorSymbolMember and AsyncIteratorSymbolMember are the two members the
// iteration rules look up, named here so a rule reads the constant rather than
// rebuilding the spelling.
var (
	IteratorSymbolMember      = wellKnownSymbolPrefix + "iterator"
	AsyncIteratorSymbolMember = wellKnownSymbolPrefix + "asyncIterator"
)
