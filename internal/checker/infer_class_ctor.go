package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// synthesizedConstructorElem returns the constructor a class with none declared gets,
// reporting ComputedKeyFieldRequiresConstructorError when a non-static field's computed
// key leaves no parameter name to bind it to.
//
// JavaScript emission calls ast.ImplicitConstructor too, so the constructor this
// returns and the one emission derives are the same element built the same way.
func synthesizedConstructorElem(decl *ast.ClassDecl) (*ast.ConstructorElem, []Error) {
	synth, blocker := ast.ImplicitConstructor(decl)
	if blocker != nil {
		return nil, []Error{ComputedKeyFieldRequiresConstructorError{span: blocker.Span()}}
	}
	return synth, nil
}

// ctorCallableParams returns the constructor's callable parameter list —
// i.e. `Fn.Params` with the leading `&mut self` receiver stripped when the
// receiver is present. The `&mut self` parameter is not part of how callers
// invoke `Foo(...)` and must not be passed to `inferFuncParams` or used to
// compute callable arity. When `ctor.Receiver` is nil (a malformed
// constructor missing its receiver), `Fn.Params[0]` is a real user
// parameter and must not be stripped — otherwise downstream inference
// produces a callable with the first param dropped, compounding the
// already-reported MissingMutSelfParameterError with a misleading arity.
func ctorCallableParams(ctor *ast.ConstructorElem) []*ast.Param {
	if ctor.Receiver == nil {
		return ctor.Fn.Params
	}
	if len(ctor.Fn.Params) == 0 {
		return nil
	}
	return ctor.Fn.Params[1:]
}

// validateConstructorSelf checks that the constructor's first parameter
// is a well-formed `&mut self` receiver and that the constructor does not
// declare an explicit return type. Returns the diagnostics; an empty
// slice means the signature shape is acceptable.
//
// `Receiver` is the parser's record of the receiver the user wrote. A nil
// receiver means none was written. The first `ast.Param`, when present,
// carries the `self` pattern itself, which is where a stray type
// annotation would live.
func validateConstructorSelf(ctor *ast.ConstructorElem) []Error {
	errors := []Error{}
	span := ctor.Span()

	switch {
	case ctor.Receiver == nil:
		errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfMissing, span: span})
	case ctor.Receiver.Consumes():
		errors = append(errors, ConstructorConsumesSelfError{span: span})
	case !ctor.Receiver.Mut:
		errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfNotMut, span: span})
	}

	// Only check the type annotation when `self` is actually present —
	// otherwise the "missing" error above already covers the case and a
	// secondary "has-type-annotation" would be noise.
	if ctor.Receiver != nil && len(ctor.Fn.Params) > 0 {
		selfParam := ctor.Fn.Params[0]
		if selfParam.TypeAnn != nil {
			errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfHasTypeAnnotation, span: selfParam.Span()})
		}
	}

	// A lifetime on the constructor's `self` is rejected: constructors
	// return a freshly-allocated `Self`, so there is no inbound borrow
	// for the lifetime to constrain.
	if ctor.Receiver != nil && ctor.Receiver.Lifetime != nil {
		errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfHasLifetime, span: ctor.Receiver.Lifetime.Span()})
	}

	if ctor.Fn.Return != nil {
		errors = append(errors, ConstructorWithReturnTypeError{span: ctor.Fn.Return.Span()})
	}

	return errors
}

// inferConstructorSig builds the callable `FuncType` for an in-body
// `ConstructorElem`. It mirrors `inferFuncSig` but bakes in the three
// places a constructor signature diverges from a normal function:
//
//  1. The return type is fixed to the class's instance type (`retType`,
//     which already carries the class's type arguments). User-written
//     return annotations are rejected via `ConstructorWithReturnTypeError`
//     (see `validateConstructorSelf`) and ignored when building the type.
//  2. The leading `&mut self` parameter is stripped from the callable
//     arity — it is not part of how callers invoke `Foo(...)`. The
//     receiver shape is validated via `validateConstructorSelf`.
//  3. Class-level type params remain in scope (via the caller's
//     `declCtx`) and are prepended to any constructor-level type params
//     so the resulting `FuncType` quantifies over both.
//
// The returned context is the constructor's own scope (with type params
// in scope) so the caller can reuse it for body checking. Param
// bindings cover the post-`&mut self` parameters only; the caller is
// responsible for adding `self` separately when the body is checked.
func (c *Checker) inferConstructorSig(
	declCtx Context,
	ctor *ast.ConstructorElem,
	classTypeParams []*type_system.TypeParam,
	retType type_system.Type,
	prov provenance.Provenance,
) (*type_system.FuncType, Context, map[string]*type_system.Binding, []Error) {
	errors := validateConstructorSelf(ctor)
	ctorCtx := declCtx.WithNewScope()

	// (3) Constructor-level type params (rare) are layered on top of the
	// class's own type params; the class's params remain in scope via
	// `declCtx`.
	ctorLocalTypeParams, tpErrors := c.resolveTypeParams(declCtx, ctorCtx, ctor.Fn.TypeParams)
	errors = slices.Concat(errors, tpErrors)
	ctorTypeParams := classTypeParams
	if len(ctorLocalTypeParams) > 0 {
		ctorTypeParams = append(append([]*type_system.TypeParam{}, classTypeParams...), ctorLocalTypeParams...)
	}

	// (2) Skip Fn.Params[0] — the `&mut self` receiver — when computing
	// the callable arity.
	params, paramBindings, paramErrors := c.inferFuncParams(ctorCtx, ctorCallableParams(ctor))
	errors = slices.Concat(errors, paramErrors)

	var throwsType type_system.Type = type_system.NewNeverType(nil)
	if ctor.Fn.Throws != nil {
		var throwsErrors []Error
		throwsType, throwsErrors = c.inferTypeAnn(ctorCtx, ctor.Fn.Throws)
		errors = slices.Concat(errors, throwsErrors)
	}

	// (1) Return type is `Self`-with-type-args, supplied by the caller
	// as `retType`. Any user-written `Fn.Return` was already reported by
	// `validateConstructorSelf`.
	funcType := type_system.NewFuncType(
		prov,
		ctorTypeParams,
		params,
		retType,
		throwsType,
	)
	// Note: constructors deliberately do NOT carry SelfParam. A
	// constructor produces a receiver via its return type — there is no
	// pre-existing receiver to bind. Callers invoke `C(args)`, not
	// `instance.constructor(args)`. The validateConstructorSelf check
	// above still enforces that the AST-level `&mut self` is present.
	return funcType, ctorCtx, paramBindings, errors
}

// classFieldName extracts a printable identifier for a class field's key.
// Used in diagnostics where we need a name to attach to a `FieldElem`.
// Computed-key fields fall back to a placeholder.
func classFieldName(key ast.ObjKey) string {
	switch k := key.(type) {
	case *ast.IdentExpr:
		return k.Name
	case *ast.StrLit:
		return k.Value
	case *ast.ComputedKey:
		return "<computed>"
	default:
		return "<unknown>"
	}
}
