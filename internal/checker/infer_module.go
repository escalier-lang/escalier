package checker

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// A module can contain declarations from mutliple source files.
// The order of the declarations doesn't matter because we compute the dependency
// graph and codegen will ensure that the declarations are emitted in the correct
// order.
func (c *Checker) InferModule(ctx Context, m *ast.Module) (*type_system.Namespace, []Error) {
	depGraph := dep_graph.BuildDepGraph(m)
	return c.InferDepGraph(ctx, depGraph)
}

func (c *Checker) InferDepGraph(ctx Context, depGraph *dep_graph.DepGraph) (*type_system.Namespace, []Error) {
	components := depGraph.FindStronglyConnectedComponents(0)

	// Define a module scope so that declarations don't leak into the global scope
	// TODO: Move this call before the call to InferDepGraph
	// ctx = ctx.WithNewScope()

	var errors []Error
	for _, component := range components {
		declsErrors := c.InferComponent(ctx, depGraph, component)
		errors = slices.Concat(errors, declsErrors)
	}

	return ctx.Scope.Namespace, errors
}

// GetNamespaceCtx returns a new Context with the namespace set to the namespace of
// the declaration with the given declID. If the namespace doesn't exist yet, it
// creates one.
func GetNamespaceCtx(
	ctx Context,
	depGraph *dep_graph.DepGraph,
	declID dep_graph.DeclID,
) Context {
	nsName, _ := depGraph.GetDeclNamespace(declID)
	if nsName == "" {
		return ctx
	}
	ns := ctx.Scope.Namespace
	nsCtx := ctx
	for part := range strings.SplitSeq(nsName, ".") {
		if _, ok := ns.Namespaces[part]; !ok {
			ns.Namespaces[part] = type_system.NewNamespace()
		}
		ns = ns.Namespaces[part]
		nsCtx = nsCtx.WithNewScopeAndNamespace(ns)
	}
	return nsCtx
}

// inferTypeParams infers type parameters from AST type parameters by creating
// fresh type variables for constraints and defaults.
//
// This helper is intended for module-level type declarations such as TypeDecl,
// ClassDecl, EnumDecl, and InterfaceDecl. It only mirrors the AST type parameter
// list into a corresponding slice of *type_system.TypeParam by allocating fresh
// type variables for any constraint and default types.
//
// Note that this function:
//   - does NOT add the inferred type parameters to any scope,
//   - does NOT perform any constraint checking or error reporting, and
//   - is NOT a replacement for inferFuncTypeParams, which is responsible for
//     function-level generic parameter handling and associated diagnostics.
func (c *Checker) inferTypeParams(astTypeParams []*ast.TypeParam) []*type_system.TypeParam {
	typeParams := make([]*type_system.TypeParam, len(astTypeParams))
	for i, typeParam := range astTypeParams {
		var constraintType type_system.Type
		var defaultType type_system.Type
		if typeParam.Constraint != nil {
			constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
		}
		if typeParam.Default != nil {
			defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
		}
		typeParams[i] = &type_system.TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
	}
	return typeParams
}

// unifyTypeParams unifies the placeholder type parameters (with FreshVar constraints/defaults)
// with the fully inferred type parameters (with resolved constraint/default types).
func (c *Checker) unifyTypeParams(
	ctx Context,
	existingTypeParams []*type_system.TypeParam,
	inferredTypeParams []*type_system.TypeParam,
) []Error {
	errors := []Error{}

	for i, existingTypeParam := range existingTypeParams {
		if i >= len(inferredTypeParams) {
			break // Safety check in case of mismatched lengths
		}
		inferredTypeParam := inferredTypeParams[i]

		if existingTypeParam.Constraint != nil && inferredTypeParam.Constraint != nil {
			constraintErrors := c.Unify(ctx, existingTypeParam.Constraint, inferredTypeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if existingTypeParam.Default != nil && inferredTypeParam.Default != nil {
			defaultErrors := c.Unify(ctx, existingTypeParam.Default, inferredTypeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
	}

	return errors
}

func (c *Checker) InferComponent(
	ctx Context,
	depGraph *dep_graph.DepGraph,
	component []dep_graph.DeclID,
) []Error {
	errors := []Error{}

	// TODO:
	// - ensure there are no duplicate declarations in the module

	// inferFuncSig returns bindings for all of the parameters.  We store these
	// so that they can be used later when inferring the function body.
	paramBindingsForDecl := make(map[dep_graph.DeclID]map[string]*type_system.Binding)

	declCtxMap := make(map[dep_graph.DeclID]Context)
	declMethodCtxs := make([][]Context, len(component))

	// Infer placeholders
	for i, declID := range component {
		// TODO: rename this to nsCtx instead of nsCtx
		nsCtx := GetNamespaceCtx(ctx, depGraph, declID)
		decl, _ := depGraph.GetDecl(declID)

		switch decl := decl.(type) {
		case *ast.FuncDecl:
			funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(nsCtx, &decl.FuncSig, decl)
			paramBindingsForDecl[declID] = paramBindings
			errors = slices.Concat(errors, sigErrors)

			// Save the context for inferring the function body later
			declCtxMap[declID] = funcCtx

			nsCtx.Scope.setValue(decl.Name.Name, &type_system.Binding{
				Source:  &ast.NodeProvenance{Node: decl},
				Type:    funcType,
				Mutable: false,
			})
		case *ast.VarDecl:
			patType, bindings, patErrors := c.inferPattern(ctx, decl.Pattern)
			errors = slices.Concat(errors, patErrors)

			// TODO: handle the situation where both decl.Init and decl.TypeAnn
			// are nil

			if decl.TypeAnn != nil {
				taType, taErrors := c.inferTypeAnn(nsCtx, decl.TypeAnn)
				errors = slices.Concat(errors, taErrors)

				unifyErrors := c.Unify(nsCtx, patType, taType)
				errors = slices.Concat(errors, unifyErrors)
			}

			for name, binding := range bindings {
				nsCtx.Scope.setValue(name, binding)
			}

			// This is used when inferring the definitions below
			decl.InferredType = patType
		case *ast.TypeDecl:
			// TODO: add new type aliases to ctx.Scope.Types as we go to handle
			// things like:
			// type Point = {x: number, y: number}
			// val p: Point = {x: 1, y: 2}
			typeParams := c.inferTypeParams(decl.TypeParams)

			typeAlias := &type_system.TypeAlias{
				Type:       c.FreshVar(&ast.NodeProvenance{Node: decl}),
				TypeParams: typeParams,
			}

			nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
		case *ast.ClassDecl:
			instanceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

			typeParams := c.inferTypeParams(decl.TypeParams)

			typeAlias := &type_system.TypeAlias{
				Type:       instanceType,
				TypeParams: typeParams,
			}

			nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)
			declCtx := nsCtx.WithNewScope()
			declCtxMap[declID] = declCtx

			for _, typeParam := range typeParams {
				var t type_system.Type = type_system.NewUnknownType(nil)
				if typeParam.Constraint != nil {
					t = typeParam.Constraint
				}
				declCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
					Type:       t,
					TypeParams: []*type_system.TypeParam{},
				})
			}

			objTypeElems := []type_system.ObjTypeElem{}
			staticElems := []type_system.ObjTypeElem{}
			methodCtxs := make([]Context, len(decl.Body))
			instanceSymbolKeyMap := make(map[int]any)
			staticSymbolKeyMap := make(map[int]any)

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
						// Static fields go to the class object type
						tvar := c.FreshVar(nil)
						tvar.FromBinding = true
						propElem := type_system.NewPropertyElem(*key, tvar)
						propElem.Readonly = elem.Readonly
						staticElems = append(
							staticElems,
							propElem,
						)
					} else {
						// Instance fields go to the instance type
						tvar := c.FreshVar(nil)
						tvar.FromBinding = true
						propElem := type_system.NewPropertyElem(*key, tvar)
						propElem.Readonly = elem.Readonly
						objTypeElems = append(
							objTypeElems,
							propElem,
						)
					}
				case *ast.MethodElem:
					key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					methodType, methodCtx, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
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

					methodCtxs[i] = methodCtx
					if elem.Static {
						// Static methods go to the class object type
						staticElems = append(
							staticElems,
							type_system.NewMethodElem(*key, methodType, nil), // static methods don't have self
						)
					} else {
						// Instance methods go to the instance type
						objTypeElems = append(
							objTypeElems,
							type_system.NewMethodElem(*key, methodType, elem.MutSelf),
						)
					}
				case *ast.GetterElem:
					key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					funcType, _, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
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
						// Static getters go to the class object type
						staticElems = append(
							staticElems,
							type_system.NewGetterElem(*key, funcType),
						)
					} else {
						// Instance getters go to the instance type
						objTypeElems = append(
							objTypeElems,
							type_system.NewGetterElem(*key, funcType),
						)
					}
				case *ast.SetterElem:
					key, keyErrors := c.astKeyToTypeKey(declCtx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					funcType, _, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig, elem.Fn)
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
						// Static setters go to the class object type
						staticElems = append(
							staticElems,
							type_system.NewSetterElem(*key, funcType),
						)
					} else {
						// Instance setters go to the instance type
						objTypeElems = append(
							objTypeElems,
							type_system.NewSetterElem(*key, funcType),
						)
					}
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

			// TODO: call c.bind() directly
			unifyErrors := c.Unify(ctx, instanceType, objType)
			errors = slices.Concat(errors, unifyErrors)

			params, paramBindings, paramErrors := c.inferFuncParams(declCtx, decl.Params)
			errors = slices.Concat(errors, paramErrors)
			paramBindingsForDecl[declID] = paramBindings

			typeArgs := make([]type_system.Type, len(typeParams))
			for i := range typeParams {
				typeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
			}

			retType := &type_system.MutabilityType{
				Type:       type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
				Mutability: type_system.MutabilityUncertain,
			}

			funcType := type_system.NewFuncType(
				provenance,
				typeParams,
				params,
				retType,
				type_system.NewNeverType(nil), // throws type
			)

			// Create an object type with a constructor element and static methods/properties
			constructorElem := &type_system.ConstructorElem{Fn: funcType}
			classObjTypeElems := []type_system.ObjTypeElem{constructorElem}
			classObjTypeElems = append(classObjTypeElems, staticElems...)

			classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
			classObjType.SymbolKeyMap = staticSymbolKeyMap

			ctor := &type_system.Binding{
				Source:  &ast.NodeProvenance{Node: decl},
				Type:    classObjType,
				Mutable: false,
			}
			nsCtx.Scope.setValue(decl.Name.Name, ctor)
			declMethodCtxs[i] = methodCtxs
		case *ast.EnumDecl:
			// Create a new namespace for the enum
			ns := type_system.NewNamespace()
			nsCtx.Scope.setNamespace(decl.Name.Name, ns)

			// Create a fresh type variable for the enum
			enumType := c.FreshVar(&ast.NodeProvenance{Node: decl})

			// Infer type parameters
			typeParams := c.inferTypeParams(decl.TypeParams)

			// Create a type alias with placeholder type
			typeAlias := &type_system.TypeAlias{
				Type:       enumType,
				TypeParams: typeParams,
			}

			nsCtx.Scope.SetTypeAlias(decl.Name.Name, typeAlias)

			// Create a context for inferring enum variants
			declCtx := nsCtx.WithNewScope()
			declCtxMap[declID] = declCtx

			// Add each type param as a type alias in the declCtx so that
			// they can be referenced when inferring the enum variants
			for _, typeParam := range typeParams {
				var t type_system.Type = type_system.NewUnknownType(nil)
				if typeParam.Constraint != nil {
					t = typeParam.Constraint
				}
				declCtx.Scope.SetTypeAlias(typeParam.Name, &type_system.TypeAlias{
					Type:       t,
					TypeParams: []*type_system.TypeParam{},
				})
			}
		case *ast.InterfaceDecl:
			// Similar to TypeDecl, but we need to handle interface merging
			typeParams := c.inferTypeParams(decl.TypeParams)

			// Check if an interface with this name already exists
			existingAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			if existingAlias == nil {
				// First declaration - create a fresh type variable for the interface
				interfaceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

				typeAlias := &type_system.TypeAlias{
					Type:       interfaceType,
					TypeParams: typeParams,
				}

				// Directly set in the namespace to allow interface merging
				nsCtx.Scope.Namespace.Types[decl.Name.Name] = typeAlias
			}
			// If it already exists, we'll merge during the definition phase
			// Type parameter validation happens in inferInterface
		}
	}

	// Infer definitions
	for i, declID := range component {
		nsCtx := GetNamespaceCtx(ctx, depGraph, declID)
		decl, _ := depGraph.GetDecl(declID)

		if decl == nil {
			continue
		}

		// Skip declarations that use the `declare` keyword, since they are
		// already fully typed and don't have a body or initializer to infer.
		if decl.Declare() {
			continue
		}

		switch decl := decl.(type) {
		case *ast.FuncDecl:
			// We reuse the binding that was previous created for the function
			// declaration, so that we can unify the signature with the body's
			// inferred type.
			funcBinding := nsCtx.Scope.GetValue(decl.Name.Name)
			paramBindings := paramBindingsForDecl[declID]
			funcType := funcBinding.Type.(*type_system.FuncType)

			declCtx := declCtxMap[declID]

			if decl.Body != nil {
				inferErrors := c.inferFuncBodyWithFuncSigType(
					declCtx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
				errors = slices.Concat(errors, inferErrors)
			}

		case *ast.VarDecl:
			// TODO: if there's a type annotation, unify the initializer with it
			if decl.Init != nil {
				initType, initErrors := c.inferExpr(nsCtx, decl.Init)
				errors = slices.Concat(errors, initErrors)
				if decl.TypeAnn != nil {
					taType := decl.TypeAnn.InferredType()
					unifyErrors := c.Unify(ctx, initType, taType)
					errors = slices.Concat(errors, unifyErrors)
				} else {
					patType := decl.InferredType
					unifyErrors := c.Unify(ctx, initType, patType)
					errors = slices.Concat(errors, unifyErrors)
				}
			}
		case *ast.TypeDecl:
			typeAlias, declErrors := c.inferTypeDecl(nsCtx, decl)
			errors = slices.Concat(errors, declErrors)

			// Unified the type alias' inferred type with its placeholder type
			existingTypeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			unifyErrors := c.Unify(nsCtx, existingTypeAlias.Type, typeAlias.Type)
			errors = slices.Concat(errors, unifyErrors)

			// Unify the type parameters
			typeParamErrors := c.unifyTypeParams(nsCtx, existingTypeAlias.TypeParams, typeAlias.TypeParams)
			errors = slices.Concat(errors, typeParamErrors)
		case *ast.InterfaceDecl:
			interfaceAlias, declErrors := c.inferInterface(nsCtx, decl)
			errors = slices.Concat(errors, declErrors)

			// Get the existing type alias (which might be a fresh var or a previous interface)
			existingTypeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			prunedType := type_system.Prune(existingTypeAlias.Type)

			// Check if the pruned type is already an ObjectType (from a previous interface)
			if existingObjType, ok := prunedType.(*type_system.ObjectType); ok && existingObjType.Interface {
				// Merge with existing interface
				if newObjType, ok := interfaceAlias.Type.(*type_system.ObjectType); ok {
					// Note: validation is done in inferInterface, no need to duplicate it here
					// Merge the elements
					existingObjType.Elems = append(existingObjType.Elems, newObjType.Elems...)
					// Keep the Interface flag true
					existingObjType.Interface = true
					// The merged type is already in the scope via the binding, no need to update
					continue
				}
			}

			// First interface declaration or unification needed
			unifyErrors := c.Unify(nsCtx, existingTypeAlias.Type, interfaceAlias.Type)
			errors = slices.Concat(errors, unifyErrors)

			// Unify the type parameters
			typeParamErrors := c.unifyTypeParams(nsCtx, existingTypeAlias.TypeParams, interfaceAlias.TypeParams)
			errors = slices.Concat(errors, typeParamErrors)
		case *ast.EnumDecl:
			// Get the namespace and type alias created in the placeholder phase
			ns := nsCtx.Scope.getNamespace(decl.Name.Name)
			typeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			typeParams := typeAlias.TypeParams
			declCtx := declCtxMap[declID]

			typeArgs := make([]type_system.Type, len(typeParams))
			for i := range typeParams {
				typeArgs[i] = type_system.NewTypeRefType(nil, typeParams[i].Name, nil)
			}

			variantTypes := make([]type_system.Type, len(decl.Elems))

			for i, elem := range decl.Elems {
				switch elem := elem.(type) {
				case *ast.EnumVariant:
					instanceType := type_system.NewNominalObjectType(
						&ast.NodeProvenance{Node: elem}, []type_system.ObjTypeElem{})
					instanceTypeAlias := &type_system.TypeAlias{
						Type:       instanceType,
						TypeParams: typeParams,
					}
					ns.Types[elem.Name.Name] = instanceTypeAlias

					params, _, paramErrors := c.inferFuncParams(declCtx, elem.Params)
					errors = slices.Concat(errors, paramErrors)

					// Build the constructor function type
					// If the enum has type parameters, the constructor should be generic
					funcType := type_system.NewFuncType(
						&ast.NodeProvenance{Node: elem},
						typeParams,
						params,
						type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
						type_system.NewNeverType(nil),
					)
					constructorElem := &type_system.ConstructorElem{Fn: funcType}

					classObjTypeElems := []type_system.ObjTypeElem{constructorElem}

					// Build [Symbol.customMatcher](subject: C) -> [T] method
					symbol := ctx.Scope.GetValue("Symbol")
					key := PropertyKey{
						Name:     "customMatcher",
						OptChain: false,
						span:     DEFAULT_SPAN,
					}
					customMatcher, _ := c.getMemberType(ctx, symbol.Type, key)

					// Create the SymbolKeyMap for the object type
					symbolKeyMap := make(map[int]any)

					switch customMatcher := type_system.Prune(customMatcher).(type) {
					case *type_system.UniqueSymbolType:
						self := false
						subjectPat := &type_system.IdentPat{Name: "subject"}
						// The subject type should include type arguments if the enum is generic
						subjectType := type_system.NewTypeRefType(
							nil, elem.Name.Name, instanceTypeAlias, typeArgs...)
						paramTypes := make([]type_system.Type, len(elem.Params))
						for i, param := range elem.Params {
							t, _ := c.inferTypeAnn(declCtx, param.TypeAnn)
							paramTypes[i] = t
						}
						returnType := type_system.NewTupleType(nil, paramTypes...)

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

						// Store the Symbol.customMatcher expression in the SymbolKeyMap
						symbolMemberExpr := ast.NewMember(
							ast.NewIdent("Symbol", DEFAULT_SPAN),
							ast.NewIdentifier("customMatcher", DEFAULT_SPAN),
							false,
							DEFAULT_SPAN,
						)
						symbolKeyMap[customMatcher.Value] = symbolMemberExpr
					default:
						panic("Symbol.customMatcher is not a unique symbol")
					}

					provenance := &ast.NodeProvenance{Node: elem}
					classObjType := type_system.NewObjectType(provenance, classObjTypeElems)
					classObjType.SymbolKeyMap = symbolKeyMap

					ctor := &type_system.Binding{
						Source:  provenance,
						Type:    classObjType,
						Mutable: false,
					}

					ns.Values[elem.Name.Name] = ctor

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

			// Build the union type and unify with the placeholder
			enumUnionType := type_system.NewUnionType(
				&ast.NodeProvenance{Node: decl}, variantTypes...)

			unifyErrors := c.Unify(nsCtx, typeAlias.Type, enumUnionType)
			errors = slices.Concat(errors, unifyErrors)
		case *ast.ClassDecl:
			methodCtxs := declMethodCtxs[i]
			typeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			instanceType := type_system.Prune(typeAlias.Type).(*type_system.ObjectType)

			// Get the class binding to access static methods
			classBinding := nsCtx.Scope.GetValue(decl.Name.Name)
			classType := classBinding.Type.(*type_system.ObjectType)

			// We reuse the binding that was previous created for the function
			// declaration, so that we can unify the signature with the body's
			// inferred type.
			paramBindings := paramBindingsForDecl[declID]

			declCtx := declCtxMap[declID]
			bodyCtx := declCtx.WithNewScope()

			for name, binding := range paramBindings {
				bodyCtx.Scope.setValue(name, binding)
			}

			// Process each element in the class body
			for i, bodyElem := range decl.Body {
				switch bodyElem := bodyElem.(type) {
				case *ast.FieldElem:
					var prop *type_system.PropertyElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding property in either instance or class type
					var targetType *type_system.ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if propElem, ok := elem.(*type_system.PropertyElem); ok {
								if propElem.Name == *astKey {
									prop = propElem
									break
								}
							}
						}
					}

					if prop != nil {
						if bodyElem.Type != nil {
							// TODO: handle type annotations
						} else {
							if bodyElem.Value != nil {
								initType, initErrors := c.inferExpr(bodyCtx, bodyElem.Value)
								errors = slices.Concat(errors, initErrors)

								unifyErrors := c.Unify(ctx, prop.Value, initType)
								errors = slices.Concat(errors, unifyErrors)
							} else {
								var binding *type_system.Binding
								switch name := bodyElem.Name.(type) {
								case *ast.IdentExpr:
									binding = bodyCtx.Scope.GetValue(name.Name)
								case *ast.StrLit:
									binding = bodyCtx.Scope.GetValue(name.Value)
								case *ast.NumLit:
									binding = bodyCtx.Scope.GetValue(strconv.FormatFloat(name.Value, 'f', -1, 64))
								case *ast.ComputedKey:
									panic("computed keys are not supported in shorthand field declarations")
								}

								unifyErrors := c.Unify(ctx, prop.Value, binding.Type)
								errors = slices.Concat(errors, unifyErrors)
							}
						}
					}

				case *ast.MethodElem:
					var methodType *type_system.MethodElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding method in either instance or class type
					var targetType *type_system.ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if methodElem, ok := elem.(*type_system.MethodElem); ok {
								if methodElem.Name == *astKey {
									methodType = methodElem
									break
								}
							}
						}
					}

					if methodType != nil {
						paramBindings := make(map[string]*type_system.Binding)

						// For instance methods, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
							if methodType.MutSelf != nil && *methodType.MutSelf {
								t = type_system.NewMutableType(nil, t)
							}

							paramBindings["self"] = &type_system.Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: methodType.MutSelf != nil && *methodType.MutSelf,
							}
						}

						// For static methods, no 'self' parameter is added

						for _, param := range methodType.Fn.Params {
							paramBindings[param.Pattern.String()] = &type_system.Binding{
								Source:  &type_system.TypeProvenance{Type: param.Type},
								Type:    param.Type,
								Mutable: false,
							}
						}

						methodCtx := methodCtxs[i]
						bodyErrors := c.inferFuncBodyWithFuncSigType(methodCtx, methodType.Fn, paramBindings, bodyElem.Fn.Body, false)
						errors = slices.Concat(errors, bodyErrors)
					}

				case *ast.GetterElem:
					var getterType *type_system.GetterElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding getter in either instance or class type
					var targetType *type_system.ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if getterElem, ok := elem.(*type_system.GetterElem); ok {
								if getterElem.Name == *astKey {
									getterType = getterElem
									break
								}
							}
						}
					}

					if getterType != nil {
						paramBindings := make(map[string]*type_system.Binding)

						// For instance getters, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)

							paramBindings["self"] = &type_system.Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: false, // getters don't mutate self
							}
						}

						// For static getters, no 'self' parameter is added

						// Add any explicit parameters from the getter function signature
						for _, param := range getterType.Fn.Params {
							paramBindings[param.Pattern.String()] = &type_system.Binding{
								Source:  &type_system.TypeProvenance{Type: param.Type},
								Type:    param.Type,
								Mutable: false,
							}
						}

						if bodyElem.Fn.Body != nil {
							bodyErrors := c.inferFuncBodyWithFuncSigType(bodyCtx, getterType.Fn, paramBindings, bodyElem.Fn.Body, false)
							errors = slices.Concat(errors, bodyErrors)
						}
					}

				case *ast.SetterElem:
					var setterType *type_system.SetterElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding setter in either instance or class type
					var targetType *type_system.ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if setterElem, ok := elem.(*type_system.SetterElem); ok {
								if setterElem.Name == *astKey {
									setterType = setterElem
									break
								}
							}
						}
					}

					if setterType != nil {
						paramBindings := make(map[string]*type_system.Binding)

						// For instance setters, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t type_system.Type = type_system.NewTypeRefType(nil, decl.Name.Name, typeAlias)
							// Setters typically need mutable self to modify the instance
							t = type_system.NewMutableType(nil, t)

							paramBindings["self"] = &type_system.Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: true, // setters may mutate self
							}
						}

						// For static setters, no 'self' parameter is added

						// Add any explicit parameters from the setter function signature
						for _, param := range setterType.Fn.Params {
							paramBindings[param.Pattern.String()] = &type_system.Binding{
								Source:  &type_system.TypeProvenance{Type: param.Type},
								Type:    param.Type,
								Mutable: false,
							}
						}

						if bodyElem.Fn.Body != nil {
							bodyErrors := c.inferFuncBodyWithFuncSigType(bodyCtx, setterType.Fn, paramBindings, bodyElem.Fn.Body, false)
							errors = slices.Concat(errors, bodyErrors)
						}
					}
				}
			}
		}
	}

	return errors
}

// validateInterfaceMerge checks that when merging interface declarations,
// properties with the same name have compatible (identical) types as required by TypeScript.
func (c *Checker) validateInterfaceMerge(
	ctx Context,
	existingInterface *type_system.ObjectType,
	newInterface *type_system.ObjectType,
	decl *ast.InterfaceDecl,
) []Error {
	errors := []Error{}

	// Build a map of property names to their types from the existing interface
	existingProps := make(map[type_system.ObjTypeKey]type_system.Type)
	for _, elem := range existingInterface.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			existingProps[elem.Name] = elem.Value
		case *type_system.MethodElem:
			existingProps[elem.Name] = elem.Fn
		case *type_system.GetterElem:
			existingProps[elem.Name] = elem.Fn.Return
		case *type_system.SetterElem:
			existingProps[elem.Name] = elem.Fn.Params[0].Type
		}
	}

	// Check each property in the new interface against the existing interface
	for _, elem := range newInterface.Elems {
		var name type_system.ObjTypeKey
		var newType type_system.Type

		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			name = elem.Name
			newType = elem.Value
		case *type_system.MethodElem:
			name = elem.Name
			newType = elem.Fn
		case *type_system.GetterElem:
			name = elem.Name
			newType = elem.Fn.Return
		case *type_system.SetterElem:
			name = elem.Name
			newType = elem.Fn.Params[0].Type
		default:
			continue
		}

		// If a property with this name already exists, check type compatibility
		if existingType, exists := existingProps[name]; exists {
			// Properties with the same name must have identical types
			unifyErrors := c.Unify(ctx, newType, existingType)
			if len(unifyErrors) > 0 {
				// Add a more specific error for interface merging
				errors = append(errors, &InterfaceMergeError{
					InterfaceName: decl.Name.Name,
					PropertyName:  name.String(),
					ExistingType:  existingType,
					NewType:       newType,
					span:          decl.Name.Span(),
				})
			}
		}
	}

	return errors
}
