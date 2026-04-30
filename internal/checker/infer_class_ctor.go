package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// ctorCallableParams returns the constructor's callable parameter list —
// i.e. `Fn.Params` with the leading `mut self` receiver stripped. The
// `mut self` parameter is not part of how callers invoke `Foo(...)` and
// must not be passed to `inferFuncParams` or used to compute callable
// arity.
func ctorCallableParams(ctor *ast.ConstructorElem) []*ast.Param {
	if len(ctor.Fn.Params) == 0 {
		return nil
	}
	return ctor.Fn.Params[1:]
}

// validateConstructorSelf checks that the constructor's first parameter
// is a well-formed `mut self` receiver and that the constructor does not
// declare an explicit return type. Returns the diagnostics; an empty
// slice means the signature shape is acceptable.
//
// `MutSelf` is the parser's truth source for whether the user wrote
// `self` / `mut self`: nil means absent, false means `self`, true means
// `mut self`. The first `ast.Param` (when present) carries the `self`
// pattern itself, which is where a stray type annotation would live.
func validateConstructorSelf(ctor *ast.ConstructorElem) []Error {
	errors := []Error{}
	span := ctor.Span()

	switch {
	case ctor.MutSelf == nil:
		errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfMissing, span: span})
	case !*ctor.MutSelf:
		errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfNotMut, span: span})
	}

	// Only check the type annotation when `self` is actually present —
	// otherwise the "missing" error above already covers the case and a
	// secondary "has-type-annotation" would be noise.
	if ctor.MutSelf != nil && len(ctor.Fn.Params) > 0 {
		selfParam := ctor.Fn.Params[0]
		if selfParam.TypeAnn != nil {
			errors = append(errors, MissingMutSelfParameterError{Reason: MutSelfHasTypeAnnotation, span: selfParam.Span()})
		}
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
//  2. The leading `mut self` parameter is stripped from the callable
//     arity — it is not part of how callers invoke `Foo(...)`. The
//     receiver shape is validated via `validateConstructorSelf`.
//  3. Class-level type params remain in scope (via the caller's
//     `declCtx`) and are prepended to any constructor-level type params
//     so the resulting `FuncType` quantifies over both.
//
// The returned context is the constructor's own scope (with type params
// in scope) so the caller can reuse it for body checking. Param
// bindings cover the post-`mut self` parameters only; the caller is
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
	ctorLocalTypeParams, tpErrors := c.inferFuncTypeParams(declCtx, ctorCtx, ctor.Fn.TypeParams)
	errors = slices.Concat(errors, tpErrors)
	ctorTypeParams := classTypeParams
	if len(ctorLocalTypeParams) > 0 {
		ctorTypeParams = append(append([]*type_system.TypeParam{}, classTypeParams...), ctorLocalTypeParams...)
	}

	// (2) Skip Fn.Params[0] — the `mut self` receiver — when computing
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
	return funcType, ctorCtx, paramBindings, errors
}

// fieldInitializer returns the field's initializer expression — the
// `= expr` form (`Default`) or the legacy `x: expr` shorthand
// (`Value`), in that order — or nil if the field has neither. Callers
// that need to know "is this field already initialized?" should use
// this helper so the synthesizer's view of "has a default" stays in
// sync with the rest of the class-checker.
func fieldInitializer(field *ast.FieldElem) ast.Expr {
	if field.Default != nil {
		return field.Default
	}
	if field.Value != nil {
		return field.Value
	}
	return nil
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

// synthesizeConstructorElem builds a `*ast.ConstructorElem` from a class's
// instance-field declarations. Per the implementation plan §2.7, this runs
// only when the class has neither a primary-ctor head nor an in-body
// `constructor`. The synthesized constructor's parameters mirror the
// non-static, non-optional, default-less fields in declaration order; its
// body assigns each such param into `self.<field>`, and emits
// `self.<field> = <default>` for any field that carries a default value.
//
// Subclass synthesis (`extends`) and `super(...)` forwarding are deferred
// to Future Work.
//
// Errors:
//   - A non-static field with a computed key cannot be synthesized — the
//     synthesizer has no parameter name to bind it to and no stable way
//     to refer to it from a default-only initializer either. Reports
//     `ComputedKeyFieldRequiresConstructorError`. Users with computed
//     keys must declare an explicit `constructor` block.
func (c *Checker) synthesizeConstructorElem(decl *ast.ClassDecl) (*ast.ConstructorElem, []Error) {
	errors := []Error{}
	classSpan := decl.Name.Span()

	// `mut self` synthesized at the class-name span so any diagnostics
	// produced against the synthesized constructor land on the header.
	selfPat := ast.NewIdentPat("self", true /* mutable */, nil, nil, classSpan)
	selfParam := &ast.Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
	params := []*ast.Param{selfParam}
	stmts := []ast.Stmt{}

	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*ast.FieldElem)
		if !ok {
			continue
		}
		if field.Static {
			continue
		}

		fieldSpan := field.Span()
		defaultExpr := fieldInitializer(field)
		hasDefault := defaultExpr != nil

		// Determine the field's user-facing name. Computed-key fields
		// are unconditionally rejected for synthesis — see the function
		// docstring. All other ObjKey kinds are exhaustive at the AST
		// level (`*ast.IdentExpr`, `*ast.StrLit`, `*ast.ComputedKey`),
		// so any new variant should be handled explicitly here.
		var fieldName string
		switch k := field.Name.(type) {
		case *ast.IdentExpr:
			fieldName = k.Name
		case *ast.StrLit:
			fieldName = k.Value
		case *ast.ComputedKey:
			// We cannot synthesize a parameter name for a computed
			// key, so abort synthesis entirely rather than emitting a
			// partial constructor that silently drops the field.
			// Callers must guard on a nil return.
			errors = append(errors, ComputedKeyFieldRequiresConstructorError{span: fieldSpan})
			return nil, errors
		default:
			panic("synthesizeConstructorElem: unexpected ObjKey variant")
		}

		// Build `self.<field> = <rhs>`. For default-bearing fields the
		// RHS is the default expression; otherwise the RHS is a reference
		// to the synthesized parameter of the same name.
		selfRef := ast.NewIdent("self", fieldSpan)
		lhs := ast.NewMember(selfRef, ast.NewIdentifier(fieldName, fieldSpan), false, fieldSpan)

		var rhs ast.Expr
		if hasDefault {
			rhs = defaultExpr
		} else {
			rhs = ast.NewIdent(fieldName, fieldSpan)
			// Synthesized parameter: same name and type as the field.
			paramPat := ast.NewIdentPat(fieldName, false /* not mutable */, nil, nil, fieldSpan)
			params = append(params, &ast.Param{
				Pattern:  paramPat,
				TypeAnn:  field.Type,
				Optional: false,
			})
		}

		assign := ast.NewBinary(lhs, rhs, ast.Assign, fieldSpan)
		stmts = append(stmts, ast.NewExprStmt(assign, fieldSpan))
	}

	body := &ast.Block{Stmts: stmts, Span: classSpan}
	mutSelf := true
	fnExpr := ast.NewFuncExpr(nil, nil, params, nil, nil, false, body, classSpan)
	return &ast.ConstructorElem{
		Fn:      fnExpr,
		MutSelf: &mutSelf,
		Private: false,
		Span_:   classSpan,
	}, errors
}
