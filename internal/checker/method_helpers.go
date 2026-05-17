package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// makeSelfParamWithLifetime returns a FuncParam representing an implicit
// `self` receiver for a method on `selfType`, optionally annotated with a
// receiver lifetime. `mutSelf` is the AST-level mutability flag (nil = no
// receiver, *false = `self`, *true = `mut self`); returns nil when there
// is no receiver. `selfType` is the class/interface receiver TypeRefType
// — shared across all methods of a class as `classSelfRef` — and a fresh
// shallow clone is made here so that setting `.Lifetime` does not poison
// sibling methods that declared a different (or no) receiver lifetime.
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

// buildMethodReceiver packages the receiver shape for inferFuncSig /
// inferFuncTypeAnn. When `receiverType` is nil (e.g. a method-shaped
// element inside a structural object-type annotation), there is no
// receiver to attach a lifetime to — we surface a diagnostic rather
// than silently drop `'a self`. Returns (nil, nil) for the no-receiver,
// no-lifetime case so plain object-literal methods continue to work.
func buildMethodReceiver(
	receiverType *type_system.TypeRefType,
	astRecv *ast.MethodReceiver,
) (*methodReceiver, []Error) {
	var mutSelf *bool
	var lifetimeNode ast.LifetimeAnnNode
	if astRecv != nil {
		m := astRecv.Mut
		mutSelf = &m
		lifetimeNode = astRecv.Lifetime
	}
	if receiverType == nil {
		if lifetimeNode != nil {
			return nil, []Error{ReceiverLifetimeOutsideMemberError{span: lifetimeNode.Span()}}
		}
		return nil, nil
	}
	return &methodReceiver{
		Type:         receiverType,
		MutSelf:      mutSelf,
		LifetimeNode: lifetimeNode,
	}, nil
}

// methodReceiver bundles the bits inferFuncSig / inferFuncTypeAnn need
// to wire a `self` receiver onto the resulting FuncType. Pass nil for
// plain (non-method) callers; pass a populated value for class methods,
// getters, setters, and interface method type-annotations. `Type` is
// the class/interface-instance ref the method is attached to;
// `LifetimeNode` is the optional `'a self` annotation (nil when absent).
type methodReceiver struct {
	Type         *type_system.TypeRefType
	MutSelf      *bool
	LifetimeNode ast.LifetimeAnnNode
}

// setReceiverMut sets the receiver mutability of fn by adding or removing
// a MutType wrapper on fn.SelfParam.Type. No-op if fn or SelfParam is nil.
// Used by the .d.ts prelude passes that classify lib methods as mut/non-mut
// after populateSelfParams has wired the receiver.
func setReceiverMut(fn *type_system.FuncType, mut bool) {
	if fn == nil || fn.SelfParam == nil {
		return
	}
	inner := fn.SelfParam.Type
	if mt, ok := inner.(*type_system.MutType); ok {
		inner = mt.Type
	}
	if mut {
		fn.SelfParam.Type = type_system.NewMutType(nil, inner)
	} else {
		fn.SelfParam.Type = inner
	}
}

// populateSelfParams backfills FuncType.SelfParam on every instance
// method/getter/setter in the namespace whose owning type is an
// ObjectType. Source-inferred types already get SelfParam at inference
// time (see infer_module.go, infer_stmt.go, infer_expr.go); this pass
// covers the .d.ts-loaded TypeScript lib types — Array, Map, Set,
// Promise, etc. — which arrive without a receiver representation.
//
// MethodElem defaults to `mut self`. TypeScript .d.ts carries no
// receiver-mutability annotation, so for any method we haven't
// positively classified, we don't know whether it mutates. Defaulting
// to mut is the conservative choice: an under-classified mutating
// method gates correctly behind `Mut[T]`, where defaulting to non-mut
// would silently launder mutation past the `val` boundary.
// UpdateMethodMutability and mergeReadonlyVariant run afterwards and
// strip `mut` from individual receivers via setReceiverMut when
// positively classified as non-mutating.
//
// GetterElem defaults to non-mut self and SetterElem defaults to
// `mut self`: accessor shape itself is the tier-3 strong signal
// (reading state doesn't mutate; assignment does). Without this
// asymmetry every .d.ts-loaded getter not in mutabilityOverrides
// would be hidden by isMemberVisible on a non-mut receiver.
//
// Recurses into nested namespaces so namespaced lib types (e.g.
// `Intl.Collator`) are covered too.
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
					e.Fn.SelfParam = type_system.NewSelfParam(selfRef, true)
				}
			case *type_system.GetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = type_system.NewSelfParam(selfRef, false)
				}
			case *type_system.SetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = type_system.NewSelfParam(selfRef, true)
				}
			}
		}
	}
}
