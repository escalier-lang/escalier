package checker

import (
	"fmt"
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// inferClassDecl handles `class C { ... }` appearing inside a function body
// (i.e. via `inferDecl` -> `inferStmt`). Top-level classes go through the
// multi-phase pipeline in InferComponent (infer_module.go); body-level
// classes are processed in source order with no SCC/cyclic-decl concerns,
// so the placeholder and definition phases run sequentially in one pass.
//
// Body-level classes intentionally skip:
//   - component-wide call-site / generalization batching, since the body's
//     enclosing FuncDecl drives those at its own scope;
//   - the Phase 8.7 lifetime fixed-point loop (single-decl, no SCC).
//
// The behaviour mirrors the inlined ClassDecl branches at
// infer_module.go:390-798 (placeholder) and infer_module.go:1193-1687
// (definition); helpers are reused unchanged.
func (c *Checker) inferClassDecl(ctx Context, decl *ast.ClassDecl) []Error {
	errors := []Error{}

	// --- Placeholder phase ---------------------------------------------------

	instanceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

	typeParams := c.inferTypeParams(decl.TypeParams)

	declCtx := ctx.WithNewScope()

	// Declare class-level lifetime params on declCtx so fields/methods can
	// reference them via inline annotations.
	lifetimeParams := c.declareLifetimeParams(declCtx.Scope, decl.LifetimeParams)

	typeAlias := &type_system.TypeAlias{
		Type:           instanceType,
		TypeParams:     typeParams,
		LifetimeParams: lifetimeParams,
		Exported:       decl.Export(),
	}

	ctx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)

	for _, typeParam := range typeParams {
		var t type_system.Type = type_system.NewUnknownType(nil)
		if typeParam.Constraint != nil {
			t = typeParam.Constraint
		}
		declCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
			Type:        t,
			TypeParams:  []*type_system.TypeParam{},
			IsTypeParam: true,
		})
	}

	// Pre-walk constructors: at most one is allowed today.
	inBodyCtors := []*ast.ConstructorElem{}
	for _, bodyElem := range decl.Body {
		if ctor, ok := bodyElem.(*ast.ConstructorElem); ok {
			inBodyCtors = append(inBodyCtors, ctor)
		}
	}
	for _, ctor := range inBodyCtors {
		if ctor.Private {
			errors = append(errors, PrivateConstructorNotYetSupportedError{span: ctor.Span()})
		}
	}
	if len(inBodyCtors) > 1 {
		for _, extra := range inBodyCtors[1:] {
			errors = append(errors, MultipleConstructorsNotYetSupportedError{span: extra.Span()})
		}
	}

	// Synthesize a constructor when none is present (mirrors infer_module.go).
	// Subclasses and `declare class` blocks are excluded.
	if len(inBodyCtors) == 0 && !decl.Declare() {
		if decl.Extends != nil {
			errors = append(errors, SubclassConstructorRequiredError{
				span: decl.Name.Span(),
			})
		} else {
			synth, synthErrors := c.synthesizeConstructorElem(decl)
			errors = slices.Concat(errors, synthErrors)
			if synth != nil {
				decl.Body = append([]ast.ClassElem{synth}, decl.Body...)
				inBodyCtors = []*ast.ConstructorElem{synth}
			}
		}
	}

	objTypeElems := []type_system.ObjTypeElem{}
	staticElems := []type_system.ObjTypeElem{}
	instanceSymbolKeyMap := make(map[int]any)
	staticSymbolKeyMap := make(map[int]any)

	// Build the class instance ref once so each method/getter/setter can
	// attach it as their FuncType.SelfParam.
	classTypeArgs := make([]type_system.Type, len(typeParams))
	for i := range typeParams {
		classTypeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
	}
	classSelfRef := type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, classTypeArgs...)

	// Track method/getter/setter contexts keyed by body-element index. The
	// module-level path keys by (decl, index); for a single body-level class
	// the index alone suffices.
	methodCtxForElem := make(map[int]Context)

	for i, elem := range decl.Body {
		switch elem := elem.(type) {
		case *ast.FieldElem:
			key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
			errors = slices.Concat(errors, keyErrors)
			if key == nil {
				continue
			}

			if key.Kind == type_system.SymObjTypeKeyKind {
				if _, ok := elem.Name.(*ast.ComputedKey); ok {
					expr := elem.Name.(*ast.ComputedKey).Expr
					if elem.Static {
						staticSymbolKeyMap[key.Sym] = expr
					} else {
						instanceSymbolKeyMap[key.Sym] = expr
					}
				}
			}

			if elem.Static {
				tvar := c.FreshVar(nil)
				tvar.FromBinding = true
				propElem := type_system.NewPropertyElem(*key, tvar)
				propElem.Readonly = elem.Readonly
				staticElems = append(staticElems, propElem)
			} else {
				tvar := c.FreshVar(nil)
				tvar.FromBinding = true
				propElem := type_system.NewPropertyElem(*key, tvar)
				propElem.Readonly = elem.Readonly
				propElem.Optional = elem.Optional
				objTypeElems = append(objTypeElems, propElem)
			}
		case *ast.MethodElem:
			key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
			errors = slices.Concat(errors, keyErrors)
			if !elem.Static && elem.Receiver == nil {
				errors = append(errors, MissingSelfReceiverError{span: elem.Span_})
			}
			recv, recvErrs := buildMethodReceiver(classSelfRef, elem.Receiver)
			errors = slices.Concat(errors, recvErrs)
			methodType, methodCtx, _, sigErrors := c.inferFuncSig(
				declCtx, &elem.Fn.FuncSig, elem.Fn, recv)
			errors = slices.Concat(errors, sigErrors)
			if key == nil {
				continue
			}

			if key.Kind == type_system.SymObjTypeKeyKind {
				if _, ok := elem.Name.(*ast.ComputedKey); ok {
					expr := elem.Name.(*ast.ComputedKey).Expr
					if elem.Static {
						staticSymbolKeyMap[key.Sym] = expr
					} else {
						instanceSymbolKeyMap[key.Sym] = expr
					}
				}
			}

			methodCtxForElem[i] = methodCtx
			if elem.Static {
				staticElems = append(staticElems,
					type_system.NewMethodElem(*key, methodType))
			} else {
				objTypeElems = append(objTypeElems,
					type_system.NewMethodElem(*key, methodType))
			}
		case *ast.GetterElem:
			key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
			errors = slices.Concat(errors, keyErrors)
			if !elem.Static && elem.Receiver == nil {
				errors = append(errors, MissingSelfReceiverError{span: elem.Span_})
			}
			recv, recvErrs := buildMethodReceiver(classSelfRef, elem.Receiver)
			errors = slices.Concat(errors, recvErrs)
			funcType, _, _, sigErrors := c.inferFuncSig(
				declCtx, &elem.Fn.FuncSig, elem.Fn, recv)
			errors = slices.Concat(errors, sigErrors)
			if key == nil {
				continue
			}

			if key.Kind == type_system.SymObjTypeKeyKind {
				if _, ok := elem.Name.(*ast.ComputedKey); ok {
					expr := elem.Name.(*ast.ComputedKey).Expr
					if elem.Static {
						staticSymbolKeyMap[key.Sym] = expr
					} else {
						instanceSymbolKeyMap[key.Sym] = expr
					}
				}
			}

			if elem.Static {
				staticElems = append(staticElems,
					type_system.NewGetterElem(*key, funcType))
			} else {
				objTypeElems = append(objTypeElems,
					type_system.NewGetterElem(*key, funcType))
			}
		case *ast.SetterElem:
			key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
			errors = slices.Concat(errors, keyErrors)
			if !elem.Static && elem.Receiver == nil {
				errors = append(errors, MissingSelfReceiverError{span: elem.Span_})
			}
			recv, recvErrs := buildMethodReceiver(classSelfRef, elem.Receiver)
			errors = slices.Concat(errors, recvErrs)
			funcType, _, _, sigErrors := c.inferFuncSig(
				declCtx, &elem.Fn.FuncSig, elem.Fn, recv)
			errors = slices.Concat(errors, sigErrors)
			if key == nil {
				continue
			}

			if key.Kind == type_system.SymObjTypeKeyKind {
				if _, ok := elem.Name.(*ast.ComputedKey); ok {
					expr := elem.Name.(*ast.ComputedKey).Expr
					if elem.Static {
						staticSymbolKeyMap[key.Sym] = expr
					} else {
						instanceSymbolKeyMap[key.Sym] = expr
					}
				}
			}

			if elem.Static {
				staticElems = append(staticElems,
					type_system.NewSetterElem(*key, funcType))
			} else {
				objTypeElems = append(objTypeElems,
					type_system.NewSetterElem(*key, funcType))
			}
		case *ast.ConstructorElem:
			// Constructor signatures are handled separately below via
			// inferConstructorSig; nothing to do during the element walk.
		default:
			errors = append(errors, &UnimplementedError{
				message: fmt.Sprintf("Unsupported class element type: %T", elem),
				span:    elem.Span(),
			})
		}
	}

	provenance := &ast.NodeProvenance{Node: decl}
	objType := type_system.NewNominalObjectType(provenance, objTypeElems)
	objType.SymbolKeyMap = instanceSymbolKeyMap

	if decl.Extends != nil {
		extendsType, extendsErrors := c.inferTypeAnn(declCtx, decl.Extends)
		errors = slices.Concat(errors, extendsErrors)
		if extendsType != nil {
			if typeRef, ok := type_system.Prune(extendsType).(*type_system.TypeRefType); ok {
				objType.Extends = []*type_system.TypeRefType{typeRef}
			}
		}
	}

	var implementsTypes []*type_system.TypeRefType
	for _, implTypeAnn := range decl.Implements {
		implType, implErrors := c.inferTypeAnn(declCtx, implTypeAnn)
		errors = slices.Concat(errors, implErrors)
		typeRef, ok := type_system.Prune(implType).(*type_system.TypeRefType)
		if !ok {
			continue
		}
		implementsTypes = append(implementsTypes, typeRef)
	}
	objType.Implements = implementsTypes

	unifyErrors := c.Unify(ctx, instanceType, objType)
	errors = slices.Concat(errors, unifyErrors)

	errors = slices.Concat(errors, c.checkImplements(declCtx, decl, objType))

	// Build the constructor signature. The instance ref built above
	// (classSelfRef) is reused as the constructor's return type.
	var ctorFuncType *type_system.FuncType
	var ctorParamBindings map[string]*type_system.Binding
	var ctorCtx Context
	if len(inBodyCtors) > 0 {
		ctor := inBodyCtors[0]
		var sigErrors []Error
		ctorFuncType, ctorCtx, ctorParamBindings, sigErrors = c.inferConstructorSig(
			declCtx, ctor, typeParams, classSelfRef, provenance,
		)
		errors = slices.Concat(errors, sigErrors)
	} else {
		// Synthesis failed or `declare class`; emit a zero-arg placeholder so
		// downstream phases don't crash. The class is already in an error
		// state in the synthesis-failure case.
		ctorFuncType = type_system.NewFuncType(
			provenance,
			typeParams,
			nil,
			classSelfRef,
			type_system.NewNeverType(nil),
		)
	}

	constructorElem := &type_system.ConstructorElem{Fn: ctorFuncType}
	classObjTypeElems := []type_system.ObjTypeElem{constructorElem}
	classObjTypeElems = append(classObjTypeElems, staticElems...)

	classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
	classObjType.SymbolKeyMap = staticSymbolKeyMap

	classBinding := &type_system.Binding{
		Source:     provenance,
		Type:       classObjType,
		Assignable: false,
		Mutable:    false,
		Exported:   decl.Export(),
	}
	ctx.Scope.setValue(decl.Name.Name, classBinding)

	// --- Definition phase ----------------------------------------------------

	bodyCtx := declCtx.WithNewScope()

	for i, bodyElem := range decl.Body {
		switch bodyElem := bodyElem.(type) {
		case *ast.FieldElem:
			isStatic := bodyElem.Static
			var targetType *type_system.ObjectType
			if isStatic {
				targetType = classObjType
			} else {
				targetType = objType
			}

			astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
			errors = slices.Concat(errors, keyErrors)
			var prop *type_system.PropertyElem
			if astKey != nil {
				for _, e := range targetType.Elems {
					if propElem, ok := e.(*type_system.PropertyElem); ok {
						if propElem.Name == *astKey {
							prop = propElem
							break
						}
					}
				}
			}

			if prop == nil {
				continue
			}

			if bodyElem.Type != nil {
				annType, annErrors := c.inferTypeAnn(bodyCtx, bodyElem.Type)
				errors = slices.Concat(errors, annErrors)
				unifyErrors := c.Unify(ctx, prop.Value, annType)
				errors = slices.Concat(errors, unifyErrors)
			}
			if bodyElem.Value != nil {
				if !isStatic {
					errors = append(errors, FieldInitializerNotAllowedError{
						FieldName: classFieldName(bodyElem.Name),
						span:      bodyElem.Span(),
					})
				} else {
					initType, initErrors := c.inferExpr(bodyCtx, bodyElem.Value)
					errors = slices.Concat(errors, initErrors)
					unifyErrors := c.Unify(ctx, initType, prop.Value)
					errors = slices.Concat(errors, unifyErrors)
				}
			}

			if isStatic && bodyElem.Value == nil {
				resolved, expandErrors := c.ExpandType(ctx, prop.Value, 1)
				errors = slices.Concat(errors, expandErrors)
				if !typeContainsUndefined(type_system.Prune(resolved)) {
					errors = append(errors, StaticFieldMissingInitializerError{
						FieldName: classFieldName(bodyElem.Name),
						span:      bodyElem.Span(),
					})
				}
			}

		case *ast.MethodElem:
			isStatic := bodyElem.Static
			var targetType *type_system.ObjectType
			if isStatic {
				targetType = classObjType
			} else {
				targetType = objType
			}

			astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
			errors = slices.Concat(errors, keyErrors)
			var methodType *type_system.MethodElem
			if astKey != nil {
				for _, e := range targetType.Elems {
					if methodElem, ok := e.(*type_system.MethodElem); ok {
						if methodElem.Name == *astKey {
							methodType = methodElem
							break
						}
					}
				}
			}

			if methodType == nil {
				continue
			}

			paramBindings := make(map[string]*type_system.Binding)
			if !isStatic {
				// We use the name of the class as the type here to avoid
				// a RecursiveUnificationError.
				// TODO(#574): handle generic classes — we deliberately
				// drop type args and the `'a self` lifetime here
				// because downstream codegen (MemberExpr →
				// .bind(this) heuristic in builder.go) misclassifies
				// stored function fields once self carries its type
				// args. Reusing methodType.Fn.SelfParam.Type would
				// be more correct but requires fixing that
				// classifier first.
				isMutableSelf := type_system.ReceiverIsMut(methodType.Fn)
				var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
				if isMutableSelf {
					t = type_system.NewMutType(nil, t)
				}
				paramBindings["self"] = &type_system.Binding{
					Source:     &ast.NodeProvenance{Node: bodyElem},
					Type:       t,
					Assignable: false,
					Mutable:    isMutableSelf,
				}
			}

			for paramIdx, param := range methodType.Fn.Params {
				if param.Pattern == nil {
					continue
				}
				_, typeIsMut := param.Type.(*type_system.MutType)
				patIsMut := false
				if paramIdx < len(bodyElem.Fn.Params) {
					if astIdent, ok := bodyElem.Fn.Params[paramIdx].Pattern.(*ast.IdentPat); ok {
						patIsMut = astIdent.Mutable
					}
				}
				paramBindings[param.Pattern.String()] = &type_system.Binding{
					Source:     &type_system.TypeProvenance{Type: param.Type},
					Type:       param.Type,
					Assignable: false,
					Mutable:    patIsMut || typeIsMut,
				}
			}

			methodCtx := methodCtxForElem[i]
			bodyErrors := c.inferFuncBodyWithFuncSigType(
				methodCtx, methodType.Fn, paramBindings,
				bodyElem.Fn.Params, bodyElem.Fn.Body,
				asyncModeFrom(bodyElem.Fn.Async), nonConstructorBody,
			)
			errors = slices.Concat(errors, bodyErrors)

		case *ast.GetterElem:
			isStatic := bodyElem.Static
			var targetType *type_system.ObjectType
			if isStatic {
				targetType = classObjType
			} else {
				targetType = objType
			}

			astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
			errors = slices.Concat(errors, keyErrors)
			var getterType *type_system.GetterElem
			if astKey != nil {
				for _, e := range targetType.Elems {
					if getterElem, ok := e.(*type_system.GetterElem); ok {
						if getterElem.Name == *astKey {
							getterType = getterElem
							break
						}
					}
				}
			}

			if getterType == nil {
				continue
			}

			paramBindings := make(map[string]*type_system.Binding)
			if !isStatic {
				// TODO(#574): handle generic classes — see the matching
				// note on the method branch above. Same codegen
				// constraint applies here.
				isMutableSelf := type_system.ReceiverIsMut(getterType.Fn)
				var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
				if isMutableSelf {
					t = type_system.NewMutType(nil, t)
				}
				paramBindings["self"] = &type_system.Binding{
					Source:     &ast.NodeProvenance{Node: bodyElem},
					Type:       t,
					Assignable: false,
					Mutable:    isMutableSelf,
				}
			}

			for _, param := range getterType.Fn.Params {
				if param.Pattern != nil {
					_, isMut := param.Type.(*type_system.MutType)
					paramBindings[param.Pattern.String()] = &type_system.Binding{
						Source:     &type_system.TypeProvenance{Type: param.Type},
						Type:       param.Type,
						Assignable: false,
						Mutable:    isMut,
					}
				}
			}

			if bodyElem.Fn.Body != nil {
				bodyErrors := c.inferFuncBodyWithFuncSigType(
					bodyCtx, getterType.Fn, paramBindings,
					bodyElem.Fn.Params, bodyElem.Fn.Body,
					asyncModeFrom(bodyElem.Fn.Async), nonConstructorBody,
				)
				errors = slices.Concat(errors, bodyErrors)
			}

		case *ast.SetterElem:
			isStatic := bodyElem.Static
			var targetType *type_system.ObjectType
			if isStatic {
				targetType = classObjType
			} else {
				targetType = objType
			}

			astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
			errors = slices.Concat(errors, keyErrors)
			var setterType *type_system.SetterElem
			if astKey != nil {
				for _, e := range targetType.Elems {
					if setterElem, ok := e.(*type_system.SetterElem); ok {
						if setterElem.Name == *astKey {
							setterType = setterElem
							break
						}
					}
				}
			}

			if setterType == nil {
				continue
			}

			paramBindings := make(map[string]*type_system.Binding)
			if !isStatic {
				// TODO(#574): handle generic classes — see the matching
				// note on the method branch above. Same codegen
				// constraint applies here.
				isMutableSelf := type_system.ReceiverIsMut(setterType.Fn)
				var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
				if isMutableSelf {
					t = type_system.NewMutType(nil, t)
				}
				paramBindings["self"] = &type_system.Binding{
					Source:     &ast.NodeProvenance{Node: bodyElem},
					Type:       t,
					Assignable: false,
					Mutable:    isMutableSelf,
				}
			}

			for _, param := range setterType.Fn.Params {
				if param.Pattern != nil {
					_, isMut := param.Type.(*type_system.MutType)
					paramBindings[param.Pattern.String()] = &type_system.Binding{
						Source:     &type_system.TypeProvenance{Type: param.Type},
						Type:       param.Type,
						Assignable: false,
						Mutable:    isMut,
					}
				}
			}

			if bodyElem.Fn.Body != nil {
				bodyErrors := c.inferFuncBodyWithFuncSigType(
					bodyCtx, setterType.Fn, paramBindings,
					bodyElem.Fn.Params, bodyElem.Fn.Body,
					asyncModeFrom(bodyElem.Fn.Async), nonConstructorBody,
				)
				errors = slices.Concat(errors, bodyErrors)
			}

		case *ast.ConstructorElem:
			if len(inBodyCtors) == 0 || bodyElem != inBodyCtors[0] || ctorFuncType == nil {
				continue
			}

			ctorBindings := make(map[string]*type_system.Binding)
			maps.Copy(ctorBindings, ctorParamBindings)

			// `self` inside a constructor is always `mut Self` so the body
			// can assign fields, even when the class's default mutability
			// is non-mut. Reuses classSelfRef built during the placeholder
			// phase (same type args).
			ctorBindings["self"] = &type_system.Binding{
				Source:     &ast.NodeProvenance{Node: bodyElem},
				Type:       type_system.NewMutType(nil, classSelfRef),
				Assignable: false,
				Mutable:    true,
			}

			if bodyElem.Fn.Body == nil {
				continue
			}

			// The body's return type is Void (statements fall off the end);
			// the callable signature keeps Return = Self. Run the body
			// checker against a temporary FuncType to avoid a spurious
			// unification failure, then copy inferred Throws back.
			bodyFuncType := type_system.NewFuncType(
				ctorFuncType.Provenance(),
				ctorFuncType.TypeParams,
				ctorFuncType.Params,
				type_system.NewVoidType(nil),
				ctorFuncType.Throws,
			)

			ctorBodyCtx := bodyCtx
			if ctorCtx.Scope != nil {
				ctorBodyCtx = ctorCtx.WithNewScope()
			}

			bodyErrors := c.inferFuncBodyWithFuncSigType(
				ctorBodyCtx, bodyFuncType, ctorBindings,
				ctorCallableParams(bodyElem), bodyElem.Fn.Body,
				syncFunc, constructorBody,
			)
			errors = slices.Concat(errors, bodyErrors)
			ctorFuncType.Throws = bodyFuncType.Throws

			c.InferConstructorLifetimes(decl, typeAlias, ctorFuncType)

			if decl.Extends == nil {
				required := requiredFieldNames(decl)
				initErrors := c.checkConstructorInit(bodyElem, required)
				errors = slices.Concat(errors, initErrors)
			}
		}
	}

	return errors
}
