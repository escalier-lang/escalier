package ecma262

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// MemberSort is where on its owner a member sits. The type source and the
// spec agree on the owner and the member name but not on this axis, so the
// join carries it explicitly. See planning/ecma-262/implementation_plan.md §5
// and Appendix C.
type MemberSort string

const (
	// SortInstance is a member reached through the owner's prototype, so a
	// call to it has a receiver. `Array.prototype.push` is one.
	SortInstance MemberSort = "instance"
	// SortStatic is a member of a constructor itself. `Array.from` is one.
	// It has no receiver, so its parameter 0 is its first real argument.
	SortStatic MemberSort = "static"
	// SortNamespaceFunc is a function on an object the language exposes as a
	// plain namespace rather than a constructor. `Math.max` is one. Like a
	// static it has no receiver, so the join applies only a fact's parameter,
	// throw, and reject claims.
	SortNamespaceFunc MemberSort = "namespace-func"
)

// MemberKeyKind separates a member named by a string property from one named
// by a well-known symbol.
type MemberKeyKind string

const (
	StrKey MemberKeyKind = "str"
	SymKey MemberKeyKind = "sym"
)

// MemberKey names one member of an owner. It mirrors
// type_system.ObjTypeKey so a symbol-keyed member joins by kind plus
// payload, matching how ClassifyMethodByName's callers in
// internal/dts_to_esc already tell string- from symbol-keyed names apart.
type MemberKey struct {
	Kind MemberKeyKind
	// Name is the property name for a StrKey, and the well-known symbol's
	// own name without the spec's `@@` marker for a SymKey. The spec writes
	// `Array.prototype [ @@iterator ]`, which keys as Name "iterator".
	Name string
}

// StrMember builds the key for a member named by a string property.
func StrMember(name string) MemberKey { return MemberKey{Kind: StrKey, Name: name} }

// SymMember builds the key for a member named by a well-known symbol. Pass
// the symbol's own name, so `@@iterator` is SymMember("iterator").
func SymMember(name string) MemberKey { return MemberKey{Kind: SymKey, Name: name} }

// String renders a symbol key the way source spells it, so a report reads
// `[Symbol.iterator]` rather than the spec's `@@iterator`.
func (k MemberKey) String() string {
	if k.Kind == SymKey {
		return "[Symbol." + k.Name + "]"
	}
	return k.Name
}

// AccessorKind tags a spec algorithm that defines a property accessor rather
// than a method.
type AccessorKind string

const (
	// NotAccessor is an ordinary method or function.
	NotAccessor AccessorKind = ""
	// GetAccessor is a `get X.prototype.y` algorithm.
	GetAccessor AccessorKind = "get"
	// SetAccessor is a `set X.prototype.y` algorithm.
	SetAccessor AccessorKind = "set"
)

// MemberRef addresses one member of the type source: the owner's dotted
// path, the member key, and the sort. The dotted path lets a constructor
// nested in a namespace resolve, so `Intl.DateTimeFormat` is just a longer
// owner than `Array`.
//
// Accessor is part of the address too. The spec keys `Map.prototype.size` and
// `get Map.prototype.size` as different algorithms, so a method never picks
// up an accessor's fact and an accessor never picks up a method's. It also
// tells the join to leave an accessor's fixed mutability alone.
type MemberRef struct {
	Owner    string
	Member   MemberKey
	Sort     MemberSort
	Accessor AccessorKind
}

// String renders the address in one line for a report. The sort leads,
// because `Array.from` and `Math.max` are spelled alike but join
// differently.
func (r MemberRef) String() string {
	var prefix string
	if r.Accessor != NotAccessor {
		prefix = string(r.Accessor) + " "
	}
	return fmt.Sprintf("%s%s %s.%s", prefix, r.Sort, r.Owner, r.Member)
}

// knownNamespaces are the objects the language exposes as a plain namespace
// instead of a constructor. A member of one is a function with no receiver.
// This is the small reviewed list requirements.md FR7 enumerates. ECMA-262
// itself defines only Atomics, JSON, Math, and Reflect; the other two come
// from the companion specs the same key space covers.
var knownNamespaces = set.FromSlice([]string{
	"Atomics", "Intl", "JSON", "Math", "Reflect", "WebAssembly",
})

// Normalize maps a canonical spec key onto the address the converter holds.
// It reports false for a key that addresses no member of an owner:
//
//   - a bare global the spec keys without a host, such as `parseInt` or the
//     `Array` constructor itself;
//
//   - an internal function whose name is not a dotted path, such as the
//     closure keyed "`Promise.all`ResolveElementFunction";
//
//   - a key whose last segment is `prototype`, which names the prototype
//     object rather than a member of it.
//
//     "Array.prototype.push"                   → instance Array.push
//     "Array.prototype [ @@iterator ]"         → instance Array.[Symbol.iterator]
//     "get Map.prototype.size"                 → get instance Map.size
//     "Array.from"                             → static Array.from
//     "Array [ @@species ]"                    → static Array.[Symbol.species]
//     "Math.max"                               → namespace-func Math.max
//     "Intl.DateTimeFormat.prototype.format"   → instance Intl.DateTimeFormat.format
//     "Intl.DateTimeFormat.supportedLocalesOf" → static Intl.DateTimeFormat.supportedLocalesOf
func Normalize(specKey string) (MemberRef, bool) {
	rest := strings.TrimSpace(specKey)

	accessor := NotAccessor
	switch {
	case strings.HasPrefix(rest, "get "):
		accessor, rest = GetAccessor, strings.TrimSpace(rest[len("get "):])
	case strings.HasPrefix(rest, "set "):
		accessor, rest = SetAccessor, strings.TrimSpace(rest[len("set "):])
	}

	path, member, ok := splitMember(rest)
	if !ok {
		return MemberRef{}, false
	}
	owner, sort, ok := ownerSort(path)
	if !ok {
		return MemberRef{}, false
	}
	return MemberRef{Owner: owner, Member: member, Sort: sort, Accessor: accessor}, true
}

// splitMember separates a spec key into the dotted path of the object that
// holds the member and the key of the member itself. A trailing bracket form
// names a well-known symbol, so `Array.prototype [ @@iterator ]` splits into
// the path "Array.prototype" and the symbol `iterator`. Otherwise the last
// dotted segment is the member, so `Array.prototype.push` splits into the
// same path and the string `push`.
func splitMember(key string) (string, MemberKey, bool) {
	if open := strings.IndexByte(key, '['); open >= 0 {
		if !strings.HasSuffix(key, "]") {
			return "", MemberKey{}, false
		}
		symbol, found := strings.CutPrefix(strings.TrimSpace(key[open+1:len(key)-1]), "@@")
		if !found || !isIdent(symbol) {
			return "", MemberKey{}, false
		}
		return strings.TrimSpace(key[:open]), SymMember(symbol), true
	}
	dot := strings.LastIndexByte(key, '.')
	if dot < 1 {
		// A key with no dot names a global rather than a member of an owner.
		return "", MemberKey{}, false
	}
	member := key[dot+1:]
	if !isIdent(member) || member == "prototype" {
		// A trailing `prototype` names the prototype object itself, which is
		// where instance members hang rather than a member the join carries.
		// `Function.prototype` is the one builtin spelled that way.
		return "", MemberKey{}, false
	}
	return key[:dot], StrMember(member), true
}

// ownerSort reads the owner and the sort out of the dotted path that holds a
// member. It rejects a path whose segments are not identifiers, which is how
// the closures an algorithm defines stay out of the join — their spec names
// are prose, such as "`Promise.all`ResolveElementFunction".
func ownerSort(path string) (string, MemberSort, bool) {
	segments := strings.Split(path, ".")
	for _, segment := range segments {
		if !isIdent(segment) {
			return "", "", false
		}
	}
	last := len(segments) - 1

	// An instance member hangs off its owner's prototype.
	if last > 0 && segments[last] == "prototype" {
		return strings.Join(segments[:last], "."), SortInstance, true
	}
	// A prototype the language gives no name of its own is the root of the
	// path instead of a `prototype` segment, so `%ArrayIteratorPrototype%`'s
	// `next` is keyed `ArrayIteratorPrototype.next` and still has a receiver.
	if last == 0 && strings.HasSuffix(segments[0], "Prototype") {
		return segments[0], SortInstance, true
	}
	// A known namespace with no further constructor segment holds functions.
	// `Intl.DateTimeFormat` has one, so its members are statics on a nested
	// constructor rather than namespace functions.
	if last == 0 && knownNamespaces.Contains(segments[0]) {
		return segments[0], SortNamespaceFunc, true
	}
	return path, SortStatic, true
}

// isIdent reports whether s is spelled like a JavaScript identifier. The
// check is deliberately narrow: it accepts ASCII letters, digits, `_`, and
// `$`, and rejects a leading digit.
func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == '$':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
