package checker

import "github.com/escalier-lang/escalier/internal/type_system"

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
// already populated MutSelf on these elements.
func populateSelfParams(ns *type_system.Namespace) {
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
