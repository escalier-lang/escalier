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

	// Tier 5: ECMA-262 facts — planning/ecma-262/requirements.md FR8.
	if result, ok := classifyFact(ctx); ok {
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

// classifyFact implements tier 5: the receiver mutability the ECMA-262
// analysis published for this member.
//
// The lookup is by the member's dotted runtime path, so it fires only for a
// class the global scope declares. A module path names an imported package,
// whose `String` is its own class rather than the builtin the spec keys, so
// nothing there resolves.
//
// Only a method resolves. A getter and a setter are settled by tier 3 above,
// and a data property has no spec algorithm to carry a fact.
func classifyFact(ctx ClassifyContext) (ClassifyResult, bool) {
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
// It is ClassifyMemberByName with no fact source, for a caller that holds a
// name and nothing that says which builtin the name belongs to.
func ClassifyMethodByName(name string) (mut bool, ok bool) {
	return ClassifyMemberByName(nil, "", ecma262.StrMember(name))
}

// ClassifyMemberByName runs the tiers of Classify that need no declaration
// against one member of one owner, and returns the resulting
// receiver-mutability classification. In cascade order those are the
// well-known non-mutating method names of tier 3, the ECMA-262 fact for the
// member at tier 5, the `get*` prefix rule of tier 6, and the name-based
// heuristics of tier 7.
//
// Two callers hold a member without the dts_parser.ClassMember the full
// Classify entry point takes. The trio fusion in this package builds a class
// from interface signatures, and the checker prelude pass walks the
// type_system.MethodElem of a `.d.ts`-loaded lib type. Both know the member's
// owner, which is its dotted runtime path — see ReceiverFacts.
//
// owner is "" for a caller with no owner to name, which leaves the fact tier
// with nothing to look up. A symbol-keyed member is answered by a fact alone,
// since every tier below reads a string name.
//
// Returns (mut, true) when a tier classifies the member; (false, false) when
// none does and the caller should keep its own default.
func ClassifyMemberByName(facts *ReceiverFacts, owner string, member ecma262.MemberKey) (mut bool, ok bool) {
	name := member.Name
	// Tier 3 (name-only subset): well-known non-mutating method names
	// that apply regardless of the containing type.
	if member.Kind == ecma262.StrKey && wellKnownNonMutatingMethods.Contains(name) {
		return false, true
	}
	// Tier 5: the receiver the spec analysis published for this member.
	if mut, ok := facts.Instance(owner, member); ok {
		return mut, true
	}
	if member.Kind != ecma262.StrKey || name == "" {
		return false, false
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

// MethodNames names the methods of one owner whose receiver an override marks
// non-mutating. Membership means "strip `mut self`". There is no counterpart
// set for marking a method mutating, because a `.d.ts` method carries
// `mut self` by default and needs no entry to keep it.
type MethodNames = set.Set[string]

// nonMutatingOverrides names, per owner, the methods whose receiver every tier
// below it answers wrongly or leaves unanswered.
//
// An entry is warranted only where the ECMA-262 facts have no claim to make.
// Every `web:*` owner is one, since the spec keys no algorithm for a member of
// `Console` or `Response`. `String.substr` is another: it is an Annex B method
// the committed graph does not carry, and the heuristics match no prefix in it.
// A method a published fact answers needs no entry, and
// planning/ecma-262/validation_diff.md records the 24 that were removed once
// the facts reached both readers.
//
// The two readers are `checker.UpdateMethodMutability`, which strips
// `mut self` from the `.d.ts`-loaded lib types, and `ValidateReceivers`, which
// measures the facts against the hand-written answers. Classify does not
// consult this table. Its tier 4 reads the override store of
// `internal/interop`, whose built-in subtree is still empty.
//
// The key is the name of the interface the `.d.ts` declares the member on.
//
// TODO(#500): extend this for Promise, Error, and other classes whose
// non-mutating methods should be callable on a non-mut receiver.
var nonMutatingOverrides = map[string]MethodNames{
	"String": set.FromSlice([]string{
		// An Annex B method, absent from the committed graph and matching
		// no prefix. The facts answer every other non-mutating `String`
		// method.
		"substr",
	}),
	// `RegExp` needs no entry. `toString` sits on the well-known allow-list
	// ClassifyMemberByName consults. `compile` mutates, and `exec` and `test`
	// write `lastIndex` when the pattern is global or sticky, so all three
	// keep the default `mut self`. `Symbol.search` and `Symbol.split` are
	// non-mutating per spec, and this string-keyed map cannot address a
	// symbol-keyed member. See #620.
	//
	// `Object`, `Function`, `Number`, `Boolean`, and `Date` need no entry
	// either. A published fact answers every non-mutating method they
	// declare, and the name-only tiers cover the rest through the `get*` and
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
