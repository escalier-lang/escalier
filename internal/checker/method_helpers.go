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

// populateSelfParams does two structural per-method adjustments in a
// single recursive walk over ns.
//
// 1. Backfills FuncType.SelfParam on every instance method / getter /
//    setter whose owning type is an ObjectType. Source-inferred types
//    already get SelfParam at inference time (see infer_module.go,
//    infer_stmt.go, infer_expr.go); this pass covers the .d.ts-loaded
//    TypeScript lib types — Array, Map, Set, Promise, etc. — which
//    arrive without a receiver representation.
//
//    Defaults:
//      - MethodElem → `mut self`. TS .d.ts carries no receiver-mut
//        annotation, so for any method we haven't positively classified
//        we don't know whether it mutates. Defaulting to mut is the
//        conservative choice (UpdateMethodMutability and
//        mergeReadonlyVariant strip `mut` afterwards where the method
//        is positively classified as non-mutating).
//      - GetterElem → non-mut self.
//      - SetterElem → `mut self`.
//    Accessor shape is the tier-3 strong signal — reading state doesn't
//    mutate; assignment does — so getters and setters get opposite
//    defaults rather than both defaulting to mut.
//
// 2. For symbol-keyed MethodElems whose name is `Symbol.iterator` or
//    `Symbol.asyncIterator`, strips `mut` from the receiver and wraps
//    the return type in `MutType`. This bakes the structural rule
//    "iterator producers don't mutate the source; the iterator they
//    produce is owned by the caller and is mut" into the receiver and
//    return-type representations. See iterable.go for why both
//    adjustments are needed (and which is subsumed by #614).
//
// Recurses into nested namespaces so namespaced lib types (e.g.
// `Intl.Collator`) are covered too.
func populateSelfParams(ns *type_system.Namespace) {
	iterID, hasIter := lookupSymbolID(ns, "iterator")
	asyncIterID, hasAsync := lookupSymbolID(ns, "asyncIterator")
	walkPopulateSelfParams(ns, iterID, hasIter, asyncIterID, hasAsync)
}

func walkPopulateSelfParams(
	ns *type_system.Namespace,
	iterID int, hasIter bool,
	asyncIterID int, hasAsync bool,
) {
	for _, child := range ns.Namespaces {
		walkPopulateSelfParams(child, iterID, hasIter, asyncIterID, hasAsync)
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
				// Apply fixups to every arm. At PR-A there is at most one
				// arm per method (no merge pass yet); the loop is forward-
				// compatible with overloaded methods, where receiver-
				// mutability uniformity is enforced at merge time so each
				// arm sees the same fixup.
				for _, fn := range e.Signatures {
					if fn == nil {
						continue
					}
					if fn.SelfParam == nil {
						fn.SelfParam = type_system.NewSelfParam(selfRef, true)
					}
					// Iterator-protocol fixup: `[Symbol.iterator]()` /
					// `[Symbol.asyncIterator]()` are non-mutating on the
					// source and return a freshly-owned (mut) iterator.
					if e.Name.Kind == type_system.SymObjTypeKeyKind &&
						((hasIter && e.Name.Sym == iterID) ||
							(hasAsync && e.Name.Sym == asyncIterID)) {
						setReceiverMut(fn, false)
						wrapReturnMut(fn)
					}
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

// wrapReturnMut wraps fn.Return in MutType if it isn't already.
// No-op when fn or fn.Return is nil.
func wrapReturnMut(fn *type_system.FuncType) {
	if fn == nil || fn.Return == nil {
		return
	}
	if _, isMut := type_system.Prune(fn.Return).(*type_system.MutType); isMut {
		return
	}
	fn.Return = type_system.NewMutType(nil, fn.Return)
}
