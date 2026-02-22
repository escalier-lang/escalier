package interop

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// convertStatement attempts to convert a dts_parser.Statement to an ast.Decl.
// Returns nil for statements that can't be represented as Decl (like imports).
func convertStatement(stmt dts_parser.Statement) (ast.Decl, error) {
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
		return convertClassDecl(s)
	case *dts_parser.InterfaceDecl:
		return convertInterfaceDecl(s)
	case *dts_parser.ImportDecl,
		*dts_parser.NamedExportStmt, *dts_parser.ExportAllStmt,
		*dts_parser.ExportAssignmentStmt, *dts_parser.ExportAsNamespaceStmt:
		// Skip imports and standalone export statements
		return nil, nil
	case *dts_parser.AmbientDecl:
		// Unwrap and recursively convert the inner declaration
		return convertStatement(s.Declaration)
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
	pattern := ast.NewIdentPat(dv.Name.Name, nil, nil, convertSpan(dv.Name.Span()))

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
		returnType, err = convertTypeAnn(df.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting return type for function %s: %w", df.Name.Name, err)
		}
	}

	return ast.NewFuncDecl(
		ast.NewIdentifier(df.Name.Name, convertSpan(df.Name.Span())),
		typeParams,
		params,
		returnType,
		nil,   // throws - not parsed from .d.ts for now
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
func convertClassDecl(dc *dts_parser.ClassDecl) (*ast.ClassDecl, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(dc.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting type parameters for class %s: %w", dc.Name.Name, err)
	}

	// Convert class members to constructor params and body elements
	var constructorParams []*ast.Param
	var bodyElems []ast.ClassElem

	for _, member := range dc.Members {
		switch m := member.(type) {
		case *dts_parser.ConstructorDecl:
			// Extract constructor parameters
			params, err := convertParams(m.Params)
			if err != nil {
				return nil, fmt.Errorf("converting constructor parameters for class %s: %w", dc.Name.Name, err)
			}
			constructorParams = params

		case *dts_parser.MethodDecl:
			elem, err := convertMethodDecl(m)
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
			elem, err := convertGetterDecl(m)
			if err != nil {
				return nil, fmt.Errorf("converting getter for class %s: %w", dc.Name.Name, err)
			}
			bodyElems = append(bodyElems, elem)

		case *dts_parser.SetterDecl:
			elem, err := convertSetterDecl(m)
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

	// TODO: Handle Extends and Implements
	// These would require extending ast.ClassDecl or storing as metadata

	return ast.NewClassDecl(
		ast.NewIdentifier(dc.Name.Name, convertSpan(dc.Name.Span())),
		typeParams,
		nil, // extends - TODO: parse extends clause from .d.ts files
		constructorParams,
		bodyElems,
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
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

	if di.Name.Name == "PromiseLike" || di.Name.Name == "Promise" {
		errorTypeParam := ast.NewTypeParam(
			"E",
			nil,
			ast.NewAnyTypeAnn(ast.NewSpan(ast.Location{Line: 0, Column: 0}, ast.Location{Line: 0, Column: 0}, 0)),
		)
		typeParams = append(typeParams, &errorTypeParam)
		visitor := &PromiseVisitor{
			ast.DefaultVisitor{},
		}
		objType.Accept(visitor)
	}

	return ast.NewInterfaceDecl(
		ast.NewIdentifier(di.Name.Name, convertSpan(di.Name.Span())),
		typeParams,
		extends,
		objType,
		false, // export - will be set by export handling
		true,  // declare is always true for .d.ts files
		convertSpan(di.Span()),
	), nil
}

type PromiseVisitor struct {
	ast.DefaultVisitor
}

func (v *PromiseVisitor) ExitTypeAnn(ta ast.TypeAnn) {
	if typeRef, ok := ta.(*ast.TypeRefTypeAnn); ok {
		if ident, ok := typeRef.Name.(*ast.Ident); ok && (ident.Name == "Promise" || ident.Name == "PromiseLike") {
			// Add the error type parameter "E" with "any" as the default
			eIdent := ast.NewIdentifier("E", ast.NewSpan(ast.Location{Line: 0, Column: 0}, ast.Location{Line: 0, Column: 0}, 0))
			errorTypeParam := ast.NewRefTypeAnn(
				eIdent,
				nil,
				ast.NewSpan(ast.Location{Line: 0, Column: 0}, ast.Location{Line: 0, Column: 0}, 0),
			)
			typeRef.TypeArgs = append(typeRef.TypeArgs, errorTypeParam)
		}
	}
}
