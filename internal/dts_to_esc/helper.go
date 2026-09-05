package dts_to_esc

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

	typeParam := ast.NewTypeParam(tp.Name.Name, constraint, defaultType, tp.Span())
	return &typeParam, nil
}

// TODO(#259): Handle all function param patterns
func convertParam(p *dts_parser.Param) (*ast.Param, error) {
	// Convert the parameter name to an IdentPat pattern
	var pattern ast.Pat = ast.NewIdentPat(p.Name.Name, false, nil, nil, p.Span())
	if p.Rest {
		pattern = ast.NewRestPat(pattern, p.Span())
	}

	var typeAnn ast.TypeAnn
	if p.Type != nil {
		var err error
		typeAnn, err = convertParamTypeAnn(p.Type)
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

// convertParamTypeAnn converts a parameter's type annotation. A `void` parameter accepts
// no useful argument in TypeScript, which rejects every call that passes a value. `never`
// is the Escalier type with no values, so it rejects the same calls.
//
// One behavior is deliberately not carried over. TypeScript lets a `void` parameter go
// unsupplied, so `f()` is legal for `declare function f(x: void)`, while a `never`
// parameter is still required and makes that call an arity error. Marking the parameter
// optional as well would reproduce it. The shape does not occur in the pinned TypeScript
// corpus, whose only non-return `void` is the `declare const name: void` global.
//
// A `void` in any other input position lowers to `undefined` through convertTypeAnn, and a
// return lowers to `unknown` through convertReturnTypeAnn.
func convertParamTypeAnn(ta dts_parser.TypeAnn) (ast.TypeAnn, error) {
	if prim, ok := ta.(*dts_parser.PrimitiveType); ok && prim.Kind == dts_parser.PrimVoid {
		return ast.NewNeverTypeAnn(prim.Span()), nil
	}
	return convertTypeAnn(ta)
}

// convertExpr converts a dts_parser.Expr to an ast.Expr
func convertExpr(expr dts_parser.Expr) (ast.Expr, error) {
	switch e := expr.(type) {
	case *dts_parser.IdentExpr:
		return ast.NewIdent(e.Name, e.Span()), nil
	case *dts_parser.MemberExpr:
		obj, err := convertExpr(e.Object)
		if err != nil {
			return nil, err
		}
		prop := ast.NewIdentifier(e.Prop.Name, e.Prop.Span())
		return ast.NewMember(obj, prop, false, e.Span()), nil
	case *dts_parser.LitExpr:
		switch lit := e.Lit.(type) {
		case *dts_parser.StringLiteral:
			return ast.NewLitExpr(ast.NewString(lit.Value, lit.Span())), nil
		case *dts_parser.NumberLiteral:
			return ast.NewLitExpr(ast.NewNumber(lit.Value, lit.Span())), nil
		default:
			return nil, fmt.Errorf("convertExpr: unsupported literal type %T", lit)
		}
	default:
		return nil, fmt.Errorf("convertExpr: unsupported expression type %T", expr)
	}
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
		expr, err := convertExpr(k.Expr)
		if err != nil {
			return nil, fmt.Errorf("converting computed key: %w", err)
		}
		return ast.NewComputedKey(expr), nil
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
		returnType, err := convertReturnTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting call signature return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(nil, typeParams, params, returnType, nil, m.Span())
		return ast.NewCallableTypeAnn(fn, m.Span()), nil
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
		fn := ast.NewFuncTypeAnn(nil, typeParams, params, returnType, nil, m.Span())
		return ast.NewConstructorTypeAnn(fn, m.Span()), nil
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
		returnType, err := convertReturnTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting method signature return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(nil, typeParams, params, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting method name: %w", err)
		}
		if m.Optional {
			// Escalier has no optional method syntax: `foo?(): T` does
			// not parse. An optional property holding a function type
			// says the same thing and does parse, so `apply?(t: T): any`
			// becomes `apply?: fn (t: T) -> any`. Dropping the marker
			// instead would make every ProxyHandler trap required, and
			// a handler is meant to supply the traps it wants.
			//
			// Nothing is lost to the shape change. A method signature
			// can be overloaded where a property cannot, and none of the
			// 22 optional methods in the pinned lib set is: the repeated
			// names sit in different interfaces.
			prop := ast.NewPropertyTypeAnn(name, true, false, fn, m.Span())
			prop.SetDoc(m.Doc())
			return prop, nil
		}
		elem := ast.NewMethodTypeAnn(name, fn, nil, m.Span())
		elem.SetDoc(m.Doc())
		return elem, nil
	case *dts_parser.PropertySignature:
		typeAnn, err := convertTypeAnn(m.TypeAnn)
		if err != nil {
			return nil, fmt.Errorf("converting property type: %w", err)
		}
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting property name: %w", err)
		}
		elem := ast.NewPropertyTypeAnn(name, m.Optional, m.Readonly, typeAnn, m.Span())
		elem.SetDoc(m.Doc())
		return elem, nil
	case *dts_parser.GetterSignature:
		// Getter has no parameters, returns the type
		returnType, err := convertTypeAnn(m.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting getter return type: %w", err)
		}
		fn := ast.NewFuncTypeAnn(nil, nil, []*ast.Param{}, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting getter name: %w", err)
		}
		elem := ast.NewGetterTypeAnn(name, fn, nil, m.Span())
		elem.SetDoc(m.Doc())
		return elem, nil
	case *dts_parser.SetterSignature:
		// Setter has one parameter, returns undefined
		param, err := convertParam(m.Param)
		if err != nil {
			return nil, fmt.Errorf("converting setter parameter: %w", err)
		}
		returnType := ast.NewLitTypeAnn(ast.NewUndefined(m.Span()), m.Span())
		fn := ast.NewFuncTypeAnn(nil, nil, []*ast.Param{param}, returnType, nil, m.Span())
		name, err := convertPropertyKey(m.Name)
		if err != nil {
			return nil, fmt.Errorf("converting setter name: %w", err)
		}
		elem := ast.NewSetterTypeAnn(name, fn, nil, m.Span())
		elem.SetDoc(m.Doc())
		return elem, nil
	case *dts_parser.IndexSignature:
		// Held back, not unrepresentable. Escalier writes an index
		// signature as a mapped type in its shorthand spelling,
		// `[key: K]: V`, and emitting one is the five lines this case
		// would hold: convert the key and value types, carry `readonly`
		// as a MappedModifier, and build a MappedTypeAnn with
		// Shorthand set.
		//
		// The checker is what is not ready. A property access that
		// reaches an unexpanded MappedElem panics in expand_type.go with
		// "MappedElems should have been expanded before property
		// access", and emitting these takes the interop_mutability
		// fixture down that path. expandMappedElems already does the
		// right thing when it runs — a primitive constraint becomes an
		// IndexSignatureElem — so what is missing is the expansion
		// reaching that access, not the representation.
		//
		// Until then PropertyDescriptorMap emits empty and
		// Object.defineProperties takes an unconstrained object. See
		// #1417.
		return nil, nil
	default:
		return nil, fmt.Errorf("convertInterfaceMember: unknown interface member type %T", member)
	}
}

// convertReturnTypeAnn converts a return-type annotation, the one position where
// TypeScript's `void` needs a reading of its own. A `void` return is bivariant in
// TypeScript: the caller discards the value, so a function returning anything satisfies
// the slot, which is what lets `xs.forEach((x) => x.trim())` type-check. Escalier has no
// `void`, and lowering it to `undefined` would make the slot invariant and reject that
// callback. `unknown` is the type that keeps the position permissive, since every type is
// a subtype of it and the value is never read.
//
// Only a return position takes this reading. A `void` anywhere else lowers to `undefined`
// through convertTypeAnn, so `Promise<void>` becomes `Promise<undefined>`.
func convertReturnTypeAnn(ta dts_parser.TypeAnn) (ast.TypeAnn, error) {
	if prim, ok := ta.(*dts_parser.PrimitiveType); ok && prim.Kind == dts_parser.PrimVoid {
		return ast.NewUnknownTypeAnn(prim.Span()), nil
	}
	return convertTypeAnn(ta)
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
			// TypeScript's `void` becomes `undefined`, the Escalier type for a function
			// that returns no value. Escalier has no `void`, and the converter prints its
			// output as Escalier source, so lowering to a `void` node would emit source
			// the parser rejects.
			return ast.NewLitTypeAnn(ast.NewUndefined(span), span), nil
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
		case dts_parser.PrimUniqueSymbol:
			return ast.NewUniqueSymbolTypeAnn(span), nil
		case dts_parser.PrimObject:
			// `object` is TypeScript's "any non-primitive", so it accepts
			// an object with any properties. Escalier spells that `{...}`,
			// an inexact object type. Plain `{}` is the exact empty
			// object, which accepts one with no properties at all.
			//
			// The distinction is load-bearing: `ObjectConstructor`
			// declares both `keys(o: object)` and `keys(o: {})`, and
			// lowering `object` to `{}` made them one signature.
			obj := ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{}, span)
			obj.Inexact = true
			return obj, nil
		case dts_parser.PrimIntrinsic:
			return ast.NewIntrinsicTypeAnn(span), nil
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
		// Array types in TypeScript are represented as TypeRef to
		// Array<T>; the `readonly` modifier maps to a ReadonlyArray<T>
		// reference so the readonly-twin rewrite in ConvertBucket can
		// flip it to Escalier's immutable `Array<T>` spelling (and
		// leave `mut Array<T>` to be wrapped onto the plain form).
		//
		// t.Readonly is only set for the `readonly T[]` shorthand;
		// a source written as `ReadonlyArray<T>` lands in the
		// TypeReference case above instead.
		name := "Array"
		if t.Readonly {
			name = "ReadonlyArray"
		}
		ident := ast.NewIdentifier(name, t.Span())
		return ast.NewRefTypeAnn(ident, []ast.TypeAnn{elemType}, t.Span()), nil
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
		returnType, err := convertReturnTypeAnn(t.ReturnType)
		if err != nil {
			return nil, fmt.Errorf("converting function return type: %w", err)
		}
		return ast.NewFuncTypeAnn(nil, typeParams, params, returnType, nil, t.Span()), nil
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
		return ast.NewFuncTypeAnn(nil, typeParams, params, returnType, nil, t.Span()), nil
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
		// dts_parser has no Check or Extends field, so a converted mapped member
		// carries neither.
		mappedElem := ast.NewMappedTypeAnn(
			indexParam, asClause, valueType, optional, readonly, nil, nil, false, t.Span(),
		)
		return ast.NewObjectTypeAnn([]ast.ObjTypeAnnElem{mappedElem}, t.Span()), nil
	case *dts_parser.TemplateLiteralType:
		// The printer walks the quasis and emits the interpolation after
		// each, so it needs one more quasi than type. The dts parser
		// omits an empty quasi rather than recording it, so
		// `${string}-${string}` arrives as type, "-", type and the
		// leading and trailing empties have to go back. Without them the
		// printer runs out of quasis and drops the trailing
		// interpolations: `${string}-${string}-${string}-${string}-${string}`
		// on `Crypto.randomUUID` printed as
		// `-${string}-${string}-${string}-${string}`, which matches no UUID.
		quasis := []*ast.Quasi{}
		typeAnns := []ast.TypeAnn{}
		for _, part := range t.Parts {
			switch p := part.(type) {
			case *dts_parser.TemplateString:
				quasis = append(quasis, &ast.Quasi{Value: p.Value, Span: p.Span()})
			case *dts_parser.TemplateType:
				if len(quasis) == len(typeAnns) {
					quasis = append(quasis, &ast.Quasi{Value: "", Span: p.Span()})
				}
				typeAnn, err := convertTypeAnn(p.Type)
				if err != nil {
					return nil, fmt.Errorf("converting template literal type part: %w", err)
				}
				typeAnns = append(typeAnns, typeAnn)
			}
		}
		if len(quasis) == len(typeAnns) {
			quasis = append(quasis, &ast.Quasi{Value: "", Span: t.Span()})
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
		// A type predicate only ever appears as a return type, and Escalier has
		// no surface for the narrowing it declares. The honest conversion is what
		// the function returns at runtime, and the two predicate forms differ
		// there. A guard, `arg is T`, returns a boolean. An assertion,
		// `asserts arg is T`, either throws or returns no value, so it lowers to
		// `undefined`. Converting the right-hand type instead would claim that
		// `isArray(arg: any): arg is any[]` returns an array.
		// TODO(#229): add support for type predicates to Escalier
		if t.Asserts {
			return ast.NewLitTypeAnn(ast.NewUndefined(t.Span()), t.Span()), nil
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
// className is the enclosing class name, passed to Classify for tier-3 signals (explicit author signals).
func convertMethodDecl(cctx *convertCtx, md *dts_parser.MethodDecl, className string) (*ast.MethodElem, error) {
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
		returnType, err = convertReturnTypeAnn(md.ReturnType)
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
	funcExpr := ast.NewFuncExpr(nil, typeParams, params, returnType, nil, md.Modifiers.Async, nil, md.Span())

	// Classify receiver mutability. Static methods have no receiver.
	var receiver *ast.MethodReceiver
	if !md.Modifiers.Static {
		result := cctx.classifyMember(md, className)
		receiver = &ast.MethodReceiver{Mut: result.Mut, Span_: md.Span()}
	}

	return &ast.MethodElem{
		Name:     name,
		Fn:       funcExpr,
		Receiver: receiver,
		Static:   md.Modifiers.Static,
		Private:  md.Modifiers.Private,
		Span_:    md.Span(),
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
		Type:     typeAnn,
		Static:   pd.Modifiers.Static,
		Private:  pd.Modifiers.Private,
		Readonly: pd.Modifiers.Readonly,
		Optional: pd.Optional,
		Span_:    pd.Span(),
	}, nil
}

// convertGetterDecl converts a dts_parser.GetterDecl to an ast.GetterElem.
// className is the enclosing class name, passed to Classify for tier-3 signals (explicit author signals).
func convertGetterDecl(cctx *convertCtx, gd *dts_parser.GetterDecl, className string) (*ast.GetterElem, error) {
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
	funcExpr := ast.NewFuncExpr(nil, nil, []*ast.Param{}, returnType, nil, false, nil, gd.Span())

	// Classify receiver mutability. Static getters have no receiver.
	var receiver *ast.MethodReceiver
	if !gd.Modifiers.Static {
		result := cctx.classifyMember(gd, className)
		receiver = &ast.MethodReceiver{Mut: result.Mut, Span_: gd.Span()}
	}

	return &ast.GetterElem{
		Name:     name,
		Fn:       funcExpr,
		Receiver: receiver,
		Static:   gd.Modifiers.Static,
		Private:  gd.Modifiers.Private,
		Span_:    gd.Span(),
	}, nil
}

// convertSetterDecl converts a dts_parser.SetterDecl to an ast.SetterElem.
// className is the enclosing class name, passed to Classify for tier-3 signals (explicit author signals).
func convertSetterDecl(cctx *convertCtx, sd *dts_parser.SetterDecl, className string) (*ast.SetterElem, error) {
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
	funcExpr := ast.NewFuncExpr(nil, nil, []*ast.Param{param}, returnType, nil, false, nil, sd.Span())

	// Classify receiver mutability. Static setters have no receiver.
	var receiver *ast.MethodReceiver
	if !sd.Modifiers.Static {
		result := cctx.classifyMember(sd, className)
		receiver = &ast.MethodReceiver{Mut: result.Mut, Span_: sd.Span()}
	}

	return &ast.SetterElem{
		Name:     name,
		Fn:       funcExpr,
		Receiver: receiver,
		Static:   sd.Modifiers.Static,
		Private:  sd.Modifiers.Private,
		Span_:    sd.Span(),
	}, nil
}
