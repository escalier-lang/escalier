package checker

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// Phase 11: Lifetime elision rules for body-less function declarations.
//
// Elision applies *only* when the user wrote no explicit lifetime
// annotations on the signature. The presence of any explicit lifetime
// shows up as a non-empty FuncType.LifetimeParams (every inline `'a`
// must be declared in the `<'a>` clause; otherwise inferFuncSig has
// already reported UndeclaredLifetimeError). So an empty
// LifetimeParams field is the marker for "user wants elision".

// IsReferenceType returns true if the type can participate in aliasing
// — i.e. it is a candidate for carrying a lifetime annotation.
//
// Object, tuple, and function types can alias. Type references that
// resolve to those shapes can alias. Unresolved type parameters are
// conservatively treated as reference types — at the call site the
// caller may instantiate the parameter with an object, in which case
// a lifetime is needed; if instead the parameter is instantiated with
// a primitive, the lifetime is harmless. A type parameter with a
// constraint that is itself non-reference (e.g. `T extends number`)
// can be treated as non-reference. Primitives, void, never, null, and
// undefined cannot alias.
func IsReferenceType(t type_system.Type) bool {
	if t == nil {
		return false
	}
	switch ty := type_system.Prune(t).(type) {
	case *type_system.MutType:
		return IsReferenceType(ty.Type)
	case *type_system.ObjectType, *type_system.TupleType, *type_system.FuncType:
		return true
	case *type_system.TypeRefType:
		// Walk into the alias body when available; otherwise
		// conservatively treat as a reference. Type-param refs with
		// no constraint are also conservatively treated as references
		// since the caller may instantiate with an object.
		if ty.TypeAlias != nil && ty.TypeAlias.Type != nil {
			return IsReferenceType(ty.TypeAlias.Type)
		}
		return true
	case *type_system.UnionType:
		return slices.ContainsFunc(ty.Types, IsReferenceType)
	case *type_system.IntersectionType:
		return slices.ContainsFunc(ty.Types, IsReferenceType)
	}
	// PrimType, LitType, VoidType, NeverType, NullType, UndefinedType,
	// AnyType, UnknownType — none can carry a lifetime by themselves.
	return false
}

// AmbiguousLifetimeElisionError reports a body-less declaration whose
// signature has multiple reference-typed parameters and a reference-
// typed return, so the elision rules cannot pick a single source for
// the return's lifetime. The user must annotate the signature
// explicitly.
type AmbiguousLifetimeElisionError struct {
	NumRefParams int
	span         ast.Span
}

func (e AmbiguousLifetimeElisionError) isError()        {}
func (e AmbiguousLifetimeElisionError) IsWarning() bool { return false }
func (e AmbiguousLifetimeElisionError) Span() ast.Span  { return e.span }
func (e AmbiguousLifetimeElisionError) Message() string {
	return "cannot infer lifetime for return type: signature has multiple reference parameters; add an explicit `<'a>` clause"
}

// ApplyLifetimeElision applies default lifetime rules to a body-less
// function signature that carries no explicit lifetime annotations.
//
// Rules:
//  1. If the return type is not a reference type, no lifetimes are
//     needed — exit.
//  2. If the signature has exactly one reference-typed parameter, the
//     output lifetime matches that parameter's lifetime.
//  3. If the signature has zero reference-typed parameters, the
//     return must come from "fresh" storage (the callee allocates);
//     no lifetime is inferred.
//  4. If the signature has more than one reference-typed parameter,
//     elision is ambiguous — report AmbiguousLifetimeElisionError.
//
// (The "method receiver" rule from the spec is not applied here:
// methods carry their receiver implicitly, separate from FuncType.Params,
// and the receiver's lifetime is supplied at call sites by the
// existing method-call machinery rather than by elision on the
// signature itself.)
func (c *Checker) ApplyLifetimeElision(
	funcType *type_system.FuncType,
) []Error {
	if funcType == nil {
		return nil
	}
	// User-explicit annotation: skip elision entirely.
	if len(funcType.LifetimeParams) > 0 {
		return nil
	}
	// While loading TypeScript .d.ts (prelude or import), Phase 12
	// owns interop semantics. Skip elision so we don't synthesize
	// lifetime annotations on signatures whose calling conventions
	// don't match Escalier's aliasing rules.
	if c.loadingExternalTypes {
		return nil
	}

	if !IsReferenceType(funcType.Return) {
		return nil
	}

	var refParamIndices []int
	for i, p := range funcType.Params {
		if IsReferenceType(p.Type) {
			refParamIndices = append(refParamIndices, i)
		}
	}

	switch len(refParamIndices) {
	case 0:
		return nil
	case 1:
		lv := c.FreshLifetimeVar("a")
		funcType.LifetimeParams = []*type_system.LifetimeVar{lv}
		setLifetimeOnType(funcType.Params[refParamIndices[0]].Type, lv)
		setLifetimeOnType(funcType.Return, lv)
		return nil
	default:
		// TODO(phase-13): emit AmbiguousLifetimeElisionError here so
		// users are forced to annotate. Currently lenient: the
		// signature is left unannotated (callers see a fresh return),
		// which is sound w.r.t. what the type system records but
		// doesn't reflect the likely real aliasing of the body.
		return nil
	}
}

// VerifyLifetimeCompatibility checks that an implementation method's
// inferred lifetimes are compatible with an interface method's
// declared lifetimes. An implementation may be *more conservative*
// than the interface (return a fresh value when the interface says it
// may alias), but never *less* conservative (alias when the interface
// says it does not).
//
// Compatibility rules:
//   - Lifetime count and parameter positions must match between
//     interface and implementation.
//   - If the interface ties parameter i's lifetime to the return
//     value, the implementation may either match (also alias param i)
//     or return a fresh value (no lifetime on the return) — but it
//     must not alias a *different* parameter.
//   - If the interface declares no lifetime tying parameter i to the
//     return, the implementation must not alias parameter i either.
//
// NOT YET WIRED: this routine is unused at runtime today. The
// `implements` clause it depends on is deferred to Phase 12
// (TypeScript interop), which owns both the parser support for
// `implements` and the integration that calls this function from
// interface-implementation checking. Until then, the only callers are
// direct unit tests in `elision_test.go` that pin the compatibility
// rules in place so Phase 12 can wire them up without redesign.
func (c *Checker) VerifyLifetimeCompatibility(
	ifaceMethod *type_system.FuncType,
	implMethod *type_system.FuncType,
	span ast.Span,
) []Error {
	if ifaceMethod == nil || implMethod == nil {
		return nil
	}
	if len(ifaceMethod.Params) != len(implMethod.Params) {
		return []Error{InterfaceLifetimeMismatchError{
			Reason: "parameter count differs between interface and implementation",
			span:   span,
		}}
	}

	// Build a mapping from each interface parameter index to the
	// LifetimeVar (if any) shared with the interface's return type.
	ifaceReturnLT := type_system.GetLifetime(ifaceMethod.Return)
	implReturnLT := type_system.GetLifetime(implMethod.Return)

	if ifaceReturnLT == nil {
		// Interface promises a fresh return: implementation must also
		// not alias any parameter.
		if implReturnLT == nil {
			return nil
		}
		for i, p := range implMethod.Params {
			pLT := type_system.GetLifetime(p.Type)
			if pLT == nil {
				continue
			}
			if lifetimesMatch(pLT, implReturnLT) {
				return []Error{InterfaceLifetimeMismatchError{
					Reason: "implementation aliases parameter " + paramName(p, i) +
						" but interface declares the return value is independent",
					span: span,
				}}
			}
		}
		return nil
	}

	// Interface ties the return to one or more parameters.
	if implReturnLT == nil {
		// Implementation returns a fresh value — strictly more
		// conservative, always safe.
		return nil
	}
	// Determine which interface parameter(s) share a lifetime with
	// the interface return.
	tiedIfaceParams := make(map[int]bool)
	for i, p := range ifaceMethod.Params {
		pLT := type_system.GetLifetime(p.Type)
		if pLT == nil {
			continue
		}
		if lifetimesMatch(pLT, ifaceReturnLT) {
			tiedIfaceParams[i] = true
		}
	}
	// The implementation must alias only parameters that the
	// interface also ties to the return.
	for i, p := range implMethod.Params {
		pLT := type_system.GetLifetime(p.Type)
		if pLT == nil {
			continue
		}
		if !lifetimesMatch(pLT, implReturnLT) {
			continue
		}
		if !tiedIfaceParams[i] {
			return []Error{InterfaceLifetimeMismatchError{
				Reason: "implementation aliases parameter " + paramName(p, i) +
					" but interface does not declare that alias",
				span: span,
			}}
		}
	}
	return nil
}

// lifetimesMatch reports whether two lifetimes refer to the same
// LifetimeVar after pruning. Used to detect "return shares a parameter
// lifetime" within a single signature; cross-signature comparison
// (interface vs implementation) is structural — both the interface
// and the implementation introduce their own LifetimeVars, so we
// compare by parameter *position*, not by var identity.
func lifetimesMatch(a, b type_system.Lifetime) bool {
	a = type_system.PruneLifetime(a)
	b = type_system.PruneLifetime(b)
	if a == nil || b == nil {
		return a == b
	}
	if av, ok := a.(*type_system.LifetimeVar); ok {
		if bv, ok := b.(*type_system.LifetimeVar); ok {
			return av.ID == bv.ID
		}
	}
	if av, ok := a.(*type_system.LifetimeValue); ok {
		if bv, ok := b.(*type_system.LifetimeValue); ok {
			return av.ID == bv.ID
		}
	}
	return false
}

func paramName(p *type_system.FuncParam, idx int) string {
	if p == nil || p.Pattern == nil {
		return "#" + strconv.Itoa(idx)
	}
	if ip, ok := p.Pattern.(*type_system.IdentPat); ok && ip.Name != "" {
		return "'" + ip.Name + "'"
	}
	return "#" + strconv.Itoa(idx)
}

// InterfaceLifetimeMismatchError reports an interface implementation
// whose lifetime relationships are not compatible with the interface
// declaration. See VerifyLifetimeCompatibility.
type InterfaceLifetimeMismatchError struct {
	Reason string
	span   ast.Span
}

func (e InterfaceLifetimeMismatchError) isError()        {}
func (e InterfaceLifetimeMismatchError) IsWarning() bool { return false }
func (e InterfaceLifetimeMismatchError) Span() ast.Span  { return e.span }
func (e InterfaceLifetimeMismatchError) Message() string {
	return "interface implementation lifetime mismatch: " + e.Reason
}
