package checker

import (
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// jsGlobalsAllowList holds hand-authored JS-side names that are
// legal `@js("...")` targets but do not appear in lib.*.d.ts.
// Currently only Escalier's custom-matcher Symbol; grows as future
// stdlib bootstrap (§7) adds new well-known re-exports.
var jsGlobalsAllowList = set.FromSlice([]string{
	"Symbol.customMatcher",
})

// knownJSGlobals returns the set of dotted JS paths that may legally
// appear as the argument to `@js("...")` in a pseudo-package
// declaration (loader rule §3.4(4)). The set is:
//
//   - every top-level name in GlobalScope.Namespace.Values
//     (e.g. "Math", "parseInt", "Symbol", "Array") — values only,
//     because rule 4 guards against runtime ReferenceError and only
//     value-level names have a runtime referent; pure type names
//     (`PromiseLike`, `Thenable`, …) must not validate;
//   - for each such name N, "N.<member>" for every string-keyed
//     property/method/getter/setter on the resolved type — covering
//     dotted decorator targets like "Math.sin", "Array.isArray",
//     "Symbol.iterator". Member resolution follows TypeRefType through
//     to the aliased interface, so type-only constructor interfaces
//     (MathConstructor, ArrayConstructor, …) still surface their
//     members via the value-side binding;
//   - jsGlobalsAllowList entries (hand-authored Escalier additions).
//
// Symbol-keyed and numeric-keyed members are skipped — no current or
// foreseeable decorator argument uses them. The walk is intentionally
// shallow (one level): TS lib types don't nest globals beyond a single
// dot in any path we need to lower.
//
// The result is recomputed on each call rather than cached. Prelude
// clones GlobalScope per Checker (prelude.go ~L892), so a process-wide
// cache keyed by *Scope would never hit and would leak scopes across
// Checker lifetimes; and within a single Checker, global augmentations
// from later `.d.ts` loads keep mutating Namespace.Values, so a
// memoised set would go stale. The walk is shallow enough to redo
// cheaply on each pseudo-package load.
//
// Returns nil if GlobalScope hasn't been initialised. Callers use this
// as the signal to skip rule 4 entirely — an allow-list-only set would
// false-positive every legitimate lib global (`parseInt`, `Math.PI`,
// …) while still accepting hand-authored entries, which is worse than
// not checking at all.
func (c *Checker) knownJSGlobals() set.Set[string] {
	if c.GlobalScope == nil || c.GlobalScope.Namespace == nil {
		return nil
	}
	result := jsGlobalsAllowList.Clone()
	for name, binding := range c.GlobalScope.Namespace.Values {
		result.Add(name)
		if binding == nil {
			continue
		}
		for _, member := range objectMemberNames(binding.Type) {
			result.Add(name + "." + member)
		}
	}
	return result
}

// objectMemberNames returns the string-keyed property/method/getter/
// setter names of t, resolving through TypeRefType -> TypeAlias chains
// up to a small depth so interface-typed globals (Math, ArrayConstructor,
// SymbolConstructor, …) surface their members. Returns nil for any
// non-object shape.
func objectMemberNames(t type_system.Type) []string {
	obj := resolveToObjectType(t)
	if obj == nil {
		return nil
	}
	out := make([]string, 0, len(obj.Elems))
	for _, elem := range obj.Elems {
		var key type_system.ObjTypeKey
		switch e := elem.(type) {
		case *type_system.PropertyElem:
			key = e.Name
		case *type_system.MethodElem:
			key = e.Name
		case *type_system.GetterElem:
			key = e.Name
		case *type_system.SetterElem:
			key = e.Name
		default:
			continue
		}
		if key.Kind == type_system.StrObjTypeKeyKind {
			out = append(out, key.Str)
		}
	}
	return out
}
