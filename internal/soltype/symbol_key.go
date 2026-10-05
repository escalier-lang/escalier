package soltype

import (
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// symbol_key.go names the members a declaration keys off a symbol.
//
// `interface Iterable<T> { [Symbol.iterator]() -> Iterator<T> }` declares a member
// under a symbol rather than a string. An object element carries a plain `Name
// string` and nothing else, so each such member is stored under a reserved spelling
// instead. There are two spellings, one for each way a program names a symbol:
//
//   - A well-known symbol is spelled by its own name behind a `@@` prefix.
//     `[Symbol.iterator]` is the member `@@iterator`.
//   - A `unique symbol` the program declares is spelled by its UniqueSymbolType id
//     behind a `@@#` prefix. With `declare val sym: unique symbol` minted as
//     `unique symbol#0`, `{[sym]: 1}` has the member `@@#0`.
//
// The prefixes are an internal spelling and never reach a reader. Every name that does
// goes through DisplayMemberName or printObjectKeyName, which render `[Symbol.iterator]`
// and `[unique symbol#0]`. The `@@` form is the ECMAScript spec's own editorial
// notation for a well-known symbol rather than anything a program writes, which is why
// it is safe here and would not be in output.
//
// A key kind beside the name is the better shape, and the rest of the tree already has
// it: `type_system.ObjTypeKey` carries `{Kind, Str, Num, Sym}`, and `ecma262.MemberKey`
// carries `{Kind, Name}` and says it mirrors the former. Encoding into the name instead
// is what costs the collision below. Giving an object element the same kind-plus-payload
// key retires this file, which is what #1246 tracks.
//
// Two symbols never share a spelling. WellKnownSymbols is fixed and closed and each
// member of it has a distinct name, and none of those names starts with `#`. Every
// UniqueSymbolType a run mints carries an id no other one in that run carries, and the
// id is spelled in canonical decimal, so one id has one spelling.
//
// One symbol never has two spellings either. The prelude declares each well-known
// symbol as a UniqueSymbolType, such as the type of `Symbol.iterator`, and internal/solver
// spells a key of that type the well-known way rather than by its id.
//
// A string key spelling a reserved name would resolve to the same member, so objKeyName
// in internal/solver declines one rather than storing it. Every spelling is a string a
// program can write, so no choice of spelling avoids that rule.
const wellKnownSymbolPrefix = "@@"

// uniqueSymbolPrefix starts the reserved name of a member keyed off a declared
// `unique symbol`. The id follows it.
const uniqueSymbolPrefix = wellKnownSymbolPrefix + "#"

// WellKnownSymbols is the closed set of symbols the reserved spelling assumes. It holds
// the symbols ECMAScript exposes as properties of `Symbol`, plus `customMatcher`. A
// `Symbol.foo` outside the set names no well-known symbol, so a member keyed off one
// stays unsupported rather than resolving to a name two different symbols could share.
//
// `customMatcher` comes from the pattern-matching proposal rather than from the
// standard. It is in the set because a class declares what a match against it binds by
// keying a static member off it, so an extractor class cannot be typed without it.
var WellKnownSymbols = []string{
	"asyncDispose",
	"asyncIterator",
	"customMatcher",
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

// UniqueSymbolMemberName returns the reserved name a member keyed off the unique symbol
// with the given id is stored under.
func UniqueSymbolMemberName(id int) string {
	return uniqueSymbolPrefix + strconv.Itoa(id)
}

// UniqueSymbolOfMemberName returns the id of the unique symbol a reserved member name
// stands for, and false for any other name. Only the canonical spelling
// UniqueSymbolMemberName produces reads back, so `@@#01` names no symbol.
func UniqueSymbolOfMemberName(name string) (int, bool) {
	digits, found := strings.CutPrefix(name, uniqueSymbolPrefix)
	if !found {
		return 0, false
	}
	id, err := strconv.Atoi(digits)
	if err != nil || id < 0 || strconv.Itoa(id) != digits {
		return 0, false
	}
	return id, true
}

// IsSymbolMemberName reports whether name is the reserved name of a member keyed off a
// symbol, either a well-known one or a declared `unique symbol`.
func IsSymbolMemberName(name string) bool {
	if _, isWellKnown := SymbolOfMemberName(name); isWellKnown {
		return true
	}
	_, isUnique := UniqueSymbolOfMemberName(name)
	return isUnique
}

// DisplayMemberName renders a member name the way the source writes it, so a message
// naming one matches what the reader wrote. A member keyed off a well-known symbol is
// shown as the computed key, `[Symbol.iterator]`. A member keyed off a declared unique
// symbol is shown as a computed key holding the symbol's rendered type,
// `[unique symbol#0]`, since the type carries no name for the declaration. Every other
// name is its own display form.
func DisplayMemberName(name string) string {
	if sym, isSymbol := SymbolOfMemberName(name); isSymbol {
		return "[Symbol." + sym + "]"
	}
	if id, isUnique := UniqueSymbolOfMemberName(name); isUnique {
		return "[" + Print(&UniqueSymbolType{ID: id}) + "]"
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
