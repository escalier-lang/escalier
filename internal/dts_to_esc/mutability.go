package dts_to_esc

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/escalier-lang/escalier/internal/set"
)

// ResolutionTier identifies which tier in the eight-tier resolution order
// produced a mutability classification for a class member's receiver.
//
// The tiers match the requirements document, with the ECMA-262 facts added by
// planning/ecma-262/requirements.md FR8:
//  0. User-authored .esc source (sentinel; not produced by Classify — see §11.2)
//  1. User override files
//  2. @esctype tag (round-trip from Escalier source)
//  3. Explicit author signals (this: Readonly<T>, getters/setters, Readonly<T>, readonly props)
//  4. Builtin overrides (stdlib, FP libraries)
//  5. ECMA-262 facts derived from the spec algorithm
//  6. get* prefix rule (with documented exceptions)
//  7. Name-based heuristics
//  8. Default: mutating
type ResolutionTier int

const (
	TierUserSource      ResolutionTier = iota // 0: user-authored .esc source (sentinel)
	TierUserOverride                          // 1
	TierEsctype                               // 2
	TierExplicitSignal                        // 3
	TierBuiltinOverride                       // 4
	TierECMA262Fact                           // 5
	TierGetPrefix                             // 6
	TierNameHeuristic                         // 7
	TierDefault                               // 8
)

// ClassifyResult is the outcome of Classify.
type ClassifyResult struct {
	Mut    bool           // true = receiver is mutating; false = non-mutating
	Source ResolutionTier // which tier produced this classification
}

// ClassifyContext carries the information needed to classify a class member's
// receiver mutability. Fields other than Member are optional and used by
// higher-numbered tiers.
type ClassifyContext struct {
	Member     dts_parser.ClassMember // the declaration being classified
	ClassName  string                 // enclosing class name (empty if none)
	ModulePath string                 // module path (empty if none)

	// NamespacePath is the dotted name of the enclosing `namespace`
	// chain, such as "Outer.Inner". It is empty when the class lives at
	// the module root. The override store combines it with ClassName to
	// address the member through nested namespace scopes.
	NamespacePath string

	// Store, if non-nil, is consulted by tiers 1 and 4 for a recorded
	// receiver mutability. nil means no overrides are registered.
	Store OverrideLookup

	// Facts, if non-nil, is the ECMA-262 receiver source consulted by tier
	// 5. nil means the converter was given no spec graph, which leaves every
	// method to the name tiers below.
	Facts *ReceiverFacts

	// Base, if non-nil, is the inheritance fallthrough context: if all
	// per-class tiers (1–7) miss on `Member`, `Classify` recurses on
	// *Base. The caller is responsible for resolving the same-named
	// member on the base class and constructing the new context. See §7.3.
	Base *ClassifyContext
}

// OverrideLookup reports the receiver mutability that an override file
// records for a class member. The runtime override store in
// internal/interop implements it. Keeping the lookup behind an interface
// is what lets this package convert `.d.ts` declarations without linking
// against the store's type representation.
//
// The second return value is false when no override addresses the member
// in ctx, which leaves the remaining tiers to decide.
type OverrideLookup interface {
	LookupReceiverMut(ctx ClassifyContext) (ClassifyResult, bool)
}

// Classify determines the mutability of a class member's receiver using the
// resolution order defined in planning/interop_mutability/requirements.md,
// with the ECMA-262 fact tier that planning/ecma-262/requirements.md FR8 adds
// above the name tiers.
func Classify(ctx ClassifyContext) ClassifyResult {
	// Consult the override store once. The hit's `Source` says whether
	// it is a tier-1 user override or a tier-4 built-in one, so it is
	// applied at the correct rung below.
	var override *ClassifyResult
	if ctx.Store != nil {
		if hit, ok := ctx.Store.LookupReceiverMut(ctx); ok {
			override = &hit
		}
	}

	// Tier 1: user override files — §5.
	if override != nil && override.Source == TierUserOverride {
		return *override
	}

	// Tier 2: @esctype tag — §9.

	// Tier 3: explicit author signals.
	if result, ok := classifyExplicitSignal(ctx); ok {
		return result
	}

	// Tier 4: builtin overrides (stdlib, FP libraries) — §6.
	if override != nil && override.Source == TierBuiltinOverride {
		return *override
	}

	// Tier 4 (owner-wide): a type wrapping an immutable primitive has
	// no mutating method to find, so the heuristics below have nothing
	// to decide. See ImmutableOwners.
	if ImmutableOwners.Contains(ctx.ClassName) {
		return ClassifyResult{Mut: false, Source: TierBuiltinOverride}
	}

	// Tier 5: ECMA-262 facts — planning/ecma-262/requirements.md FR8.
	if result, ok := classifyReceiverFact(ctx); ok {
		return result
	}

	// Tier 6: get* prefix rule.
	if result, ok := classifyGetPrefix(ctx); ok {
		return result
	}

	// Tier 7: name-based heuristics.
	if result, ok := classifyNameHeuristic(ctx); ok {
		return result
	}

	// IMPORTANT: when adding new per-class tiers (1, 2, 4, 5), insert them
	// ABOVE this block. Inheritance fallthrough must only fire after
	// every per-class tier has missed on the subclass; placing a new
	// tier below this point would let the base override a stronger
	// subclass signal.
	//
	// §7.3 inheritance fallthrough: re-run the cascade against the
	// same-named member on the nearest base class. The inherited result
	// carries the base method's tier — inheritance never upgrades
	// certainty.
	if ctx.Base != nil {
		return Classify(*ctx.Base)
	}

	// Tier 8: default to mutating.
	return ClassifyResult{Mut: true, Source: TierDefault}
}

// classifyReceiverFact implements tier 5: the receiver mutability the ECMA-262
// analysis published for this member.
//
// It reads one determination of the several a fact carries. The parameter
// dispositions, the return-borrow seed, `throws` and `rejects` are
// curation-grade, so they reach the `.esc` through the overlay rather than
// through this cascade. See planning/ecma-262/implementation_plan.md §7.
//
// The lookup is by the member's dotted runtime path, so it fires only for a
// class the global scope declares. A module path names an imported package,
// whose `String` is its own class rather than the builtin the spec keys, so
// nothing there resolves.
//
// Only an instance method resolves. A getter and a setter are settled by tier
// 3 above, and a data property has no spec algorithm to carry a fact. A static
// is refused here rather than left unanswered: its fact carries the receiver
// kind `none`, which says the builtin takes no receiver at all, so there is
// nothing for this tier to write.
func classifyReceiverFact(ctx ClassifyContext) (ClassifyResult, bool) {
	if ctx.Facts == nil || ctx.ModulePath != "" {
		return ClassifyResult{}, false
	}
	method, ok := ctx.Member.(*dts_parser.MethodDecl)
	if !ok || method.Modifiers.Static {
		return ClassifyResult{}, false
	}
	key, ok := memberKeyOf(method.Name)
	if !ok {
		return ClassifyResult{}, false
	}
	mut, ok := ctx.Facts.Instance(qualifiedName(ctx.NamespacePath, ctx.ClassName), key)
	if !ok {
		return ClassifyResult{}, false
	}
	return ClassifyResult{Mut: mut, Source: TierECMA262Fact}, true
}

// memberKeyOf reads the member address a `.d.ts` property key spells. A plain
// identifier and a string literal are string keys. A computed `[Symbol.x]` is
// the well-known symbol `x`, which the spec keys as `@@x`. Every other computed
// key names no member a fact can address.
func memberKeyOf(key dts_parser.PropertyKey) (ecma262.MemberKey, bool) {
	switch k := key.(type) {
	case *dts_parser.Ident:
		return ecma262.StrMember(k.Name), true
	case *dts_parser.StringLiteral:
		return ecma262.StrMember(k.Value), true
	case *dts_parser.ComputedKey:
		member, ok := k.Expr.(*dts_parser.MemberExpr)
		if !ok {
			return ecma262.MemberKey{}, false
		}
		obj, ok := member.Object.(*dts_parser.IdentExpr)
		if !ok || obj.Name != "Symbol" {
			return ecma262.MemberKey{}, false
		}
		return ecma262.SymMember(member.Prop.Name), true
	}
	return ecma262.MemberKey{}, false
}

// ClassifyMethodByName runs the name-only tiers of Classify against a bare
// method name and returns the resulting receiver-mutability classification.
// It covers the well-known non-mutating method allow-list (from tier 3),
// the tier-6 `get*` prefix rule, and the tier-7 name-based heuristics —
// i.e. every tier whose decision depends only on the method's name. The fact
// tier is not among them: a fact addresses a member of a named owner, and this
// entry point holds no owner. ReceiverMutates is the one that does.
//
// Used by the checker prelude pass on .d.ts-loaded lib types, where the
// caller has a type_system.MethodElem and a string name but no
// dts_parser.ClassMember to feed the full Classify entry point.
//
// Returns (mut, true) when a tier classifies the name; (false, false)
// when no tier matches and the caller should keep its own default.
func ClassifyMethodByName(name string) (mut bool, ok bool) {
	if name == "" {
		return false, false
	}
	// Tier 3 (name-only subset): well-known non-mutating method names
	// that apply regardless of the containing type.
	if wellKnownNonMutatingMethods.Contains(name) {
		return false, true
	}
	// Tier 6: `get*` prefix with documented mutate-on-miss fall-throughs.
	if classifyGetPrefixByName(name) {
		return false, true
	}
	// Tier 7: name-based heuristics. Mutating wins when both match.
	isMut := matchesAnyPrefix(name, mutatingPrefixes) || mutatingExact.Contains(name)
	isNonMut := matchesAnyPrefix(name, nonMutatingPrefixes) || nonMutatingExact.Contains(name)
	switch {
	case isMut:
		return true, true
	case isNonMut:
		return false, true
	}
	return false, false
}

// ReceiverMutates reports whether calling the member named by key on owner
// mutates the receiver, deciding between `self` and `mut self` on the emitted
// class. It serves a caller holding an owner and a member key rather than a
// dts_parser.ClassMember, which is what trio fusion holds.
//
// It runs the owner-wide tiers, then the ECMA-262 fact for the member, then
// the name-only tiers, then Classify's default of mutating. Applying that
// default itself is why it returns a bare bool where Classify and
// ClassifyMethodByName also report whether a tier matched.
//
// `facts` is the fact source, and nil leaves the fact tier with nothing to
// answer from. A symbol-keyed member reaches only the two tiers that address a
// member rather than read a name, which are the well-known symbols of tier 3
// and the fact of tier 5.
func ReceiverMutates(facts *ReceiverFacts, owner string, key ecma262.MemberKey) bool {
	// Tier 3 (name-only subset): the members that are non-mutating by
	// convention regardless of the containing type, which are the well-known
	// method names and the well-known symbols.
	if wellKnownMember(key) {
		return false
	}
	if key.Kind == ecma262.StrKey {
		// Tier 4, owner-wide and then per-method.
		if ImmutableOwners.Contains(owner) {
			return false
		}
		if NonMutatingOverrides(owner).Contains(key.Name) {
			return false
		}
	}
	// Tier 5: the receiver the spec analysis published for this member.
	if mut, ok := facts.Instance(owner, key); ok {
		return mut
	}
	if key.Kind != ecma262.StrKey {
		return true
	}
	if mut, ok := ClassifyMethodByName(key.Name); ok {
		return mut
	}
	return true
}

// wellKnownMember reports whether a member address is one of the tier-3
// conventions, which are the well-known non-mutating method names and the
// well-known symbols. It is the MemberKey counterpart of isWellKnownMethod, so
// a class fused from interface signatures reaches the same answer as one
// converted from a `.d.ts` class declaration.
func wellKnownMember(key ecma262.MemberKey) bool {
	if key.Kind == ecma262.SymKey {
		return wellKnownSymbols.Contains(key.Name)
	}
	return wellKnownNonMutatingMethods.Contains(key.Name)
}

// MethodNames names the methods of one owner whose receiver an override marks
// non-mutating. Membership means "strip `mut self`". There is no counterpart
// set for marking a method mutating, because a `.d.ts` method carries
// `mut self` by default and needs no entry to keep it.
type MethodNames = set.Set[string]

// ImmutableOwners names the types whose instance methods never take
// `mut self`. Each wraps a JavaScript primitive, and a primitive is
// immutable at the language level.
//
// Naming the owner rather than its methods is what makes the rule hold
// as TypeScript grows. A per-method list covers only the names someone
// thought of, and `strike`, `italics`, and the other Annex B wrappers
// match no heuristic prefix, so they reach the mutating default.
var ImmutableOwners = set.FromSlice([]string{
	"String",
	"Number",
	"Boolean",
	"Symbol",
	"BigInt",
})

// nonMutatingOverrides names, per owner, the methods whose receiver the
// name-only tiers get wrong.
//
// An entry is warranted when ClassifyMethodByName misses the name outright,
// as it does for `String.charAt`, which matches no prefix, or answers it the
// wrong way, as it does for `String.replace`, whose `replace` prefix reads as
// mutating. A name the heuristics already answer correctly is redundant and
// does not belong here. The reader applies the heuristics as a fall-through
// for any method with no entry.
//
// Two readers consult it: `checker.UpdateMethodMutability`, which strips
// `mut self` from the `.d.ts`-loaded lib types, and `ReceiverMutates`,
// which the converter's trio fusion calls. `Classify` does not — its tier 4
// reads the override store of `internal/interop`, whose built-in subtree is
// still empty.
//
// The key is the name of the interface the `.d.ts` declares the member on.
//
// TODO(#500): extend this for Error and the other classes whose
// non-mutating methods should be callable on a non-mut receiver.
var nonMutatingOverrides = map[string]MethodNames{
	"String": set.FromSlice([]string{
		// Names the heuristics miss because they match no prefix, plus
		// `replace` and `replaceAll`, which the mutating `replace` prefix
		// claims.
		"charAt",
		"charCodeAt",
		"codePointAt",
		"endsWith",
		"localeCompare",
		"match",
		"matchAll",
		"normalize",
		"padEnd",
		"padStart",
		"repeat",
		"replace",
		"replaceAll",
		"search",
		"split",
		"startsWith",
		"substr",
		"substring",
		"trim",
		"trimEnd",
		"trimStart",
	}),
	// `RegExp` needs no entry. `toString` sits on the well-known allow-list
	// ClassifyMethodByName consults. `compile` mutates, and `exec` and `test`
	// write `lastIndex` when the pattern is global or sticky, so all three
	// keep the default `mut self`. `Symbol.search` and `Symbol.split` are
	// non-mutating per spec, and this string-keyed map cannot address a
	// symbol-keyed member. See #620.
	"Object": set.FromSlice([]string{
		// A heuristic miss. `propertyIsEnumerable` starts with no
		// non-mutating prefix. The heuristics answer the rest of
		// `Object.prototype`.
		"propertyIsEnumerable",
	}),
	"Function": set.FromSlice([]string{
		// Heuristic misses. `toString` sits on the well-known list.
		"apply",
		"bind",
		"call",
	}),
	"Promise": set.FromSlice([]string{
		// Attaching a handler appends to the promise's reaction lists,
		// which is a write the specification makes and no reader can
		// observe: nothing reachable through `Promise<T, E>` differs
		// before and after. A `mut self` receiver would fail the ordinary
		// use of a promise, since it demands unique mutable access to
		// attach a handler at all. `val p = fetch(url)` could not call
		// `p.then(…)`, and two consumers of one promise could not each
		// attach their own.
		"catch",
		"finally",
		"then",
	}),
	// `Number`, `Boolean`, and `Date` need no entry. The name-only tiers
	// answer every non-mutating method they declare, through the `get*` and
	// `to*` prefixes and the well-known `toString` and `valueOf` names.
	"Console": set.FromSlice([]string{
		// Heuristic misses, since most Console methods are bare nouns. The
		// `clear` prefix claims `clear` as mutating, but `Console.clear`
		// writes nothing on the Console object itself.
		"assert",
		"clear",
		"debug",
		"dir",
		"dirxml",
		"error",
		"group",
		"groupCollapsed",
		"groupEnd",
		"info",
		"log",
		"table",
		"time",
		"timeEnd",
		"timeLog",
		"timeStamp",
		"trace",
		"warn",
	}),
	"Body": set.FromSlice([]string{
		// Heuristic misses. Every Body method is a bare noun.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
	"Response": set.FromSlice([]string{
		// Heuristic misses. The `clone` prefix answers `clone`.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
	"Request": set.FromSlice([]string{
		// Heuristic misses. The `clone` prefix answers `clone`.
		"arrayBuffer",
		"blob",
		"bytes",
		"formData",
		"json",
		"text",
	}),
}

// NonMutatingOverrides returns the methods of owner an override marks
// non-mutating. An owner with no entry reads back empty, which leaves every
// one of its methods to the name-only heuristics.
func NonMutatingOverrides(owner string) MethodNames {
	return nonMutatingOverrides[owner]
}

// classifyGetPrefixByName is the name-only counterpart to
// classifyGetPrefix. Returns true iff `name` should be classified
// non-mutating under the tier-6 rule (bare `get` or `get` + uppercase
// continuation, excluding the `getOr*` mutate-on-miss prefixes).
func classifyGetPrefixByName(name string) bool {
	if name != "get" && !hasPrefixWithUpperContinuation(name, "get") {
		return false
	}
	for _, p := range getOrMutatingPrefixes {
		if !strings.HasPrefix(name, p) {
			continue
		}
		if len(name) == len(p) {
			return false
		}
		r, _ := utf8.DecodeRuneInString(name[len(p):])
		if !unicode.IsLower(r) {
			return false
		}
	}
	return true
}

// classifyGetPrefix implements tier 6: `get*` methods are non-mutating,
// except for the documented mutate-on-miss prefixes (`getOrInsert`,
// `getOrUpdate`, `getOrCreate`), which fall through to tier 7 and get
// classified mutating there.
func classifyGetPrefix(ctx ClassifyContext) (ClassifyResult, bool) {
	m, ok := ctx.Member.(*dts_parser.MethodDecl)
	if !ok {
		return ClassifyResult{}, false
	}
	name := identName(m.Name)
	// Match bare `get` (the canonical JS lookup idiom — Map.prototype.get,
	// URLSearchParams.prototype.get, etc.) and `get` + uppercase
	// continuation (`getFoo`, `getX`). Lowercase continuations
	// (`getter`, `gets`) fall through.
	if name != "get" && !hasPrefixWithUpperContinuation(name, "get") {
		return ClassifyResult{}, false
	}
	// Mutating exceptions: getOrInsert*, getOrUpdate*, getOrCreate*.
	//
	// Returning `(_, false)` here is the fall-through signal — it means
	// tier 6 declines to classify, so `Classify` proceeds to tier 7 where
	// `mutatingPrefixes` (which includes `getOrMutatingPrefixes`) picks
	// the name up as mutating. This is *not* a non-mutating return.
	//
	// Exact-name matches (e.g. bare `getOrInsert`) and any uppercase or
	// non-ASCII continuation fall through; only an ASCII-lowercase
	// continuation like `getOrInserter` stays at tier 6.
	for _, p := range getOrMutatingPrefixes {
		if !strings.HasPrefix(name, p) {
			continue
		}
		if len(name) == len(p) {
			return ClassifyResult{}, false
		}
		r, _ := utf8.DecodeRuneInString(name[len(p):])
		if !unicode.IsLower(r) {
			return ClassifyResult{}, false
		}
	}
	return ClassifyResult{Mut: false, Source: TierGetPrefix}, true
}

// getOrMutatingPrefixes are `get`-led names whose leading `get` is
// followed by a write-on-miss action. Tier 6 must not classify these as
// non-mutating; tier 7's mutating-prefix list picks them up.
var getOrMutatingPrefixes = []string{
	"getOrInsert", "getOrUpdate", "getOrCreate",
}

// classifyNameHeuristic implements tier 7: name-based heuristics drawn
// from requirements.md §"Heuristics". When a name matches both a
// mutating and non-mutating signal, mutating wins (requirements: "if
// both, prefer mutating"). The slices below are the source of truth and
// must stay synced with the requirements document.
func classifyNameHeuristic(ctx ClassifyContext) (ClassifyResult, bool) {
	// Heuristics are about method-call semantics ("does calling this
	// mutate the receiver?"). Properties are classified by tier 3
	// (readonly modifier) and otherwise fall through to the default;
	// they must not be name-matched here.
	if _, ok := ctx.Member.(*dts_parser.MethodDecl); !ok {
		return ClassifyResult{}, false
	}
	name := memberName(ctx.Member)
	if name == "" {
		return ClassifyResult{}, false
	}
	isMut := matchesAnyPrefix(name, mutatingPrefixes) || mutatingExact.Contains(name)
	isNonMut := matchesAnyPrefix(name, nonMutatingPrefixes) || nonMutatingExact.Contains(name)
	switch {
	case isMut:
		return ClassifyResult{Mut: true, Source: TierNameHeuristic}, true
	case isNonMut:
		return ClassifyResult{Mut: false, Source: TierNameHeuristic}, true
	}
	return ClassifyResult{}, false
}

// Source of truth: requirements.md §"Heuristics" → "Medium signals".
var nonMutatingPrefixes = []string{
	// Predicate prefixes.
	"is", "has", "can", "should", "will", "was", "did",
	// Conversion / projection prefixes.
	"to", "as", "with",
	// Query / search prefixes.
	"find", "filter", "map", "reduce", "count",
	// Copy / clone prefixes.
	"clone", "copy",
}

var nonMutatingExact = set.FromSlice([]string{
	// Predicate / equality.
	"contains", "includes", "equals", "matches",
	// Query / search.
	"every", "some", "indexOf", "lastIndexOf", "at",
	// Iteration accessors.
	"keys", "values", "entries", "forEach",
	// Copy / projection.
	"slice", "concat",
})

// Source of truth: requirements.md §"Heuristics" → "Mutating-name signals".
// The `getOr*` entries are appended from getOrMutatingPrefixes so tier 6's
// fall-throughs and tier 7's mutating list stay in sync.
var mutatingPrefixes = append([]string{
	"set", "add", "remove", "delete", "clear", "reset", "init",
	"push", "pop", "shift", "unshift", "insert", "replace", "update",
	"register", "unregister", "dispatch", "emit", "write", "flush",
}, getOrMutatingPrefixes...)

var mutatingExact = set.FromSlice([]string{
	"sort", "reverse",
	// `Array.prototype.copyWithin` and `TypedArray.prototype.copyWithin` write
	// their receiver in place, and the `copy` prefix would otherwise read them
	// as projections. The exact match wins under the prefer-mutating rule. See
	// planning/ecma-262/validation_diff.md for the spec evidence.
	"copyWithin",
})

// hasPrefixWithUpperContinuation reports whether name == prefix + UpperRune + rest.
// Used by tier 6 where bare prefix or lowercase continuation must NOT match.
func hasPrefixWithUpperContinuation(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) || len(name) <= len(prefix) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name[len(prefix):])
	return unicode.IsUpper(r)
}

// matchesAnyPrefix reports whether name starts with one of the prefixes
// AND is followed by end-of-string or an uppercase letter (so `to` and
// `toUpperCase` both match `to`, but `today` does not).
func matchesAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if !strings.HasPrefix(name, p) {
			continue
		}
		if len(name) == len(p) {
			return true
		}
		r, _ := utf8.DecodeRuneInString(name[len(p):])
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// memberName returns the identifier-style name of a class member, or ""
// if the member has no usable name (symbol-keyed, etc.).
func memberName(member dts_parser.ClassMember) string {
	switch m := member.(type) {
	case *dts_parser.MethodDecl:
		return identName(m.Name)
	case *dts_parser.GetterDecl:
		return identName(m.Name)
	case *dts_parser.SetterDecl:
		return identName(m.Name)
	}
	return ""
}

// identName extracts a plain identifier name from a PropertyKey. Returns
// "" for computed keys (symbol-keyed members are not name-classified).
func identName(key dts_parser.PropertyKey) string {
	if id, ok := key.(*dts_parser.Ident); ok {
		return id.Name
	}
	return ""
}

// classifyExplicitSignal applies explicit author signals (tier 3):
//   - Getters never mutate the receiver.
//   - Setters always mutate the receiver.
//   - Methods with a `this: Readonly<T>` (or `this: readonly T[]`) parameter are non-mutating.
//   - Well-known symbol methods (toString, toJSON, etc.) are non-mutating.
//
// Property mutability is handled outside Classify (see convertPropertyDecl
// in helper.go) — PropertyDecl is intentionally not a case here.
func classifyExplicitSignal(ctx ClassifyContext) (ClassifyResult, bool) {
	nonMut := ClassifyResult{Mut: false, Source: TierExplicitSignal}
	mut := ClassifyResult{Mut: true, Source: TierExplicitSignal}

	switch m := ctx.Member.(type) {
	case *dts_parser.GetterDecl:
		return nonMut, true

	case *dts_parser.SetterDecl:
		return mut, true

	case *dts_parser.MethodDecl:
		// Well-known symbol methods are non-mutating by convention.
		if isWellKnownMethod(m.Name) {
			return nonMut, true
		}
		// Explicit `this: Readonly<T>` (or `this: readonly T[]`) parameter.
		if hasReadonlyThisParam(m.Params) {
			return nonMut, true
		}
	}

	return ClassifyResult{}, false
}

// wellKnownNonMutatingMethods lists method names that are non-mutating by
// convention regardless of the containing type.
var wellKnownNonMutatingMethods = set.FromSlice([]string{
	"toString",
	"toJSON",
	"toLocaleString",
	"valueOf",
})

// wellKnownSymbols lists Symbol.* property names whose methods are
// non-mutating by convention.
var wellKnownSymbols = set.FromSlice([]string{
	"iterator",
	"asyncIterator",
	"toPrimitive",
})

// isWellKnownMethod returns true when the method name is in the
// well-known non-mutating allow-list or is a well-known Symbol method.
func isWellKnownMethod(key dts_parser.PropertyKey) bool {
	switch k := key.(type) {
	case *dts_parser.Ident:
		return wellKnownNonMutatingMethods.Contains(k.Name)
	case *dts_parser.ComputedKey:
		member, ok := k.Expr.(*dts_parser.MemberExpr)
		if !ok {
			return false
		}
		obj, ok := member.Object.(*dts_parser.IdentExpr)
		if !ok || obj.Name != "Symbol" {
			return false
		}
		return wellKnownSymbols.Contains(member.Prop.Name)
	}
	return false
}

// hasReadonlyThisParam returns true when the parameter list starts with a
// `this` parameter whose type is Readonly<T>, ReadonlyArray<T>, etc.
func hasReadonlyThisParam(params []*dts_parser.Param) bool {
	if len(params) == 0 {
		return false
	}
	first := params[0]
	if first.Name == nil || first.Name.Name != "this" {
		return false
	}
	if first.Type == nil {
		return false
	}
	return isReadonlyWrapperType(first.Type)
}

// isReadonlyWrapperType returns true for Readonly<T>, ReadonlyArray<T>,
// ReadonlySet<T>, ReadonlyMap<K, V>, and readonly T[].
func isReadonlyWrapperType(t dts_parser.TypeAnn) bool {
	if arr, ok := t.(*dts_parser.ArrayType); ok {
		return arr.Readonly
	}
	typeRef, ok := t.(*dts_parser.TypeReference)
	if !ok {
		return false
	}
	ident, ok := typeRef.Name.(*dts_parser.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "Readonly", "ReadonlyArray", "ReadonlySet", "ReadonlyMap":
		return true
	}
	return false
}
