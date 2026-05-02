package checker

import (
	"fmt"
	"slices"
	"unicode"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// isValidJSIdentifier reports whether s can be used as a parameter name
// without quoting. Accepts any Unicode letter (per `unicode.IsLetter`),
// digits, `_` and `$`, with the first character constrained to a letter,
// `_`, or `$`. This matches the subset of identifiers the synthesized
// constructor can safely emit as a parameter pattern.
func isValidJSIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := unicode.IsLetter(r) || r == '_' || r == '$'
		if i == 0 {
			if !isLetter {
				return false
			}
			continue
		}
		if !isLetter && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

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
// only when the class has no in-body `constructor`. The synthesized
// constructor's parameters mirror the non-static instance fields in
// declaration order; its body assigns each param into `self.<field>`.
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

	// tempCounter generates parameter names for fields whose keys are not
	// valid JS identifiers (e.g. `"foo-bar"`). The synthesized parameter
	// is named `_field<N>` and the assignment uses index access on `self`.
	tempCounter := 0

	for _, bodyElem := range decl.Body {
		field, ok := bodyElem.(*ast.FieldElem)
		if !ok {
			continue
		}
		if field.Static {
			continue
		}

		fieldSpan := field.Span()

		// Resolve the field key to (paramName, lhs):
		//   - paramName is the synthesized constructor parameter's name.
		//   - lhs is the assignment target on `self` — either
		//     `self.<ident>` for identifier-shaped keys or
		//     `self["<key>"]` for string keys that aren't valid
		//     identifiers. Computed-key fields are rejected outright;
		//     synthesizing a param name for an arbitrary expression
		//     would silently drop the field.
		var paramName string
		var lhs ast.Expr
		selfRef := ast.NewIdent("self", fieldSpan)
		switch k := field.Name.(type) {
		case *ast.IdentExpr:
			paramName = k.Name
			lhs = ast.NewMember(selfRef, ast.NewIdentifier(paramName, fieldSpan), false, fieldSpan)
		case *ast.StrLit:
			if isValidJSIdentifier(k.Value) {
				paramName = k.Value
				lhs = ast.NewMember(selfRef, ast.NewIdentifier(paramName, fieldSpan), false, fieldSpan)
			} else {
				tempCounter++
				paramName = fmt.Sprintf("_field%d", tempCounter)
				keyExpr := ast.NewLitExpr(ast.NewString(k.Value, fieldSpan))
				lhs = ast.NewIndex(selfRef, keyExpr, false, fieldSpan)
			}
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

		// Build `<lhs> = <paramName>`: a synthesized parameter of the
		// same type as the field, assigned into self. Field-level
		// defaults are no longer supported in the parser, so every required
		// field receives a corresponding ctor parameter.
		rhs := ast.NewIdent(paramName, fieldSpan)

		paramPat := ast.NewIdentPat(paramName, false /* not mutable */, nil, nil, fieldSpan)
		params = append(params, &ast.Param{
			Pattern:  paramPat,
			TypeAnn:  field.Type,
			Optional: false,
		})

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
