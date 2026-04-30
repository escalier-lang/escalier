package checker

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// inferConstructorSig builds the callable `FuncType` for an in-body
// `ConstructorElem`. It mirrors `inferFuncSig` but bakes in the three
// places a constructor signature diverges from a normal function:
//
//  1. The return type is fixed to the class's instance type (`retType`,
//     which already carries the class's type arguments). User-written
//     return annotations are intentionally ignored here; Phase 3+ will
//     reject them with `ConstructorWithReturnTypeError`.
//  2. The leading `mut self` parameter is stripped from the callable
//     arity — it is not part of how callers invoke `Foo(...)`.
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
	errors := []Error{}
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
	astCallableParams := ctor.Fn.Params
	if len(astCallableParams) > 0 {
		astCallableParams = astCallableParams[1:]
	}
	params, paramBindings, paramErrors := c.inferFuncParams(ctorCtx, astCallableParams)
	errors = slices.Concat(errors, paramErrors)

	var throwsType type_system.Type = type_system.NewNeverType(nil)
	if ctor.Fn.Throws != nil {
		var throwsErrors []Error
		throwsType, throwsErrors = c.inferTypeAnn(ctorCtx, ctor.Fn.Throws)
		errors = slices.Concat(errors, throwsErrors)
	}

	// (1) Return type is `Self`-with-type-args, supplied by the caller
	// as `retType`.
	funcType := type_system.NewFuncType(
		prov,
		ctorTypeParams,
		params,
		retType,
		throwsType,
	)
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
		hasDefault := field.Default != nil || field.Value != nil
		var defaultExpr ast.Expr
		if field.Default != nil {
			defaultExpr = field.Default
		} else if field.Value != nil {
			defaultExpr = field.Value
		}

		// Determine the field's user-facing name. Computed-key fields
		// are unconditionally rejected for synthesis — see the function
		// docstring.
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
			continue
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
