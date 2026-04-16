package checker

import (
	"fmt"
	"slices"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

func (c *Checker) inferStmt(ctx Context, stmt ast.Stmt) []Error {
	switch stmt := stmt.(type) {
	case *ast.ExprStmt:
		_, errors := c.inferExpr(ctx, stmt.Expr)
		return errors
	case *ast.DeclStmt:
		return c.inferDecl(ctx, stmt.Decl)
	case *ast.ReturnStmt:
		errors := []Error{}
		if stmt.Expr != nil {
			// The inferred type is ignored here, but inferExpr still attaches
			// the inferred type to the expression.  This is used later on this
			// file, search for `ReturnVisitor` to see how it is used.
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = exprErrors
		}
		return errors
	case *ast.ImportStmt:
		errors := c.inferImport(ctx, stmt)
		return errors
	case *ast.ForInStmt:
		return c.inferForInStmt(ctx, stmt)
	case *ast.ErrorStmt:
		return nil
	default:
		panic(fmt.Sprintf("Unknown statement type: %T", stmt))
	}
}

func (c *Checker) inferDecl(ctx Context, decl ast.Decl) []Error {
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		// Handle incomplete function declarations
		if decl.Name.Name == "" {
			return []Error{}
		}
		return c.inferFuncDecl(ctx, decl)
	case *ast.VarDecl:
		bindings, errors := c.inferVarDecl(ctx, decl)
		maps.Copy(ctx.Scope.Namespace.Values, bindings)
		return errors
	case *ast.TypeDecl:
		typeAlias, errors := c.inferTypeDecl(ctx, decl)
		ctx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
		return errors
	case *ast.InterfaceDecl:
		typeAlias, errors := c.inferInterface(ctx, decl)
		// For interfaces, we directly set the type alias in the namespace
		// because interface merging is handled in inferInterface
		ctx.Scope.Namespace.Types[decl.Name.Name] = typeAlias
		return errors
	case *ast.ClassDecl:
		panic("TODO: infer class declaration")
	case *ast.EnumDecl:
		return c.inferEnumDecl(ctx, decl)
	default:
		panic(fmt.Sprintf("Unknown declaration type: %T", decl))
	}
}

// TODO: refactor this to return the binding map instead of copying them over
// immediately
func (c *Checker) inferVarDecl(
	ctx Context,
	decl *ast.VarDecl,
) (map[string]*type_system.Binding, []Error) {
	errors := []Error{}

	patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
	errors = slices.Concat(errors, patErrors)

	if decl.TypeAnn == nil && decl.Init == nil {
		return nil, errors
	}

	// TODO: infer a structural placeholder based on the expression and then
	// unify it with the pattern type.  Then we can pass in map of the new bindings
	// which will be added to a new scope before inferring function expressions
	// in the expressions.

	if decl.TypeAnn != nil {
		taType, taErrors := c.inferTypeAnn(ctx, decl.TypeAnn)
		errors = slices.Concat(errors, taErrors)

		unifyErrors := c.Unify(ctx, taType, patType)
		errors = slices.Concat(errors, unifyErrors)

		if decl.Init != nil {
			initType, initErrors := c.inferExpr(ctx, decl.Init)
			errors = slices.Concat(errors, initErrors)

			unifyErrors = c.Unify(ctx, initType, taType)
			errors = slices.Concat(errors, unifyErrors)
		}
	} else {
		if decl.Init == nil {
			// TODO: report an error, but set initType to be `unknown`
			panic("Expected either a type annotation or an initializer expression")
		}
		initType, initErrors := c.inferExpr(ctx, decl.Init)
		errors = slices.Concat(errors, initErrors)

		unifyErrors := c.Unify(ctx, initType, patType)
		errors = slices.Concat(errors, unifyErrors)
	}

	return bindings, errors
}

func (c *Checker) inferFuncDecl(ctx Context, decl *ast.FuncDecl) []Error {
	errors := []Error{}

	funcType, _, paramBindings, sigErrors := c.inferFuncSig(ctx, &decl.FuncSig, decl)
	errors = slices.Concat(errors, sigErrors)

	// For declared functions, we don't have a body to infer from
	if decl.Declare() && (decl.Body == nil || len(decl.Body.Stmts) == 0) {
		// For declared async functions, validate that the return type is a Promise
		if decl.FuncSig.Async {
			if promiseType, ok := funcType.Return.(*type_system.TypeRefType); ok &&
				type_system.QualIdentToString(promiseType.Name) == "Promise" {
				// Good, it's a Promise type. Ensure it has the right structure.
				if len(promiseType.TypeArgs) == 1 {
					// Promise<T> should become Promise<T, never>
					promiseAlias := ctx.Scope.GetTypeAlias("Promise")
					if promiseAlias != nil {
						// Update the function type to have Promise<T, never>
						newPromiseType := type_system.NewTypeRefType(
							nil, "Promise", promiseAlias, promiseType.TypeArgs[0], type_system.NewNeverType(nil))
						funcType.Return = newPromiseType
					}
				} else if len(promiseType.TypeArgs) >= 2 {
					// Promise<T, E> is already correct
				} else {
					// Promise with no args, this shouldn't happen but let's handle it
					errors = append(errors, &UnimplementedError{
						message: "Promise type must have at least one type argument",
						span:    decl.Span(),
					})
				}
			} else {
				// Declared async function must return a Promise type
				errors = append(errors, &UnimplementedError{
					message: "Declared async functions must return a Promise type",
					span:    decl.Span(),
				})
			}
		}
	} else if decl.Body != nil {
		// Allocate call-site maps so body inference and resolveCallSites share them.
		callSites := make(map[int][]*type_system.FuncType)
		callSiteTypeVars := make(map[int]*type_system.TypeVarType)
		ctx.CallSites = &callSites
		ctx.CallSiteTypeVars = &callSiteTypeVars

		inferErrors := c.inferFuncBodyWithFuncSigType(
			ctx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)
	}

	// Resolve deferred call sites and generalize type variables into type parameters
	c.resolveCallSites(ctx)
	GeneralizeFuncType(funcType)

	binding := type_system.Binding{
		Source:  &ast.NodeProvenance{Node: decl},
		Type:    funcType,
		Mutable: false,
	}
	ctx.Scope.setValue(decl.Name.Name, &binding)
	return errors
}

// TypeParamsResult contains the result of processing type parameters.
type TypeParamsResult struct {
	// TypeParams in declaration order (critical for correct substitution)
	TypeParams []*type_system.TypeParam
	// Context with all type parameters in scope
	Ctx Context
	// Errors collected during processing
	Errors []Error
}

// buildTypeParams processes AST type parameters and returns them in declaration order,
// along with a context that has all type parameters in scope.
//
// This handles:
// - Topological sorting so constraints can reference earlier params
// - Inferring constraint and default types
// - Registering type aliases for each parameter in scope
// - Optionally registering a "Self" type alias (for interfaces)
func (c *Checker) buildTypeParams(
	ctx Context,
	astTypeParams []*ast.TypeParam,
	selfTypeAlias *type_system.TypeAlias, // optional, pass nil for non-interface types
) TypeParamsResult {
	var errors []Error

	// Sort type parameters topologically so dependencies come first for processing
	sortedTypeParams := ast.SortTypeParamsTopologically(astTypeParams)

	// Map to store inferred type params by name (for lookup by declaration order later)
	typeParamMap := make(map[string]*type_system.TypeParam)

	// Create a context that accumulates type parameters as we process them
	// This allows later type parameters to reference earlier ones in their constraints
	typeCtx := ctx.WithNewScope()

	// Register Self type alias if provided (for interfaces)
	if selfTypeAlias != nil {
		typeCtx.Scope.SetTypeAlias("Self", selfTypeAlias)
	}

	// Process in topologically sorted order (so constraints can reference earlier params)
	for _, typeParam := range sortedTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			var constraintErrors []Error
			constraintType, constraintErrors = c.inferTypeAnn(typeCtx, typeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if typeParam.Default != nil {
			var defaultErrors []Error
			defaultType, defaultErrors = c.inferTypeAnn(typeCtx, typeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
		typeParamMap[typeParam.Name] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}

		// Add this type parameter to the scope for subsequent parameters
		typeParamTypeRef := type_system.NewTypeRefType(nil, typeParam.Name, nil)
		typeParamAlias := &type_system.TypeAlias{
			Type:       typeParamTypeRef,
			TypeParams: []*type_system.TypeParam{},
		}
		typeCtx.Scope.SetTypeAlias(typeParam.Name, typeParamAlias)
	}

	// Build final typeParams in DECLARATION order (not sorted order)
	// This is critical for correct substitution when the type is instantiated
	typeParams := make([]*type_system.TypeParam, len(astTypeParams))
	for i, astParam := range astTypeParams {
		typeParams[i] = typeParamMap[astParam.Name]
	}

	return TypeParamsResult{
		TypeParams: typeParams,
		Ctx:        typeCtx,
		Errors:     errors,
	}
}

func (c *Checker) inferTypeDecl(
	ctx Context,
	decl *ast.TypeDecl,
) (*type_system.TypeAlias, []Error) {
	result := c.buildTypeParams(ctx, decl.TypeParams, nil)
	errors := result.Errors

	var t type_system.Type
	if decl.TypeAnn == nil {
		t = type_system.NewErrorType(nil)
	} else {
		var typeErrors []Error
		t, typeErrors = c.inferTypeAnn(result.Ctx, decl.TypeAnn)
		errors = slices.Concat(errors, typeErrors)
	}

	typeAlias := type_system.TypeAlias{
		Type:       t,
		TypeParams: result.TypeParams,
	}

	return &typeAlias, errors
}

func (c *Checker) inferInterface(
	ctx Context,
	decl *ast.InterfaceDecl,
) (*type_system.TypeAlias, []Error) {
	// Build typeArgs in declaration order for the Self type
	typeArgs := make([]type_system.Type, len(decl.TypeParams))
	for i, typeParam := range decl.TypeParams {
		typeArgs[i] = type_system.NewTypeRefType(nil, typeParam.Name, nil)
	}
	selfType := type_system.NewTypeRefType(nil, decl.Name.Name, nil, typeArgs...)
	selfTypeAlias := type_system.TypeAlias{Type: selfType, TypeParams: []*type_system.TypeParam{}}

	result := c.buildTypeParams(ctx, decl.TypeParams, &selfTypeAlias)
	errors := result.Errors
	typeParams := result.TypeParams
	typeCtx := result.Ctx

	objType, typeErrors := c.inferObjectTypeAnn(typeCtx, decl.TypeAnn)
	errors = slices.Concat(errors, typeErrors)

	// Infer the Extends clause if present
	if decl.Extends != nil {
		var extendsTypes []*type_system.TypeRefType
		for _, extends := range decl.Extends {
			extendsType, extendsErrors := c.inferTypeAnn(typeCtx, extends)
			errors = slices.Concat(errors, extendsErrors)

			if extendsType == nil {
				continue
			}

			// The extends type should be a TypeRefType
			if typeRef, ok := extendsType.(*type_system.TypeRefType); ok {
				extendsTypes = append(extendsTypes, typeRef)
			} else {
				// If it's not a TypeRefType, we still set it but wrap it if needed
				// This handles cases where the type might be pruned or indirect
				prunedType := type_system.Prune(extendsType)
				if typeRef, ok := prunedType.(*type_system.TypeRefType); ok {
					extendsTypes = append(extendsTypes, typeRef)
				}
			}
		}
		objType.Extends = extendsTypes
	}

	objType.Interface = true
	objType.Nominal = true

	// Check if an interface with this name already exists in the CURRENT namespace only.
	// We don't use GetTypeAlias here because it searches up the scope chain,
	// which would incorrectly try to merge package-level declarations with global ones.
	existingAlias := ctx.Scope.Namespace.Types[decl.Name.Name]
	if existingAlias != nil {
		// Validate that type parameters match
		validateErrors := c.validateTypeParams(
			ctx,
			existingAlias.TypeParams,
			typeParams,
			decl.Name.Name,
			decl.Name.Span(),
		)
		errors = slices.Concat(errors, validateErrors)

		// If it exists, merge the elements
		if existingObjType, ok := type_system.Prune(existingAlias.Type).(*type_system.ObjectType); ok &&
			existingObjType.Interface {
			// Validate that duplicate properties have compatible types
			mergeErrors := c.validateInterfaceMerge(ctx, existingObjType, objType, decl)
			errors = slices.Concat(errors, mergeErrors)

			// Merge the elements from the new interface into the existing one
			mergedElems := append(existingObjType.Elems, objType.Elems...)
			objType.Elems = mergedElems
			objType.ID = existingObjType.ID
			// Preserve other flags
			objType.Exact = existingObjType.Exact
			objType.Immutable = existingObjType.Immutable
			objType.Mutable = existingObjType.Mutable
			objType.Nominal = existingObjType.Nominal
			objType.Interface = true // Ensure interface flag is set
		}
	}

	typeAlias := type_system.TypeAlias{
		Type:       objType,
		TypeParams: typeParams,
	}

	return &typeAlias, errors
}

func (c *Checker) inferForInStmt(ctx Context, stmt *ast.ForInStmt) []Error {
	errors := []Error{}

	// Validate async context for 'for await'
	if stmt.IsAwait && !ctx.IsAsync {
		errors = append(errors, &UnimplementedError{
			message: "'for await' is only allowed in async functions",
			span:    stmt.Span(),
		})
	}

	// Infer the type of the iterable expression
	iterableType, errs := c.inferExpr(ctx, stmt.Iterable)
	errors = slices.Concat(errors, errs)

	// Extract element type from Iterable<T> or AsyncIterable<T>
	var elementType type_system.Type
	if stmt.IsAwait {
		// for await...in can iterate over both async and sync iterables.
		// Try async iterable first, then fall back to sync iterable.
		elementType = c.GetAsyncIterableElementType(ctx, iterableType)
		if elementType == nil {
			elementType = c.GetIterableElementType(ctx, iterableType)
		}
	} else {
		elementType = c.GetIterableElementType(ctx, iterableType)
	}

	if elementType == nil {
		errors = append(errors, &UnimplementedError{
			message: fmt.Sprintf("Type '%s' is not iterable", iterableType),
			span:    stmt.Iterable.Span(),
		})
		elementType = type_system.NewNeverType(nil)
	}

	// Create new scope for loop body
	loopCtx := ctx.WithNewScope()

	// Infer pattern and unify with element type
	patType, bindings, patErrors := c.inferPattern(ctx, stmt.Pattern)
	errors = slices.Concat(errors, patErrors)

	unifyErrors := c.Unify(ctx, elementType, patType)
	errors = slices.Concat(errors, unifyErrors)

	// Add bindings to loop scope (loop variables are immutable like `val`)
	for name, binding := range bindings {
		binding.Mutable = false
		loopCtx.Scope.setValue(name, binding)
	}

	// Infer body statements
	for _, bodyStmt := range stmt.Body.Stmts {
		errs := c.inferStmt(loopCtx, bodyStmt)
		errors = slices.Concat(errors, errs)
	}

	return errors
}

func (c *Checker) inferEnumDecl(ctx Context, decl *ast.EnumDecl) []Error {
	errors := []Error{}

	// Create a namespace for the enum. Each variant's type and constructor
	// value will be registered in this namespace (e.g. Option.Some, Option.None).
	ns := type_system.NewNamespace()
	ctx.Scope.setNamespace(decl.Name.Name, ns)

	// Infer type parameters (e.g. the T in `enum Option<T>`) and build type
	// args that reference them for use in constructor return types and variant
	// type refs below.
	result := c.buildTypeParams(ctx, decl.TypeParams, nil)
	errors = slices.Concat(errors, result.Errors)
	typeParams := result.TypeParams
	declCtx := result.Ctx

	typeArgs := make([]type_system.Type, len(typeParams))
	for i := range typeParams {
		typeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
	}

	// Forward-declare the enum's TypeAlias with a placeholder before processing
	// variants. This ensures constructor return types can reference the TypeAlias
	// directly, eliminating the need for fallback resolution in downstream consumers.
	enumPlaceholder := c.FreshVar(&ast.NodeProvenance{Node: decl})
	typeAlias := &type_system.TypeAlias{
		Type:       enumPlaceholder,
		TypeParams: typeParams,
		Exported:   decl.Export(),
	}
	ctx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)

	variantTypes := make([]type_system.Type, len(decl.Elems))

	for i, elem := range decl.Elems {
		switch elem := elem.(type) {
		case *ast.EnumVariant:
			// Each variant gets its own nominal object type so that variants
			// are distinguishable from one another in the type system.
			instanceType := type_system.NewNominalObjectType(
				&ast.NodeProvenance{Node: elem}, []type_system.ObjTypeElem{})
			instanceTypeAlias := &type_system.TypeAlias{
				Type:       instanceType,
				TypeParams: typeParams,
				Exported:   decl.Export(),
			}
			ns.Types[elem.Name.Name] = instanceTypeAlias

			params, _, paramErrors := c.inferFuncParams(declCtx, elem.Params)
			errors = slices.Concat(errors, paramErrors)

			// Build the constructor function type. The return type references
			// the parent enum with appropriate type args (e.g. Some(value: T) -> Option<T>).
			funcType := type_system.NewFuncType(
				&ast.NodeProvenance{Node: elem},
				typeParams,
				params,
				type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
				type_system.NewNeverType(nil),
			)
			constructorElem := &type_system.ConstructorElem{Fn: funcType}

			classObjTypeElems := []type_system.ObjTypeElem{constructorElem}

			// Build the [Symbol.customMatcher] method to enable destructuring
			// in match expressions. The method signature is:
			//   [Symbol.customMatcher](subject: Enum.Variant<T>) -> [ParamTypes...]
			// e.g. for Some(value: T):
			//   [Symbol.customMatcher](subject: Option.Some<T>) -> [T]
			symbol := ctx.Scope.GetValue("Symbol")
			if symbol == nil {
				panic("Symbol binding not found in scope")
			}
			symKey := PropertyKey{
				Name:     "customMatcher",
				OptChain: false,
				span:     DEFAULT_SPAN,
			}
			// Symbol.customMatcher is a well-known global that is always present
			// in the prelude, so the error can safely be ignored here.
			customMatcher, _ := c.getMemberType(ctx, symbol.Type, symKey, AccessRead)

			symbolKeyMap := make(map[int]any)

			switch customMatcher := type_system.Prune(customMatcher).(type) {
			case *type_system.UniqueSymbolType:
				self := false
				subjectPat := &type_system.IdentPat{Name: "subject"}
				subjectType := type_system.NewTypeRefType(
					nil, elem.Name.Name, instanceTypeAlias, typeArgs...)
				paramTypes := make([]type_system.Type, len(elem.Params))
				for j, param := range elem.Params {
					// Enum variant param types are validated during the earlier
					// type annotation inference pass, so errors can be ignored.
					t, _ := c.inferTypeAnn(declCtx, param.TypeAnn)
					paramTypes[j] = t
				}
				returnType := type_system.NewTupleType(nil, paramTypes...)

				// e.g. for `enum Option<T> { Some(value: T), None }`, the Some
				// variant produces: [Symbol.customMatcher](subject: Option.Some<T>) -> [T]
				methodElem := &type_system.MethodElem{
					Name: type_system.ObjTypeKey{
						Kind: type_system.SymObjTypeKeyKind,
						Sym:  customMatcher.Value,
					},
					Fn: type_system.NewFuncType(
						nil,
						typeParams,
						[]*type_system.FuncParam{{
							Pattern: subjectPat,
							Type:    subjectType,
						}},
						returnType,
						type_system.NewNeverType(nil),
					),
					MutSelf: &self,
				}
				classObjTypeElems = append(classObjTypeElems, methodElem)

				symbolMemberExpr := ast.NewMember(
					ast.NewIdent("Symbol", DEFAULT_SPAN),
					ast.NewIdentifier("customMatcher", DEFAULT_SPAN),
					false,
					DEFAULT_SPAN,
				)
				symbolKeyMap[customMatcher.Value] = symbolMemberExpr
			default:
				// This is an internal invariant: Symbol.customMatcher must always
				// resolve to a UniqueSymbolType from the prelude. If it doesn't,
				// something is fundamentally wrong and panicking is appropriate.
				panic("Symbol.customMatcher is not a unique symbol")
			}

			// Combine the constructor and customMatcher method into a single
			// object type. This is what you get when referencing a variant in
			// expression context (e.g. Option.Some) — it's both callable
			// (constructor) and matchable (customMatcher).
			provenance := &ast.NodeProvenance{Node: elem}
			classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
			classObjType.SymbolKeyMap = symbolKeyMap

			ctor := &type_system.Binding{
				Source:   provenance,
				Type:     classObjType,
				Mutable:  false,
				Exported: decl.Export(),
			}

			ns.Values[elem.Name.Name] = ctor

			// Record a TypeRefType with a qualified name (e.g. Option.Some)
			// for use in the union type below.
			variantName := &type_system.Member{
				Left:  type_system.NewIdent(decl.Name.Name),
				Right: type_system.NewIdent(elem.Name.Name),
			}

			variantTypes[i] = &type_system.TypeRefType{
				Name:      variantName,
				TypeArgs:  typeArgs,
				TypeAlias: instanceTypeAlias,
			}
		case *ast.EnumSpread:
			panic("TODO: infer enum spreads")
		}
	}

	// Build the union type for the enum itself and unify it with the
	// placeholder that was forward-declared before processing variants.
	enumUnionType := type_system.NewUnionType(
		&ast.NodeProvenance{Node: decl}, variantTypes...)

	unifyErrors := c.Unify(ctx, enumPlaceholder, enumUnionType)
	errors = slices.Concat(errors, unifyErrors)

	return errors
}
