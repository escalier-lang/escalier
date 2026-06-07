package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeAnn converts an M2-supported type annotation into a soltype.Type,
// returning ok=false when the annotation is outside the supported set. M2 needs
// only the primitive annotations that annotated params and return types use
// (number/string/boolean); everything richer — type references, generics,
// object/tuple/function annotations, unions — is represented by types later
// milestones add (M3/M4/M6) and resolves to an UnsupportedNodeError here, with
// ok=false and a `never` placeholder so a caller can recover by keeping the type
// it already inferred (rather than constraining against / adopting `never`, which
// would cascade a spurious `<: never` error and poison the binding). It takes the
// current inference level `lvl` so a supported generic with an UNSUPPORTED inner (a
// malformed `Promise<…>`) can recover its inner to a fresh var at the right level
// while keeping the wrapper; the primitive arms ignore lvl. Full name resolution
// against the type scope still arrives with TypeRef support (M7).
func (c *checker) resolveTypeAnn(ta ast.TypeAnn, lvl int) (soltype.Type, bool) {
	switch ta := ta.(type) {
	case *ast.NumberTypeAnn:
		return c.annPrim(ta, soltype.NumPrim), true
	case *ast.StringTypeAnn:
		return c.annPrim(ta, soltype.StrPrim), true
	case *ast.BooleanTypeAnn:
		return c.annPrim(ta, soltype.BoolPrim), true
	case *ast.TypeRefTypeAnn:
		// M3 (PR3) recognises a single generic stdlib reference: Promise<T>. The
		// real, alias-driven TypeRef resolution arrives in M7 — until then, any
		// other name (or arity) reports unsupported with a `never` placeholder so
		// the caller can recover by keeping the inferred type.
		if ast.QualIdentToString(ta.Name) == "Promise" && len(ta.TypeArgs) == 1 {
			// A lifetime-annotated Promise (`'a Promise<T>` or `Promise<'a, T>`) is not
			// supported: M3's PromiseType carries no lifetime, so silently accepting it
			// would drop the lifetime. Reject it as an unsupported feature rather than
			// coercing to a plain Promise<T>. (Lifetimes on referenced types land with
			// the wider TypeRef/lifetime work.)
			if len(ta.LifetimeArgs) > 0 || ta.Lifetime != nil {
				return c.reportUnsupportedFeature(ta, "lifetime annotation on Promise"), false
			}
			inner, ok := c.resolveTypeAnn(ta.TypeArgs[0], lvl)
			if !ok {
				// The inner annotation was unsupported and already reported its own
				// error. The Promise itself IS supported, so keep the WRAPPER rather
				// than collapsing the whole annotation to the bare-var recovery the
				// caller applies on ok=false: `p: Promise<bad>` should stay Promise-
				// shaped (so `await p` and the rendered signature read as a Promise),
				// not degrade to an unconstrained var. Recover the inner to a fresh var
				// — cascade-safe in BOTH directions (an initializer flowing into
				// `Promise<freshVar>` constrains the var without failing; a `never` or
				// `unknown` inner would instead cascade a spurious `<: never` / `<:
				// unknown`, since constrain has no rule for either as an input).
				//
				// PR8 (planning/simple_sub/m3-implementation-plan.md) replaces this
				// fresh var with the dedicated error-recovery type, so the recovered
				// inner reads as `error` rather than as an anonymous coalesced var.
				inner = c.freshAt(lvl)
			}
			t := &soltype.PromiseType{Inner: inner}
			c.recordProv(t, ta, AnnotationType)
			return t, true
		}
		return c.reportUnsupported(ta), false
	default:
		return c.reportUnsupported(ta), false
	}
}

// annPrim mints a FRESH PrimType for an annotation and records it against the
// annotation node (AnnotationType origin) — the "fresh-atom discipline" (§3.3).
//
// Why fresh, rather than a single shared/interned `number` value? Provenance is
// the reason. The Prov side table is keyed by POINTER IDENTITY
// (soltype.Type -> Origin), so the only way to record "this primitive came from
// THIS annotation node" is for the primitive to be its own pointer, unique to this
// annotation. Three consequences follow:
//
//   - Precise blame. A unique atom per annotation lets `val x: number = "hi"`
//     resolve its `number` operand back to the exact annotation node — surfaced as
//     the related "expected here" span — and lets a prim/prim mismatch blame the
//     offending annotation instead of degrading to the constraint site (§3.3, §3.7).
//   - No Prov-invariant conflict. recordProv requires each type pointer to map to a
//     single node; the debugProv guard panics when a pointer is re-recorded against
//     a DIFFERENT node (prov.go). A shared `number` would be recorded against every
//     `number` annotation's node in turn — a conflicting overwrite. Fresh atoms each
//     write a distinct pointer, so there is never a conflict and no last-write-wins
//     blame.
//   - Free, because correctness ignores identity. constrain compares PrimType.Prim
//     BY VALUE (`r.Prim == l.Prim`, constrain.go), never by pointer, so two
//     distinct-but-equal `number`s still subtype-match. Freshness only ever adds a
//     redundant coinductive-`seen` entry, never a loop or a spurious mismatch.
//
// (soltype interns no primitive singletons anyway, so there is nothing to share —
// minting fresh is the natural choice here, not an added cost.)
func (c *checker) annPrim(ta ast.TypeAnn, p soltype.Prim) soltype.Type {
	t := &soltype.PrimType{Prim: p}
	c.recordProv(t, ta, AnnotationType)
	return t
}
