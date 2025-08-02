package checker

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	. "github.com/escalier-lang/escalier/internal/type_system"
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

	// Infer placeholders
	for _, declID := range component {
		declCtx := getDeclCtx(ctx, depGraph, declID)
		decl, _ := depGraph.GetDecl(declID)

		switch decl := decl.(type) {
		case *ast.FuncDecl:
			funcType, _, sigErrors := c.inferFuncSig(declCtx, &decl.FuncSig)
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

				unifyErrors := c.unify(declCtx, taType, patType)
				errors = slices.Concat(errors, unifyErrors)
			}

			for name, binding := range bindings {
				declCtx.Scope.setValue(name, binding)
			}
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
			// It's okay to re-infer the function signature here since all
			// function signatures are fully typed for top-level functions
			// declarations in modules.
			funcType, paramBindings, sigErrors := c.inferFuncSig(declCtx, &decl.FuncSig)
			errors = slices.Concat(errors, sigErrors)

			// TODO(#93): unify Throws
			if decl.Body != nil {
				returnType, bodyErrors := c.inferFuncBody(declCtx, paramBindings, decl.Body)
				errors = slices.Concat(errors, bodyErrors)
				unifyErrors := c.unify(declCtx, funcType.Return, returnType)
				errors = slices.Concat(errors, unifyErrors)
			}
		case *ast.VarDecl:
			bindings, declErrors := c.inferVarDecl(declCtx, decl)
			errors = slices.Concat(errors, declErrors)

			// NOTE: The semantics of how function declarations are inferred vs
			// function expressions in variable declarations is different.  I'm
			// not sure if it's important to align these or not.

			for name, binding := range bindings {
				existingBinding := declCtx.Scope.getValue(name)
				unifyErrors := c.unify(declCtx, existingBinding.Type, binding.Type)
				errors = slices.Concat(errors, unifyErrors)
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

	// TODO(#93): unify Throws
	if decl.Body != nil {
		returnType, bodyErrors := c.inferFuncBody(ctx, paramBindings, decl.Body)
		errors = slices.Concat(errors, bodyErrors)
		unifyErrors := c.unify(ctx, funcType.Return, returnType)
		errors = slices.Concat(errors, unifyErrors)
	}

	binding := Binding{
		Source:  &ast.NodeProvenance{Node: decl},
		Type:    funcType,
		Mutable: false,
	}
	ctx.Scope.setValue(decl.Name.Name, &binding)
	return errors
}

func (c *Checker) inferExpr(ctx Context, expr ast.Expr) (Type, []Error) {
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		neverType := NewNeverType()

		if expr.Op == ast.Assign {
			// TODO: check if expr.Left is a valid lvalue
			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)

			errors := slices.Concat(leftErrors, rightErrors)
			// RHS must be a subtype of LHS because we're assigning RHS to LHS
			unifyErrors := c.unify(ctx, rightType, leftType)
			errors = slices.Concat(errors, unifyErrors)

			return neverType, errors
		}

		opBinding := ctx.Scope.getValue(string(expr.Op))
		if opBinding == nil {
			return neverType, []Error{&UnknownOperatorError{
				Operator: string(expr.Op),
			}}
		}

		// TODO: extract this into a unifyCall method
		// TODO: handle function overloading
		if fnType, ok := opBinding.Type.(*FuncType); ok {
			if len(fnType.Params) != 2 {
				return neverType, []Error{&InvalidNumberOfArgumentsError{
					Callee: fnType,
					Args:   []ast.Expr{expr.Left, expr.Right},
				}}
			}

			errors := []Error{}

			leftType, leftErrors := c.inferExpr(ctx, expr.Left)
			rightType, rightErrors := c.inferExpr(ctx, expr.Right)
			errors = slices.Concat(errors, leftErrors, rightErrors)

			leftErrors = c.unify(ctx, leftType, fnType.Params[0].Type)
			rightErrors = c.unify(ctx, rightType, fnType.Params[1].Type)
			errors = slices.Concat(errors, leftErrors, rightErrors)

			return fnType.Return, errors
		}

		return neverType, []Error{&UnknownOperatorError{Operator: string(expr.Op)}}
	case *ast.UnaryExpr:
		if expr.Op == ast.UnaryMinus {
			if lit, ok := expr.Arg.(*ast.LiteralExpr); ok {
				if num, ok := lit.Lit.(*ast.NumLit); ok {
					return NewLitType(&NumLit{Value: num.Value * -1}), []Error{}
				}
			}
		}
		return NewNeverType(), []Error{&UnimplementedError{
			message: "Handle unary operators",
			span:    expr.Span(),
		}}
	case *ast.CallExpr:
		errors := []Error{}
		calleeType, calleeErrors := c.inferExpr(ctx, expr.Callee)
		errors = slices.Concat(errors, calleeErrors)

		argTypes := make([]Type, len(expr.Args))
		for i, arg := range expr.Args {
			argType, argErrors := c.inferExpr(ctx, arg)
			errors = slices.Concat(errors, argErrors)
			argTypes[i] = argType
		}

		// TODO: handle calleeType being something other than a function, e.g.
		// TypeRef, ObjType with callable signature, etc.
		// TODO: handle generic functions
		// TODO: extract this into a unifyCall method
		if fnType, ok := calleeType.(*FuncType); ok {
			// TODO: handle rest params and spread args
			if len(fnType.Params) != len(expr.Args) {
				return NewNeverType(), []Error{&InvalidNumberOfArgumentsError{
					Callee: fnType,
					Args:   expr.Args,
				}}
			}

			for argType, param := range Zip(argTypes, fnType.Params) {
				paramType := param.Type
				paramErrors := c.unify(ctx, argType, paramType)
				errors = slices.Concat(errors, paramErrors)
			}

			// for i, arg := range expr.Args {
			// 	argType, argErrors := c.inferExpr(ctx, arg)
			// 	errors = slices.Concat(errors, argErrors)

			// 	paramType := fnType.Params[i].Type
			// 	paramErrors := c.unify(ctx, argType, paramType)
			// 	errors = slices.Concat(errors, paramErrors)
			// }

			return fnType.Return, errors
		} else {
			return NewNeverType(), []Error{
				&CalleeIsNotCallableError{Type: calleeType, span: expr.Callee.Span()}}
		}
	case *ast.MemberExpr:
		// TODO: create a getPropType function to handle this so that we can
		// call it recursively if need be.
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		propType, propErrors := c.getPropType(ctx, objType, expr.Prop, expr.OptChain)
		return propType, slices.Concat(objErrors, propErrors)
	case *ast.IndexExpr:
		objType, objErrors := c.inferExpr(ctx, expr.Object)
		indexType, indexErrors := c.inferExpr(ctx, expr.Index)

		errors := slices.Concat(objErrors, indexErrors)

		objType = Prune(objType)
		indexType = Prune(indexType)

		switch objType := objType.(type) {
		case *TypeRefType:
			if objType.Name == "Array" {
				unifyErrors := c.unify(ctx, indexType, NewNumType())
				errors = slices.Concat(errors, unifyErrors)
				return objType.TypeArgs[0], errors
			} else {
				errors = append(errors, &ExpectedArrayError{Type: objType})
				return NewNeverType(), errors
			}
		case *TupleType:
			if indexLit, ok := indexType.(*LitType); ok {
				if indexType, ok := indexLit.Lit.(*NumLit); ok {
					index := int(indexType.Value)
					if index < len(objType.Elems) {
						return objType.Elems[index], errors
					} else {
						errors = append(errors, &OutOfBoundsError{
							Index:  index,
							Length: len(objType.Elems),
							span:   expr.Index.Span(),
						})
						return NewNeverType(), errors
					}
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexType,
				span: expr.Index.Span(),
			})
			return NewNeverType(), errors
		case *ObjectType:
			// TODO: create a helper to convert indexType to a ObjTypeKey
			if indexLit, ok := indexType.(*LitType); ok {
				if indexType, ok := indexLit.Lit.(*StrLit); ok {
					for _, elem := range objType.Elems {
						switch elem := elem.(type) {
						case *PropertyElemType:
							if elem.Name == NewStrKey(indexType.Value) {
								return elem.Value, errors
							}
						case *MethodElemType:
							if elem.Name == NewStrKey(indexType.Value) {
								return elem.Fn, errors
							}
						default:
							panic(fmt.Sprintf("Unknown object type element: %#v", elem))
						}
					}
				}
			}
			errors = append(errors, &InvalidObjectKeyError{
				Key:  indexType,
				span: expr.Index.Span(),
			})
			return NewNeverType(), errors
		default:
			panic(fmt.Sprintf("Unknown object type: %#v", objType))
		}
	case *ast.IdentExpr:
		if binding := ctx.Scope.getValue(expr.Name); binding != nil {
			// We create a new type and set its provenance to be the identifier
			// instead of the binding source.  This ensures that errors are reported
			// on the identifier itself instead of the binding source.
			t := Prune(binding.Type).WithProvenance(&ast.NodeProvenance{Node: expr})
			expr.SetInferredType(t)
			expr.Source = binding.Source
			return t, nil
		} else if namespace := ctx.Scope.getNamespace(expr.Name); namespace != nil {
			t := &NamespaceType{Namespace: namespace}
			t.SetProvenance(&ast.NodeProvenance{Node: expr})
			expr.SetInferredType(t)
			return t, nil
		} else {
			t := NewNeverType()
			expr.SetInferredType(t)
			return t, []Error{&UnknownIdentifierError{Ident: expr, span: expr.Span()}}
		}
	case *ast.LiteralExpr:
		t, errors := c.inferLit(expr.Lit)
		expr.SetInferredType(t)
		return t, errors
	case *ast.TupleExpr:
		types := make([]Type, len(expr.Elems))
		errors := []Error{}
		for i, elem := range expr.Elems {
			elemType, elemErrors := c.inferExpr(ctx, elem)
			types[i] = elemType
			errors = slices.Concat(errors, elemErrors)
		}
		tupleType := NewTupleType(types...)
		expr.SetInferredType(tupleType)
		return tupleType, errors
	case *ast.ObjectExpr:
		elems := make([]ObjTypeElem, len(expr.Elems))
		errors := []Error{}
		for i, elem := range expr.Elems {
			switch elem := elem.(type) {
			case *ast.PropertyExpr:
				if elem.Value != nil {
					t, elemErrors := c.inferExpr(ctx, elem.Value)
					errors = slices.Concat(errors, elemErrors)
					elems[i] = NewPropertyElemType(astKeyToTypeKey(elem.Name), t)
				} else {
					switch key := elem.Name.(type) {
					case *ast.IdentExpr:
						// TODO: dedupe with *ast.IdentExpr case
						if binding := ctx.Scope.getValue(key.Name); binding != nil {
							expr.SetInferredType(binding.Type)
							elems[i] = NewPropertyElemType(astKeyToTypeKey(elem.Name), binding.Type)
						} else {
							t := NewNeverType()
							expr.SetInferredType(t)
							elems[i] = NewPropertyElemType(astKeyToTypeKey(elem.Name), t)
							errors = append(
								errors,
								&UnknownIdentifierError{Ident: key, span: key.Span()},
							)
						}
					}
				}
			default:
				panic(fmt.Sprintf("TODO: handle object expression element: %#v", elem))
			}
		}

		objType := NewObjectType(elems)
		expr.SetInferredType(objType)

		return objType, errors
	case *ast.FuncExpr:
		funcType, bindings, sigErrors := c.inferFuncSig(ctx, &expr.FuncSig)
		returnType, bodyErrors := c.inferFuncBody(ctx, bindings, &expr.Body)
		unifyErrors := c.unify(ctx, funcType.Return, returnType)
		expr.SetInferredType(funcType)
		return funcType, slices.Concat(sigErrors, bodyErrors, unifyErrors)
	case *ast.IfElseExpr:
		return c.inferIfElse(ctx, expr)
	default:
		return NewNeverType(), []Error{
			&UnimplementedError{
				message: "Infer expression type: " + fmt.Sprintf("%T", expr),
				span:    expr.Span(),
			},
		}
	}
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

func (v *TypeExpansionVisitor) EnterType(t Type) {
	v.depth++
}

func (v *TypeExpansionVisitor) ExitType(t Type) Type {
	defer func() { v.depth-- }()

	switch t := t.(type) {
	case *NamespaceType:
		// Don't expand NamespaceTypes - return them as-is
		return t
	case *TypeRefType:
		// Check if we've reached the maximum expansion depth
		if v.depth > v.maxDepth {
			// Return the type reference without expanding
			return t
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
			// Create substitution map from type parameter names to type arguments
			substitutions := make(map[string]Type)
			for i, typeParam := range typeAlias.TypeParams {
				if i < len(t.TypeArgs) {
					substitutions[typeParam.Name] = t.TypeArgs[i]
				}
			}
			expandedType = v.checker.substituteTypeParams(typeAlias.Type, substitutions)
		}

		// Recursively expand the resolved type using the same visitor to maintain state
		return expandedType.Accept(v)
	case *CondType:
		errors := v.checker.unify(v.ctx, t.Check, t.Extends)

		if len(errors) > 0 {
			return t.Alt
		} else {
			return t.Cons
		}
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

func (c *Checker) getPropType(ctx Context, objType Type, prop *ast.Ident, optChain bool) (Type, []Error) {
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

	var propType Type

	switch t := objType.(type) {
	case *ObjectType:
		for _, elem := range t.Elems {
			switch elem := elem.(type) {
			case *PropertyElemType:
				if elem.Name == NewStrKey(prop.Name) {
					propType = elem.Value

					if elem.Optional {
						propType = NewUnionType(propType, NewLitType(&UndefinedLit{}))
					}
				}
			case *MethodElemType:
				if elem.Name == NewStrKey(prop.Name) {
					propType = elem.Fn
				}
			case *GetterElemType:
				if elem.Name == NewStrKey(prop.Name) {
					propType = elem.Fn.Return
				}
			case *SetterElemType:
				if elem.Name == NewStrKey(prop.Name) {
					propType = elem.Fn.Params[0].Type
				}
			default:
				panic(fmt.Sprintf("Unknown object type element: %#v", elem))
			}
		}
	case *UnionType:
		undefinedElems := []Type{}
		definedElems := []Type{}
		for _, elem := range t.Types {
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
			errors = append(errors, &ExpectedObjectError{Type: objType})
			return propType, errors
		}

		if len(definedElems) == 1 {
			if len(undefinedElems) == 0 {
				return c.getPropType(ctx, definedElems[0], prop, optChain)
			}

			if len(undefinedElems) > 0 && !optChain {
				errors = append(errors, &ExpectedObjectError{Type: objType})
				return propType, errors
			}

			pType, pErrors := c.getPropType(ctx, definedElems[0], prop, optChain)
			errors = slices.Concat(errors, pErrors)
			propType = NewUnionType(pType, NewLitType(&UndefinedLit{}))
		}

		if len(definedElems) > 1 {
			panic("TODO: handle getting property from union type with multiple defined elements")
		}
	case *NamespaceType:
		if value := t.Namespace.Values[prop.Name]; value != nil {
			propType = value.Type
		} else if namespace := t.Namespace.Namespaces[prop.Name]; namespace != nil {
			propType = &NamespaceType{Namespace: namespace}
		} else {
			errors = append(errors, &UnknownPropertyError{
				ObjectType: objType,
				Property:   prop.Name,
				span:       prop.Span(),
			})
			propType = NewNeverType()
		}
	default:
		errors = append(errors, &ExpectedObjectError{Type: objType})
	}

	if propType == nil {
		errors = append(errors, &UnknownPropertyError{
			ObjectType: objType,
			Property:   prop.Name,
			span:       prop.Span(),
		})
		propType = NewNeverType()
	}

	return propType, errors
}

func astKeyToTypeKey(key ast.ObjKey) ObjTypeKey {
	switch key := key.(type) {
	case *ast.IdentExpr:
		return NewStrKey(key.Name)
	case *ast.StrLit:
		return NewStrKey(key.Value)
	case *ast.NumLit:
		return NewNumKey(key.Value)
	case *ast.ComputedKey:
		panic("TODO: handle computed key")
	default:
		panic(fmt.Sprintf("Unknown object key type: %T", key))
	}
}

func (c *Checker) inferIfElse(ctx Context, expr *ast.IfElseExpr) (Type, []Error) {
	condType, condErrors := c.inferExpr(ctx, expr.Cond)
	unifyErrors := c.unify(ctx, condType, NewBoolType())
	errors := slices.Concat(condErrors, unifyErrors)

	var consType Type = NewNeverType()
	for _, stmt := range expr.Cons.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}
	if len(expr.Cons.Stmts) > 0 {
		lastStmt := expr.Cons.Stmts[len(expr.Cons.Stmts)-1]
		if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
			consType = exprStmt.Expr.InferredType()
		}
	}

	var altType Type = NewNeverType()
	if expr.Alt != nil {
		alt := expr.Alt
		if alt.Block != nil {
			for _, stmt := range alt.Block.Stmts {
				stmtErrors := c.inferStmt(ctx, stmt)
				errors = slices.Concat(errors, stmtErrors)
			}
			if len(alt.Block.Stmts) > 0 {
				lastStmt := alt.Block.Stmts[len(alt.Block.Stmts)-1]
				if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
					altType = exprStmt.Expr.InferredType()
				}
			}
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

func Zip[T, U any](t []T, u []U) iter.Seq2[T, U] {
	return func(yield func(T, U) bool) {
		for i := range min(len(t), len(u)) { // range over int (Go 1.22)
			if !yield(t[i], u[i]) {
				return
			}
		}
	}
}

func (c *Checker) inferFuncSig(
	ctx Context,
	sig *ast.FuncSig, // TODO: make FuncSig an interface
) (*FuncType, map[string]*Binding, []Error) {
	// TODO: handle generic functions
	// typeParams := c.inferTypeParams(ctx, sig.TypeParams)
	errors := []Error{}
	bindings := map[string]*Binding{}
	params := make([]*FuncParam, len(sig.Params))

	for i, param := range sig.Params {
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
			Optional: false, // TODO
		}
	}

	var returnType Type
	if sig.Return == nil {
		returnType = c.FreshVar()
	} else {
		var returnErrors []Error
		returnType, returnErrors = c.inferTypeAnn(ctx, sig.Return)
		errors = slices.Concat(errors, returnErrors)
	}

	t := &FuncType{
		Params:     params,
		Return:     returnType,
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
		Self:       nil,
	}

	return t, bindings, errors
}

type ReturnVisitor struct {
	ast.DefaulVisitor
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

// TODO(#93): infer Throws
func (c *Checker) inferFuncBody(
	ctx Context,
	bindings map[string]*Binding,
	body *ast.Block,
) (Type, []Error) {

	ctx = ctx.WithNewScope()
	maps.Copy(ctx.Scope.Namespace.Values, bindings)

	errors := []Error{}
	for _, stmt := range body.Stmts {
		stmtErrors := c.inferStmt(ctx, stmt)
		errors = slices.Concat(errors, stmtErrors)
	}

	visitor := &ReturnVisitor{
		DefaulVisitor: ast.DefaulVisitor{},
		Returns:       []*ast.ReturnStmt{},
	}

	for _, stmt := range body.Stmts {
		// TODO: don't visit statements that are unreachable
		stmt.Accept(visitor)
	}

	returnTypes := []Type{}
	for _, returnStmt := range visitor.Returns {
		if returnStmt.Expr != nil {
			returnType, returnErrors := c.inferExpr(ctx, returnStmt.Expr)
			returnTypes = append(returnTypes, returnType)
			errors = slices.Concat(errors, returnErrors)
		}
	}

	// TODO: We also need to do dead code analysis to account for unreachable
	// code.

	if len(returnTypes) == 1 {
		return returnTypes[0], errors
	}

	if len(returnTypes) > 1 {
		return NewUnionType(returnTypes...), errors
	}

	return NewLitType(&UndefinedLit{}), errors
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
		t = NewLitType(&RegexLit{Value: lit.Value})
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
			t = c.FreshVar()
			// TODO: report an error if the name is already bound
			bindings[p.Name] = &Binding{
				Source:  &ast.NodeProvenance{Node: p},
				Type:    t,
				Mutable: false, // TODO
			}
			errors = []Error{}
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
					elems = append(elems, NewPropertyElemType(name, t))
				case *ast.ObjShorthandPat:
					// We can't infer the type of the shorthand pattern yet, so
					// we use a fresh type variable.
					t := c.FreshVar()
					name := NewStrKey(elem.Key.Name)
					// TODO: report an error if the name is already bound
					bindings[elem.Key.Name] = &Binding{
						Source:  &ast.NodeProvenance{Node: elem.Key},
						Type:    t,
						Mutable: false, // TODO
					}
					elems = append(elems, NewPropertyElemType(name, t))
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
			Optional: false,
		}
	}
	returnType, returnErrors := c.inferTypeAnn(ctx, funcTypeAnn.Return)
	errors = slices.Concat(errors, returnErrors)

	funcType := FuncType{
		Params:     params,
		Return:     returnType,
		Throws:     NewNeverType(),
		TypeParams: []*TypeParam{},
		Self:       nil,
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
	case *ast.AnyTypeAnn:
		t = NewAnyType()
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
			t = NewLitType(&RegexLit{Value: lit.Value})
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
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &MethodElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.GetterTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &GetterElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.SetterTypeAnn:
				fn, fnErrors := c.inferFuncTypeAnn(ctx, elem.Fn)
				errors = slices.Concat(errors, fnErrors)
				elems[i] = &SetterElemType{Name: astKeyToTypeKey(elem.Name), Fn: fn}
			case *ast.PropertyTypeAnn:
				var t Type
				if elem.Value != nil {
					typeAnnType, typeAnnErrors := c.inferTypeAnn(ctx, elem.Value)
					errors = slices.Concat(errors, typeAnnErrors)
					t = typeAnnType
				} else {
					t = NewLitType(&UndefinedLit{})
				}
				elems[i] = &PropertyElemType{
					Name:     astKeyToTypeKey(elem.Name),
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
		checkType, checkErrors := c.inferTypeAnn(ctx, typeAnn.Check)
		errors = slices.Concat(errors, checkErrors)

		extendsType, extendsErrors := c.inferTypeAnn(ctx, typeAnn.Extends)
		errors = slices.Concat(errors, extendsErrors)

		consType, consErrors := c.inferTypeAnn(ctx, typeAnn.Cons)
		errors = slices.Concat(errors, consErrors)

		altType, altErrors := c.inferTypeAnn(ctx, typeAnn.Alt)
		errors = slices.Concat(errors, altErrors)

		t = NewCondType(checkType, extendsType, consType, altType)
	default:
		panic(fmt.Sprintf("Unknown type annotation: %T", typeAnn))
	}

	t.SetProvenance(&ast.NodeProvenance{
		Node: typeAnn,
	})
	typeAnn.SetInferredType(t)

	return t, errors
}
