package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// makeSelfParam returns a FuncParam representing an implicit `self` receiver
// for a method whose containing type is `containingType`. `mutSelf` is the
// AST-level mutability flag (nil = static / no self, *false = `self`,
// *true = `mut self`). Returns nil when there is no receiver.
//
// The receiver's pattern is always `self`; the type is the containing type,
// wrapped in MutType when mutSelf points to true. Used during method
// inference to populate FuncType.SelfParam in lockstep with the existing
// MutSelf *bool denormalized cache on element nodes.
func makeSelfParam(containingType type_system.Type, mutSelf *bool) *type_system.FuncParam {
	if mutSelf == nil || containingType == nil {
		return nil
	}
	t := containingType
	if *mutSelf {
		t = type_system.NewMutType(nil, containingType)
	}
	return &type_system.FuncParam{
		Pattern: type_system.NewIdentPat("self"),
		Type:    t,
	}
}

// makeSelfParamWithLifetime is the lifetime-bearing form of makeSelfParam.
// `selfType` is the class/interface receiver TypeRefType — shared across all
// methods of a class as `classSelfRef` — and a fresh shallow clone is made
// here so that setting `.Lifetime` does not poison sibling methods that
// declared a different (or no) receiver lifetime. When `lifetime` is nil
// the result is identical to `makeSelfParam(selfType, mutSelf)`.
func makeSelfParamWithLifetime(
	selfType *type_system.TypeRefType,
	mutSelf *bool,
	lifetime type_system.Lifetime,
) *type_system.FuncParam {
	if mutSelf == nil || selfType == nil {
		return nil
	}
	clone := *selfType
	if lifetime != nil {
		clone.Lifetime = lifetime
	}
	receiver := &clone
	var t type_system.Type = receiver
	if *mutSelf {
		t = type_system.NewMutType(nil, receiver)
	}
	return &type_system.FuncParam{
		Pattern: type_system.NewIdentPat("self"),
		Type:    t,
	}
}

// inferMethodFuncSig is the method-context wrapper around inferFuncSig.
// It runs signature inference, resolves the optional `'a self` lifetime
// against the function's own scope, wires SelfParam onto the resulting
// FuncType, then runs the §9.7 class 1 unused-lifetime-params check —
// in that order so a `<'a>` referenced only by `'a self` participates
// in the "used" set.
//
// `receiver` is the class/interface-instance ref the method is attached
// to. `mutSelf` carries the AST `self` vs `mut self` flag. Pass nil for
// `selfLifetimeNode` when there is no `'a self` annotation.
func (c *Checker) inferMethodFuncSig(
	ctx Context,
	sig *ast.FuncSig,
	node ast.Node,
	receiver *type_system.TypeRefType,
	mutSelf *bool,
	selfLifetimeNode ast.LifetimeAnnNode,
) (*type_system.FuncType, Context, map[string]*type_system.Binding, []Error) {
	fn, funcCtx, bindings, errors := c.inferFuncSig(ctx, sig, node)
	selfLT, ltErrs := c.resolveLifetimeAnn(funcCtx.Scope, selfLifetimeNode)
	errors = slices.Concat(errors, ltErrs)
	fn.SelfParam = makeSelfParamWithLifetime(receiver, mutSelf, selfLT)
	errors = slices.Concat(errors, reportUnusedLifetimeParams(fn, sig.LifetimeParams, node.Span()))
	return fn, funcCtx, bindings, errors
}

// inferMethodFuncTypeAnn is the method-context wrapper around
// inferFuncTypeAnn. Mirrors inferMethodFuncSig but for type-annotation
// positions (interface method/getter/setter bodies).
//
// Because inferFuncTypeAnn does not return its per-function scope, this
// wrapper resolves `'a self` via resolveSelfLifetimeForFn — which
// rebuilds a scope from `fn.LifetimeParams` on top of `ctx.Scope` so
// the receiver lifetime can be looked up by name. See issue #572 for
// the plan to unify this with resolveLifetimeAnn.
func (c *Checker) inferMethodFuncTypeAnn(
	ctx Context,
	fnAnn *ast.FuncTypeAnn,
	receiver *type_system.TypeRefType,
	mutSelf *bool,
	selfLifetimeNode ast.LifetimeAnnNode,
) (*type_system.FuncType, []Error) {
	fn, errors := c.inferFuncTypeAnn(ctx, fnAnn)
	selfLT, ltErrs := c.resolveSelfLifetimeForFn(ctx.Scope, fn, selfLifetimeNode)
	errors = slices.Concat(errors, ltErrs)
	fn.SelfParam = makeSelfParamWithLifetime(receiver, mutSelf, selfLT)
	errors = slices.Concat(errors, reportUnusedLifetimeParams(fn, fnAnn.LifetimeParams, fnAnn.Span()))
	return fn, errors
}

// methodRequiresMutSelf reports whether a method/getter requires a `mut self`
// receiver. Reads FuncType.SelfParam when populated (single source of truth);
// falls back to the denormalized MutSelf flag for elements that haven't gone
// through SelfParam population yet (a defensive fallback — every code path
// that constructs MethodElem/GetterElem/SetterElem now sets SelfParam, and
// the prelude backfill covers .d.ts-loaded types).
func methodRequiresMutSelf(fn *type_system.FuncType, mutSelf *bool) bool {
	if fn != nil && fn.SelfParam != nil {
		_, isMut := fn.SelfParam.Type.(*type_system.MutType)
		return isMut
	}
	return mutSelf != nil && *mutSelf
}

// populateSelfParams backfills FuncType.SelfParam on every instance
// method/getter/setter in the namespace whose owning type is an
// ObjectType. Source-inferred types already get SelfParam at inference
// time (see infer_module.go, infer_stmt.go, infer_expr.go); this pass
// covers the .d.ts-loaded TypeScript lib types — Array, Map, Set,
// Promise, etc. — which arrive without a receiver representation. The
// pass runs in initializeGlobalScope after UpdateMethodMutability has
// already populated MutSelf on these elements. Recurses into nested
// namespaces so namespaced lib types (e.g. `Intl.Collator`) are
// covered too.
func populateSelfParams(ns *type_system.Namespace) {
	for _, child := range ns.Namespaces {
		populateSelfParams(child)
	}
	for name, typeAlias := range ns.Types {
		objType, ok := type_system.Prune(typeAlias.Type).(*type_system.ObjectType)
		if !ok {
			continue
		}
		// Build a TypeRef to this alias to use as the receiver type.
		// Type args mirror the alias's TypeParams in declaration order.
		typeArgs := make([]type_system.Type, len(typeAlias.TypeParams))
		for i, tp := range typeAlias.TypeParams {
			typeArgs[i] = type_system.NewTypeRefType(nil, tp.Name, nil)
		}
		selfRef := type_system.NewTypeRefType(nil, name, typeAlias, typeArgs...)

		for _, elem := range objType.Elems {
			switch e := elem.(type) {
			case *type_system.MethodElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, e.MutSelf)
				}
			case *type_system.GetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, e.MutSelf)
				}
			case *type_system.SetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, e.MutSelf)
				}
			}
		}
	}
}
