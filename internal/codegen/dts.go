package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	type_sys "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/moznion/go-optional"
)

func (b *Builder) BuildDefinitions(bindings map[string]checker.Binding) *Module {
	stmts := []Stmt{}

	for name, binding := range bindings {
		typeAnn := buildTypeAnn(binding.Type)
		decl := &VarDecl{
			Kind:    VariableKind(ast.ValKind),
			Pattern: NewIdentPat(name, optional.None[Expr](), nil),
			TypeAnn: optional.Some(typeAnn),
			Init:    nil,
			declare: false,
			export:  false,
			span:    nil,
			source:  nil,
		}
		stmts = append(stmts, &DeclStmt{
			Decl:   decl,
			span:   nil,
			source: nil,
		})
	}

	return &Module{Stmts: stmts}
}

func buildTypeAnn(t type_sys.Type) TypeAnn {
	switch t := type_sys.Prune(t).(type) {
	case *type_sys.TypeVarType:
		panic("TODO: generalize types before building .d.ts files")
	case *type_sys.TypeRefType:
		typeArgs := make([]TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			typeArgs[i] = buildTypeAnn(arg)
		}
		return NewRefTypeAnn(t.Name, typeArgs)
	case *type_sys.PrimType:
		switch t.Prim {
		case type_sys.NumPrim:
			return NewNumberTypeAnn(nil)
		case type_sys.BoolPrim:
			return NewBooleanTypeAnn(nil)
		case type_sys.StrPrim:
			return NewStringTypeAnn(nil)
		case type_sys.BigIntPrim:
			panic("TODO: typeToTypeAnn - handle BigIntPrim")
		case type_sys.SymbolPrim:
			panic("TODO: typeToTypeAnn - handle BigIntPrim")
		default:
			panic("typeToTypeAnn: unknown primitive type")
		}
	case *type_sys.LitType:
		return NewLitTypeAnn(litToLit(t.Lit))
	case *type_sys.UniqueSymbolType:
		panic("TODO: implement UniqueSymbolType")
	case *type_sys.UnknownType:
		return NewUnknownTypeAnn(nil)
	case *type_sys.NeverType:
		return NewNeverTypeAnn(nil)
	case *type_sys.GlobalThisType:
		panic("TODO: implement GlobalThisType")
	case *type_sys.FuncType:
		typeParams := optional.None[[]TypeParam]()
		params := make([]*Param, len(t.Params))
		for i, param := range t.Params {
			typeAnn := buildTypeAnn(param.Type)
			params[i] = &Param{
				Pattern:  patToPat(param.Pattern),
				Optional: param.Optional,
				TypeAnn:  optional.Some(typeAnn),
			}
		}
		return NewFuncTypeAnn(
			typeParams,
			params,
			buildTypeAnn(t.Return),
			optional.None[TypeAnn](),
			nil,
		)
	case *type_sys.ObjectType:
		elems := make([]ObjTypeAnnElem, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = buildObjTypeAnnElem(elem)
		}
		return NewObjectTypeAnn(elems)
	case *type_sys.TupleType:
		elems := make([]TypeAnn, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = buildTypeAnn(elem)
		}
		return NewTupleTypeAnn(elems)
	case *type_sys.RestSpreadType:
		panic("TODO: implement RestSpreadType")
	case *type_sys.UnionType:
		types := make([]TypeAnn, len(t.Types))
		for i, type_ := range t.Types {
			types[i] = buildTypeAnn(type_)
		}
		return NewUnionTypeAnn(types)
	case *type_sys.IntersectionType:
		types := make([]TypeAnn, len(t.Types))
		for i, type_ := range t.Types {
			types[i] = buildTypeAnn(type_)
		}
		return NewIntersectionTypeAnn(types)
	case *type_sys.KeyOfType:
		return NewKeyOfTypeAnn(buildTypeAnn(t.Type))
	case *type_sys.IndexType:
		return NewIndexTypeAnn(
			buildTypeAnn(t.Target),
			buildTypeAnn(t.Index),
		)
	case *type_sys.CondType:
		return NewCondTypeAnn(
			buildTypeAnn(t.Check),
			buildTypeAnn(t.Extends),
			buildTypeAnn(t.Cons),
			buildTypeAnn(t.Alt),
		)
	case *type_sys.InferType:
		return NewInferTypeAnn(t.Name)
	case *type_sys.WildcardType:
		return NewAnyTypeAnn(nil)
	case *type_sys.ExtractorType:
		panic("TODO: implement ExtractorType")
	case *type_sys.TemplateLitType:
		types := make([]TypeAnn, len(t.Types))
		for i, type_ := range t.Types {
			types[i] = buildTypeAnn(type_)
		}
		quasis := make([]*Quasi, len(t.Quasis))
		for i, quasi := range t.Quasis {
			quasis[i] = &Quasi{
				Value: quasi.Value,
				Span:  nil,
			}
		}
		return NewTemplateLitTypeAnn(quasis, types)
	case *type_sys.IntrinsicType:
		return NewIntrinsicTypeAnn(t.Name, nil)
	default:
		panic("unknown type")
	}
}

func buildObjTypeAnnElem(elem type_sys.ObjTypeElem) ObjTypeAnnElem {
	switch elem := elem.(type) {
	case *type_sys.CallableElemType:
		return &CallableTypeAnn{
			Fn: buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.ConstructorElemType:
		return &ConstructorTypeAnn{
			Fn: buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.MethodElemType:
		return &MethodTypeAnn{
			Name: buildObjKey(elem.Name),
			Fn:   buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.GetterElemType:
		return &GetterTypeAnn{
			Name: buildObjKey(elem.Name),
			Fn:   buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.SetterElemType:
		return &SetterTypeAnn{
			Name: buildObjKey(elem.Name),
			Fn:   buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.PropertyElemType:
		return &PropertyTypeAnn{
			Name:     buildObjKey(elem.Name),
			Optional: elem.Optional,
			Readonly: elem.Readonly,
			Value:    optional.Some(buildTypeAnn(elem.Value)),
		}
	case *type_sys.MappedElemType:
		typeParam := &IndexParamTypeAnn{
			Name:       elem.TypeParam.Name,
			Constraint: buildTypeAnn(elem.TypeParam.Constraint),
		}
		return &MappedTypeAnn{
			TypeParam: typeParam,
			Name:      optional.None[TypeAnn](),
			Value:     buildTypeAnn(elem.Value),
			Optional:  mapMappedModifier(elem.Optional),
			ReadOnly:  mapMappedModifier(elem.ReadOnly),
		}
	case *type_sys.RestSpreadElemType:
		return &RestSpreadTypeAnn{
			Value: buildTypeAnn(elem.Value),
		}
	default:
		panic("unknown object type element")
	}
}

func funcTypeToParams(fnType *type_sys.FuncType) []*Param {
	params := make([]*Param, len(fnType.Params))
	for i, param := range fnType.Params {
		typeAnn := buildTypeAnn(param.Type)
		params[i] = &Param{
			Pattern:  patToPat(param.Pattern),
			Optional: param.Optional,
			TypeAnn:  optional.Some(typeAnn),
		}
	}
	return params
}

func buildFuncTypeAnn(funcType *type_sys.FuncType) FuncTypeAnn {
	params := make([]*Param, len(funcType.Params))
	for i, param := range funcType.Params {
		typeAnn := buildTypeAnn(param.Type)
		params[i] = &Param{
			Pattern:  patToPat(param.Pattern),
			Optional: param.Optional,
			TypeAnn:  optional.Some(typeAnn),
		}
	}

	return FuncTypeAnn{
		TypeParams: optional.None[[]TypeParam](),
		Params:     params,
		Return:     buildTypeAnn(funcType.Return),
		Throws:     optional.None[TypeAnn](),
		span:       nil,
		source:     nil,
	}
}

func buildObjKey(key type_sys.ObjTypeKey) ObjKey {
	switch key.Kind {
	case type_sys.StrObjTypeKeyKind:
		// TODO: Check if key.Str is a valid identifier and if it is then return
		// an IdentExpr instead of a StrLit.
		return &StrLit{
			Value: key.Str,
			span:  nil,
		}
	case type_sys.NumObjTypeKeyKind:
		return &NumLit{
			Value: key.Num,
			span:  nil,
		}
	case type_sys.SymObjTypeKeyKind:
		panic("TODO: objTypeKeyToObjKey - SymObjTypeKey")
	default:
		panic("unknown object key type")
	}
}

func mapMappedModifier(mod *type_sys.MappedModifier) *MappedModifier {
	if mod == nil {
		return nil
	}

	switch *mod {
	case type_sys.MMAdd:
		result := MMAdd
		return &result
	case type_sys.MMRemove:
		result := MMRemove
		return &result
	default:
		panic("unknown mapped modifier")
	}
}

func litToLit(t type_sys.Lit) Lit {
	switch lit := t.(type) {
	case *type_sys.BoolLit:
		return NewBoolLit(lit.Value, nil)
	case *type_sys.NumLit:
		return NewNumLit(lit.Value, nil)
	case *type_sys.StrLit:
		return NewStrLit(lit.Value, nil)
	default:
		panic("unknown literal type")
	}
}

func patToPat(pat type_sys.Pat) Pat {
	switch pat := pat.(type) {
	case *type_sys.IdentPat:
		return NewIdentPat(pat.Name, optional.None[Expr](), nil)
	case *type_sys.ObjectPat:
		panic("TODO: patToPat - ObjectPat")
	case *type_sys.ExtractorPat:
		panic("TODO: patToPat - ExtractorPat")
	case *type_sys.RestPat:
		return NewRestPat(patToPat(pat.Pattern), nil)
	case *type_sys.LitPat:
		return NewLitPat(litToLit(pat.Lit), nil)
	case *type_sys.WildcardPat:
		panic("TODO: patToPat - WildcardPat")
	default:
		panic("unknown pattern type")
	}
}
