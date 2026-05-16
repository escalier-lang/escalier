package interop

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// ResolutionTier identifies which tier in the seven-tier resolution order
// produced a mutability classification for a class member's receiver.
//
// The tiers match the requirements document:
//  0. User-authored .esc source (sentinel; not produced by Classify — see §11.2)
//  1. User override files
//  2. @esctype tag (round-trip from Escalier source)
//  3. Explicit author signals (this: Readonly<T>, getters/setters, Readonly<T>, readonly props)
//  4. Builtin overrides (stdlib, FP libraries)
//  5. get* prefix rule (with documented exceptions)
//  6. Name-based heuristics
//  7. Default: mutating
type ResolutionTier int

const (
	TierUserSource      ResolutionTier = iota // 0: user-authored .esc source (sentinel)
	TierUserOverride                          // 1
	TierEsctype                               // 2
	TierExplicitSignal                        // 3
	TierBuiltinOverride                       // 4
	TierGetPrefix                             // 5
	TierNameHeuristic                         // 6
	TierDefault                               // 7
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
	// chain (e.g. "Outer.Inner"); empty when the class lives at the
	// module root. pathForMember combines it with ClassName to build a
	// Member-chain Owner that OverrideStore.walkChild can descend
	// through nested NamespaceScopes.
	NamespacePath string

	// Store, if non-nil, is the merged override store consulted by tiers
	// 1 and 4 (user overrides and built-in overrides). nil is permitted
	// and means "no overrides registered".
	Store *OverrideStore

	// Base, if non-nil, is the inheritance fallthrough context: if all
	// per-class tiers (1–6) miss on `Member`, `Classify` recurses on
	// *Base. The caller is responsible for resolving the same-named
	// member on the base class and constructing the new context. See §7.3.
	Base *ClassifyContext
}

// Classify determines the mutability of a class member's receiver using the
// seven-tier resolution order defined in
// planning/interop_mutability/requirements.md.
func Classify(ctx ClassifyContext) ClassifyResult {
	// Consult the override store once. Its Source field tells us whether
	// this is a tier-1 (user) hit or a tier-4 (built-in) hit — applied at
	// the correct rung below.
	var override *Effective
	if ctx.Store != nil {
		override = ctx.Store.Resolve(pathForMember(ctx))
	}

	// Tier 1: user override files — §5.
	if override != nil && override.Source == TierUserOverride {
		return overrideToResult(override)
	}

	// Tier 2: @esctype tag — §9.

	// Tier 3: explicit author signals.
	if result, ok := classifyExplicitSignal(ctx); ok {
		return result
	}

	// Tier 4: builtin overrides (stdlib, FP libraries) — §6.
	if override != nil && override.Source == TierBuiltinOverride {
		return overrideToResult(override)
	}

	// Tier 5: get* prefix rule.
	if result, ok := classifyGetPrefix(ctx); ok {
		return result
	}

	// Tier 6: name-based heuristics.
	if result, ok := classifyNameHeuristic(ctx); ok {
		return result
	}

	// IMPORTANT: when adding new per-class tiers (1, 2, 4), insert them
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

	// Tier 7: default to mutating.
	return ClassifyResult{Mut: true, Source: TierDefault}
}

// classifyGetPrefix implements tier 5: `get*` methods are non-mutating,
// except for the documented mutate-on-miss prefixes (`getOrInsert`,
// `getOrUpdate`, `getOrCreate`), which fall through to tier 6 and get
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
	// tier 5 declines to classify, so `Classify` proceeds to tier 6 where
	// `mutatingPrefixes` (which includes `getOrMutatingPrefixes`) picks
	// the name up as mutating. This is *not* a non-mutating return.
	//
	// Exact-name matches (e.g. bare `getOrInsert`) and any uppercase or
	// non-ASCII continuation fall through; only an ASCII-lowercase
	// continuation like `getOrInserter` stays at tier 5.
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
// followed by a write-on-miss action. Tier 5 must not classify these as
// non-mutating; tier 6's mutating-prefix list picks them up.
var getOrMutatingPrefixes = []string{
	"getOrInsert", "getOrUpdate", "getOrCreate",
}

// classifyNameHeuristic implements tier 6: name-based heuristics drawn
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
// The `getOr*` entries are appended from getOrMutatingPrefixes so tier 5's
// fall-throughs and tier 6's mutating list stay in sync.
var mutatingPrefixes = append([]string{
	"set", "add", "remove", "delete", "clear", "reset", "init",
	"push", "pop", "shift", "unshift", "insert", "replace", "update",
	"register", "unregister", "dispatch", "emit", "write", "flush",
}, getOrMutatingPrefixes...)

var mutatingExact = set.FromSlice([]string{
	"sort", "reverse",
})

// hasPrefixWithUpperContinuation reports whether name == prefix + UpperRune + rest.
// Used by tier 5 where bare prefix or lowercase continuation must NOT match.
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

