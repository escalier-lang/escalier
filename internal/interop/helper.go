package interop

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// convertSpan converts a dts_parser span to an ast span.
// Since both use ast.Span, this is a simple identity function.
// TODO: Update AST nodes to make the span field optional and add a `provenance`
// field so that we can link converted nodes back to their original source in a
// .d.ts files.
func convertSpan(span ast.Span) ast.Span {
	return span
}

func convertIdent(id *dts_parser.Ident) *ast.Ident {
	return ast.NewIdentifier(id.Name, id.Span())
}

func convertQualIdent(qi dts_parser.QualIdent) ast.QualIdent {
	switch q := qi.(type) {
	case *dts_parser.Ident:
		return convertIdent(q)
	case *dts_parser.Member:
		return &ast.Member{
			Left:  convertQualIdent(q.Left),
			Right: convertIdent(q.Right),
		}
	default:
		return nil
	}
}

func convertTypeParam(tp *dts_parser.TypeParam) (*ast.TypeParam, error) {
	var constraint ast.TypeAnn
	if tp.Constraint != nil {
		var err error
		constraint, err = convertTypeAnn(tp.Constraint)
		if err != nil {
			return nil, fmt.Errorf("converting type parameter constraint: %w", err)
		}
	}

	var defaultType ast.TypeAnn
	if tp.Default != nil {
		var err error
		defaultType, err = convertTypeAnn(tp.Default)
		if err != nil {
			return nil, fmt.Errorf("converting type parameter default: %w", err)
		}
	}

	typeParam := ast.NewTypeParam(tp.Name.Name, constraint, defaultType)
	return &typeParam, nil
}

func convertParam(p *dts_parser.Param) (*ast.Param, error) {
	// Convert the parameter name to an IdentPat pattern
	pattern := ast.NewIdentPat(p.Name.Name, nil, nil, p.Span())

	var typeAnn ast.TypeAnn
	if p.Type != nil {
		var err error
		typeAnn, err = convertTypeAnn(p.Type)
		if err != nil {
			return nil, fmt.Errorf("converting parameter type: %w", err)
		}
	}

	return &ast.Param{
		Pattern:  pattern,
		Optional: p.Optional,
		TypeAnn:  typeAnn,
	}, nil
}

func convertPropertyKey(pk dts_parser.PropertyKey) (ast.ObjKey, error) {
	switch k := pk.(type) {
	case *dts_parser.Ident:
		return ast.NewIdent(k.Name, k.Span()), nil
	case *dts_parser.StringLiteral:
		return ast.NewString(k.Value, k.Span()), nil
	case *dts_parser.NumberLiteral:
		return ast.NewNumber(k.Value, k.Span()), nil
	case *dts_parser.ComputedKey:
		// In dts_parser, ComputedKey.Expr is a TypeAnn
		// In ast, ComputedKey.Expr is an Expr
		// We need to handle this conversion somehow
		// TODO: implement conversion for computed keys
		return nil, fmt.Errorf("convertPropertyKey: ComputedKey not yet implemented")
	default:
		return nil, fmt.Errorf("convertPropertyKey: unknown property key type %T", pk)
	}
}

func convertInterfaceMember(member dts_parser.InterfaceMember) (ast.ObjTypeAnnElem, error) {
	switch m := member.(type) {
	case *dts_parser.CallSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			var err error
			typeParams[i], err = convertTypeParam(tp)
			if err != nil {
				return nil, fmt.Errorf("converting call signature type parameter: %w", err)
			}
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			var err error
			params[i], err = convertParam(p)
			if err != nil {
				return nil, fmt.Errorf("converting call signature parameter: %w", err)
			}
		}
		returnType, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting call signature return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		return &ast.CallableTypeAnn{Fn: fn}, nil
	case *dts_parser.ConstructSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			var err error
			typeParams[i], err = convertTypeParam(tp)
			if err != nil {
				return nil, fmt.Errorf("converting construct signature type parameter: %w", err)
			}
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			var err error
			params[i], err = convertParam(p)
			if err != nil {
				return nil, fmt.Errorf("converting construct signature parameter: %w", err)
			}
		}
		returnType, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting construct signature return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		return &ast.ConstructorTypeAnn{Fn: fn}, nil
	case *dts_parser.MethodSignature:
		typeParams := make([]*ast.TypeParam, len(m.TypeParams))
		for i, tp := range m.TypeParams {
			var err error
			typeParams[i], err = convertTypeParam(tp)
			if err != nil {
				return nil, fmt.Errorf("converting method signature type parameter: %w", err)
			}
		}
		params := make([]*ast.Param, len(m.Params))
		for i, p := range m.Params {
			var err error
			params[i], err = convertParam(p)
			if err != nil {
				return nil, fmt.Errorf("converting method signature parameter: %w", err)
			}
		}
		returnType, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting method signature return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(typeParams, params, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting method name: %w", err)
		}
		return &ast.MethodTypeAnn{
			Name: name,
			Fn:   fn,
		}, nil
	case *dts_parser.PropertySignature:
		typeAnn, err := convertTypeAnn(m.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("converting property type: %w", err)
		}
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting property name: %w", err)
		}
		return &ast.PropertyTypeAnn{
			Name:     name,
			Optional: m.Optional,
			Readonly: m.Readonly,
			Value:    typeAnn,
		}, nil
	case *dts_parser.GetterSignature:
		// Getter has no parameters, returns the type
		returnType, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting getter return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(nil, []*ast.Param{}, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting getter name: %w", err)
		}
		return &ast.GetterTypeAnn{
			Name: name,
			Fn:   fn,
		}, nil
	case *dts_parser.SetterSignature:
		// Setter has one parameter, returns undefined
		param, err := convertParam(m.Param)
		if err != nil {
			return nil, fmt.Errorf("converting setter parameter: %w", err)
		}
		returnType := ast.NewLitTypeAnn(ast.NewUndefined(m.Span()), m.Span())
		fn := ast.NewFuncTypeAnn(nil, []*ast.Param{param}, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting setter name: %w", err)
		}
		return &ast.SetterTypeAnn{
			Name: name,
			Fn:   fn,
		}, nil
	case *dts_parser.IndexSignature:
		// Index signatures don't have a direct equivalent in Escalier's ObjTypeAnnElem
		// We could potentially use a MappedTypeAnn or skip them for now
		// For now, we'll skip index signatures
		// TODO: determine how to properly represent index signatures
		return nil, nil
	default:
		return nil, fmt.Errorf("convertInterfaceMember: unknown interface member type %T", member)
	}
}

func convertTypeAnn(ta dts_parser.TypeAnn) (ast.TypeAnn, error) {
	switch t := ta.(type) {
	case *dts_parser.PrimitiveType:
		span := t.Span()
		switch t.Kind {
		case dts_parser.PrimAny:
			return ast.NewAnyTypeAnn(span), nil
		case dts_parser.PrimUnknown:
			return ast.NewUnknownTypeAnn(span), nil
		case dts_parser.PrimVoid:
			return ast.NewVoidTypeAnn(span), nil
		case dts_parser.PrimNull:
			return ast.NewLitTypeAnn(ast.NewNull(span), span), nil
		case dts_parser.PrimUndefined:
			return ast.NewLitTypeAnn(ast.NewUndefined(span), span), nil
		case dts_parser.PrimNever:
			return ast.NewNeverTypeAnn(span), nil
		case dts_parser.PrimString:
			return ast.NewStringTypeAnn(span), nil
		case dts_parser.PrimNumber:
			return ast.NewNumberTypeAnn(span), nil
		case dts_parser.PrimBoolean:
			return ast.NewBooleanTypeAnn(span), nil
		case dts_parser.PrimBigInt:
			return ast.NewBigintTypeAnn(span), nil
		case dts_parser.PrimSymbol:
			return ast.NewSymbolTypeAnn(span), nil
		case dts_parser.PrimObject:
			return ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{}, span), nil
		default:
			return nil, fmt.Errorf("convertTypeAnn: unknown primitive type %d", t.Kind)
		}
	case *dts_parser.LiteralType:
		span := t.Span()
		switch lit := t.Literal.(type) {
		case *dts_parser.StringLiteral:
			return ast.NewLitTypeAnn(ast.NewString(lit.Value, lit.Span()), span), nil
		case *dts_parser.NumberLiteral:
			return ast.NewLitTypeAnn(ast.NewNumber(lit.Value, lit.Span()), span), nil
		case *dts_parser.BooleanLiteral:
			return ast.NewLitTypeAnn(ast.NewBoolean(lit.Value, lit.Span()), span), nil
		case *dts_parser.BigIntLiteral:
			// TODO: parse the string value into a big.Int
			return nil, fmt.Errorf("convertTypeAnn: BigIntLiteral not yet implemented")
		default:
			return nil, fmt.Errorf("convertTypeAnn: unknown literal type %T", lit)
		}
	case *dts_parser.TypeReference:
		typeArgs := make([]ast.TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			var err error
			typeArgs[i], err = convertTypeAnn(arg)
			if err != nil {
				return nil, fmt.Errorf("converting type reference argument %d: %w", i, err)
			}
		}
		return ast.NewRefTypeAnn(convertQualIdent(t.Name), typeArgs, t.Span()), nil
	case *dts_parser.ArrayType:
		elemType, err := convertTypeAnn(t.ElementType)
		if err != nil {
			return nil, fmt.Errorf("converting array element type: %w", err)
		}
		// Array types in TypeScript are represented as TypeRef to Array<T>
		arrayIdent := ast.NewIdentifier("Array", t.Span())
		return ast.NewRefTypeAnn(arrayIdent, []ast.TypeAnn{elemType}, t.Span()), nil
	case *dts_parser.TupleType:
		elems := make([]ast.TypeAnn, len(t.Elements))
		for i, elem := range t.Elements {
			elemType, err := convertTypeAnn(elem.Type)
			if err != nil {
				return nil, fmt.Errorf("converting tuple element %d: %w", i, err)
			}
			if elem.Rest {
				elems[i] = ast.NewRestSpreadTypeAnn(elemType, elem.Span())
			} else {
				elems[i] = elemType
			}
			// TODO: handle optional elements and named elements
		}
		return ast.NewTupleTypeAnn(elems, t.Span()), nil
	case *dts_parser.UnionType:
		types := make([]ast.TypeAnn, len(t.Types))
		for i, typ := range t.Types {
			var err error
			types[i], err = convertTypeAnn(typ)
			if err != nil {
				return nil, fmt.Errorf("converting union type %d: %w", i, err)
			}
		}
		return ast.NewUnionTypeAnn(types, t.Span()), nil
	case *dts_parser.IntersectionType:
		types := make([]ast.TypeAnn, len(t.Types))
		for i, typ := range t.Types {
			var err error
			types[i], err = convertTypeAnn(typ)
			if err != nil {
				return nil, fmt.Errorf("converting intersection type %d: %w", i, err)
			}
		}
		return ast.NewIntersectionTypeAnn(types, t.Span()), nil
	case *dts_parser.FunctionType:
		typeParams := make([]*ast.TypeParam, len(t.TypeParams))
		for i, tp := range t.TypeParams {
			var err error
			typeParams[i], err = convertTypeParam(tp)
			if err != nil {
				return nil, fmt.Errorf("converting function type parameter %d: %w", i, err)
			}
		}
		params := make([]*ast.Param, len(t.Params))
		for i, p := range t.Params {
			var err error
			params[i], err = convertParam(p)
			if err != nil {
				return nil, fmt.Errorf("converting function parameter %d: %w", i, err)
			}
		}
		returnType, err := convertTypeAnn(t.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting function return type: %w", err)
		}
		return ast.NewFuncTypeAnn(typeParams, params, returnType, nil, t.Span()), nil
	case *dts_parser.ConstructorType:
		// Constructor types don't have a direct equivalent in Escalier
		// Convert to a function type for now
		typeParams := make([]*ast.TypeParam, len(t.TypeParams))
		for i, tp := range t.TypeParams {
			var err error
			typeParams[i], err = convertTypeParam(tp)
			if err != nil {
				return nil, fmt.Errorf("converting constructor type parameter %d: %w", i, err)
			}
		}
		params := make([]*ast.Param, len(t.Params))
		for i, p := range t.Params {
			var err error
			params[i], err = convertParam(p)
			if err != nil {
				return nil, fmt.Errorf("converting constructor parameter %d: %w", i, err)
			}
		}
		returnType, err := convertTypeAnn(t.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting constructor return type: %w", err)
		}
		return ast.NewFuncTypeAnn(typeParams, params, returnType, nil, t.Span()), nil
	case *dts_parser.ObjectType:
		elems := make([]ast.ObjTypeAnnElem, 0, len(t.Members))
		for _, member := range t.Members {
			elem, err := convertInterfaceMember(member)
			if err != nil {
				return nil, fmt.Errorf("converting interface member: %w", err)
			}
			if elem != nil { // Skip nil elements (e.g., index signatures)
				elems = append(elems, elem)
			}
		}
		return ast.NewObjectTypeAnn(elems, t.Span()), nil
	case *dts_parser.ParenthesizedType:
		return convertTypeAnn(t.Type)
	case *dts_parser.IndexedAccessType:
		target, err := convertTypeAnn(t.ObjectType)
		if err != nil {
			return nil, fmt.Errorf("converting indexed access target type: %w", err)
		}
		index, err := convertTypeAnn(t.IndexType)
		if err != nil {
			return nil, fmt.Errorf("converting indexed access index type: %w", err)
		}
		return ast.NewIndexTypeAnn(target, index, t.Span()), nil
	case *dts_parser.ConditionalType:
		check, err := convertTypeAnn(t.CheckType)
		if err != nil {
			return nil, fmt.Errorf("converting conditional check type: %w", err)
		}
		extends, err := convertTypeAnn(t.ExtendsType)
		if err != nil {
			return nil, fmt.Errorf("converting conditional extends type: %w", err)
		}
		trueType, err := convertTypeAnn(t.TrueType)
		if err != nil {
			return nil, fmt.Errorf("converting conditional true type: %w", err)
		}
		falseType, err := convertTypeAnn(t.FalseType)
		if err != nil {
			return nil, fmt.Errorf("converting conditional false type: %w", err)
		}
		return ast.NewCondTypeAnn(check, extends, trueType, falseType, t.Span()), nil
	case *dts_parser.InferType:
		return ast.NewInferTypeAnn(t.TypeParam.Name.Name, t.Span()), nil
	case *dts_parser.MappedType:
		// Convert type parameter
		var constraint ast.TypeAnn
		if t.TypeParam.Constraint != nil {
			var err error
			constraint, err = convertTypeAnn(t.TypeParam.Constraint)
			if err != nil {
				return nil, fmt.Errorf("converting mapped type parameter constraint: %w", err)
			}
		}
		indexParam := &ast.IndexParamTypeAnn{
			Name:       t.TypeParam.Name.Name,
			Constraint: constraint,
		}

		// Convert value type
		valueType, err := convertTypeAnn(t.ValueType)
		if err != nil {
			return nil, fmt.Errorf("converting mapped type value: %w", err)
		}

		// Convert optional modifier
		var optional *ast.MappedModifier
		switch t.Optional {
		case dts_parser.OptionalAdd:
			m := ast.MMAdd
			optional = &m
		case dts_parser.OptionalRemove:
			m := ast.MMRemove
			optional = &m
		case dts_parser.OptionalNone:
			optional = nil
		}

		// Convert readonly modifier
		var readonly *ast.MappedModifier
		switch t.Readonly {
		case dts_parser.ReadonlyAdd:
			m := ast.MMAdd
			readonly = &m
		case dts_parser.ReadonlyRemove:
			m := ast.MMRemove
			readonly = &m
		case dts_parser.ReadonlyNone:
			readonly = nil
		}

		// Convert as clause (key remapping)
		var asClause ast.TypeAnn
		if t.AsClause != nil {
			var err error
			asClause, err = convertTypeAnn(t.AsClause)
			if err != nil {
				return nil, fmt.Errorf("converting mapped type as clause: %w", err)
			}
		}

		// MappedTypeAnn is an ObjTypeAnnElem, so wrap it in an ObjectTypeAnn
		mappedElem := &ast.MappedTypeAnn{
			TypeParam: indexParam,
			Name:      asClause,
			Value:     valueType,
			Optional:  optional,
			ReadOnly:  readonly,
			Check:     nil, // dts_parser doesn't have Check field
			Extends:   nil, // dts_parser doesn't have Extends field
		}
		return ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{mappedElem}, t.Span()), nil
	case *dts_parser.TemplateLiteralType:
		quasis := []*ast.Quasi{}
		typeAnns := []ast.TypeAnn{}
		for _, part := range t.Parts {
			switch p := part.(type) {
			case *dts_parser.TemplateString:
				quasis = append(quasis, &ast.Quasi{Value: p.Value, Span: p.Span()})
			case *dts_parser.TemplateType:
				typeAnn, err := convertTypeAnn(p.Type)
				if err != nil {
					return nil, fmt.Errorf("converting template literal type part: %w", err)
				}
				typeAnns = append(typeAnns, typeAnn)
			}
		}
		return ast.NewTemplateLitTypeAnn(quasis, typeAnns, t.Span()), nil
	case *dts_parser.KeyOfType:
		typ, err := convertTypeAnn(t.Type)
		if err != nil {
			return nil, fmt.Errorf("converting keyof type: %w", err)
		}
		return ast.NewKeyOfTypeAnn(typ, t.Span()), nil
	case *dts_parser.TypeOfType:
		return ast.NewTypeOfTypeAnn(convertQualIdent(t.Expr), t.Span()), nil
	case *dts_parser.ImportType:
		typeArgs := make([]ast.TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			var err error
			typeArgs[i], err = convertTypeAnn(arg)
			if err != nil {
				return nil, fmt.Errorf("converting import type argument %d: %w", i, err)
			}
		}
		var qualifier ast.QualIdent
		if t.Name != nil {
			qualifier = convertQualIdent(t.Name)
		}
		return ast.NewImportType(t.Module, qualifier, typeArgs, t.Span()), nil
	case *dts_parser.TypePredicate:
		// Type predicates don't have a direct equivalent in Escalier
		// Convert to the type being asserted
		// TODO(#229): add support for type predicates to Escalier
		if t.Type != nil {
			return convertTypeAnn(t.Type)
		}
		return ast.NewBooleanTypeAnn(t.Span()), nil
	case *dts_parser.ThisType:
		// Map TypeScript's `this` type to Escalier's `Self` type
		selfIdent := ast.NewIdentifier("Self", t.Span())
		return ast.NewRefTypeAnn(selfIdent, []ast.TypeAnn{}, t.Span()), nil
	case *dts_parser.RestType:
		typ, err := convertTypeAnn(t.Type)
		if err != nil {
			return nil, fmt.Errorf("converting rest type: %w", err)
		}
		return ast.NewRestSpreadTypeAnn(typ, t.Span()), nil
	case *dts_parser.OptionalType:
		// Optional types in tuples - convert to union with undefined
		typ, err := convertTypeAnn(t.Type)
		if err != nil {
			return nil, fmt.Errorf("converting optional type: %w", err)
		}
		undefinedType := ast.NewLitTypeAnn(ast.NewUndefined(t.Span()), t.Span())
		return ast.NewUnionTypeAnn([]ast.TypeAnn{typ, undefinedType}, t.Span()), nil
	default:
		return nil, fmt.Errorf("convertTypeAnn: unknown type annotation %T", ta)
	}
}

// convertTypeParams converts a slice of dts_parser.TypeParam to a slice of ast.TypeParam.
func convertTypeParams(typeParams []*dts_parser.TypeParam) ([]*ast.TypeParam, error) {
	result := make([]*ast.TypeParam, len(typeParams))
	for i, tp := range typeParams {
		var err error
		result[i], err = convertTypeParam(tp)
		if err != nil {
			return nil, fmt.Errorf("converting type parameter %d: %w", i, err)
		}
	}
	return result, nil
}

// convertParams converts a slice of dts_parser.Param to a slice of ast.Param.
func convertParams(params []*dts_parser.Param) ([]*ast.Param, error) {
	result := make([]*ast.Param, len(params))
	for i, p := range params {
		var err error
		result[i], err = convertParam(p)
		if err != nil {
			return nil, fmt.Errorf("converting parameter %d: %w", i, err)
		}
	}
	return result, nil
}

// convertMethodDecl converts a dts_parser.MethodDecl to an ast.MethodElem.
func convertMethodDecl(md *dts_parser.MethodDecl) (*ast.MethodElem, error) {
	// Convert type parameters
	typeParams, err := convertTypeParams(md.TypeParams)
	if err != nil {
		return nil, fmt.Errorf("converting method type parameters: %w", err)
	}

	// Convert parameters
	params, err := convertParams(md.Params)
	if err != nil {
		return nil, fmt.Errorf("converting method parameters: %w", err)
	}

	// Convert return type
	var returnType ast.TypeAnn
	if md.ReturnType != nil {
		returnType, err = convertTypeAnn(md.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting method return type: %w", err)
		}
	}

	// Convert property key to object key
	name, err := convertPropertyKey(md.Name)
	if err != nil {
		return nil, fmt.Errorf("converting method name: %w", err)
	}

	// Create a function expression for the method
	funcExpr := ast.NewFuncExpr(typeParams, params, returnType, nil, md.Modifiers.Async, nil, md.Span())

	return &ast.MethodElem{
		Name:    name,
		Fn:      funcExpr,
		MutSelf: nil, // Not determined from .d.ts
		Static:  md.Modifiers.Static,
		Private: md.Modifiers.Private,
		Span_:   md.Span(),
	}, nil
}

// convertPropertyDecl converts a dts_parser.PropertyDecl to an ast.FieldElem.
func convertPropertyDecl(pd *dts_parser.PropertyDecl) (*ast.FieldElem, error) {
	// Convert property key to object key
	name, err := convertPropertyKey(pd.Name)
	if err != nil {
		return nil, fmt.Errorf("converting property name: %w", err)
	}

	// Convert type annotation
	var typeAnn ast.TypeAnn
	if pd.TypeAnn != nil {
		typeAnn, err = convertTypeAnn(pd.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("converting property type: %w", err)
		}
	}

	return &ast.FieldElem{
		Name:     name,
		Value:    nil, // No value in declarations
		Type:     typeAnn,
		Default:  nil, // No default in declarations
		Static:   pd.Modifiers.Static,
		Private:  pd.Modifiers.Private,
		Readonly: pd.Modifiers.Readonly,
		Span_:    pd.Span(),
	}, nil
}

// convertGetterDecl converts a dts_parser.GetterDecl to an ast.GetterElem.
func convertGetterDecl(gd *dts_parser.GetterDecl) (*ast.GetterElem, error) {
	// Convert property key to object key
	name, err := convertPropertyKey(gd.Name)
	if err != nil {
		return nil, fmt.Errorf("converting getter name: %w", err)
	}

	// Convert return type
	var returnType ast.TypeAnn
	if gd.ReturnType != nil {
		returnType, err = convertTypeAnn(gd.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting getter return type: %w", err)
		}
	}

	// Create a function expression for the getter (no params, returns the type)
	funcExpr := ast.NewFuncExpr(nil, []*ast.Param{}, returnType, nil, false, nil, gd.Span())

	return &ast.GetterElem{
		Name:    name,
		Fn:      funcExpr,
		Static:  gd.Modifiers.Static,
		Private: gd.Modifiers.Private,
		Span_:   gd.Span(),
	}, nil
}

// convertSetterDecl converts a dts_parser.SetterDecl to an ast.SetterElem.
func convertSetterDecl(sd *dts_parser.SetterDecl) (*ast.SetterElem, error) {
	// Convert property key to object key
	name, err := convertPropertyKey(sd.Name)
	if err != nil {
		return nil, fmt.Errorf("converting setter name: %w", err)
	}

	// Convert parameter
	param, err := convertParam(sd.Param)
	if err != nil {
		return nil, fmt.Errorf("converting setter parameter: %w", err)
	}

	// Create a function expression for the setter (one param, returns undefined)
	returnType := ast.NewLitTypeAnn(ast.NewUndefined(sd.Span()), sd.Span())
	funcExpr := ast.NewFuncExpr(nil, []*ast.Param{param}, returnType, nil, false, nil, sd.Span())

	return &ast.SetterElem{
		Name:    name,
		Fn:      funcExpr,
		Static:  sd.Modifiers.Static,
		Private: sd.Modifiers.Private,
		Span_:   sd.Span(),
	}, nil
}
