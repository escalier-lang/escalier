package checker

import (
	"fmt"
	"iter"
	"os"
	"slices"
	"strconv"
	"strings"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/graphql"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/validator/rules"
)

func (c *Checker) InferScript(ctx Context, m *ast.Script) (*Scope, []Error) {
	errors := []Error{}
	ctx = ctx.WithNewScope()

	for _, stmt := range m.Stmts {
		switch stmt := stmt.(type) {
		case *ast.DeclStmt:
			declErrors := c.inferDecl(ctx, stmt.Decl)
			errors = slices.Concat(errors, declErrors)
		case *ast.ExprStmt:
			_, exprErrors := c.inferExpr(ctx, stmt.Expr)
			errors = slices.Concat(errors, exprErrors)
		case *ast.ReturnStmt:
			panic("TODO: infer return statement")
		}
	}

	return ctx.Scope, errors
}

type QualifiedIdent string

// NewQualifiedIdent creates a QualifiedIdent from a slice of string parts
func NewQualifiedIdent(parts []string) QualifiedIdent {
	return QualifiedIdent(strings.Join(parts, "."))
}

// Parts returns the QualifiedIdent as a slice of string parts
func (qi QualifiedIdent) Parts() []string {
	if qi == "" {
		return []string{}
	}
	return strings.Split(string(qi), ".")
}

func PrintDeclIdent(decl ast.Decl) string {
	// Print the identifier of the declaration, e.g. "foo.bar.baz"
	// This is used for debugging and logging purposes.
	switch decl := decl.(type) {
	case *ast.FuncDecl:
		return decl.Name.Name
	case *ast.VarDecl:
		// get all bindings introduced by the decl
		bindings := ast.FindBindings(decl.Pattern)
		return strings.Join(bindings.ToSlice(), ", ")
	case *ast.TypeDecl:
		return decl.Name.Name
	default:
		return fmt.Sprintf("%T", decl)
	}
}

// A module can contain declarations from mutliple source files.
// The order of the declarations doesn't matter because we compute the dependency
// graph and codegen will ensure that the declarations are emitted in the correct
// order.
func (c *Checker) InferModule(ctx Context, m *ast.Module) (*Namespace, []Error) {
	depGraph := dep_graph.BuildDepGraph(m)
	return c.InferDepGraph(ctx, depGraph)
}

func (c *Checker) InferDepGraph(ctx Context, depGraph *dep_graph.DepGraph) (*Namespace, []Error) {
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

// getNsCtx returns a new Context with the namespace set to the namespace of
// the declaration with the given declID. If the namespace doesn't exist yet, it
// creates one.
func getNsCtx(ctx Context, depGraph *dep_graph.DepGraph, declID dep_graph.DeclID) Context {
	nsName, _ := depGraph.GetDeclNamespace(declID)
	if nsName == "" {
		return ctx
	}
	ns := ctx.Scope.Namespace
	nsCtx := ctx
	for part := range strings.SplitSeq(nsName, ".") {
		if _, ok := ns.Namespaces[part]; !ok {
			ns.Namespaces[part] = NewNamespace()
		}
		ns = ns.Namespaces[part]
		nsCtx = nsCtx.WithNewScopeAndNamespace(ns)
	}
	return nsCtx
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
	paramBindingsForDecl := make(map[dep_graph.DeclID]map[string]*Binding)

	declCtxMap := make(map[dep_graph.DeclID]Context)
	declMethodCtxs := make([][]Context, len(component))

	// Infer placeholders
	for i, declID := range component {
		// TODO: rename this to nsCtx instead of nsCtx
		nsCtx := getNsCtx(ctx, depGraph, declID)
		decl, _ := depGraph.GetDecl(declID)

		switch decl := decl.(type) {
		case *ast.FuncDecl:
			funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(nsCtx, &decl.FuncSig, decl)
			paramBindingsForDecl[declID] = paramBindings
			errors = slices.Concat(errors, sigErrors)

			// Save the context for inferring the function body later
			declCtxMap[declID] = funcCtx

			nsCtx.Scope.setValue(decl.Name.Name, &Binding{
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

				unifyErrors := c.unify(nsCtx, patType, taType)
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
			typeParams := make([]*TypeParam, len(decl.TypeParams))
			for i, typeParam := range decl.TypeParams {
				var constraintType Type
				var defaultType Type
				if typeParam.Constraint != nil {
					constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
				}
				if typeParam.Default != nil {
					defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
				}
				typeParams[i] = &TypeParam{
					Name:       typeParam.Name,
					Constraint: constraintType,
					Default:    defaultType,
				}
			}

			typeAlias := &TypeAlias{
				Type:       c.FreshVar(&ast.NodeProvenance{Node: decl}),
				TypeParams: typeParams,
			}

			nsCtx.Scope.setTypeAlias(decl.Name.Name, typeAlias)
		case *ast.ClassDecl:
			instanceType := c.FreshVar(&ast.NodeProvenance{Node: decl})

			typeParams := make([]*TypeParam, len(decl.TypeParams))
			for i, typeParam := range decl.TypeParams {
				var constraintType Type
				var defaultType Type
				if typeParam.Constraint != nil {
					constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
				}
				if typeParam.Default != nil {
					defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
				}
				typeParams[i] = &TypeParam{
					Name:       typeParam.Name,
					Constraint: constraintType,
					Default:    defaultType,
				}
			}

			typeAlias := &TypeAlias{
				Type:       instanceType,
				TypeParams: typeParams,
			}

			nsCtx.Scope.setTypeAlias(decl.Name.Name, typeAlias)
			declCtx := nsCtx.WithNewScope()
			declCtxMap[declID] = declCtx

			for _, typeParam := range typeParams {
				var t Type = NewUnknownType(nil)
				if typeParam.Constraint != nil {
					t = typeParam.Constraint
				}
				declCtx.Scope.setTypeAlias(typeParam.Name, &TypeAlias{
					Type:       t,
					TypeParams: []*TypeParam{},
				})
			}

			objTypeElems := []ObjTypeElem{}
			staticElems := []ObjTypeElem{}
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

					if key.Kind == SymObjTypeKeyKind {
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
						staticElems = append(
							staticElems,
							NewPropertyElem(*key, c.FreshVar(nil)),
						)
					} else {
						// Instance fields go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewPropertyElem(*key, c.FreshVar(nil)),
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

					if key.Kind == SymObjTypeKeyKind {
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
							NewMethodElem(*key, methodType, nil), // static methods don't have self
						)
					} else {
						// Instance methods go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewMethodElem(*key, methodType, elem.MutSelf),
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

					if key.Kind == SymObjTypeKeyKind {
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
							NewGetterElem(*key, funcType),
						)
					} else {
						// Instance getters go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewGetterElem(*key, funcType),
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

					if key.Kind == SymObjTypeKeyKind {
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
							NewSetterElem(*key, funcType),
						)
					} else {
						// Instance setters go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewSetterElem(*key, funcType),
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
			objType := NewNominalObjectType(provenance, objTypeElems)
			objType.SymbolKeyMap = instanceSymbolKeyMap

			// TODO: call c.bind() directly
			unifyErrors := c.unify(ctx, instanceType, objType)
			errors = slices.Concat(errors, unifyErrors)

			params, paramBindings, paramErrors := c.inferFuncParams(declCtx, decl.Params)
			errors = slices.Concat(errors, paramErrors)
			paramBindingsForDecl[declID] = paramBindings

			typeArgs := make([]Type, len(typeParams))
			for i := range typeParams {
				typeArgs[i] = NewTypeRefType(nil, typeParams[i].Name, nil)
			}

			funcType := NewFuncType(
				provenance,
				typeParams,
				params,
				NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
				NewNeverType(nil),
			)

			// Create an object type with a constructor element and static methods/properties
			constructorElem := &ConstructorElem{Fn: funcType}
			classObjTypeElems := []ObjTypeElem{constructorElem}
			classObjTypeElems = append(classObjTypeElems, staticElems...)

			classObjType := NewObjectType(provenance, classObjTypeElems)
			classObjType.SymbolKeyMap = staticSymbolKeyMap

			ctor := &Binding{
				Source:  &ast.NodeProvenance{Node: decl},
				Type:    classObjType,
				Mutable: false,
			}
			nsCtx.Scope.setValue(decl.Name.Name, ctor)
			declMethodCtxs[i] = methodCtxs
		case *ast.EnumDecl:
			// I think we can infer the whole thing at once here.

			// We need a new namespace
			ns := NewNamespace()
			nsCtx.Scope.setNamespace(decl.Name.Name, ns)

			enumType := c.FreshVar(&ast.NodeProvenance{Node: decl})

			typeParams := make([]*TypeParam, len(decl.TypeParams))
			for i, typeParam := range decl.TypeParams {
				var constraintType Type
				var defaultType Type
				if typeParam.Constraint != nil {
					constraintType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Constraint})
				}
				if typeParam.Default != nil {
					defaultType = c.FreshVar(&ast.NodeProvenance{Node: typeParam.Default})
				}
				typeParams[i] = &TypeParam{
					Name:       typeParam.Name,
					Constraint: constraintType,
					Default:    defaultType,
				}
			}

			declCtx := nsCtx.WithNewScope()
			declCtxMap[declID] = declCtx

			// Add each type param as a type alias in the declCtx so that
			// they can be referenced when inferring the enum variants
			for _, typeParam := range typeParams {
				var t Type = NewUnknownType(nil)
				if typeParam.Constraint != nil {
					t = typeParam.Constraint
				}
				declCtx.Scope.setTypeAlias(typeParam.Name, &TypeAlias{
					Type:       t,
					TypeParams: []*TypeParam{},
				})
			}

			typeAlias := &TypeAlias{
				Type:       enumType,
				TypeParams: typeParams,
			}

			typeArgs := make([]Type, len(typeParams))
			for i := range typeParams {
				typeArgs[i] = NewTypeRefType(nil, typeParams[i].Name, nil)
			}

			variantTypes := make([]Type, len(decl.Elems))

			for i, elem := range decl.Elems {
				switch elem := elem.(type) {
				case *ast.EnumVariant:
					// TODO: build an instance type, e.g. given the enum variant
					// Some(value: T)
					// we want to build a type that looks like:
					// { [Symbol.customMatcher](subject: C) -> [T] }
					// Where `C` is the instance type of the enum variant

					instanceType := NewNominalObjectType(&ast.NodeProvenance{Node: elem}, []ObjTypeElem{})
					instanceTypeAlias := &TypeAlias{
						Type:       instanceType,
						TypeParams: nil,
					}
					ns.Types[elem.Name.Name] = instanceTypeAlias

					params, _, paramErrors := c.inferFuncParams(declCtx, elem.Params)
					errors = slices.Concat(errors, paramErrors)

					// Build the constructor function type
					funcType := NewFuncType(
						&ast.NodeProvenance{Node: elem},
						nil,
						params,
						NewTypeRefType(nil, decl.Name.Name, typeAlias, typeArgs...),
						NewNeverType(nil),
					)
					constructorElem := &ConstructorElem{Fn: funcType}

					classObjTypeElems := []ObjTypeElem{constructorElem}

					// Build [Symbol.customMatcher](subject: C) -> [T] method
					symbol := ctx.Scope.getValue("Symbol")
					key := PropertyKey{
						Name:     "customMatcher",
						OptChain: false,
						Span:     DEFAULT_SPAN,
					}
					customMatcher, _ := c.getMemberType(ctx, symbol.Type, key)
					switch customMatcher := Prune(customMatcher).(type) {
					case *UniqueSymbolType:
						self := false
						subjectPat := &IdentPat{Name: "subject"}
						subjectType := NewTypeRefType(nil, elem.Name.Name, instanceTypeAlias)
						paramTypes := make([]Type, len(elem.Params))
						for i, param := range elem.Params {
							t, _ := c.inferTypeAnn(declCtx, param.TypeAnn)
							paramTypes[i] = t
						}
						returnType := NewTupleType(nil, paramTypes...)

						methodElem := &MethodElem{
							Name: ObjTypeKey{Kind: SymObjTypeKeyKind, Sym: customMatcher.Value},
							Fn: NewFuncType(
								nil,
								nil,
								[]*FuncParam{{Pattern: subjectPat, Type: subjectType}},
								returnType,
								NewNeverType(nil),
							),
							MutSelf: &self,
						}
						classObjTypeElems = append(classObjTypeElems, methodElem)
					default:
						panic("Symbol.customMatcher is not a unique symbol")
					}

					provenance := &ast.NodeProvenance{Node: elem}
					classObjType := NewObjectType(provenance, classObjTypeElems)

					ctor := &Binding{
						Source:  provenance,
						Type:    classObjType,
						Mutable: false,
					}

					ns.Values[elem.Name.Name] = ctor

					variantName := &Member{
						Left:  NewIdent(decl.Name.Name),
						Right: NewIdent(elem.Name.Name),
					}

					variantTypes[i] = &TypeRefType{
						Name:      variantName,
						TypeArgs:  typeArgs,
						TypeAlias: instanceTypeAlias,
					}
				case *ast.EnumSpread:
					panic("TODO: infer enum spreads")
				}
			}

			enumUnionType := NewUnionType(&ast.NodeProvenance{Node: decl}, variantTypes...)
			fmt.Fprintf(os.Stderr, "enumUnionType: %s\n", enumUnionType.String())
			enumTypeAlias := &TypeAlias{
				Type:       enumUnionType,
				TypeParams: typeParams,
			}

			nsCtx.Scope.setTypeAlias(decl.Name.Name, enumTypeAlias)
		}
	}

	// Infer definitions
	for i, declID := range component {
		nsCtx := getNsCtx(ctx, depGraph, declID)
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
			funcBinding := nsCtx.Scope.getValue(decl.Name.Name)
			paramBindings := paramBindingsForDecl[declID]
			funcType := funcBinding.Type.(*FuncType)

			declCtx := declCtxMap[declID]

			if decl.Body != nil {
				inferErrors := c.inferFuncBodyWithFuncSigType(declCtx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
				errors = slices.Concat(errors, inferErrors)
			}

		case *ast.VarDecl:
			// TODO: if there's a type annotation, unify the initializer with it
			if decl.Init != nil {
				initType, initErrors := c.inferExpr(nsCtx, decl.Init)
				errors = slices.Concat(errors, initErrors)
				if decl.TypeAnn != nil {
					taType := decl.TypeAnn.InferredType()
					unifyErrors := c.unify(ctx, initType, taType)
					errors = slices.Concat(errors, unifyErrors)
				} else {
					patType := decl.InferredType
					unifyErrors := c.unify(ctx, initType, patType)
					errors = slices.Concat(errors, unifyErrors)
				}
			}
		case *ast.TypeDecl:
			typeAlias, declErrors := c.inferTypeDecl(nsCtx, decl)
			errors = slices.Concat(errors, declErrors)

			// TODO:
			// - unify the Default and Constraint types for each type param

			// Unified the type alias' inferred type with its placeholder type
			existingTypeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			unifyErrors := c.unify(nsCtx, existingTypeAlias.Type, typeAlias.Type)
			errors = slices.Concat(errors, unifyErrors)
		case *ast.ClassDecl:
			methodCtxs := declMethodCtxs[i]
			typeAlias := nsCtx.Scope.getTypeAlias(decl.Name.Name)
			instanceType := Prune(typeAlias.Type).(*ObjectType)

			// Get the class binding to access static methods
			classBinding := nsCtx.Scope.getValue(decl.Name.Name)
			classType := classBinding.Type.(*ObjectType)

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
					var prop *PropertyElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding property in either instance or class type
					var targetType *ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if propElem, ok := elem.(*PropertyElem); ok {
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

								unifyErrors := c.unify(ctx, prop.Value, initType)
								errors = slices.Concat(errors, unifyErrors)
							} else {
								var binding *Binding
								switch name := bodyElem.Name.(type) {
								case *ast.IdentExpr:
									binding = bodyCtx.Scope.getValue(name.Name)
								case *ast.StrLit:
									binding = bodyCtx.Scope.getValue(name.Value)
								case *ast.NumLit:
									binding = bodyCtx.Scope.getValue(strconv.FormatFloat(name.Value, 'f', -1, 64))
								case *ast.ComputedKey:
									panic("computed keys are not supported in shorthand field declarations")
								}

								unifyErrors := c.unify(ctx, prop.Value, binding.Type)
								errors = slices.Concat(errors, unifyErrors)
							}
						}
					}

				case *ast.MethodElem:
					var methodType *MethodElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding method in either instance or class type
					var targetType *ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if methodElem, ok := elem.(*MethodElem); ok {
								if methodElem.Name == *astKey {
									methodType = methodElem
									break
								}
							}
						}
					}

					if methodType != nil {
						paramBindings := make(map[string]*Binding)

						// For instance methods, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t Type = NewTypeRefType(nil, decl.Name.Name, typeAlias)
							if methodType.MutSelf != nil && *methodType.MutSelf {
								t = NewMutableType(nil, t)
							}

							paramBindings["self"] = &Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: methodType.MutSelf != nil && *methodType.MutSelf,
							}
						}

						// For static methods, no 'self' parameter is added

						for _, param := range methodType.Fn.Params {
							paramBindings[param.Pattern.String()] = &Binding{
								Source:  &TypeProvenance{Type: param.Type},
								Type:    param.Type,
								Mutable: false,
							}
						}

						methodCtx := methodCtxs[i]
						bodyErrors := c.inferFuncBodyWithFuncSigType(methodCtx, methodType.Fn, paramBindings, bodyElem.Fn.Body, false)
						errors = slices.Concat(errors, bodyErrors)
					}

				case *ast.GetterElem:
					var getterType *GetterElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding getter in either instance or class type
					var targetType *ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if getterElem, ok := elem.(*GetterElem); ok {
								if getterElem.Name == *astKey {
									getterType = getterElem
									break
								}
							}
						}
					}

					if getterType != nil {
						paramBindings := make(map[string]*Binding)

						// For instance getters, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t Type = NewTypeRefType(nil, decl.Name.Name, typeAlias)

							paramBindings["self"] = &Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: false, // getters don't mutate self
							}
						}

						// For static getters, no 'self' parameter is added

						// Add any explicit parameters from the getter function signature
						for _, param := range getterType.Fn.Params {
							paramBindings[param.Pattern.String()] = &Binding{
								Source:  &TypeProvenance{Type: param.Type},
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
					var setterType *SetterElem
					var isStatic bool = bodyElem.Static

					// Find the corresponding setter in either instance or class type
					var targetType *ObjectType
					if isStatic {
						targetType = classType
					} else {
						targetType = instanceType
					}

					astKey, keyErrors := c.astKeyToTypeKey(bodyCtx, bodyElem.Name)
					errors = slices.Concat(errors, keyErrors)
					if astKey != nil {
						for _, elem := range targetType.Elems {
							if setterElem, ok := elem.(*SetterElem); ok {
								if setterElem.Name == *astKey {
									setterType = setterElem
									break
								}
							}
						}
					}

					if setterType != nil {
						paramBindings := make(map[string]*Binding)

						// For instance setters, add 'self' parameter
						if !isStatic {
							// We use the name of the class as the type here to avoid
							// a RecursiveUnificationError.
							// TODO: handle generic classes
							var t Type = NewTypeRefType(nil, decl.Name.Name, typeAlias)
							// Setters typically need mutable self to modify the instance
							t = NewMutableType(nil, t)

							paramBindings["self"] = &Binding{
								Source:  &ast.NodeProvenance{Node: bodyElem},
								Type:    t,
								Mutable: true, // setters may mutate self
							}
						}

						// For static setters, no 'self' parameter is added

						// Add any explicit parameters from the setter function signature
						for _, param := range setterType.Fn.Params {
							paramBindings[param.Pattern.String()] = &Binding{
								Source:  &TypeProvenance{Type: param.Type},
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
		ctx.Scope.setTypeAlias(decl.Name.Name, typeAlias)
		return errors
	case *ast.ClassDecl:
		panic("TODO: infer class declaration")
	default:
		panic(fmt.Sprintf("Unknown declaration type: %T", decl))
	}
}

// TODO: refactor this to return the binding map instead of copying them over
// immediately
func (c *Checker) inferVarDecl(ctx Context, decl *ast.VarDecl) (map[string]*Binding, []Error) {
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

		unifyErrors := c.unify(ctx, taType, patType)
		errors = slices.Concat(errors, unifyErrors)

		if decl.Init != nil {
			initType, initErrors := c.inferExpr(ctx, decl.Init)
			errors = slices.Concat(errors, initErrors)

			unifyErrors = c.unify(ctx, initType, taType)
			errors = slices.Concat(errors, unifyErrors)
		}
	} else {
		if decl.Init == nil {
			// TODO: report an error, but set initType to be `unknown`
			panic("Expected either a type annotation or an initializer expression")
		}
		initType, initErrors := c.inferExpr(ctx, decl.Init)
		errors = slices.Concat(errors, initErrors)

		unifyErrors := c.unify(ctx, initType, patType)
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
			if promiseType, ok := funcType.Return.(*TypeRefType); ok && QualIdentToString(promiseType.Name) == "Promise" {
				// Good, it's a Promise type. Ensure it has the right structure.
				if len(promiseType.TypeArgs) == 1 {
					// Promise<T> should become Promise<T, never>
					promiseAlias := ctx.Scope.getTypeAlias("Promise")
					if promiseAlias != nil {
						// Update the function type to have Promise<T, never>
						newPromiseType := NewTypeRefType(nil, "Promise", promiseAlias, promiseType.TypeArgs[0], NewNeverType(nil))
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
		inferErrors := c.inferFuncBodyWithFuncSigType(ctx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)
	}

	binding := Binding{
		Source:  &ast.NodeProvenance{Node: decl},
		Type:    funcType,
		Mutable: false,
	}
	ctx.Scope.setValue(decl.Name.Name, &binding)
	return errors
}

func (c *Checker) inferCallExpr(ctx Context, expr *ast.CallExpr) (resultType Type, errors []Error) {
	errors = []Error{}
	calleeType, calleeErrors := c.inferExpr(ctx, expr.Callee)
	errors = slices.Concat(errors, calleeErrors)

	argTypes := make([]Type, len(expr.Args))
	for i, arg := range expr.Args {
		argType, argErrors := c.inferExpr(ctx, arg)
		errors = slices.Concat(errors, argErrors)
		argTypes[i] = argType
	}

	// Check if calleeType is a FuncType
	if fnType, ok := calleeType.(*FuncType); ok {
		// Handle generic functions by replacing type refs with fresh type variables
		if len(fnType.TypeParams) > 0 {
			// Create a copy of the function type without type params
			fnTypeWithoutParams := NewFuncType(
				&TypeProvenance{Type: fnType},
				nil,
				fnType.Params,
				fnType.Return,
				fnType.Throws,
			)

			// Create fresh type variables for each type parameter
			substitutions := make(map[string]Type)
			for _, typeParam := range fnType.TypeParams {
				// TODO: handle defaults
				t := c.FreshVar(nil)
				if typeParam.Constraint != nil {
					t.Constraint = typeParam.Constraint
				}
				substitutions[typeParam.Name] = t
			}

			// Substitute type refs in the copied function type with fresh type variables
			fnType = c.substituteTypeParams(fnTypeWithoutParams, substitutions).(*FuncType)
		}
		// Find if the function has a rest parameter
		var restIndex = -1
		for i, param := range fnType.Params {
			if param.Pattern != nil {
				if _, isRest := param.Pattern.(*RestPat); isRest {
					restIndex = i
					break
				}
			}
		}

		if restIndex != -1 {
			// Function has rest parameters
			// Must have at least as many args as required parameters (before rest)
			if len(expr.Args) < restIndex {
				return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
					Callee: fnType,
					Args:   expr.Args,
				}}
			}

			// Unify fixed parameters (before rest)
			for i := 0; i < restIndex; i++ {
				argType := argTypes[i]
				paramType := fnType.Params[i].Type
				paramErrors := c.unify(ctx, argType, paramType)
				errors = slices.Concat(errors, paramErrors)
			}

			// Unify rest arguments with rest parameter type
			if len(expr.Args) > restIndex {
				restParam := fnType.Params[restIndex]
				// Rest parameter should be Array<T>, extract T
				if arrayType, ok := restParam.Type.(*TypeRefType); ok && QualIdentToString(arrayType.Name) == "Array" && len(arrayType.TypeArgs) > 0 {
					elementType := arrayType.TypeArgs[0]
					// Unify each excess argument with the element type
					for i := restIndex; i < len(expr.Args); i++ {
						argType := argTypes[i]
						paramErrors := c.unify(ctx, argType, elementType)
						errors = slices.Concat(errors, paramErrors)
					}
				} else {
					// Rest parameter is not Array<T>, this is an error
					return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
						Callee: fnType,
						Args:   expr.Args,
					}}
				}
			}

			return fnType.Return, errors
		} else {
			// Function has no rest parameters, use strict equality check
			if len(fnType.Params) != len(expr.Args) {
				return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
					Callee: fnType,
					Args:   expr.Args,
				}}
			} else {
				for argType, param := range Zip(argTypes, fnType.Params) {
					paramType := param.Type
					paramErrors := c.unify(ctx, argType, paramType)
					errors = slices.Concat(errors, paramErrors)
				}

				return fnType.Return, errors
			}
		}
	} else if objType, ok := calleeType.(*ObjectType); ok {
		// Check if ObjectType has a constructor or callable element
		var fnTypeToUse *FuncType = nil

		for _, elem := range objType.Elems {
			if constructorElem, ok := elem.(*ConstructorElem); ok {
				fnTypeToUse = constructorElem.Fn
				break
			} else if callableElem, ok := elem.(*CallableElem); ok {
				fnTypeToUse = callableElem.Fn
				break
			}
		}

		if fnTypeToUse == nil {
			return NewNeverType(nil), []Error{
				&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
		}

		// Handle generic functions by replacing type refs with fresh type variables
		if len(fnTypeToUse.TypeParams) > 0 {
			// Create a copy of the function type without type params
			fnTypeWithoutParams := NewFuncType(
				&TypeProvenance{Type: fnTypeToUse},
				nil,
				fnTypeToUse.Params,
				fnTypeToUse.Return,
				fnTypeToUse.Throws,
			)

			// Create fresh type variables for each type parameter
			substitutions := make(map[string]Type)
			for _, typeParam := range fnTypeToUse.TypeParams {
				substitutions[typeParam.Name] = c.FreshVar(nil)
			}

			// Substitute type refs in the copied function type with fresh type variables
			fnTypeToUse = c.substituteTypeParams(fnTypeWithoutParams, substitutions).(*FuncType)
		}

		// Use the same logic as for direct function calls
		// Find if the function has a rest parameter
		var restIndex = -1
		for i, param := range fnTypeToUse.Params {
			if param.Pattern != nil {
				if _, isRest := param.Pattern.(*RestPat); isRest {
					restIndex = i
					break
				}
			}
		}

		if restIndex != -1 {
			// Function has rest parameters
			// Must have at least as many args as required parameters (before rest)
			if len(expr.Args) < restIndex {
				return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
					Callee: fnTypeToUse,
					Args:   expr.Args,
				}}
			}

			// Unify fixed parameters (before rest)
			for i := 0; i < restIndex; i++ {
				argType := argTypes[i]
				paramType := fnTypeToUse.Params[i].Type
				paramErrors := c.unify(ctx, argType, paramType)
				errors = slices.Concat(errors, paramErrors)
			}

			// Unify rest arguments with rest parameter type
			if len(expr.Args) > restIndex {
				restParam := fnTypeToUse.Params[restIndex]
				// Rest parameter should be Array<T>, extract T
				if arrayType, ok := restParam.Type.(*TypeRefType); ok && QualIdentToString(arrayType.Name) == "Array" && len(arrayType.TypeArgs) > 0 {
					elementType := arrayType.TypeArgs[0]
					// Unify each excess argument with the element type
					for i := restIndex; i < len(expr.Args); i++ {
						argType := argTypes[i]
						paramErrors := c.unify(ctx, argType, elementType)
						errors = slices.Concat(errors, paramErrors)
					}
				} else {
					// Rest parameter is not Array<T>, this is an error
					return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
						Callee: fnTypeToUse,
						Args:   expr.Args,
					}}
				}
			}

			return fnTypeToUse.Return, errors
		} else {
			// Function has no rest parameters, use strict equality check
			if len(fnTypeToUse.Params) != len(expr.Args) {
				return NewNeverType(nil), []Error{&InvalidNumberOfArgumentsError{
					Callee: fnTypeToUse,
					Args:   expr.Args,
				}}
			} else {
				for argType, param := range Zip(argTypes, fnTypeToUse.Params) {
					paramType := param.Type
					paramErrors := c.unify(ctx, argType, paramType)
					errors = slices.Concat(errors, paramErrors)
				}

				return fnTypeToUse.Return, errors
			}
		}
	} else {
		return NewNeverType(nil), []Error{
			&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
	}
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (Type, []Error) {
	var resultType Type
	var errors []Error

	provenance := &ast.NodeProvenance{Node: expr}

	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := NewNeverType(nil)

		if expr.Op == ast.Assign {
			// TODO: check if expr.Left is a valid lvalue
			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)

			errors = slices.Concat(leftErrors, rightErrors)

			// Check if we're trying to mutate an immutable object
			if memberExpr, ok := expr.Left.(*ast.MemberExpr); ok {
				objType, objErrors := c.inferExpr(ctx, memberExpr.Object)
				errors = slices.Concat(errors, objErrors)

				// Check if the object type allows mutation
				if !c.isMutableType(objType) {
					errors = append(errors, &CannotMutateImmutableError{
						Type: objType,
						span: expr.Left.Span(),
					})
				}
			} else if indexExpr, ok := expr.Left.(*ast.IndexExpr); ok {
				objType, objErrors := c.inferExpr(ctx, indexExpr.Object)
				errors = slices.Concat(errors, objErrors)

				// Check if the object type allows mutation
				if !c.isMutableType(objType) {
					errors = append(errors, &CannotMutateImmutableError{
						Type: objType,
						span: expr.Left.Span(),
					})
				}
			}

			// RHS must be a subtype of LHS because we're assigning RHS to LHS
			unifyErrors := c.unify(ctx, rightType, leftType)
			errors = slices.Concat(errors, unifyErrors)

			resultType = neverType
		} else {
			opBinding := ctx.Scope.getValue(string(expr.Op))
			if opBinding == nil {
				resultType = neverType
				errors = []Error{&UnknownOperatorError{
					Operator: string(expr.Op),
				}}
			} else {
				// TODO: extract this into a unifyCall method
				// TODO: handle function overloading
				if fnType, ok := opBinding.Type.(*FuncType); ok {
					if len(fnType.Params) != 2 {
						resultType = neverType
						errors = []Error{&InvalidNumberOfArgumentsError{
							Callee: fnType,
							Args:   []ast.Expr{expr.Left, expr.Right},
						}}
					} else {
						errors = []Error{}

						leftType, leftErrors := c.inferExpr(ctx, expr.Left)
						rightType, rightErrors := c.inferExpr(ctx, expr.Right)
						errors = slices.Concat(errors, leftErrors, rightErrors)

						leftErrors = c.unify(ctx, leftType, fnType.Params[0].Type)
						rightErrors = c.unify(ctx, rightType, fnType.Params[1].Type)
						errors = slices.Concat(errors, leftErrors, rightErrors)

						resultType = fnType.Return
					}
				} else {
					resultType = neverType
					errors = []Error{&UnknownOperatorError{Operator: string(expr.Op)}}
				}
			}
		}
	case *ast.UnaryExpr:
		if expr.Op == ast.UnaryMinus {
			if lit, ok := expr.Arg.(*ast.LiteralExpr); ok {
				if num, ok := lit.Lit.(*ast.NumLit); ok {
					resultType = NewNumLitType(provenance, -num.Value)
					errors = []Error{}
				} else {
					resultType = NewNeverType(nil)
					errors = []Error{&UnimplementedError{
						message: "Handle unary operators",
						span:    expr.Span(),
					}}
				}
			} else {
				resultType = NewNeverType(nil)
				errors = []Error{&UnimplementedError{
					message: "Handle unary operators",
					span:    expr.Span(),
				}}
			}
		} else {
			resultType = NewNeverType(nil)
			errors = []Error{&UnimplementedError{
				message: "Handle unary operators",
				span:    expr.Span(),
			}}
		}
	case *ast.CallExpr:
		resultType, errors = c.inferCallExpr(ctx, expr)
	case *ast.MemberExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, Span: expr.Prop.Span()}
		propType, propErrors := c.getMemberType(ctx, objType, key)

		resultType = propType

		if methodType, ok := propType.(*FuncType); ok {
			if retType, ok := methodType.Return.(*TypeRefType); ok && QualIdentToString(retType.Name) == "Self" {
				t := *methodType   // Create a copy of the struct
				t.Return = objType // Replace `Self` with the object type
				resultType = &t
			}
		}

		errors = slices.Concat(objErrors, propErrors)
	case *ast.IndexExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		indexType, indexErrors := c.inferExpr(ctx, expr.Index)

		errors = slices.Concat(objErrors, indexErrors)

		key := IndexKey{Type: indexType, Span: expr.Index.Span()}
		accessType, accessErrors := c.getMemberType(ctx, objType, key)
		resultType = accessType
		errors = slices.Concat(errors, accessErrors)
	case *ast.IdentExpr:
		if binding := ctx.Scope.getValue(expr.Name); binding != nil {
			// We create a new type and set its provenance to be the identifier
			// instead of the binding source.  This ensures that errors are reported
			// on the identifier itself instead of the binding source.
			t := Prune(binding.Type)
			resultType = t.Copy()
			resultType.SetProvenance(&ast.NodeProvenance{Node: expr})
			expr.Source = binding.Source
			errors = nil
		} else if namespace := ctx.Scope.getNamespace(expr.Name); namespace != nil {
			t := NewNamespaceType(provenance, namespace)
			resultType = t
			errors = nil
		} else {
			resultType = NewNeverType(nil)
			errors = []Error{&UnknownIdentifierError{Ident: expr, span: expr.Span()}}
		}
	case *ast.LiteralExpr:
		resultType, errors = c.inferLit(expr.Lit)
	case *ast.TupleExpr:
		types := make([]Type, len(expr.Elems))
		errors = []Error{}
		for i, elem := range expr.Elems {
			elemType, elemErrors := c.inferExpr(ctx, elem)
			types[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		resultType = NewTupleType(provenance, types...)
	case *ast.ObjectExpr:
		// Create a context for the object so that we can add a `Self` type to it
		objCtx := ctx.WithNewScope()

		typeElems := make([]ObjTypeElem, len(expr.Elems))
		types := make([]Type, len(expr.Elems))
		paramBindingsSlice := make([]map[string]*Binding, len(expr.Elems))

		selfType := c.FreshVar(nil)
		selfTypeAlias := TypeAlias{Type: selfType, TypeParams: []*TypeParam{}}
		objCtx.Scope.setTypeAlias("Self", &selfTypeAlias)

		methodCtxs := make([]Context, len(expr.Elems))

		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					t := c.FreshVar(&ast.NodeProvenance{Node: elem})
					types[i] = t
					typeElems[i] = NewPropertyElem(*key, t)
				}
			case *ast.MethodExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					methodType, methodCtx, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					methodCtxs[i] = methodCtx
					paramBindingsSlice[i] = paramBindings
					types[i] = methodType
					typeElems[i] = NewMethodElem(*key, methodType, elem.MutSelf)
				}
			case *ast.GetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, _, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &GetterElem{Fn: funcType, Name: *key}
				}
			case *ast.SetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, _, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig, elem.Fn)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &SetterElem{Fn: funcType, Name: *key}
				}
			}
		}

		objType := NewObjectType(provenance, typeElems)
		bindErrors := c.bind(objCtx, selfType, objType)
		errors = slices.Concat(errors, bindErrors)

		i := 0 // indexes into paramBindingsSlice
		for t, exprElem := range Zip(types, expr.Elems) {
			switch elem := exprElem.(type) {
			case *ast.PropertyExpr:
				if elem.Value != nil {
					valueType, valueErrors := c.inferExpr(objCtx, elem.Value)
					unifyErrors := c.unify(objCtx, valueType, t)

					errors = slices.Concat(errors, valueErrors, unifyErrors)
				} else {
					switch key := elem.Name.(type) {
					case *ast.IdentExpr:
						// TODO: dedupe with *ast.IdentExpr case
						if binding := objCtx.Scope.getValue(key.Name); binding != nil {
							unifyErrors := c.unify(objCtx, binding.Type, t)
							errors = slices.Concat(errors, unifyErrors)
						} else {
							unifyErrors := c.unify(objCtx, NewNeverType(nil), t)
							errors = slices.Concat(errors, unifyErrors)

							errors = append(
								errors,
								&UnknownIdentifierError{Ident: key, span: key.Span()},
							)
						}
					}
				}
			case *ast.MethodExpr:
				methodType := t.(*FuncType)
				methodCtx := methodCtxs[i]
				methodExpr := elem
				paramBindings := paramBindingsSlice[i]

				if methodExpr.MutSelf != nil {
					var selfType Type = NewTypeRefType(nil, "Self", &selfTypeAlias)
					if *methodExpr.MutSelf {
						selfType = NewMutableType(nil, selfType)
					}
					paramBindings["self"] = &Binding{
						Source:  &ast.NodeProvenance{Node: expr},
						Type:    selfType,
						Mutable: false, // `self` cannot be reassigned
					}
				}

				inferErrors := c.inferFuncBodyWithFuncSigType(
					methodCtx, methodType, paramBindings, methodExpr.Fn.Body, methodExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)

			case *ast.GetterExpr:
				funcType := t.(*FuncType)
				paramBindings := paramBindingsSlice[i]
				paramBindings["self"] = &Binding{
					Source:  &ast.NodeProvenance{Node: expr},
					Type:    NewTypeRefType(nil, "Self", &selfTypeAlias),
					Mutable: false, // `self` cannot be reassigned
				}

				getterExpr := elem
				inferErrors := c.inferFuncBodyWithFuncSigType(
					objCtx, funcType, paramBindings, getterExpr.Fn.Body, getterExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)

			case *ast.SetterExpr:
				funcType := t.(*FuncType)
				paramBindings := paramBindingsSlice[i]
				paramBindings["self"] = &Binding{
					Source:  &ast.NodeProvenance{Node: expr},
					Type:    NewMutableType(nil, NewTypeRefType(nil, "Self", &selfTypeAlias)),
					Mutable: false, // `self` cannot be reassigned
				}

				setterExpr := elem
				inferErrors := c.inferFuncBodyWithFuncSigType(
					objCtx, funcType, paramBindings, setterExpr.Fn.Body, setterExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)
			}

			i++
		}

		resultType = selfType
	case *ast.FuncExpr:
		funcType, funcCtx, paramBindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig, expr)
		errors = slices.Concat(errors, sigErrors)

		inferErrors := c.inferFuncBodyWithFuncSigType(funcCtx, funcType, paramBindings, expr.Body, expr.FuncSig.Async)
		errors = slices.Concat(errors, inferErrors)

		resultType = funcType
	case *ast.IfElseExpr:
		resultType, errors = c.inferIfElse(ctx, expr)
	case *ast.DoExpr:
		resultType, errors = c.inferDoExpr(ctx, expr)
	case *ast.MatchExpr:
		resultType, errors = c.inferMatchExpr(ctx, expr)
	case *ast.ThrowExpr:
		// Infer the type of the argument being thrown
		_, argErrors := c.inferExpr(ctx, expr.Arg)
		errors = argErrors
		// Throw expressions have type never since they don't return a value
		resultType = NewNeverType(nil)
	case *ast.AwaitExpr:
		// Await can only be used inside async functions
		if !ctx.IsAsync {
			errors = []Error{
				&UnimplementedError{
					message: "await can only be used inside async functions",
					span:    expr.Span(),
				},
			}
			resultType = NewNeverType(nil)
		} else {
			// Infer the type of the expression being awaited
			argType, argErrors := c.inferExpr(ctx, expr.Arg)
			errors = argErrors

			// If the argument is a Promise<T, E>, the result type is T
			// and the throws type should be E (stored in expr.Throws for later use)
			if promiseType, ok := argType.(*TypeRefType); ok && QualIdentToString(promiseType.Name) == "Promise" {
				if len(promiseType.TypeArgs) >= 2 {
					resultType = promiseType.TypeArgs[0]  // T
					expr.Throws = promiseType.TypeArgs[1] // E (store for throw inference)
				} else {
					resultType = NewNeverType(nil)
				}
			} else {
				// If not a Promise type, this is an error
				errors = append(errors, &UnimplementedError{
					message: "await expression expects a Promise type",
					span:    expr.Span(),
				})
				resultType = NewNeverType(nil)
			}
		}
	case *ast.TaggedTemplateLitExpr:
		// Create string literals from the quasis (static parts)
		stringElems := make([]ast.Expr, len(expr.Quasis))
		for i, quasi := range expr.Quasis {
			strLit := ast.NewString(quasi.Value, quasi.Span)
			stringElems[i] = ast.NewLitExpr(strLit)
		}

		// Create array of string literals as first argument
		stringsArray := ast.NewArray(stringElems, expr.Span())

		// Combine the strings array with the interpolated expressions
		args := make([]ast.Expr, 1+len(expr.Exprs))
		args[0] = stringsArray
		copy(args[1:], expr.Exprs)

		// If expr.Tag is the identifier `gql` then do some custom handling
		if tag, ok := expr.Tag.(*ast.IdentExpr); ok && tag.Name == "gql" {
			// TODO: Interpolate Exprs
			str := ""
			for i, quasi := range expr.Quasis {
				str += quasi.Value
				if i < len(expr.Exprs) {
					expr := expr.Exprs[i]
					t, _ := c.inferExpr(ctx, expr)

					switch t := Prune(t).(type) {
					case *LitType:
						str += t.Lit.String()
					default:
						// TODO: handle interpolating DocumentNode fragments
						panic("Can only interpolate literal types in gql tagged templates")
					}
				}
			}

			queryDoc := gqlparser.MustLoadQueryWithRules(c.Schema, str, rules.NewDefaultRules())
			result := graphql.InferGraphQLQuery(c.Schema, queryDoc)

			// `TypedDocumentNode<ResultType, VariablesType>`
			// TODO: Look up `TypedDocumentNode` from `@graphql-typed-document-node/core`
			t := NewTypeRefType(provenance, "TypedDocumentNode", nil, result.ResultType, result.VariablesType)
			return t, nil
		}

		// Create a call expression
		callExpr := ast.NewCall(expr.Tag, args, false, expr.Span())

		// Infer the call expression
		resultType, errors = c.inferCallExpr(ctx, callExpr)
	case *ast.TypeCastExpr:
		// Infer the type of the expression being cast
		exprType, exprErrors := c.inferExpr(ctx, expr.Expr)
		errors = slices.Concat(errors, exprErrors)

		// Infer the type annotation to get the target type
		targetType, typeAnnErrors := c.inferTypeAnn(ctx, expr.TypeAnn)
		errors = slices.Concat(errors, typeAnnErrors)

		// Check that the expression type is a subtype of the target type
		// For type casting, we require that exprType can be unified with targetType
		unifyErrors := c.unify(ctx, exprType, targetType)
		errors = slices.Concat(errors, unifyErrors)

		// The result type is the target type
		resultType = targetType
	default:
		resultType = NewNeverType(nil)
		errors = []Error{
			&UnimplementedError{
				message: "Infer expression type: " + fmt.Sprintf("%T", expr),
				span:    expr.Span(),
			},
		}
	}

	// Always set the inferred type on the expression before returning
	expr.SetInferredType(resultType)
	return resultType, errors
}

// TypeExpansionVisitor implements TypeVisitor for expanding type references
type TypeExpansionVisitor struct {
	checker             *Checker
	ctx                 Context
	errors              []Error
	skipTypeRefsCount   int // if > 0, skip expanding TypeRefTypes
	expandTypeRefsCount int // if > 0, number of TypeRefTypes expanded, if -1 then unlimited
}

// NewTypeExpansionVisitor creates a new visitor for expanding type references
func NewTypeExpansionVisitor(checker *Checker, ctx Context, expandTypeRefsCount int) *TypeExpansionVisitor {
	return &TypeExpansionVisitor{
		checker:             checker,
		ctx:                 ctx,
		errors:              []Error{},
		skipTypeRefsCount:   0,
		expandTypeRefsCount: expandTypeRefsCount,
	}
}

func (v *TypeExpansionVisitor) EnterType(t Type) Type {
	switch t := t.(type) {
	case *FuncType:
		v.skipTypeRefsCount++ // don't expand type refs inside function types
	case *ObjectType:
		v.skipTypeRefsCount++ // don't expand type refs inside object types
	case *CondType:
		// We need to expand the CondType's extends type on entering so that
		// we can replace InferTypes in the extends type with fresh type variables
		// and then replace the corresponding TypeVarTypes in the alt and cons types
		// with those fresh type variables.  If we did this on exit, we wouldn't
		// be able to replace all the types in nested CondTypes.
		// TODO: Add a test case to ensure that infer type shadowing works and
		// fix the bug if it doesn't.

		inferSubs := v.checker.findInferTypes(t.Extends)
		groupSubs := v.checker.findNamedGroups(t.Extends)
		extendsType := v.checker.replaceRegexGroupTypes(t.Extends, groupSubs)
		extendsType = v.checker.replaceInferTypes(extendsType, inferSubs)

		maps.Copy(inferSubs, groupSubs)

		return NewCondType(
			t.Provenance(),
			t.Check,
			extendsType,
			v.checker.substituteTypeParams(t.Then, inferSubs),
			// TODO: don't use substitutions for the Then type because the Checks
			// type didn't have any InferTypes in it, so we don't need to
			// replace them with fresh type variables.
			v.checker.substituteTypeParams(t.Else, inferSubs),
		)
	}

	return nil
}

func (v *TypeExpansionVisitor) ExitType(t Type) Type {
	switch t := t.(type) {
	case *FuncType:
		v.skipTypeRefsCount--
	case *ObjectType:
		v.skipTypeRefsCount--
	case *CondType:
		errors := v.checker.unify(v.ctx, t.Check, t.Extends)
		if len(errors) == 0 {
			return t.Then
		} else {
			return t.Else
		}
	case *UnionType:
		// filter out `never` types from the union
		var filteredTypes []Type
		for _, typ := range t.Types {
			if _, ok := typ.(*NeverType); !ok {
				filteredTypes = append(filteredTypes, typ)
			}
		}
		if len(filteredTypes) == len(t.Types) {
			return nil // No filtering needed, return nil to let Accept handle it
		}
		return NewUnionType(nil, filteredTypes...)
	case *TypeRefType:
		// TODO: implement once TypeAliases have been marked as recursive.
		// `expandType` is eager so we can't expand recursive type aliases as it
		// would lead to infinite recursion.

		// Check if we've reached the maximum expansion depth
		if v.skipTypeRefsCount > 0 {
			// Return the type reference without expanding
			return nil
		}

		if v.expandTypeRefsCount == 0 {
			// Return the type reference without expanding
			return nil
		}

		typeAlias := v.checker.resolveQualifiedTypeAliasFromString(v.ctx, QualIdentToString(t.Name))
		if typeAlias == nil {
			v.errors = append(v.errors, &UnknownTypeError{TypeName: QualIdentToString(t.Name), typeRef: t})
			neverType := NewNeverType(nil)
			neverType.SetProvenance(&TypeProvenance{Type: t})
			return neverType
		} // Replace type params with type args if the type is generic
		expandedType := typeAlias.Type

		// Don't expand nominal object types
		if t, ok := expandedType.(*ObjectType); ok {
			if t.Nominal {
				return nil
			}
		}

		// TODO:
		// - ensure that the number of type args matches the number of type params
		// - handle type params with defaults
		if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {

			isCondType := false
			if _, ok := Prune(expandedType).(*CondType); ok {
				isCondType = true
			}

			// TODO:
			// Handle case such as:
			// - type Foo<T> = boolean | T extends string ? T : number
			// - type Bar<T> = string & T extends string ? T : number
			// Do not perform distributions if the conditional type is the child
			// of any other type.
			if isCondType {
				substitutionSets, subSetErrors := v.checker.generateSubstitutionSets(v.ctx, typeAlias.TypeParams, t.TypeArgs)
				if len(subSetErrors) > 0 {
					v.errors = slices.Concat(v.errors, subSetErrors)
				}

				// If there are more than one substitution sets, distribute the
				// type arguments across the conditional type.
				if len(substitutionSets) > 1 {
					expandedTypes := make([]Type, len(substitutionSets))
					for i, substitutionSet := range substitutionSets {
						expandedTypes[i] = v.checker.substituteTypeParams(typeAlias.Type, substitutionSet)
					}
					// Create a union type of all expanded types
					expandedType = NewUnionType(nil, expandedTypes...)
				} else {
					substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
					expandedType = v.checker.substituteTypeParams(typeAlias.Type, substitutions)
				}
			} else {
				substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
				expandedType = v.checker.substituteTypeParams(typeAlias.Type, substitutions)
			}
		}

		// Recursively expand the resolved type using the same visitor to maintain state
		if v.expandTypeRefsCount == -1 {
			result, _ := v.checker.expandType(v.ctx, expandedType, -1)
			return result
		}

		result, _ := v.checker.expandType(v.ctx, expandedType, v.expandTypeRefsCount-1)
		return result
	case *TemplateLitType:
		// Expand template literal types by generating all possible string combinations
		// from the cartesian product of the union types in the template
		return v.expandTemplateLitType(t)
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// expandTemplateLitType expands a template literal type by generating all possible
// string combinations from the cartesian product of union types in the template.
// Example: `${0 | 1},${0 | 1}` => "0,0" | "0,1" | "1,0" | "1,1"
func (v *TypeExpansionVisitor) expandTemplateLitType(t *TemplateLitType) Type {
	// Extract the members of each type in the template
	// If a type is a union, we get all its members
	// If it's a literal, we get just that literal
	typeOptions := make([][]Type, len(t.Types))

	for i, t := range t.Types {
		t = Prune(t)
		t, _ = v.checker.expandType(v.ctx, t, -1) // fully expand nested type refs

		if unionType, ok := t.(*UnionType); ok {
			typeOptions[i] = unionType.Types
		} else {
			typeOptions[i] = []Type{t}
		}
	}

	// Generate cartesian product of all type options
	combinations := v.cartesianProduct(typeOptions)

	// Convert each combination into a string literal type
	resultTypes := make([]Type, 0, len(combinations))

	for _, combo := range combinations {
		newQuasis := []*Quasi{}
		newTypes := []Type{}
		currentQuasi := ""

		for i, quasi := range t.Quasis {
			currentQuasi += quasi.Value

			if i < len(combo) {
				// Check if this is a literal type that should be concatenated to currentQuasi
				if litType, ok := combo[i].(*LitType); ok {
					switch lit := litType.Lit.(type) {
					case *StrLit:
						currentQuasi += lit.Value
					case *NumLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *BoolLit:
						currentQuasi += fmt.Sprintf("%v", lit.Value)
					case *BigIntLit:
						currentQuasi += lit.Value.String()
					default:
						// Other literal types: append currentQuasi and add the type
						newQuasis = append(newQuasis, &Quasi{Value: currentQuasi})
						currentQuasi = ""
						newTypes = append(newTypes, combo[i])
					}
				} else {
					// Non-literal types: append currentQuasi and add the type
					newQuasis = append(newQuasis, &Quasi{Value: currentQuasi})
					currentQuasi = ""
					newTypes = append(newTypes, combo[i])
				}
			}
		}

		// Append the final currentQuasi (this is the tail)
		newQuasis = append(newQuasis, &Quasi{Value: currentQuasi})

		// If we have no types (all were literals), convert to a string literal
		if len(newTypes) == 0 {
			resultTypes = append(resultTypes, NewStrLitType(t.Provenance(), newQuasis[0].Value))
		} else {
			// Otherwise, create a new template literal type
			newTemplateLitType := &TemplateLitType{
				Quasis: newQuasis,
				Types:  newTypes,
			}
			newTemplateLitType.SetProvenance(t.Provenance())
			resultTypes = append(resultTypes, newTemplateLitType)
		}
	}

	// Return a union of all possible string literals
	return NewUnionType(t.Provenance(), resultTypes...)
}

// cartesianProduct generates the cartesian product of multiple slices of types
func (v *TypeExpansionVisitor) cartesianProduct(sets [][]Type) [][]Type {
	if len(sets) == 0 {
		return [][]Type{{}}
	}

	// Start with combinations from the first set
	result := make([][]Type, 0)
	for _, item := range sets[0] {
		result = append(result, []Type{item})
	}

	// For each remaining set, combine with existing results
	for i := 1; i < len(sets); i++ {
		var newResult [][]Type
		for _, existing := range result {
			for _, item := range sets[i] {
				combination := make([]Type, len(existing)+1)
				copy(combination, existing)
				combination[len(existing)] = item
				newResult = append(newResult, combination)
			}
		}
		result = newResult
	}

	return result
}

func (c *Checker) expandType(ctx Context, t Type, expandTypeRefsCount int) (Type, []Error) {
	t = Prune(t)
	visitor := NewTypeExpansionVisitor(c, ctx, expandTypeRefsCount)

	result := t.Accept(visitor)
	return result, visitor.errors
}

// AccessKey represents either a property name or an index for accessing object/array elements
type AccessKey interface {
	isAccessKey()
}

type PropertyKey struct {
	Name     string
	OptChain bool
	Span     ast.Span
}

func (pk PropertyKey) isAccessKey() {}

type IndexKey struct {
	Type Type
	Span ast.Span
}

func (ik IndexKey) isAccessKey() {}

// getMemberType is a unified function for getting types from objects via property access or indexing
func (c *Checker) getMemberType(ctx Context, objType Type, key AccessKey) (Type, []Error) {
	errors := []Error{}

	objType = Prune(objType)

	// Repeatedly expand objType until it's either an ObjectType, NamespaceType,
	// or can't be expanded any further
	for {
		expandedType, expandErrors := c.expandType(ctx, objType, 1)
		errors = slices.Concat(errors, expandErrors)

		// If expansion didn't change the type, we're done expanding
		if expandedType == objType {
			break
		}

		objType = expandedType

		// If we've reached an ObjectType or NamespaceType, we can stop expanding
		// since these are the types we can directly get properties from
		if _, ok := objType.(*ObjectType); ok {
			break
		}
		if _, ok := objType.(*NamespaceType); ok {
			break
		}
	}

	switch t := objType.(type) {
	case *MutableType:
		// For mutable types, get the access from the inner type
		return c.getMemberType(ctx, t.Type, key)
	case *TypeRefType:
		// Handle Array access
		if indexKey, ok := key.(IndexKey); ok && QualIdentToString(t.Name) == "Array" {
			unifyErrors := c.unify(ctx, indexKey.Type, NewNumPrimType(nil))
			errors = slices.Concat(errors, unifyErrors)
			return t.TypeArgs[0], errors
		} else if _, ok := key.(IndexKey); ok && QualIdentToString(t.Name) == "Array" {
			errors = append(errors, &ExpectedArrayError{Type: t})
			return NewNeverType(nil), errors
		}

		// For other TypeRefTypes, try to expand the type alias and call getAccessType recursively
		if QualIdentToString(t.Name) == "Error" {
			// Built-in Error type doesn't support property access directly
			errors = append(errors, &ExpectedObjectError{Type: objType})
			return NewNeverType(nil), errors
		}

		expandType, expandErrors := c.expandTypeRef(ctx, t)
		accessType, accessErrors := c.getMemberType(ctx, expandType, key)

		errors = slices.Concat(errors, accessErrors, expandErrors)

		return accessType, errors
	case *TupleType:
		if indexKey, ok := key.(IndexKey); ok {
			if indexLit, ok := indexKey.Type.(*LitType); ok {
				if numLit, ok := indexLit.Lit.(*NumLit); ok {
					index := int(numLit.Value)
					if index < len(t.Elems) {
						return t.Elems[index], errors
					} else {
						errors = append(errors, &OutOfBoundsError{
							Index:  index,
							Length: len(t.Elems),
							span:   indexKey.Span,
						})
						return NewNeverType(nil), errors
					}
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexKey.Type,
				span: indexKey.Span,
			})
			return NewNeverType(nil), errors
		}
		// TupleType doesn't support property access
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(nil), errors
	case *ObjectType:
		return c.getObjectAccess(t, key, errors)
	case *UnionType:
		return c.getUnionAccess(ctx, t, key, errors)
	case *NamespaceType:
		if propKey, ok := key.(PropertyKey); ok {
			if value := t.Namespace.Values[propKey.Name]; value != nil {
				return value.Type, errors
			} else if namespace := t.Namespace.Namespaces[propKey.Name]; namespace != nil {
				return NewNamespaceType(nil, namespace), errors
			} else {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: objType,
					Property:   propKey.Name,
					span:       propKey.Span,
				})
				return NewNeverType(nil), errors
			}
		}
		// NamespaceType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(nil), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(nil), errors
	}
}

func (c *Checker) expandTypeRef(ctx Context, t *TypeRefType) (Type, []Error) {
	// Resolve the type alias
	typeAlias := c.resolveQualifiedTypeAliasFromString(ctx, QualIdentToString(t.Name))
	if typeAlias == nil {
		return NewNeverType(nil), []Error{&UnknownTypeError{TypeName: QualIdentToString(t.Name), typeRef: t}}
	}

	// Expand the type alias
	expandedType := typeAlias.Type

	// Handle type parameter substitution if the type is generic
	if len(typeAlias.TypeParams) > 0 && len(t.TypeArgs) > 0 {
		substitutions := createTypeParamSubstitutions(t.TypeArgs, typeAlias.TypeParams)
		expandedType = c.substituteTypeParams(typeAlias.Type, substitutions)
	}

	return expandedType, []Error{}
}

// getObjectAccess handles property and index access on ObjectType
func (c *Checker) getObjectAccess(objType *ObjectType, key AccessKey, errors []Error) (Type, []Error) {
	switch k := key.(type) {
	case PropertyKey:
		for _, elem := range objType.Elems {
			switch elem := elem.(type) {
			case *PropertyElem:
				if elem.Name == NewStrKey(k.Name) {
					propType := elem.Value
					if elem.Optional {
						propType = NewUnionType(nil, propType, NewUndefinedType(nil))
					}
					return propType, errors
				}
			case *MethodElem:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn, errors
				}
			case *GetterElem:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn.Return, errors
				}
			case *SetterElem:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn.Params[0].Type, errors
				}
			case *ConstructorElem:
			case *CallableElem:
				continue
			default:
				panic(fmt.Sprintf("Unknown object type element: %#v", elem))
			}
		}
		errors = append(errors, &UnknownPropertyError{
			ObjectType: objType,
			Property:   k.Name,
			span:       k.Span,
		})
		return NewNeverType(nil), errors
	case IndexKey:
		if indexLit, ok := k.Type.(*LitType); ok {
			if strLit, ok := indexLit.Lit.(*StrLit); ok {
				for _, elem := range objType.Elems {
					switch elem := elem.(type) {
					case *PropertyElem:
						if elem.Name == NewStrKey(strLit.Value) {
							propType := elem.Value
							if elem.Optional {
								propType = NewUnionType(nil, propType, NewUndefinedType(nil))
							}
							return propType, errors
						}
					case *MethodElem:
						if elem.Name == NewStrKey(strLit.Value) {
							return elem.Fn, errors
						}
					default:
						panic(fmt.Sprintf("Unknown object type element: %#v", elem))
					}
				}
			}
		}
		errors = append(errors, &InvalidObjectKeyError{
			Key:  k.Type,
			span: k.Span,
		})
		return NewNeverType(nil), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(nil), errors
	}
}

func (c *Checker) getDefinedElems(unionType *UnionType) []Type {
	definedElems := []Type{}
	for _, elem := range unionType.Types {
		elem = Prune(elem)
		switch elem := elem.(type) {
		case *LitType:
			switch elem.Lit.(type) {
			case *NullLit:
				continue
			case *UndefinedLit:
				continue
			default:
				definedElems = append(definedElems, elem)
			}
		default:
			definedElems = append(definedElems, elem)
		}
	}

	return definedElems
}

// getUnionAccess handles property and index access on UnionType
func (c *Checker) getUnionAccess(ctx Context, unionType *UnionType, key AccessKey, errors []Error) (Type, []Error) {
	propKey, isPropertyKey := key.(PropertyKey)

	definedElems := c.getDefinedElems(unionType)

	undefinedCount := len(unionType.Types) - len(definedElems)
	if undefinedCount == 0 {
		errors = append(errors, &ExpectedObjectError{Type: unionType})
		return NewNeverType(nil), errors
	}

	if len(definedElems) == 1 {
		if undefinedCount == 0 {
			return c.getMemberType(ctx, definedElems[0], key)
		}

		if undefinedCount > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType})
			return NewNeverType(nil), errors
		}

		pType, pErrors := c.getMemberType(ctx, definedElems[0], key)
		errors = slices.Concat(errors, pErrors)
		propType := NewUnionType(nil, pType, NewUndefinedType(nil))
		return propType, errors
	}

	if len(definedElems) > 1 {
		panic("TODO: handle getting property from union type with multiple defined elements")
	}

	return NewNeverType(nil), errors
}

func (c *Checker) astKeyToTypeKey(ctx Context, key ast.ObjKey) (*ObjTypeKey, []Error) {
	switch key := key.(type) {
	case *ast.IdentExpr:
		newKey := NewStrKey(key.Name)
		return &newKey, nil
	case *ast.StrLit:
		newKey := NewStrKey(key.Value)
		return &newKey, nil
	case *ast.NumLit:
		newKey := NewNumKey(key.Value)
		return &newKey, nil
	case *ast.ComputedKey:
		// TODO: return the error
		keyType, _ := c.inferExpr(ctx, key.Expr) // infer the expression for side-effects

		switch t := Prune(keyType).(type) {
		case *LitType:
			switch lit := t.Lit.(type) {
			case *StrLit:
				newKey := NewStrKey(lit.Value)
				return &newKey, nil
			case *NumLit:
				newKey := NewNumKey(lit.Value)
				return &newKey, nil
			default:
				return nil, []Error{&InvalidObjectKeyError{Key: t, span: key.Span()}}
			}
		case *UniqueSymbolType:
			newKey := NewSymKey(t.Value)
			return &newKey, nil
		default:
			panic(&InvalidObjectKeyError{Key: t, span: key.Span()})
		}
	default:
		panic(fmt.Sprintf("Unknown object key type: %T", key))
	}
}

// inferBlock infers the types of all statements in a block and returns the type
// of the block. The type of the block is the type of the last statement if it's
// an expression statement, otherwise it returns the provided default type.
func (c *Checker) inferBlock(ctx Context, block *ast.Block, defaultType Type) (Type, []Error) {
	errors := []Error{}

	// Process all statements in the block
	for _, stmt := range block.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	// The type of the block is the type of the last statement if it's an expression
	resultType := defaultType
	if len(block.Stmts) > 0 {
		lastStmt := block.Stmts[len(block.Stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			if inferredType := exprStmt.Expr.InferredType(); inferredType != nil {
				resultType = inferredType
			}
		}
	}

	return resultType, errors
}

func (c *Checker) inferIfElse(ctx Context, expr *ast.IfElseExpr) (Type, []Error) {
	condType, condErrors := c.inferExpr(ctx, expr.Cond)
	unifyErrors := c.unify(ctx, condType, NewBoolPrimType(nil))
	errors := slices.Concat(condErrors, unifyErrors)

	// Infer the consequent block
	consType, consErrors := c.inferBlock(ctx, &expr.Cons, NewNeverType(nil))
	errors = slices.Concat(errors, consErrors)

	var altType Type = NewNeverType(nil)
	if expr.Alt != nil {
		alt := expr.Alt
		if alt.Block != nil {
			var altErrors []Error
			altType, altErrors = c.inferBlock(ctx, alt.Block, NewNeverType(nil))
			errors = slices.Concat(errors, altErrors)
		} else if alt.Expr != nil {
			t, altErrors := c.inferExpr(ctx, alt.Expr)
			errors = slices.Concat(errors, altErrors)
			altType = t
		} else {
			panic("alt must be a block or expression")
		}
	}

	t := NewUnionType(nil, consType, altType)
	expr.SetInferredType(t)

	return t, errors
}

func (c *Checker) inferDoExpr(ctx Context, expr *ast.DoExpr) (Type, []Error) {
	// Infer the body block - default to undefined if no expression at the end
	resultType, errors := c.inferBlock(ctx, &expr.Body, NewUndefinedType(nil))

	expr.SetInferredType(resultType)
	return resultType, errors
}

func (c *Checker) inferMatchExpr(ctx Context, expr *ast.MatchExpr) (Type, []Error) {
	errors := []Error{}

	// Infer the type of the target expression
	targetType, targetErrors := c.inferExpr(ctx, expr.Target)
	errors = slices.Concat(errors, targetErrors)

	// Collect the types of all case bodies
	caseTypes := make([]Type, 0, len(expr.Cases))

	for _, matchCase := range expr.Cases {
		// Create a new scope for this case to handle pattern bindings
		caseCtx := ctx.WithNewScope()

		// Infer the pattern type and get bindings
		patternType, patternBindings, patternErrors := c.inferPattern(caseCtx, matchCase.Pattern)
		errors = slices.Concat(errors, patternErrors)

		// Add pattern bindings to the case scope
		for name, binding := range patternBindings {
			caseCtx.Scope.setValue(name, binding)
		}

		// Unify the pattern type with the target type to ensure they're compatible
		// The pattern type must be a subtype of the target type.
		// This is opposite of what we do when inferring variable declarations.
		unifyErrors := c.unify(caseCtx, patternType, targetType)
		errors = slices.Concat(errors, unifyErrors)

		// If there's a guard, check that it's a boolean
		if matchCase.Guard != nil {
			guardType, guardErrors := c.inferExpr(caseCtx, matchCase.Guard)
			errors = slices.Concat(errors, guardErrors)

			guardUnifyErrors := c.unify(caseCtx, guardType, NewBoolPrimType(nil))
			errors = slices.Concat(errors, guardUnifyErrors)
		}

		// Infer the type of the case body
		var caseType Type
		if matchCase.Body.Block != nil {
			// Handle block body using the helper function
			var caseErrors []Error
			caseType, caseErrors = c.inferBlock(caseCtx, matchCase.Body.Block, NewUndefinedType(nil))
			errors = slices.Concat(errors, caseErrors)
		} else if matchCase.Body.Expr != nil {
			// Handle expression body
			var caseErrors []Error
			caseType, caseErrors = c.inferExpr(caseCtx, matchCase.Body.Expr)
			errors = slices.Concat(errors, caseErrors)
		} else {
			// This shouldn't happen with a well-formed AST
			caseType = NewNeverType(nil)
		}

		caseTypes = append(caseTypes, caseType)
	}

	// The type of the match expression is the union of all case types
	resultType := NewUnionType(nil, caseTypes...)

	expr.SetInferredType(resultType)
	return resultType, errors
}

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
	default:
		panic(fmt.Sprintf("Unknown statement type: %T", stmt))
	}
}

// createTypeParamSubstitutions creates a map of type parameter substitutions from type arguments and type parameters,
// handling default values when type arguments are nil.
func createTypeParamSubstitutions(typeArgs []Type, typeParams []*TypeParam) map[string]Type {
	substitutions := make(map[string]Type, len(typeArgs))
	for typeArg, param := range Zip(typeArgs, typeParams) {
		if param.Default != nil && typeArg == nil {
			// Use the default type if the type argument is nil
			substitutions[param.Name] = param.Default
		} else {
			substitutions[param.Name] = typeArg
		}
	}
	return substitutions
}

func Zip[T, U any](t []T, u []U) iter.Seq2[T, U] {
	return func(yield func(T, U) bool) {
		for i := range min(len(t), len(u)) { // range over int (Go 1.22)
			if !yield(t[i], u[i]) {
				return
			}
		}
	}
}

func (c *Checker) inferFuncParams(
	ctx Context,
	funcParams []*ast.Param,
) ([]*FuncParam, map[string]*Binding, []Error) {
	errors := []Error{}
	bindings := map[string]*Binding{}
	params := make([]*FuncParam, len(funcParams))

	for i, param := range funcParams {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)

		errors = slices.Concat(errors, patErrors)

		var typeAnn Type
		if param.TypeAnn == nil {
			typeAnn = c.FreshVar(nil)
		} else {
			var typeAnnErrors []Error
			typeAnn, typeAnnErrors = c.inferTypeAnn(ctx, param.TypeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
		}

		// TODO: handle type annotations on parameters
		c.unify(ctx, patType, typeAnn)

		maps.Copy(bindings, patBindings)

		params[i] = &FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: param.Optional,
		}
	}

	return params, bindings, errors
}

// NOTE: A new context should be created before calling this function in order
// to contain any type parameters in scope.
// Returns:
// - the inferred function type
// - the new context with type parameters in scope
// - a map of parameter bindings
// - any errors encountered during inference
// TODO: Accept an ast.Node parameter so that we can set provenance on the
// inferred type.
func (c *Checker) inferFuncSig(
	ctx Context,
	sig *ast.FuncSig, // TODO: make FuncSig an interface
	node ast.Node,
) (*FuncType, Context, map[string]*Binding, []Error) {
	errors := []Error{}

	// Create a new context with type parameters in scope
	funcCtx := ctx.WithNewScope()

	// Handle generic functions by creating type parameters
	typeParams := []*TypeParam{}
	for _, tp := range sig.TypeParams {
		var defaultType Type
		var constraintType Type
		if tp.Default != nil {
			var defaultErrors []Error
			defaultType, defaultErrors = c.inferTypeAnn(ctx, tp.Default)
			defaultType.SetProvenance(&ast.NodeProvenance{Node: tp.Default})
			errors = slices.Concat(errors, defaultErrors)
		}
		if tp.Constraint != nil {
			var constraintErrors []Error
			constraintType, constraintErrors = c.inferTypeAnn(ctx, tp.Constraint)
			constraintType.SetProvenance(&ast.NodeProvenance{Node: tp.Constraint})
			errors = slices.Concat(errors, constraintErrors)
		}
		typeParam := &TypeParam{
			Name:       tp.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
		typeParams = append(typeParams, typeParam)

		var t Type = NewUnknownType(nil)
		if typeParam.Constraint != nil {
			t = typeParam.Constraint
		}
		funcCtx.Scope.setTypeAlias(typeParam.Name, &TypeAlias{
			Type:       t,
			TypeParams: []*TypeParam{},
		})
		fmt.Fprintf(os.Stderr, "Added type param %s to scope with type %s\n", typeParam.Name, t.String())
	}

	params, bindings, paramErrors := c.inferFuncParams(funcCtx, sig.Params)
	errors = slices.Concat(errors, paramErrors)

	var returnType Type
	if sig.Return == nil {
		returnType = c.FreshVar(nil)
	} else {
		var returnErrors []Error
		returnType, returnErrors = c.inferTypeAnn(funcCtx, sig.Return)
		errors = slices.Concat(errors, returnErrors)
	}

	var throwsType Type
	if sig.Throws == nil {
		// If no throws clause is specified, we use a fresh type variable which
		// will be unified later if any throw expressions are found in the
		// function body.
		throwsType = c.FreshVar(nil)
	} else {
		var throwsErrors []Error
		throwsType, throwsErrors = c.inferTypeAnn(funcCtx, sig.Throws)
		errors = slices.Concat(errors, throwsErrors)
	}

	// For async functions, wrap the return type in a Promise<T, E>
	var finalReturnType Type
	var finalThrowsType Type
	if sig.Async {
		// For async functions, check if the user explicitly specified a Promise return type
		if promiseType, ok := returnType.(*TypeRefType); ok && QualIdentToString(promiseType.Name) == "Promise" {
			// User explicitly specified Promise<T, E>, use it as-is
			finalReturnType = returnType
			finalThrowsType = NewNeverType(nil) // Async functions don't throw directly
		} else {
			// User didn't specify Promise, wrap the return type
			promiseAlias := ctx.Scope.getTypeAlias("Promise")
			if promiseAlias != nil {
				finalReturnType = NewTypeRefType(nil, "Promise", promiseAlias, returnType, throwsType)
				finalThrowsType = NewNeverType(nil) // Async functions don't throw directly
			} else {
				// Fallback if Promise type is not available
				finalReturnType = returnType
				finalThrowsType = throwsType
			}
		}
	} else {
		finalReturnType = returnType
		finalThrowsType = throwsType
	}

	t := NewFuncType(
		&ast.NodeProvenance{Node: node},
		typeParams,
		params,
		finalReturnType,
		finalThrowsType,
	)

	return t, funcCtx, bindings, errors
}

type ReturnVisitor struct {
	ast.DefaultVisitor
	Returns []*ast.ReturnStmt
}

func (v *ReturnVisitor) EnterStmt(stmt ast.Stmt) bool {
	if returnStmt, ok := stmt.(*ast.ReturnStmt); ok {
		v.Returns = append(v.Returns, returnStmt)
	}

	return true
}
func (v *ReturnVisitor) EnterExpr(expr ast.Expr) bool {
	// Don't visit function expressions since we don't want to include any
	// return statements inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// return statements inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}
func (v *ReturnVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	// An expression like if/else could have a return statement inside one of
	// its branches.
	return true
}

type ThrowVisitor struct {
	ast.DefaultVisitor
	Throws []*ast.ThrowExpr
}

func (v *ThrowVisitor) EnterExpr(expr ast.Expr) bool {
	if throwExpr, ok := expr.(*ast.ThrowExpr); ok {
		v.Throws = append(v.Throws, throwExpr)
	}

	// Don't visit function expressions since we don't want to include any
	// throw expressions inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *ThrowVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// throw expressions inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

func (v *ThrowVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	// An expression like if/else could have a throw expression inside one of
	// its branches.
	return true
}

type AwaitVisitor struct {
	ast.DefaultVisitor
	Awaits []*ast.AwaitExpr
}

func (v *AwaitVisitor) EnterExpr(expr ast.Expr) bool {
	if awaitExpr, ok := expr.(*ast.AwaitExpr); ok {
		v.Awaits = append(v.Awaits, awaitExpr)
	}

	// Don't visit function expressions since we don't want to include any
	// await expressions inside them.
	if _, ok := expr.(*ast.FuncExpr); ok {
		return false
	}
	return true
}

func (v *AwaitVisitor) EnterDecl(decl ast.Decl) bool {
	// Don't visit function declarations since we don't want to include any
	// await expressions inside them.
	if _, ok := decl.(*ast.FuncDecl); ok {
		return false
	}
	return true
}

func (v *AwaitVisitor) EnterObjExprElem(elem ast.ObjExprElem) bool {
	return true
}

// NOTE: This function updates `funcSigType`
func (c *Checker) inferFuncBodyWithFuncSigType(
	ctx Context,
	funcSigType *FuncType,
	paramBindings map[string]*Binding,
	body *ast.Block,
	isAsync bool,
) []Error {
	errors := []Error{}

	// Create async context if this is an async function
	bodyCtx := ctx.WithNewScope()
	bodyCtx.IsAsync = isAsync

	returnType, inferredThrowType, bodyErrors := c.inferFuncBody(bodyCtx, paramBindings, body)
	errors = slices.Concat(errors, bodyErrors)

	// For async functions, we need to handle Promise return types differently
	if isAsync {
		// For async functions, the funcType.Return is Promise<T, E>
		// We need to unify the inferred return and throw types with the Promise type args
		if promiseType, ok := funcSigType.Return.(*TypeRefType); ok && QualIdentToString(promiseType.Name) == "Promise" {
			if len(promiseType.TypeArgs) >= 2 {
				unifyErrors := c.unify(ctx, returnType, promiseType.TypeArgs[0])
				unifyThrowsErrors := c.unify(ctx, inferredThrowType, promiseType.TypeArgs[1])
				errors = slices.Concat(errors, unifyErrors, unifyThrowsErrors)
			}
		}
	} else {
		// For non-async functions, use the original logic
		unifyReturnErrors := c.unify(ctx, returnType, funcSigType.Return)
		unifyThrowsErrors := c.unify(ctx, inferredThrowType, funcSigType.Throws)
		errors = slices.Concat(errors, unifyReturnErrors, unifyThrowsErrors)
	}

	return errors
}

// Infer throws type - handles throws clause inference
// NOTE: This function updates `funcSigType`
func (c *Checker) inferFuncBody(
	ctx Context,
	bindings map[string]*Binding,
	body *ast.Block,
) (Type, Type, []Error) {

	ctx = ctx.WithNewScope()
	maps.Copy(ctx.Scope.Namespace.Values, bindings)

	errors := []Error{}
	for _, stmt := range body.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	visitor := &ReturnVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Returns:        []*ast.ReturnStmt{},
	}

	throwVisitor := &ThrowVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Throws:         []*ast.ThrowExpr{},
	}

	awaitVisitor := &AwaitVisitor{
		DefaultVisitor: ast.DefaultVisitor{},
		Awaits:         []*ast.AwaitExpr{},
	}

	for _, stmt := range body.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(visitor)
		stmt.Accept(throwVisitor)
		stmt.Accept(awaitVisitor)
	}

	returnTypes := []Type{}
	for _, returnStmt := range visitor.Returns {
		if returnStmt.Expr != nil {
			returnType := returnStmt.Expr.InferredType()
			returnTypes = append(returnTypes, returnType)
		}
	}

	throwTypes := []Type{}
	for _, throwExpr := range throwVisitor.Throws {
		throwType, throwErrors := c.inferExpr(ctx, throwExpr.Arg)
		throwTypes = append(throwTypes, throwType)
		errors = slices.Concat(errors, throwErrors)
	}

	// Collect throw types from await expressions (Promise rejection types)
	for _, awaitExpr := range awaitVisitor.Awaits {
		if awaitExpr.Throws != nil {
			throwTypes = append(throwTypes, awaitExpr.Throws)
		}
	}

	// TODO: We also need to do dead code analysis to account for unreachable
	// code.

	var returnType Type
	if len(returnTypes) == 1 {
		returnType = returnTypes[0]
	} else if len(returnTypes) > 1 {
		returnType = NewUnionType(nil, returnTypes...)
	} else {
		returnType = NewUndefinedType(nil)
	}

	throwType := NewUnionType(nil, throwTypes...)

	return returnType, throwType, errors
}

func patToPat(p ast.Pat) Pat {
	switch p := p.(type) {
	case *ast.IdentPat:
		return &IdentPat{Name: p.Name}
	case *ast.LitPat:
		panic("TODO: handle literal pattern")
		// return &LitPat{Lit: p.Lit}
	case *ast.TuplePat:
		elems := make([]Pat, len(p.Elems))
		for i, elem := range p.Elems {
			elems[i] = patToPat(elem)
		}
		return &TuplePat{Elems: elems}
	case *ast.ObjectPat:
		elems := make([]ObjPatElem, len(p.Elems))
		for i, elem := range p.Elems {
			switch elem := elem.(type) {
			case *ast.ObjKeyValuePat:
				elems[i] = &ObjKeyValuePat{
					Key:   elem.Key.Name,
					Value: patToPat(elem.Value),
				}
			case *ast.ObjShorthandPat:
				elems[i] = &ObjShorthandPat{
					Key: elem.Key.Name,
				}
			case *ast.ObjRestPat:
				elems[i] = &ObjRestPat{
					Pattern: patToPat(elem.Pattern),
				}
			default:
				panic("unknown object pattern element type")
			}
		}
		return &ObjectPat{Elems: elems}
	case *ast.ExtractorPat:
		args := make([]Pat, len(p.Args))
		for i, arg := range p.Args {
			args[i] = patToPat(arg)
		}
		return &ExtractorPat{Name: ast.QualIdentToString(p.Name), Args: args}
	case *ast.RestPat:
		return &RestPat{Pattern: patToPat(p.Pattern)}
	default:
		panic("unknown pattern type: " + fmt.Sprintf("%T", p))
	}
}

func (c *Checker) inferLit(lit ast.Lit) (Type, []Error) {
	provenance := &ast.NodeProvenance{Node: lit}

	var t Type
	errors := []Error{}
	switch lit := lit.(type) {
	case *ast.StrLit:
		t = NewStrLitType(provenance, lit.Value)
	case *ast.NumLit:
		t = NewNumLitType(provenance, lit.Value)
	case *ast.BoolLit:
		t = NewBoolLitType(provenance, lit.Value)
	case *ast.RegexLit:
		// TODO: createa a separate type for regex literals
		t, _ = NewRegexTypeWithPatternString(provenance, lit.Value)
	case *ast.BigIntLit:
		t = NewBigIntLitType(provenance, lit.Value)
	case *ast.NullLit:
		t = NewNullType(provenance)
	case *ast.UndefinedLit:
		t = NewUndefinedType(provenance)
	default:
		panic(fmt.Sprintf("Unknown literal type: %T", lit))
	}

	return t, errors
}

func (c *Checker) inferPattern(
	ctx Context,
	pattern ast.Pat,
) (Type, map[string]*Binding, []Error) {

	bindings := map[string]*Binding{}
	var inferPatRec func(ast.Pat) (Type, []Error)

	inferPatRec = func(pat ast.Pat) (Type, []Error) {
		var t Type
		var errors []Error
		provenance := &ast.NodeProvenance{Node: pat}

		switch p := pat.(type) {
		case *ast.IdentPat:
			if p.TypeAnn != nil {
				// TODO: check if there's a default value, infer it, and unify
				// it with the type annotation.
				t, errors = c.inferTypeAnn(ctx, p.TypeAnn)
			} else {
				tvar := c.FreshVar(provenance)
				if p.Default != nil {
					defaultType, defaultErrors := c.inferExpr(ctx, p.Default)
					errors = append(errors, defaultErrors...)
					tvar.Default = defaultType
				}
				t = tvar
			}

			// TODO: report an error if the name is already bound
			bindings[p.Name] = &Binding{
				Source:  provenance,
				Type:    t,
				Mutable: false, // TODO
			}
		case *ast.LitPat:
			t, errors = c.inferLit(p.Lit)
		case *ast.TuplePat:
			elems := make([]Type, len(p.Elems))
			for i, elem := range p.Elems {
				elemType, elemErrors := inferPatRec(elem)
				elems[i] = elemType
				errors = append(errors, elemErrors...)
			}
			t = NewTupleType(provenance, elems...)
		case *ast.ObjectPat:
			elems := []ObjTypeElem{}
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := NewStrKey(elem.Key.Name)
					prop := NewPropertyElem(name, t)
					prop.Optional = false
					elems = append(elems, prop)
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					var t Type
					if elem.TypeAnn != nil {
						// TODO: check if there's a default value, infer it, and unify
						// it with the type annotation.
						elemType, elemErrors := c.inferTypeAnn(ctx, elem.TypeAnn)
						t = elemType
						errors = append(errors, elemErrors...)
					} else {
						tvar := c.FreshVar(&ast.NodeProvenance{Node: elem})
						if elem.Default != nil {
							defaultType, defaultErrors := c.inferExpr(ctx, elem.Default)
							errors = append(errors, defaultErrors...)
							tvar.Default = defaultType
						}
						t = tvar
					}
					name := NewStrKey(elem.Key.Name)
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = &Binding{
						Source:  &ast.NodeProvenance{Node: elem.Key},
						Type:    t,
						Mutable: false, // TODO
					}
					prop := NewPropertyElem(name, t)
					elems = append(elems, prop)
				case *ast.ObjRestPat:
					t, restErrors := inferPatRec(elem.Pattern)
					errors = slices.Concat(errors, restErrors)
					elems = append(elems, NewRestSpreadElem(t))
				}
			}
			t = NewObjectType(provenance, elems)
		case *ast.ExtractorPat:
			if binding := c.resolveQualifiedValue(ctx, p.Name); binding != nil {
				args := make([]Type, len(p.Args))
				for i, arg := range p.Args {
					argType, argErrors := inferPatRec(arg)
					args[i] = argType
					errors = append(errors, argErrors...)
				}
				t = NewExtractorType(provenance, binding.Type, args...)
			} else {
				// TODO: generate an error for unresolved identifier
				t = NewNeverType(nil)
			}
		case *ast.InstancePat:
			patType, patBindings, patErrors := c.inferPattern(ctx, p.Object)
			typeAlias := c.resolveQualifiedTypeAlias(ctx, p.ClassName)

			for name, binding := range patBindings {
				bindings[name] = binding
			}

			typeAliasType := Prune(typeAlias.Type)

			if clsType, ok := typeAliasType.(*ObjectType); ok {
				if patType, ok := Prune(patType).(*ObjectType); ok {
					// We know that the object type inferred from this pattern
					// must be an instance of the class type, so we set the ID
					// of the pattern type to be the same as the class type.
					// Without this, the unify call below would fail because
					// an object type without a matching ID is not assignable
					// to an object type with a non-zero ID.
					patType.Nominal = true
					patType.ID = clsType.ID
				}
			}

			unifyErrors := c.unify(ctx, patType, typeAliasType)

			errors = append(errors, patErrors...)
			errors = append(errors, unifyErrors...)

			t = typeAliasType
		case *ast.RestPat:
			argType, argErrors := inferPatRec(p.Pattern)
			errors = append(errors, argErrors...)
			t = NewRestSpreadType(provenance, argType)
		case *ast.WildcardPat:
			t = c.FreshVar(&ast.NodeProvenance{Node: pat})
			errors = []Error{}
		}

		t.SetProvenance(provenance)
		pat.SetInferredType(t)
		return t, errors
	}

	t, errors := inferPatRec(pattern)
	t.SetProvenance(&ast.NodeProvenance{
		Node: pattern,
	})
	pattern.SetInferredType(t)

	return t, bindings, errors
}

func (c *Checker) inferTypeDecl(
	ctx Context,
	decl *ast.TypeDecl,
) (*TypeAlias, []Error) {
	errors := []Error{}

	typeParams := make([]*TypeParam, len(decl.TypeParams))
	for i, typeParam := range decl.TypeParams {
		var constraintType Type
		var defaultType Type
		if typeParam.Constraint != nil {
			var constraintErrors []Error
			constraintType, constraintErrors = c.inferTypeAnn(ctx, typeParam.Constraint)
			errors = slices.Concat(errors, constraintErrors)
		}
		if typeParam.Default != nil {
			var defaultErrors []Error
			defaultType, defaultErrors = c.inferTypeAnn(ctx, typeParam.Default)
			errors = slices.Concat(errors, defaultErrors)
		}
		typeParams[i] = &TypeParam{
			Name:       typeParam.Name,
			Constraint: constraintType,
			Default:    defaultType,
		}
	}

	// Create a new context with type parameters in scope
	typeCtx := ctx
	if len(typeParams) > 0 {
		// Create a new scope that includes the type parameters
		typeScope := ctx.Scope.WithNewScope()

		// Add type parameters as type aliases to the scope
		for _, typeParam := range typeParams {
			typeParamTypeRef := NewTypeRefType(nil, typeParam.Name, nil)
			typeParamAlias := &TypeAlias{
				Type:       typeParamTypeRef,
				TypeParams: []*TypeParam{},
			}
			typeScope.setTypeAlias(typeParam.Name, typeParamAlias)
		}

		typeCtx = Context{
			Scope:      typeScope,
			IsAsync:    ctx.IsAsync,
			IsPatMatch: ctx.IsPatMatch,
		}
	}

	t, typeErrors := c.inferTypeAnn(typeCtx, decl.TypeAnn)
	errors = slices.Concat(errors, typeErrors)

	typeAlias := TypeAlias{
		Type:       t,
		TypeParams: typeParams,
	}

	return &typeAlias, errors
}

func (c *Checker) inferFuncTypeAnn(
	ctx Context,
	funcTypeAnn *ast.FuncTypeAnn,
) (*FuncType, []Error) {
	errors := []Error{}
	params := make([]*FuncParam, len(funcTypeAnn.Params))
	for i, param := range funcTypeAnn.Params {
		patType, patBindings, patErrors := c.inferPattern(ctx, param.Pattern)
		errors = slices.Concat(errors, patErrors)

		// TODO: make type annoations required on parameters in function type
		// annotations
		var typeAnn Type
		if param.TypeAnn == nil {
			typeAnn = c.FreshVar(nil)
		} else {
			var typeAnnErrors []Error
			typeAnn, typeAnnErrors = c.inferTypeAnn(ctx, param.TypeAnn)
			errors = slices.Concat(errors, typeAnnErrors)
		}

		c.unify(ctx, patType, typeAnn)

		maps.Copy(ctx.Scope.Namespace.Values, patBindings)

		params[i] = &FuncParam{
			Pattern:  patToPat(param.Pattern),
			Type:     typeAnn,
			Optional: param.Optional,
		}
	}
	returnType, returnErrors := c.inferTypeAnn(ctx, funcTypeAnn.Return)
	errors = slices.Concat(errors, returnErrors)

	funcType := FuncType{
		Params:     params,
		Return:     returnType,
		Throws:     NewNeverType(nil),
		TypeParams: []*TypeParam{},
	}

	return &funcType, errors
}

// resolveQualifiedTypeAliasFromString resolves a qualified type name from a string representation
func (c *Checker) resolveQualifiedTypeAliasFromString(ctx Context, qualifiedName string) *TypeAlias {
	// Simple case: no dots, just a regular identifier
	if !strings.Contains(qualifiedName, ".") {
		return ctx.Scope.getTypeAlias(qualifiedName)
	}

	// Split the qualified name and traverse namespaces
	parts := strings.Split(qualifiedName, ".")
	if len(parts) < 2 {
		return ctx.Scope.getTypeAlias(qualifiedName)
	}

	// Start from the current scope and traverse through namespaces
	// We use .getNamespace() here since it'll look through the current scope
	// and any parent scopes as needed.
	namespace := ctx.Scope.getNamespace(parts[0])

	// Navigate through all but the last part (which is the type name)
	for _, part := range parts[1 : len(parts)-1] {
		namespace = namespace.Namespaces[part]
	}

	// Look for the type in the final namespace using the proper scope method
	typeName := parts[len(parts)-1]
	return namespace.Types[typeName]
}

// resolveQualifiedTypeAlias resolves a qualified type name by traversing namespace hierarchy
func (c *Checker) resolveQualifiedTypeAlias(ctx Context, qualIdent ast.QualIdent) *TypeAlias {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.getTypeAlias(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if typeAlias, ok := leftNamespace.Types[qi.Right.Name]; ok {
			return typeAlias
		}
		return nil
	default:
		return nil
	}
}

func (c *Checker) resolveQualifiedValue(ctx Context, qualIdent ast.QualIdent) *Binding {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, use existing scope lookup
		return ctx.Scope.getValue(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B.Type
		// First resolve the left part (A.B)
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the type in the resolved namespace
		if binding, ok := leftNamespace.Values[qi.Right.Name]; ok {
			return binding
		}
		return nil
	default:
		return nil
	}
}

// resolveQualifiedNamespace resolves a qualified identifier to a namespace
func (c *Checker) resolveQualifiedNamespace(ctx Context, qualIdent ast.QualIdent) *Namespace {
	switch qi := qualIdent.(type) {
	case *ast.Ident:
		// Simple identifier, check if it's a namespace
		return ctx.Scope.getNamespace(qi.Name)
	case *ast.Member:
		// Qualified identifier like A.B
		// First resolve the left part
		leftNamespace := c.resolveQualifiedNamespace(ctx, qi.Left)
		if leftNamespace == nil {
			return nil
		}
		// Then look for the right part in the resolved namespace
		if namespace, ok := leftNamespace.Namespaces[qi.Right.Name]; ok {
			return namespace
		}
		return nil
	default:
		return nil
	}
}

func (c *Checker) inferTypeAnn(
	ctx Context,
	typeAnn ast.TypeAnn,
) (Type, []Error) {
	errors := []Error{}
	var t Type = NewNeverType(nil)
	provenance := &ast.NodeProvenance{Node: typeAnn}

	switch typeAnn := typeAnn.(type) {
	case *ast.TypeRefTypeAnn:
		typeName := ast.QualIdentToString(typeAnn.Name)
		typeAlias := c.resolveQualifiedTypeAlias(ctx, typeAnn.Name)
		if typeAlias != nil {
			typeArgs := make([]Type, len(typeAnn.TypeArgs))
			for i, typeArg := range typeAnn.TypeArgs {
				typeArgType, typeArgErrors := c.inferTypeAnn(ctx, typeArg)
				typeArgs[i] = typeArgType
				errors = slices.Concat(errors, typeArgErrors)
			}

			t = NewTypeRefType(provenance, typeName, typeAlias, typeArgs...)
		} else {
			// TODO: include type args
			typeRef := NewTypeRefType(provenance, typeName, nil, nil)
			errors = append(errors, &UnknownTypeError{TypeName: typeName, typeRef: typeRef})
		}
	case *ast.NumberTypeAnn:
		t = NewNumPrimType(provenance)
	case *ast.StringTypeAnn:
		t = NewStrPrimType(provenance)
	case *ast.BooleanTypeAnn:
		t = NewBoolPrimType(provenance)
	case *ast.SymbolTypeAnn:
		t = NewSymPrimType(provenance)
	case *ast.UniqueSymbolTypeAnn:
		c.SymbolID++
		t = NewUniqueSymbolType(provenance, c.SymbolID)
	case *ast.AnyTypeAnn:
		t = NewAnyType(provenance)
	case *ast.UnknownTypeAnn:
		t = NewUnknownType(provenance)
	case *ast.NeverTypeAnn:
		t = NewNeverType(provenance)
	case *ast.LitTypeAnn:
		switch lit := typeAnn.Lit.(type) {
		case *ast.StrLit:
			t = NewStrLitType(provenance, lit.Value)
		case *ast.NumLit:
			t = NewNumLitType(provenance, lit.Value)
		case *ast.BoolLit:
			t = NewBoolLitType(provenance, lit.Value)
		case *ast.RegexLit:
			t, _ = NewRegexTypeWithPatternString(provenance, lit.Value)
		case *ast.BigIntLit:
			t = NewBigIntLitType(provenance, lit.Value)
		case *ast.NullLit:
			t = NewNullType(provenance)
		case *ast.UndefinedLit:
			t = NewUndefinedType(provenance)
		default:
			panic(fmt.Sprintf("Unknown literal type: %T", lit))
		}
	case *ast.TupleTypeAnn:
		elems := make([]Type, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			elemType, elemErrors := c.inferTypeAnn(ctx, elem)
			elems[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		t = NewTupleType(provenance, elems...)
	case *ast.ObjectTypeAnn:
		elems := make([]ObjTypeElem, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			switch elem := elem.(type) {
			case *ast.CallableTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &CallableElem{Fn: fn}
			case *ast.ConstructorTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &ConstructorElem{Fn: fn}
			case *ast.MethodTypeAnn:
				// TODO: handle `self` and `mut self` parameters
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = NewMethodElem(*key, fn, nil)
			case *ast.GetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &GetterElem{Name: *key, Fn: fn}
			case *ast.SetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &SetterElem{Name: *key, Fn: fn}
			case *ast.PropertyTypeAnn:
				var t Type
				if elem.Value != nil {
					typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, elem.Value)
					errors = slices.Concat(errors, typeAnnErrors)
					t = typeAnnType
				} else {
					t = NewUndefinedType(nil)
				}
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				elems[i] = &PropertyElem{
					Name:     *key,
					Optional: elem.Optional,
					Readonly: elem.Readonly,
					Value:    t,
				}
			case *ast.MappedTypeAnn:
				panic("TODO: handle MappedTypeAnn")
			case *ast.RestSpreadTypeAnn:
				panic("TODO: handle RestSpreadTypeAnn")
			}
		}

		t = NewObjectType(provenance, elems)
	case *ast.UnionTypeAnn:
		types := make([]Type, len(typeAnn.Types))
		for i, unionType := range typeAnn.Types {
			unionElemType, unionElemErrors := c.inferTypeAnn(ctx, unionType)
			types[i] = unionElemType
			errors = slices.Concat(errors, unionElemErrors)
		}
		t = NewUnionType(nil, types...)
	case *ast.FuncTypeAnn:
		funcType, funcErrors := c.inferFuncTypeAnn(ctx, typeAnn)
		t = funcType
		errors = slices.Concat(errors, funcErrors)
	case *ast.CondTypeAnn:
		// TODO: this needs to be done in the Enter method of the visitor
		// so that we can we can replace InferType nodes with fresh type variables
		// and computing a new context with those infer types in scope before
		// inferring the Cons and Alt branches.
		// This only affects nested conditional types.
		checkType, checkErrors := c.inferTypeAnn(ctx, typeAnn.Check)
		errors = slices.Concat(errors, checkErrors)

		extendsType, extendsErrors := c.inferTypeAnn(ctx, typeAnn.Extends)
		errors = slices.Concat(errors, extendsErrors)

		// Find all InferType nodes in the extends type and create type aliases for them
		inferTypesMap := c.findInferTypes(extendsType)

		// TODO: find all named capture groups in the extends type
		// and add them to the context scope as type aliases
		namedCaptureGroups := c.findNamedGroups(extendsType)

		names := make([]string, 0, len(inferTypesMap)+len(namedCaptureGroups))
		for name := range inferTypesMap {
			names = append(names, name)
		}
		for name := range namedCaptureGroups {
			names = append(names, name)
		}

		// Create a new context with infer types in scope for inferring Cons and Alt
		condCtx := ctx
		if len(names) > 0 {
			// Create a new scope that includes the infer types as type aliases
			condScope := ctx.Scope.WithNewScope()

			// Add infer types as type aliases to the scope
			for _, name := range names {
				inferTypeRef := NewTypeRefType(nil, name, nil)
				inferTypeAlias := &TypeAlias{
					Type:       inferTypeRef,
					TypeParams: []*TypeParam{},
				}
				condScope.setTypeAlias(name, inferTypeAlias)
			}

			condCtx = Context{
				Scope:      condScope,
				IsAsync:    ctx.IsAsync,
				IsPatMatch: ctx.IsPatMatch,
			}
		}

		thenType, thenErrors := c.inferTypeAnn(condCtx, typeAnn.Then)
		errors = slices.Concat(errors, thenErrors)

		elseType, elseErrors := c.inferTypeAnn(condCtx, typeAnn.Else)
		errors = slices.Concat(errors, elseErrors)

		t = NewCondType(provenance, checkType, extendsType, thenType, elseType)
	case *ast.InferTypeAnn:
		t = NewInferType(provenance, typeAnn.Name)
	case *ast.MutableTypeAnn:
		targetType, targetErrors := c.inferTypeAnn(ctx, typeAnn.Target)
		errors = slices.Concat(errors, targetErrors)
		t = NewMutableType(provenance, targetType)
	case *ast.TemplateLitTypeAnn:
		types := make([]Type, len(typeAnn.TypeAnns))
		quasis := make([]*Quasi, len(typeAnn.Quasis))
		strOrNumType := NewUnionType(nil, NewStrPrimType(nil), NewNumPrimType(nil))
		for i, typeAnn := range typeAnn.TypeAnns {
			typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, typeAnn)
			// Each type in a template literal type must be a subtype of either
			// string or number.
			// TODO: Also check if the value has a .toString() method.
			unifyErrors := c.unify(ctx, typeAnnType, strOrNumType)
			types[i] = typeAnnType
			errors = slices.Concat(errors, unifyErrors, typeAnnErrors)
		}
		for i, quasi := range typeAnn.Quasis {
			quasis[i] = &Quasi{
				Value: quasi.Value,
			}
		}
		t = NewTemplateLitType(provenance, quasis, types)
	default:
		panic(fmt.Sprintf("Unknown type annotation: %T", typeAnn))
	}

	t.SetProvenance(provenance)
	typeAnn.SetInferredType(t)

	return t, errors
}

// generateSubstitutionSets creates substitution maps for type parameters and type arguments,
// handling cartesian products when union types are present in the type arguments.
func (c *Checker) generateSubstitutionSets(
	ctx Context,
	typeParams []*TypeParam,
	typeArgs []Type,
) ([]map[string]Type, []Error) {
	// If no type params or args, return empty slice
	if len(typeParams) == 0 || len(typeArgs) == 0 {
		return []map[string]Type{}, nil
	}

	var errors []Error

	// Extract all possible types for each type argument position
	argTypeSets := make([][]Type, len(typeArgs))
	for i, argType := range typeArgs {
		// TODO: recursively expand union types in case some of the elements are
		// also union types.
		argType, argErrors := c.expandType(ctx, argType, 1)
		if len(argErrors) > 0 {
			errors = append(errors, argErrors...)
		}
		if unionType, ok := argType.(*UnionType); ok {
			// For union types, use all the union members
			argTypeSets[i] = unionType.Types
		} else {
			// For non-union types, create a single-element slice
			argTypeSets[i] = []Type{argType}
		}
	}

	// Generate cartesian product
	var result []map[string]Type

	// Helper function to generate cartesian product recursively
	var generateCombinations func(int, map[string]Type)
	generateCombinations = func(pos int, current map[string]Type) {
		if pos >= len(typeParams) {
			// Make a copy of the current map and add it to results
			combination := make(map[string]Type)
			for k, v := range current {
				combination[k] = v
			}
			result = append(result, combination)
			return
		}

		// Get the type parameter name for this position
		typeParamName := typeParams[pos].Name

		// Try each possible type for this position
		for _, argType := range argTypeSets[pos] {
			current[typeParamName] = argType
			generateCombinations(pos+1, current)
		}
	}

	generateCombinations(0, make(map[string]Type))

	return result, errors
}

// findInferTypes finds all InferType nodes in a type and replaces them with fresh type variables.
// Returns a mapping from infer names to the type variables that replaced them.
func (c *Checker) findInferTypes(t Type) map[string]Type {
	visitor := &InferTypeFinder{
		checker:   c,
		inferVars: make(map[string]Type),
	}
	t.Accept(visitor)
	return visitor.inferVars
}

// replaceInferTypes substitutes infer variables in a type with their inferred values from the mapping.
func (c *Checker) replaceInferTypes(t Type, inferMapping map[string]Type) Type {
	visitor := &InferTypeReplacer{
		inferMapping: inferMapping,
	}
	return t.Accept(visitor)
}

// InferTypeFinder collects all InferType nodes and replaces them with fresh type variables
type InferTypeFinder struct {
	checker   *Checker
	inferVars map[string]Type
}

func (v *InferTypeFinder) EnterType(t Type) Type {
	// No-op - just for traversal
	return nil
}

func (v *InferTypeFinder) ExitType(t Type) Type {
	t = Prune(t)

	if inferType, ok := t.(*InferType); ok {
		if existingVar, exists := v.inferVars[inferType.Name]; exists {
			// Reuse existing type variable for same infer name
			return existingVar
		}
		// Create fresh type variable
		freshVar := v.checker.FreshVar(&TypeProvenance{Type: inferType})
		v.inferVars[inferType.Name] = freshVar
		return freshVar
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

// isMutableType checks if a type allows mutation
func (c *Checker) isMutableType(t Type) bool {
	t = Prune(t)

	switch t.(type) {
	case *MutableType:
		return true
	default:
		return false
	}
}

// InferTypeReplacer substitutes type variables that correspond to infer types
// with their actual inferred values
type InferTypeReplacer struct {
	inferMapping map[string]Type
}

func (v *InferTypeReplacer) EnterType(t Type) Type {
	// No-op - just for traversal
	return nil
}

func (v *InferTypeReplacer) ExitType(t Type) Type {
	t = Prune(t)

	// Check if this is an InferType that should be replaced
	if inferType, ok := t.(*InferType); ok {
		if typeVar, exists := v.inferMapping[inferType.Name]; exists {
			// Return the inferred type (what the type variable was unified with)
			return typeVar
		}
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}
