package checker

import (
	"fmt"
	"iter"
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

// getDeclCtx returns a new Context with the namespace set to the namespace of
// the declaration with the given declID. If the namespace doesn't exist yet, it
// creates one.
func getDeclCtx(ctx Context, depGraph *dep_graph.DepGraph, declID dep_graph.DeclID) Context {
	nsName, _ := depGraph.GetDeclNamespace(declID)
	if nsName == "" {
		return ctx
	}
	ns := ctx.Scope.Namespace
	declCtx := ctx
	for part := range strings.SplitSeq(nsName, ".") {
		if _, ok := ns.Namespaces[part]; !ok {
			ns.Namespaces[part] = NewNamespace()
		}
		ns = ns.Namespaces[part]
		declCtx = declCtx.WithNewScopeAndNamespace(ns)
	}
	return declCtx
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

	// Infer placeholders
	for _, declID := range component {
		declCtx := getDeclCtx(ctx, depGraph, declID)
		decl, _ := depGraph.GetDecl(declID)

		switch decl := decl.(type) {
		case *ast.FuncDecl:
			funcType, paramBindings, sigErrors := c.inferFuncSig(declCtx, &decl.FuncSig)
			paramBindingsForDecl[declID] = paramBindings
			errors = slices.Concat(errors, sigErrors)

			declCtx.Scope.setValue(decl.Name.Name, &Binding{
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
				taType, taErrors := c.inferTypeAnn(declCtx, decl.TypeAnn)
				errors = slices.Concat(errors, taErrors)

				unifyErrors := c.unify(declCtx, patType, taType)
				errors = slices.Concat(errors, unifyErrors)
			}

			for name, binding := range bindings {
				declCtx.Scope.setValue(name, binding)
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
					constraintType = c.FreshVar()
				}
				if typeParam.Default != nil {
					defaultType = c.FreshVar()
				}
				typeParams[i] = &TypeParam{
					Name:       typeParam.Name,
					Constraint: constraintType,
					Default:    defaultType,
				}
			}

			typeAlias := &TypeAlias{
				Type:       c.FreshVar(),
				TypeParams: typeParams,
			}

			declCtx.Scope.setTypeAlias(decl.Name.Name, typeAlias)
		case *ast.ClassDecl:
			instanceType := c.FreshVar()

			typeAlias := &TypeAlias{
				Type:       instanceType,
				TypeParams: []*TypeParam{}, // TODO
			}

			declCtx.Scope.setTypeAlias(decl.Name.Name, typeAlias)

			objTypeElems := []ObjTypeElem{}
			staticElems := []ObjTypeElem{}

			for _, elem := range decl.Body {
				switch elem := elem.(type) {
				case *ast.FieldElem:
					key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					if key == nil {
						continue
					}

					if elem.Static {
						// Static fields go to the class object type
						staticElems = append(
							staticElems,
							NewPropertyElemType(*key, c.FreshVar()),
						)
					} else {
						// Instance fields go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewPropertyElemType(*key, c.FreshVar()),
						)
					}
				case *ast.MethodElem:
					key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					funcType, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig)
					errors = slices.Concat(errors, sigErrors)
					if key == nil {
						continue
					}

					if elem.Static {
						// Static methods go to the class object type
						staticElems = append(
							staticElems,
							NewMethodElemType(*key, funcType, nil), // static methods don't have self
						)
					} else {
						// Instance methods go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewMethodElemType(*key, funcType, elem.MutSelf),
						)
					}
				case *ast.GetterElem:
					key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					funcType, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig)
					errors = slices.Concat(errors, sigErrors)
					if key == nil {
						continue
					}

					if elem.Static {
						// Static getters go to the class object type
						staticElems = append(
							staticElems,
							NewGetterElemType(*key, funcType),
						)
					} else {
						// Instance getters go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewGetterElemType(*key, funcType),
						)
					}
				case *ast.SetterElem:
					key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
					errors = slices.Concat(errors, keyErrors)
					funcType, _, sigErrors := c.inferFuncSig(declCtx, &elem.Fn.FuncSig)
					errors = slices.Concat(errors, sigErrors)
					if key == nil {
						continue
					}

					if elem.Static {
						// Static setters go to the class object type
						staticElems = append(
							staticElems,
							NewSetterElemType(*key, funcType),
						)
					} else {
						// Instance setters go to the instance type
						objTypeElems = append(
							objTypeElems,
							NewSetterElemType(*key, funcType),
						)
					}
				default:
					errors = append(errors, &UnimplementedError{
						message: fmt.Sprintf("Unsupported class element type: %T", elem),
						span:    elem.Span(),
					})
				}
			}

			objType := &ObjectType{
				Elems:      objTypeElems,
				Exact:      false,
				Immutable:  false,
				Mutable:    true,
				Nominal:    true,
				Interface:  false,
				Extends:    []*TypeRefType{},
				Implements: []*TypeRefType{},
			}
			objType.SetProvenance(&ast.NodeProvenance{Node: decl})

			unifyErrors := c.unify(ctx, instanceType, objType)
			errors = slices.Concat(errors, unifyErrors)

			params, paramBindings, paramErrors := c.inferFuncParams(ctx, decl.Params)
			errors = slices.Concat(errors, paramErrors)
			paramBindingsForDecl[declID] = paramBindings

			funcType := &FuncType{
				TypeParams: []*TypeParam{}, // TODO
				Params:     params,
				Return:     NewTypeRefType(decl.Name.Name, typeAlias),
				Throws:     NewNeverType(),
			}

			funcType.SetProvenance(&ast.NodeProvenance{Node: decl})

			// Create an object type with a constructor element and static methods/properties
			constructorElem := &ConstructorElemType{Fn: funcType}
			classObjTypeElems := []ObjTypeElem{constructorElem}
			classObjTypeElems = append(classObjTypeElems, staticElems...)

			classObjType := &ObjectType{
				Elems:      classObjTypeElems,
				Exact:      false,
				Immutable:  false,
				Mutable:    false,
				Nominal:    true,
				Interface:  false,
				Extends:    []*TypeRefType{},
				Implements: []*TypeRefType{},
			}
			classObjType.SetProvenance(&ast.NodeProvenance{Node: decl})

			ctor := &Binding{
				Source:  &ast.NodeProvenance{Node: decl},
				Type:    classObjType,
				Mutable: false,
			}
			declCtx.Scope.setValue(decl.Name.Name, ctor)
		}
	}

	// Infer definitions
	for _, declID := range component {
		declCtx := getDeclCtx(ctx, depGraph, declID)
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
			funcBinding := declCtx.Scope.getValue(decl.Name.Name)
			paramBindings := paramBindingsForDecl[declID]
			funcType := funcBinding.Type.(*FuncType)

			if decl.Body != nil {
				inferErrors := c.inferFuncBodyWithFuncSigType(ctx, funcType, paramBindings, decl.Body, decl.FuncSig.Async)
				errors = slices.Concat(errors, inferErrors)
			}

		case *ast.VarDecl:
			// TODO: if there's a type annotation, unify the initializer with it
			if decl.Init != nil {
				initType, initErrors := c.inferExpr(declCtx, decl.Init)
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
			typeAlias, declErrors := c.inferTypeDecl(declCtx, decl)
			errors = slices.Concat(errors, declErrors)

			// TODO:
			// - unify the Default and Constraint types for each type param

			// Unified the type alias' inferred type with its placeholder type
			existingTypeAlias := declCtx.Scope.getTypeAlias(decl.Name.Name)
			unifyErrors := c.unify(declCtx, existingTypeAlias.Type, typeAlias.Type)
			errors = slices.Concat(errors, unifyErrors)
		case *ast.ClassDecl:
			typeAlias := declCtx.Scope.getTypeAlias(decl.Name.Name)
			instanceType := Prune(typeAlias.Type).(*ObjectType)

			// Get the class binding to access static methods
			classBinding := declCtx.Scope.getValue(decl.Name.Name)
			classType := classBinding.Type.(*ObjectType)

			// We reuse the binding that was previous created for the function
			// declaration, so that we can unify the signature with the body's
			// inferred type.
			paramBindings := paramBindingsForDecl[declID]

			bodyCtx := declCtx.WithNewScope()

			for name, binding := range paramBindings {
				bodyCtx.Scope.setValue(name, binding)
			}

			// Process each element in the class body
			for _, bodyElem := range decl.Body {
				switch bodyElem := bodyElem.(type) {
				case *ast.FieldElem:
					var prop *PropertyElemType
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
							if propElem, ok := elem.(*PropertyElemType); ok {
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
					var methodType *MethodElemType
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
							if methodElem, ok := elem.(*MethodElemType); ok {
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
							var t Type = NewTypeRefType(decl.Name.Name, typeAlias)
							if methodType.MutSelf != nil && *methodType.MutSelf {
								t = NewMutableType(t)
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

						bodyErrors := c.inferFuncBodyWithFuncSigType(bodyCtx, methodType.Fn, paramBindings, bodyElem.Fn.Body, false)
						errors = slices.Concat(errors, bodyErrors)
					}

				case *ast.GetterElem:
					var getterType *GetterElemType
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
							if getterElem, ok := elem.(*GetterElemType); ok {
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
							var t Type = NewTypeRefType(decl.Name.Name, typeAlias)

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
					var setterType *SetterElemType
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
							if setterElem, ok := elem.(*SetterElemType); ok {
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
							var t Type = NewTypeRefType(decl.Name.Name, typeAlias)
							// Setters typically need mutable self to modify the instance
							t = NewMutableType(t)

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

	funcType, paramBindings, sigErrors := c.inferFuncSig(ctx, &decl.FuncSig)
	errors = slices.Concat(errors, sigErrors)

	// For declared functions, we don't have a body to infer from
	if decl.Declare() && (decl.Body == nil || len(decl.Body.Stmts) == 0) {
		// For declared async functions, validate that the return type is a Promise
		if decl.FuncSig.Async {
			if promiseType, ok := funcType.Return.(*TypeRefType); ok && promiseType.Name == "Promise" {
				// Good, it's a Promise type. Ensure it has the right structure.
				if len(promiseType.TypeArgs) == 1 {
					// Promise<T> should become Promise<T, never>
					promiseAlias := ctx.Scope.getTypeAlias("Promise")
					if promiseAlias != nil {
						// Update the function type to have Promise<T, never>
						newPromiseType := NewTypeRefType("Promise", promiseAlias, promiseType.TypeArgs[0], NewNeverType())
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

	// TODO: handle generic functions
	// Check if calleeType is a FuncType
	if fnType, ok := calleeType.(*FuncType); ok {
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
				return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
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
				if arrayType, ok := restParam.Type.(*TypeRefType); ok && arrayType.Name == "Array" && len(arrayType.TypeArgs) > 0 {
					elementType := arrayType.TypeArgs[0]
					// Unify each excess argument with the element type
					for i := restIndex; i < len(expr.Args); i++ {
						argType := argTypes[i]
						paramErrors := c.unify(ctx, argType, elementType)
						errors = slices.Concat(errors, paramErrors)
					}
				} else {
					// Rest parameter is not Array<T>, this is an error
					return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
						Callee: fnType,
						Args:   expr.Args,
					}}
				}
			}

			return fnType.Return, errors
		} else {
			// Function has no rest parameters, use strict equality check
			if len(fnType.Params) != len(expr.Args) {
				return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
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
			if constructorElem, ok := elem.(*ConstructorElemType); ok {
				fnTypeToUse = constructorElem.Fn
				break
			} else if callableElem, ok := elem.(*CallableElemType); ok {
				fnTypeToUse = callableElem.Fn
				break
			}
		}

		if fnTypeToUse == nil {
			return NewNeverType(), []Error{
				&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
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
				return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
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
				if arrayType, ok := restParam.Type.(*TypeRefType); ok && arrayType.Name == "Array" && len(arrayType.TypeArgs) > 0 {
					elementType := arrayType.TypeArgs[0]
					// Unify each excess argument with the element type
					for i := restIndex; i < len(expr.Args); i++ {
						argType := argTypes[i]
						paramErrors := c.unify(ctx, argType, elementType)
						errors = slices.Concat(errors, paramErrors)
					}
				} else {
					// Rest parameter is not Array<T>, this is an error
					return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
						Callee: fnTypeToUse,
						Args:   expr.Args,
					}}
				}
			}

			return fnTypeToUse.Return, errors
		} else {
			// Function has no rest parameters, use strict equality check
			if len(fnTypeToUse.Params) != len(expr.Args) {
				return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
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
		return NewNeverType(), []Error{
			&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
	}
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (Type, []Error) {
	var resultType Type
	var errors []Error

	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := NewNeverType()

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
					resultType = NewLitType(&NumLit{Value: num.Value * -1})
					errors = []Error{}
				} else {
					resultType = NewNeverType()
					errors = []Error{&UnimplementedError{
						message: "Handle unary operators",
						span:    expr.Span(),
					}}
				}
			} else {
				resultType = NewNeverType()
				errors = []Error{&UnimplementedError{
					message: "Handle unary operators",
					span:    expr.Span(),
				}}
			}
		} else {
			resultType = NewNeverType()
			errors = []Error{&UnimplementedError{
				message: "Handle unary operators",
				span:    expr.Span(),
			}}
		}
	case *ast.CallExpr:
		resultType, errors = c.inferCallExpr(ctx, expr)
	case *ast.MemberExpr:
		// TODO: create a getPropType function to handle this so that we can
		// call it recursively if need be.
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		key := PropertyKey{Name: expr.Prop.Name, OptChain: expr.OptChain, Span: expr.Prop.Span()}
		propType, propErrors := c.getAccessType(ctx, objType, key)

		resultType = propType

		if methodType, ok := propType.(*FuncType); ok {
			if retType, ok := methodType.Return.(*TypeRefType); ok && retType.Name == "Self" {
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
		accessType, accessErrors := c.getAccessType(ctx, objType, key)
		resultType = accessType
		errors = slices.Concat(errors, accessErrors)
	case *ast.IdentExpr:
		if binding := ctx.Scope.getValue(expr.Name); binding != nil {
			// We create a new type and set its provenance to be the identifier
			// instead of the binding source.  This ensures that errors are reported
			// on the identifier itself instead of the binding source.
			t := Prune(binding.Type).WithProvenance(&ast.NodeProvenance{Node: expr})
			expr.Source = binding.Source
			resultType = t
			errors = nil
		} else if namespace := ctx.Scope.getNamespace(expr.Name); namespace != nil {
			t := &NamespaceType{Namespace: namespace}
			t.SetProvenance(&ast.NodeProvenance{Node: expr})
			resultType = t
			errors = nil
		} else {
			resultType = NewNeverType()
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
		resultType = NewTupleType(types...)
	case *ast.ObjectExpr:
		// Create a context for the object so that we can add a `Self` type to it
		objCtx := ctx.WithNewScope()

		typeElems := make([]ObjTypeElem, len(expr.Elems))
		types := make([]Type, len(expr.Elems))
		paramBindingsSlice := make([]map[string]*Binding, len(expr.Elems))

		selfType := c.FreshVar()
		selfTypeAlias := TypeAlias{Type: selfType, TypeParams: []*TypeParam{}}
		objCtx.Scope.setTypeAlias("Self", &selfTypeAlias)

		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					t := c.FreshVar()
					types[i] = t
					typeElems[i] = NewPropertyElemType(*key, t)
				}
			case *ast.MethodExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = NewMethodElemType(*key, funcType, elem.MutSelf)
				}
			case *ast.GetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &GetterElemType{Fn: funcType, Name: *key}
				}
			case *ast.SetterExpr:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key != nil {
					funcType, paramBindings, _ := c.inferFuncSig(objCtx, &elem.Fn.FuncSig)
					paramBindingsSlice[i] = paramBindings
					types[i] = funcType
					typeElems[i] = &SetterElemType{Fn: funcType, Name: *key}
				}
			}
		}

		objType := NewObjectType(typeElems)
		bindErrors := c.bind(selfType, objType)
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
							unifyErrors := c.unify(objCtx, NewNeverType(), t)
							errors = slices.Concat(errors, unifyErrors)

							errors = append(
								errors,
								&UnknownIdentifierError{Ident: key, span: key.Span()},
							)
						}
					}
				}
			case *ast.MethodExpr:
				funcType := t.(*FuncType)
				methodExpr := elem
				paramBindings := paramBindingsSlice[i]

				if methodExpr.MutSelf != nil {
					var selfType Type = NewTypeRefType("Self", &selfTypeAlias)
					if *methodExpr.MutSelf {
						selfType = NewMutableType(selfType)
					}
					paramBindings["self"] = &Binding{
						Source:  &ast.NodeProvenance{Node: expr},
						Type:    selfType,
						Mutable: false, // `self` cannot be reassigned
					}
				}

				inferErrors := c.inferFuncBodyWithFuncSigType(
					objCtx, funcType, paramBindings, methodExpr.Fn.Body, methodExpr.Fn.Async)
				errors = slices.Concat(errors, inferErrors)

			case *ast.GetterExpr:
				funcType := t.(*FuncType)
				paramBindings := paramBindingsSlice[i]
				paramBindings["self"] = &Binding{
					Source:  &ast.NodeProvenance{Node: expr},
					Type:    NewTypeRefType("Self", &selfTypeAlias),
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
					Type:    NewMutableType(NewTypeRefType("Self", &selfTypeAlias)),
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
		funcType, paramBindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig)
		errors = slices.Concat(errors, sigErrors)

		inferErrors := c.inferFuncBodyWithFuncSigType(ctx, funcType, paramBindings, expr.Body, expr.FuncSig.Async)
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
		resultType = NewNeverType()
	case *ast.AwaitExpr:
		// Await can only be used inside async functions
		if !ctx.IsAsync {
			errors = []Error{
				&UnimplementedError{
					message: "await can only be used inside async functions",
					span:    expr.Span(),
				},
			}
			resultType = NewNeverType()
		} else {
			// Infer the type of the expression being awaited
			argType, argErrors := c.inferExpr(ctx, expr.Arg)
			errors = argErrors

			// If the argument is a Promise<T, E>, the result type is T
			// and the throws type should be E (stored in expr.Throws for later use)
			if promiseType, ok := argType.(*TypeRefType); ok && promiseType.Name == "Promise" {
				if len(promiseType.TypeArgs) >= 2 {
					resultType = promiseType.TypeArgs[0]  // T
					expr.Throws = promiseType.TypeArgs[1] // E (store for throw inference)
				} else {
					resultType = NewNeverType()
				}
			} else {
				// If not a Promise type, this is an error
				errors = append(errors, &UnimplementedError{
					message: "await expression expects a Promise type",
					span:    expr.Span(),
				})
				resultType = NewNeverType()
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
			t := NewTypeRefType("TypedDocumentNode", nil, result.ResultType, result.VariablesType)
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
		resultType = NewNeverType()
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
	checker  *Checker
	ctx      Context
	errors   []Error
	depth    int // current expansion depth
	maxDepth int // maximum allowed expansion depth
}

// NewTypeExpansionVisitor creates a new visitor for expanding type references
func NewTypeExpansionVisitor(checker *Checker, ctx Context) *TypeExpansionVisitor {
	return &TypeExpansionVisitor{
		checker:  checker,
		ctx:      ctx,
		errors:   []Error{},
		depth:    0,
		maxDepth: 1, // Limit expansion to depth of 1
	}
}

func (v *TypeExpansionVisitor) EnterType(t Type) Type {
	v.depth++

	switch t := t.(type) {
	case *TypeRefType:
		if t.Name == "Array" || t.Name == "Error" {
			return nil
		}

		// Check if we've reached the maximum expansion depth
		if v.depth > v.maxDepth {
			// Return the type reference without expanding
			return nil
		}

		typeAlias := v.checker.resolveQualifiedTypeAliasFromString(v.ctx, t.Name)
		if typeAlias == nil {
			v.errors = append(v.errors, &UnknownTypeError{TypeName: t.Name, typeRef: t})
			neverType := NewNeverType()
			neverType.SetProvenance(&TypeProvenance{Type: t})
			return neverType
		}

		// Replace type params with type args if the type is generic
		expandedType := typeAlias.Type
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
					expandedType = NewUnionType(expandedTypes...)
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
		return expandedType.Accept(v)
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
	defer func() { v.depth-- }()

	switch t := t.(type) {
	case *NamespaceType:
		// Don't expand NamespaceTypes - return them as-is
		return nil
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
		return NewUnionType(filteredTypes...)
	}

	// For all other types, return nil to let Accept handle the traversal
	return nil
}

func (c *Checker) expandType(ctx Context, t Type) (Type, []Error) {
	t = Prune(t)
	visitor := NewTypeExpansionVisitor(c, ctx)

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

// getAccessType is a unified function for getting types from objects via property access or indexing
func (c *Checker) getAccessType(ctx Context, objType Type, key AccessKey) (Type, []Error) {
	errors := []Error{}

	objType = Prune(objType)

	// Repeatedly expand objType until it's either an ObjectType, NamespaceType,
	// or can't be expanded any further
	for {
		expandedType, expandErrors := c.expandType(ctx, objType)
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
		return c.getAccessType(ctx, t.Type, key)
	case *TypeRefType:
		// Handle Array access
		if indexKey, ok := key.(IndexKey); ok && t.Name == "Array" {
			unifyErrors := c.unify(ctx, indexKey.Type, NewNumType())
			errors = slices.Concat(errors, unifyErrors)
			return t.TypeArgs[0], errors
		} else if _, ok := key.(IndexKey); ok && t.Name == "Array" {
			errors = append(errors, &ExpectedArrayError{Type: t})
			return NewNeverType(), errors
		}

		// For other TypeRefTypes, try to expand the type alias and call getAccessType recursively
		if t.Name == "Error" {
			// Built-in Error type doesn't support property access directly
			errors = append(errors, &ExpectedObjectError{Type: objType})
			return NewNeverType(), errors
		}

		expandType, expandErrors := c.expandTypeRef(ctx, t)
		accessType, accessErrors := c.getAccessType(ctx, expandType, key)

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
						return NewNeverType(), errors
					}
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexKey.Type,
				span: indexKey.Span,
			})
			return NewNeverType(), errors
		}
		// TupleType doesn't support property access
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(), errors
	case *ObjectType:
		return c.getObjectAccess(t, key, errors)
	case *UnionType:
		return c.getUnionAccess(ctx, t, key, errors)
	case *NamespaceType:
		if propKey, ok := key.(PropertyKey); ok {
			if value := t.Namespace.Values[propKey.Name]; value != nil {
				return value.Type, errors
			} else if namespace := t.Namespace.Namespaces[propKey.Name]; namespace != nil {
				return &NamespaceType{Namespace: namespace}, errors
			} else {
				errors = append(errors, &UnknownPropertyError{
					ObjectType: objType,
					Property:   propKey.Name,
					span:       propKey.Span,
				})
				return NewNeverType(), errors
			}
		}
		// NamespaceType doesn't support index access
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(), errors
	}
}

func (c *Checker) expandTypeRef(ctx Context, t *TypeRefType) (Type, []Error) {
	// Resolve the type alias
	typeAlias := c.resolveQualifiedTypeAliasFromString(ctx, t.Name)
	if typeAlias == nil {
		return NewNeverType(), []Error{&UnknownTypeError{TypeName: t.Name, typeRef: t}}
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
			case *PropertyElemType:
				if elem.Name == NewStrKey(k.Name) {
					propType := elem.Value
					if elem.Optional {
						propType = NewUnionType(propType, NewLitType(&UndefinedLit{}))
					}
					return propType, errors
				}
			case *MethodElemType:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn, errors
				}
			case *GetterElemType:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn.Return, errors
				}
			case *SetterElemType:
				if elem.Name == NewStrKey(k.Name) {
					return elem.Fn.Params[0].Type, errors
				}
			case *ConstructorElemType:
			case *CallableElemType:
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
		return NewNeverType(), errors
	case IndexKey:
		if indexLit, ok := k.Type.(*LitType); ok {
			if strLit, ok := indexLit.Lit.(*StrLit); ok {
				for _, elem := range objType.Elems {
					switch elem := elem.(type) {
					case *PropertyElemType:
						if elem.Name == NewStrKey(strLit.Value) {
							return elem.Value, errors
						}
					case *MethodElemType:
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
		return NewNeverType(), errors
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
		return NewNeverType(), errors
	}
}

// getUnionAccess handles property and index access on UnionType
func (c *Checker) getUnionAccess(ctx Context, unionType *UnionType, key AccessKey, errors []Error) (Type, []Error) {
	propKey, isPropertyKey := key.(PropertyKey)

	undefinedElems := []Type{}
	definedElems := []Type{}
	for _, elem := range unionType.Types {
		elem = Prune(elem)
		switch elem := elem.(type) {
		case *LitType:
			if _, ok := elem.Lit.(*UndefinedLit); ok {
				undefinedElems = append(undefinedElems, elem)
			}
		default:
			definedElems = append(definedElems, elem)
		}
	}

	if len(definedElems) == 0 {
		errors = append(errors, &ExpectedObjectError{Type: unionType})
		return NewNeverType(), errors
	}

	if len(definedElems) == 1 {
		if len(undefinedElems) == 0 {
			return c.getAccessType(ctx, definedElems[0], key)
		}

		if len(undefinedElems) > 0 && isPropertyKey && !propKey.OptChain {
			errors = append(errors, &ExpectedObjectError{Type: unionType})
			return NewNeverType(), errors
		}

		pType, pErrors := c.getAccessType(ctx, definedElems[0], key)
		errors = slices.Concat(errors, pErrors)
		propType := NewUnionType(pType, NewLitType(&UndefinedLit{}))
		return propType, errors
	}

	if len(definedElems) > 1 {
		panic("TODO: handle getting property from union type with multiple defined elements")
	}

	return NewNeverType(), errors
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
	unifyErrors := c.unify(ctx, condType, NewBoolType())
	errors := slices.Concat(condErrors, unifyErrors)

	// Infer the consequent block
	consType, consErrors := c.inferBlock(ctx, &expr.Cons, NewNeverType())
	errors = slices.Concat(errors, consErrors)

	var altType Type = NewNeverType()
	if expr.Alt != nil {
		alt := expr.Alt
		if alt.Block != nil {
			var altErrors []Error
			altType, altErrors = c.inferBlock(ctx, alt.Block, NewNeverType())
			errors = slices.Concat(errors, altErrors)
		} else if alt.Expr != nil {
			t, altErrors := c.inferExpr(ctx, alt.Expr)
			errors = slices.Concat(errors, altErrors)
			altType = t
		} else {
			panic("alt must be a block or expression")
		}
	}

	t := NewUnionType(consType, altType)
	expr.SetInferredType(t)

	return t, errors
}

func (c *Checker) inferDoExpr(ctx Context, expr *ast.DoExpr) (Type, []Error) {
	// Infer the body block - default to undefined if no expression at the end
	resultType, errors := c.inferBlock(ctx, &expr.Body, NewLitType(&UndefinedLit{}))

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

			guardUnifyErrors := c.unify(caseCtx, guardType, NewBoolType())
			errors = slices.Concat(errors, guardUnifyErrors)
		}

		// Infer the type of the case body
		var caseType Type
		if matchCase.Body.Block != nil {
			// Handle block body using the helper function
			var caseErrors []Error
			caseType, caseErrors = c.inferBlock(caseCtx, matchCase.Body.Block, NewLitType(&UndefinedLit{}))
			errors = slices.Concat(errors, caseErrors)
		} else if matchCase.Body.Expr != nil {
			// Handle expression body
			var caseErrors []Error
			caseType, caseErrors = c.inferExpr(caseCtx, matchCase.Body.Expr)
			errors = slices.Concat(errors, caseErrors)
		} else {
			// This shouldn't happen with a well-formed AST
			caseType = NewNeverType()
		}

		caseTypes = append(caseTypes, caseType)
	}

	// The type of the match expression is the union of all case types
	resultType := NewUnionType(caseTypes...)

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
			typeAnn = c.FreshVar()
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

func (c *Checker) inferFuncSig(
	ctx Context,
	sig *ast.FuncSig, // TODO: make FuncSig an interface
) (*FuncType, map[string]*Binding, []Error) {
	errors := []Error{}

	// TODO: handle generic functions
	// typeParams := c.inferTypeParams(ctx, sig.TypeParams)

	params, bindings, paramErrors := c.inferFuncParams(ctx, sig.Params)
	errors = slices.Concat(errors, paramErrors)

	var returnType Type
	if sig.Return == nil {
		returnType = c.FreshVar()
	} else {
		var returnErrors []Error
		returnType, returnErrors = c.inferTypeAnn(ctx, sig.Return)
		errors = slices.Concat(errors, returnErrors)
	}

	var throwsType Type
	if sig.Throws == nil {
		// If no throws clause is specified, we use a fresh type variable which
		// will be unified later if any throw expressions are found in the
		// function body.
		throwsType = c.FreshVar()
	} else {
		var throwsErrors []Error
		throwsType, throwsErrors = c.inferTypeAnn(ctx, sig.Throws)
		errors = slices.Concat(errors, throwsErrors)
	}

	// For async functions, wrap the return type in a Promise<T, E>
	var finalReturnType Type
	var finalThrowsType Type
	if sig.Async {
		// For async functions, check if the user explicitly specified a Promise return type
		if promiseType, ok := returnType.(*TypeRefType); ok && promiseType.Name == "Promise" {
			// User explicitly specified Promise<T, E>, use it as-is
			finalReturnType = returnType
			finalThrowsType = NewNeverType() // Async functions don't throw directly
		} else {
			// User didn't specify Promise, wrap the return type
			promiseAlias := ctx.Scope.getTypeAlias("Promise")
			if promiseAlias != nil {
				finalReturnType = NewTypeRefType("Promise", promiseAlias, returnType, throwsType)
				finalThrowsType = NewNeverType() // Async functions don't throw directly
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

	t := &FuncType{
		Params:     params,
		Return:     finalReturnType,
		Throws:     finalThrowsType,
		TypeParams: []*TypeParam{},
	}

	return t, bindings, errors
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
		if promiseType, ok := funcSigType.Return.(*TypeRefType); ok && promiseType.Name == "Promise" {
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
			returnType, returnErrors := c.inferExpr(ctx, returnStmt.Expr)
			returnTypes = append(returnTypes, returnType)
			errors = slices.Concat(errors, returnErrors)
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
		returnType = NewUnionType(returnTypes...)
	} else {
		returnType = NewLitType(&UndefinedLit{})
	}

	throwType := NewUnionType(throwTypes...)

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
		return &ExtractorPat{Name: p.Name, Args: args}
	case *ast.RestPat:
		return &RestPat{Pattern: patToPat(p.Pattern)}
	default:
		panic("unknown pattern type: " + fmt.Sprintf("%T", p))
	}
}

func (c *Checker) inferLit(lit ast.Lit) (Type, []Error) {
	var t Type
	errors := []Error{}
	switch lit := lit.(type) {
	case *ast.StrLit:
		t = NewLitType(&StrLit{Value: lit.Value})
	case *ast.NumLit:
		t = NewLitType(&NumLit{Value: lit.Value})
	case *ast.BoolLit:
		t = NewLitType(&BoolLit{Value: lit.Value})
	case *ast.RegexLit:
		// TODO: createa a separate type for regex literals
		t, _ = NewRegexType(lit.Value)
	case *ast.BigIntLit:
		t = NewLitType(&BigIntLit{Value: lit.Value})
	case *ast.NullLit:
		t = NewLitType(&NullLit{})
	case *ast.UndefinedLit:
		t = NewLitType(&UndefinedLit{})
	default:
		panic(fmt.Sprintf("Unknown literal type: %T", lit))
	}
	t.SetProvenance(&ast.NodeProvenance{
		Node: lit,
	})
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

		switch p := pat.(type) {
		case *ast.IdentPat:
			if p.TypeAnn != nil {
				t, errors = c.inferTypeAnn(ctx, p.TypeAnn)
			} else {
				t = c.FreshVar()
				errors = []Error{}
			}

			// TODO: report an error if the name is already bound
			bindings[p.Name] = &Binding{
				Source:  &ast.NodeProvenance{Node: p},
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
			t = NewTupleType(elems...)
		case *ast.ObjectPat:
			elems := []ObjTypeElem{}
			for _, elem := range p.Elems {
				switch elem := elem.(type) {
				case *ast.ObjKeyValuePat:
					t, elemErrors := inferPatRec(elem.Value)
					errors = append(errors, elemErrors...)
					name := NewStrKey(elem.Key.Name)
					optional := elem.Default != nil
					prop := NewPropertyElemType(name, t)
					prop.Optional = optional
					elems = append(elems, prop)
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					var t Type
					if elem.TypeAnn != nil {
						elemType, elemErrors := c.inferTypeAnn(ctx, elem.TypeAnn)
						t = elemType
						errors = append(errors, elemErrors...)
					} else {
						t = c.FreshVar()
					}
					name := NewStrKey(elem.Key.Name)
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = &Binding{
						Source:  &ast.NodeProvenance{Node: elem.Key},
						Type:    t,
						Mutable: false, // TODO
					}
					optional := elem.Default != nil
					prop := NewPropertyElemType(name, t)
					prop.Optional = optional
					elems = append(elems, prop)
				case *ast.ObjRestPat:
					t, restErrors := inferPatRec(elem.Pattern)
					errors = slices.Concat(errors, restErrors)
					elems = append(elems, NewRestSpreadElemType(t))
				}
			}
			t = NewObjectType(elems)
		case *ast.ExtractorPat:
			if binding := ctx.Scope.getValue(p.Name); binding != nil {
				args := make([]Type, len(p.Args))
				for i, arg := range p.Args {
					argType, argErrors := inferPatRec(arg)
					args[i] = argType
					errors = append(errors, argErrors...)
				}
				t = NewExtractorType(binding.Type, args...)
			} else {
				t = NewNeverType()
			}
		case *ast.RestPat:
			argType, argErrors := inferPatRec(p.Pattern)
			errors = append(errors, argErrors...)
			t = NewRestSpreadType(argType)
		case *ast.WildcardPat:
			t = c.FreshVar()
			errors = []Error{}
		}

		t.SetProvenance(&ast.NodeProvenance{
			Node: pat,
		})
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
			typeParamTypeRef := NewTypeRefType(typeParam.Name, nil)
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
			typeAnn = c.FreshVar()
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
		Throws:     NewNeverType(),
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
	var t Type = NewNeverType()

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

			t = NewTypeRefType(typeName, typeAlias, typeArgs...)
		} else {
			// TODO: include type args
			typeRef := NewTypeRefType(typeName, nil, nil)
			typeRef.SetProvenance(&ast.NodeProvenance{
				Node: typeAnn,
			})
			errors = append(errors, &UnknownTypeError{TypeName: typeName, typeRef: typeRef})
		}
	case *ast.NumberTypeAnn:
		t = NewNumType()
	case *ast.StringTypeAnn:
		t = NewStrType()
	case *ast.BooleanTypeAnn:
		t = NewBoolType()
	case *ast.SymbolTypeAnn:
		t = NewSymType()
	case *ast.UniqueSymbolTypeAnn:
		c.SymbolID++
		t = NewUniqueSymbolType(c.SymbolID)
	case *ast.AnyTypeAnn:
		t = NewAnyType()
	case *ast.UnknownTypeAnn:
		t = NewUnknownType()
	case *ast.NeverTypeAnn:
		t = NewNeverType()
	case *ast.LitTypeAnn:
		switch lit := typeAnn.Lit.(type) {
		case *ast.StrLit:
			t = NewLitType(&StrLit{Value: lit.Value})
		case *ast.NumLit:
			t = NewLitType(&NumLit{Value: lit.Value})
		case *ast.BoolLit:
			t = NewLitType(&BoolLit{Value: lit.Value})
		case *ast.RegexLit:
			t, _ = NewRegexType(lit.Value)
		case *ast.BigIntLit:
			t = NewLitType(&BigIntLit{Value: lit.Value})
		case *ast.NullLit:
			t = NewLitType(&NullLit{})
		case *ast.UndefinedLit:
			t = NewLitType(&UndefinedLit{})
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
		t = NewTupleType(elems...)
	case *ast.ObjectTypeAnn:
		elems := make([]ObjTypeElem, len(typeAnn.Elems))
		for i, elem := range typeAnn.Elems {
			switch elem := elem.(type) {
			case *ast.CallableTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &CallableElemType{Fn: fn}
			case *ast.ConstructorTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &ConstructorElemType{Fn: fn}
			case *ast.MethodTypeAnn:
				// TODO: handle `self` and `mut self` parameters
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = NewMethodElemType(*key, fn, nil)
			case *ast.GetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &GetterElemType{Name: *key, Fn: fn}
			case *ast.SetterTypeAnn:
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &SetterElemType{Name: *key, Fn: fn}
			case *ast.PropertyTypeAnn:
				var t Type
				if elem.Value != nil {
					typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, elem.Value)
					errors = slices.Concat(errors, typeAnnErrors)
					t = typeAnnType
				} else {
					t = NewLitType(&UndefinedLit{})
				}
				key, keyErrors := c.astKeyToTypeKey(ctx, elem.Name)
				errors = slices.Concat(errors, keyErrors)
				if key == nil {
					continue
				}
				elems[i] = &PropertyElemType{
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

		t = NewObjectType(elems)
	case *ast.UnionTypeAnn:
		types := make([]Type, len(typeAnn.Types))
		for i, unionType := range typeAnn.Types {
			unionElemType, unionElemErrors := c.inferTypeAnn(ctx, unionType)
			types[i] = unionElemType
			errors = slices.Concat(errors, unionElemErrors)
		}
		t = NewUnionType(types...)
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
				inferTypeRef := NewTypeRefType(name, nil)
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

		t = NewCondType(checkType, extendsType, thenType, elseType)
	case *ast.InferTypeAnn:
		t = NewInferType(typeAnn.Name)
	case *ast.MutableTypeAnn:
		targetType, targetErrors := c.inferTypeAnn(ctx, typeAnn.Target)
		errors = slices.Concat(errors, targetErrors)
		t = NewMutableType(targetType)
	default:
		panic(fmt.Sprintf("Unknown type annotation: %T", typeAnn))
	}

	t.SetProvenance(&ast.NodeProvenance{
		Node: typeAnn,
	})
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
		argType, argErrors := c.expandType(ctx, argType)
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
		freshVar := v.checker.FreshVar()
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
