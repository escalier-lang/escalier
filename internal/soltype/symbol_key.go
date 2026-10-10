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
//     behind a `@@#` prefix, followed by `:` and the symbol's Name when it has one.
//     With `declare val sym: unique symbol` minted with id 0, `{[sym]: 1}` has the
//     member `@@#0:sym`. A symbol minted with no name spells its member `@@#0`.
//
// The spelling holds the name because a member carries nothing but its spelling, and
// rendering the key as `[sym]` needs the name. The name does not change which members
// match. A symbol's name is fixed when the symbol is minted, so every member
// keyed off one symbol carries the same spelling. A name that comes from a later
// declaration, such as the `val i: I` that names `I`'s `readonly key: unique symbol`
// `i.key`, is chosen before the symbol is minted. internal/solver/symbol_owner.go
// chooses it.
//
// The prefixes are an internal spelling and never reach a reader. Every name that does
// goes through DisplayMemberName or printObjectKeyName, which render `[Symbol.iterator]`,
// `[sym]` and `[unique symbol#0]`. The `@@` form is the ECMAScript spec's own editorial
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
// id is spelled in canonical decimal. The name after it is the one the symbol was minted
// with, so one id has one spelling.
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

// uniqueSymbolNameSep separates a unique symbol's id from its name in the reserved
// spelling. No decimal id contains it.
const uniqueSymbolNameSep = ":"

// UniqueSymbolMemberName returns the reserved name a member keyed off the unique symbol
// sym is stored under.
func UniqueSymbolMemberName(sym *UniqueSymbolType) string {
	name := uniqueSymbolPrefix + strconv.Itoa(sym.ID)
	if sym.Name != "" {
		name += uniqueSymbolNameSep + sym.Name
	}
	return name
}

// UniqueSymbolOfMemberName returns the unique symbol a reserved member name stands for,
// carrying the id and the name the spelling holds, and false for any other name. Only
// the canonical spelling UniqueSymbolMemberName produces reads back, so `@@#01` and
// `@@#0:` name no symbol.
func UniqueSymbolOfMemberName(name string) (*UniqueSymbolType, bool) {
	rest, found := strings.CutPrefix(name, uniqueSymbolPrefix)
	if !found {
		return nil, false
	}
	digits, symName, named := strings.Cut(rest, uniqueSymbolNameSep)
	if named && symName == "" {
		return nil, false
	}
	id, err := strconv.Atoi(digits)
	if err != nil || id < 0 || strconv.Itoa(id) != digits {
		return nil, false
	}
	return &UniqueSymbolType{ID: id, Name: symName}, true
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
// symbol is shown as a computed key holding the name of the symbol's declaration, so
// `declare val sym: unique symbol` gives `[sym]`. A symbol with no name is shown by its
// id, `[unique symbol#0]`. Every other name is its own display form.
func DisplayMemberName(name string) string {
	if sym, isSymbol := SymbolOfMemberName(name); isSymbol {
		return "[Symbol." + sym + "]"
	}
	if sym, isUnique := UniqueSymbolOfMemberName(name); isUnique {
		if sym.Name != "" {
			return "[" + sym.Name + "]"
		}
		return "[unique symbol#" + strconv.Itoa(sym.ID) + "]"
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
