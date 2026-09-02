package dts_to_esc

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// convertStatement attempts to convert a dts_parser.Statement to an ast.Decl.
// Returns nil for statements that can't be represented as Decl (like imports).
func convertStatement(cctx *convertCtx, stmt dts_parser.Statement) (ast.Decl, error) {
	switch s := stmt.(type) {
	case *dts_parser.VarDecl:
		return convertVarDecl(s)
	case *dts_parser.FuncDecl:
		return convertFuncDecl(s)
	case *dts_parser.TypeDecl:
		return convertTypeDecl(s)
	case *dts_parser.EnumDecl:
		return convertEnumDecl(s)
	case *dts_parser.ClassDecl:
		return convertClassDecl(cctx, s)
	case *dts_parser.InterfaceDecl:
		return convertInterfaceDecl(s)
	case *dts_parser.ImportDecl,
		*dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt,
		*dts_parser.ExportAsNamespaceStmt:
		// Skip imports and standalone export statements
		return nil, nil
	case *dts_parser.NamespaceDecl, *dts_parser.ModuleDecl:
		// These are handled separately in the module conversion
		return nil, fmt.Errorf("namespace/module declarations should be handled at module level")
	default:
		return nil, fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

// convertVarDecl converts a dts_parser.VarDecl to an ast.VarDecl.
func convertVarDecl(dv *dts_parser.VarDecl) (*ast.VarDecl, error) {
	// Convert the identifier to a pattern
	pattern := ast.NewIdentPat(dv.Name.Name, false, nil, nil, convertSpan(dv.Name.Span()))

	// Convert the type annotation
	var typeAnn ast.TypeAnn
	if dv.TypeAnn != nil {
		var err error
		typeAnn, err = convertTypeAnn(dv.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("converting type annotation for variable %s: %w", dv.Name.Name, err)
		}
	}

	// Determine the variable kind based on readonly flag
	kind := ast.ValKind
	if !dv.Readonly {
		kind = ast.VarKind
	}

	return ast.NewVarDecl(
		kind,
		pattern,
		typeAnn,
		nil,   // Init is nil for declarations
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		convertSpan(dv.Span()),
	), nil
}

// convertFuncDecl converts a dts_parser.FuncDecl to an ast.FuncDecl.
func convertFuncDecl(df *dts_parser.FuncDecl) (*ast.FuncDecl, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(df.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters for function %s: %w", df.Name.Name, err)
	}

	// Convert parameters
	params, err := convertParams(df.Params)
	if err != nil {
		return nil, fmt.Errorf("converting parameters for function %s: %w", df.Name.Name, err)
	}

	// Convert return type
	var returnType ast.TypeAnn
	if df.ReturnType != nil {
		returnType, err = convertReturnTypeAnn(df.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting return type for function %s: %w", df.Name.Name, err)
		}
	}

	return ast.NewFuncDecl(
		ast.NewIdentifier(df.Name.Name, convertSpan(df.Name.Span())),
		nil, // .d.ts functions don't have lifetime params
		typeParams,
		params,
		returnType,
		nil,   // nil throws is equivalent to throws never (PR #384)
		nil,   // body is nil for declarations
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		false, // async - would need to be extracted from modifiers
		convertSpan(df.Span()),
	), nil
}

// convertTypeDecl converts a dts_parser.TypeDecl to an ast.TypeDecl.
func convertTypeDecl(dt *dts_parser.TypeDecl) (ast.Decl, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(dt.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters for type alias %s: %w", dt.Name.Name, err)
	}

	// Convert the type annotation
	typeAnn, err := convertTypeAnn(dt.TypeAnn)
	if err != nil {
		if err.Error() == "convertTypeAnn: unknown primitive type 12" {
			return nil, nil
		}
		return nil, fmt.Errorf("converting type annotation for type alias %s: %w", dt.Name.Name, err)
	}

	if RaiseParamDecls.Contains(dt.Name.Name) {
		typeParams = addRaiseParam(typeParams, typeAnn, dt.Span())
	}

	return ast.NewTypeDecl(
		ast.NewIdentifier(dt.Name.Name, convertSpan(dt.Name.Span())),
		typeParams,
		typeAnn,
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		convertSpan(dt.Span()),
	), nil
}

// convertEnumDecl converts a dts_parser.EnumDecl to an ast.Decl.
// TypeScript enums are different from Escalier enums - TS enums are value-level
// constructs with numeric/string values, while Escalier enums are algebraic data types.
// For now, we convert TS enums to type aliases with union of literal types.
func convertEnumDecl(de *dts_parser.EnumDecl) (ast.Decl, error) {
	// TODO: For now, return an error indicating this is not yet implemented.
	// Future implementation could:
	// 1. Convert to union of literal types
	// 2. Create a special representation
	// 3. Generate both type and value declarations
	return nil, fmt.Errorf("TypeScript enum conversion not yet implemented: %s", de.Name.Name)
}

// convertClassDecl converts a dts_parser.ClassDecl to an ast.ClassDecl.
func convertClassDecl(cctx *convertCtx, dc *dts_parser.ClassDecl) (*ast.ClassDecl, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(dc.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters for class %s: %w", dc.Name.Name, err)
	}

	// Convert class members. Any TS-side constructor becomes an in-body
	// `ConstructorElem`. The `mut self` parameter is synthesized so
	// downstream passes see the same shape as a user-written constructor.
	var bodyElems []ast.ClassElem

	for _, member := range dc.Members {
		switch m := member.(type) {
		case *dts_parser.ConstructorDecl:
			params, err := convertParams(m.Params)
			if err != nil {
				return nil, fmt.Errorf("converting constructor parameters for class %s: %w", dc.Name.Name, err)
			}
			selfSpan := convertSpan(m.Span())
			selfPat := ast.NewIdentPat("self", true, nil, nil, selfSpan)
			selfParam := &ast.Param{Pattern: selfPat, TypeAnn: nil, Optional: false}
			allParams := append([]*ast.Param{selfParam}, params...)
			fn := ast.NewFuncExpr(nil, nil, allParams, nil, nil, false, nil, convertSpan(m.Span()))
			ctorResult := cctx.classifyMember(m, dc.Name.Name)
			bodyElems = append(bodyElems, &ast.ConstructorElem{
				Fn:       fn,
				Receiver: &ast.MethodReceiver{Mut: ctorResult.Mut, Span_: selfSpan},
				Private:  false,
				Span_:    convertSpan(m.Span()),
			})

		case *dts_parser.MethodDecl:
			elem, err := convertMethodDecl(cctx, m, dc.Name.Name)
			if err != nil {
				return nil, fmt.Errorf("converting method for class %s: %w", dc.Name.Name, err)
			}
			bodyElems = append(bodyElems, elem)

		case *dts_parser.PropertyDecl:
			elem, err := convertPropertyDecl(m)
			if err != nil {
				return nil, fmt.Errorf("converting property for class %s: %w", dc.Name.Name, err)
			}
			bodyElems = append(bodyElems, elem)

		case *dts_parser.GetterDecl:
			elem, err := convertGetterDecl(cctx, m, dc.Name.Name)
			if err != nil {
				return nil, fmt.Errorf("converting getter for class %s: %w", dc.Name.Name, err)
			}
			bodyElems = append(bodyElems, elem)

		case *dts_parser.SetterDecl:
			elem, err := convertSetterDecl(cctx, m, dc.Name.Name)
			if err != nil {
				return nil, fmt.Errorf("converting setter for class %s: %w", dc.Name.Name, err)
			}
			bodyElems = append(bodyElems, elem)

		case *dts_parser.IndexSignature:
			// Index signatures in classes are not directly supported in Escalier
			// TODO: Consider how to represent these, possibly as metadata or comments
			continue

		default:
			return nil, fmt.Errorf("unsupported class member type: %T", member)
		}
	}

	var extends *ast.TypeRefTypeAnn
	if dc.Extends != nil {
		converted, err := convertTypeAnn(dc.Extends)
		if err != nil {
			return nil, fmt.Errorf("converting extends type for class %s: %w", dc.Name.Name, err)
		}
		typeRef, ok := converted.(*ast.TypeRefTypeAnn)
		if !ok {
			return nil, fmt.Errorf("extends type for class %s isn't a type ref", dc.Name.Name)
		}
		extends = typeRef
	}

	var implements []*ast.TypeRefTypeAnn
	for _, impl := range dc.Implements {
		converted, err := convertTypeAnn(impl)
		if err != nil {
			return nil, fmt.Errorf("converting implements type for class %s: %w", dc.Name.Name, err)
		}
		typeRef, ok := converted.(*ast.TypeRefTypeAnn)
		if !ok {
			return nil, fmt.Errorf("implements type for class %s isn't a type ref (got %T)", dc.Name.Name, converted)
		}
		implements = append(implements, typeRef)
	}

	return ast.NewClassDecl(
		ast.NewIdentifier(dc.Name.Name, convertSpan(dc.Name.Span())),
		nil,
		typeParams,
		extends,
		implements,
		bodyElems,
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		false, // final
		convertSpan(dc.Span()),
	), nil
}

// convertInterfaceDecl converts a dts_parser.InterfaceDecl to an ast.InterfaceDecl.
func convertInterfaceDecl(di *dts_parser.InterfaceDecl) (ast.Decl, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(di.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters for interface %s: %w", di.Name.Name, err)
	}

	// Convert interface members to object type elements
	var objElems []ast.ObjTypeAnnElem
	for _, member := range di.Members {
		elem, err := convertInterfaceMember(member)
		if err != nil {
			return nil, fmt.Errorf("converting member for interface %s: %w", di.Name.Name, err)
		}
		if elem != nil {
			objElems = append(objElems, elem)
		}
	}

	// Create an object type with the converted members
	objType := ast.NewObjectTypeAnn(objElems, convertSpan(di.Span()))

	// Convert extends clause
	var extends []*ast.TypeRefTypeAnn
	for _, extType := range di.Extends {
		convertedType, err := convertTypeAnn(extType)
		if err != nil {
			return nil, fmt.Errorf("converting extends type for interface %s: %w", di.Name.Name, err)
		}
		typeRefType, ok := convertedType.(*ast.TypeRefTypeAnn)
		if !ok {
			return nil, fmt.Errorf("extends type for interface %s isn't a type ref", di.Name.Name)
		}
		extends = append(extends, typeRefType)
	}

	if RaiseParamDecls.Contains(di.Name.Name) {
		typeParams = addRaiseParam(typeParams, objType, di.Span())
	}

	return ast.NewInterfaceDecl(
		ast.NewIdentifier(di.Name.Name, convertSpan(di.Name.Span())),
		nil,
		typeParams,
		extends,
		objType,
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		convertSpan(di.Span()),
	), nil
}

// raiseParamName is the trailing type parameter the declarations in
// RaiseParamDecls carry. It names what the declaration may raise: what a
// promise rejects with, and what advancing a generator throws.
const raiseParamName = "E"

// RaiseParamDecls are the declarations the converter gives a trailing
// raise parameter to, so `interface Promise<T>` is emitted as
// `Promise<T, E>`. Escalier's own form of each takes that parameter
// where the TypeScript declaration has no slot for it. The solver reads
// `Promise<T, E>` and `Generator<Y, R, N, E>`.
//
// The parameter defaults to `any` and no fact is consulted for it.
// Deriving it from the ECMA-262 rejects set is #1352.
var RaiseParamDecls = set.FromSlice([]string{
	"Promise", "PromiseLike",
	"Generator", "AsyncGenerator",
})

// addRaiseParam appends the raise parameter to a declaration's type
// parameters and threads it through every reference body makes to a
// declaration that carries one. Both the parameter and its uses are
// synthesized rather than read from the `.d.ts`, so they borrow a zero
// span.
//
// Inside `interface Generator<T, TReturn, TNext>` the self-reference
// `[Symbol.iterator]() -> Generator<T, TReturn, TNext>` becomes
// `Generator<T, TReturn, TNext, E>`, so the raise the declaration takes
// reaches the type it evaluates to.
func addRaiseParam(typeParams []*ast.TypeParam, body ast.Node, declSpan ast.Span) []*ast.TypeParam {
	body.Accept(&raiseParamVisitor{})
	param := ast.NewTypeParam(raiseParamName, nil, ast.NewAnyTypeAnn(synthSpan()), declSpan)
	return append(typeParams, &param)
}

// synthSpan is the span a node the converter invents carries. Nothing in
// the `.d.ts` source corresponds to it, so there is no position to point
// a diagnostic at.
func synthSpan() ast.Span {
	return ast.NewSpan(ast.Location{Line: 0, Column: 0}, ast.Location{Line: 0, Column: 0}, 0)
}

// raiseParamVisitor appends the raise argument to every reference to a
// declaration in RaiseParamDecls. It runs over the body of one such
// declaration, so the `E` it names is that declaration's own parameter.
type raiseParamVisitor struct {
	ast.DefaultVisitor
}

func (v *raiseParamVisitor) ExitTypeAnn(ta ast.TypeAnn) {
	typeRef, ok := ta.(*ast.TypeRefTypeAnn)
	if !ok {
		return
	}
	ident, ok := typeRef.Name.(*ast.Ident)
	if !ok || !RaiseParamDecls.Contains(ident.Name) {
		return
	}
	raiseArg := ast.NewRefTypeAnn(
		ast.NewIdentifier(raiseParamName, synthSpan()),
		nil,
		synthSpan(),
	)
	typeRef.TypeArgs = append(typeRef.TypeArgs, raiseArg)
}
