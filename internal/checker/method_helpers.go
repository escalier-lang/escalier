package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// makeSelfParam returns a FuncParam representing an implicit `self` receiver
// for a method whose containing type is `containingType`. `mutSelf` is the
// AST-level mutability flag (nil = static / no self, *false = `self`,
// *true = `mut self`). Returns nil when there is no receiver.
//
// Thin wrapper around type_system.NewSelfParam that bridges the AST-level
// `*bool` (where nil means "no receiver") to the type-system-level `bool`
// (where the no-receiver case is communicated by skipping the call).
func makeSelfParam(containingType type_system.Type, mutSelf *bool) *type_system.FuncParam {
	if mutSelf == nil {
		return nil
	}
	return type_system.NewSelfParam(containingType, *mutSelf)
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
// The default mutability is non-mut self; UpdateMethodMutability and
// mergeReadonlyVariant run afterwards and flip individual receivers to
// `mut self` via setReceiverMut. Recurses into nested namespaces so
// namespaced lib types (e.g. `Intl.Collator`) are covered too.
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
		nonMut := false

		for _, elem := range objType.Elems {
			switch e := elem.(type) {
			case *type_system.MethodElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, &nonMut)
				}
			case *type_system.GetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, &nonMut)
				}
			case *type_system.SetterElem:
				if e.Fn != nil && e.Fn.SelfParam == nil {
					e.Fn.SelfParam = makeSelfParam(selfRef, &nonMut)
				}
			}
		}
	}
}
