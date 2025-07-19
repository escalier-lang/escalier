package codegen

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	type_sys "github.com/escalier-lang/escalier/internal/type_system"
)

// TODO: Update this function to group bindings from the same declaration together
// and order them in the same way as the original code.
func (b *Builder) BuildDefinitions(
	decls []ast.Decl,
	namespace *checker.Namespace,
	// scope *checker.Scope,
) *Module {
	stmts := []Stmt{}

	for _, d := range decls {
		switch decl := d.(type) {
		case *ast.VarDecl:
			keys := ast.FindBindings(decl.Pattern).ToSlice()
			sort.Strings(keys)

			decls := make([]*Declarator, 0, len(keys))
			for _, name := range keys {
				binding := namespace.Values[name]
				typeAnn := buildTypeAnn(binding.Type)
				decls = append(decls, &Declarator{
					Pattern: NewIdentPat(name, nil, nil),
					TypeAnn: typeAnn,
					Init:    nil,
				})
			}

			varDecl := &VarDecl{
				Kind:    VariableKind(decl.Kind),
				Decls:   decls,
				declare: true, // Always true for .d.ts files
				export:  decl.Export(),
				span:    nil,
				source:  nil,
			}
			stmts = append(stmts, &DeclStmt{
				Decl:   varDecl,
				span:   nil,
				source: nil,
			})

		case *ast.FuncDecl:
			binding := namespace.Values[decl.Name.Name]

			funcType := binding.Type.(*type_sys.FuncType)

			fnDecl := &FuncDecl{
				Name:   NewIdentifier(decl.Name.Name, decl.Name),
				Params: funcTypeToParams(funcType),
				// TODO: Use the type annotation if there is one and if not
				// fallback to the inferred return type from the binding.
				TypeAnn: buildTypeAnn(funcType.Return),
				Body:    nil,
				declare: true, // Always true for .d.ts files
				export:  decl.Export(),
				span:    nil,
				source:  nil,
			}
			stmts = append(stmts, &DeclStmt{
				Decl:   fnDecl,
				span:   nil,
				source: nil,
			})
		case *ast.TypeDecl:
			typeParams := make([]*TypeParam, len(decl.TypeParams))
			for i, param := range decl.TypeParams {
				var constraint TypeAnn
				if param.Constraint != nil {
					t := param.Constraint.InferredType()
					if t == nil {
						// TODO: report an error if there's no inferred type
					}
					constraint = buildTypeAnn(t)
				}
				var default_ TypeAnn
				if param.Default != nil {
					t := param.Default.InferredType()
					if t == nil {
						// TODO: report an error if there's no inferred type
					}
					default_ = buildTypeAnn(t)
				}

				typeParams[i] = &TypeParam{
					Name:       param.Name,
					Constraint: constraint,
					Default:    default_,
				}
			}

			typeAnnType := decl.TypeAnn.InferredType()
			if typeAnnType == nil {
				// TODO: report an error if there's no inferred type
				continue
			}

			typeDecl := &TypeDecl{
				Name:       NewIdentifier(decl.Name.Name, decl.Name),
				TypeParams: typeParams,
				TypeAnn:    buildTypeAnn(typeAnnType),
				declare:    true, // Always true for .d.ts files
				export:     decl.Export(),
				span:       nil,
				source:     nil,
			}
			stmts = append(stmts, &DeclStmt{
				Decl:   typeDecl,
				span:   nil,
				source: nil,
			})
		}
	}

	return &Module{Stmts: stmts}
}

func buildTypeAnn(t type_sys.Type) TypeAnn {
	switch t := type_sys.Prune(t).(type) {
	case *type_sys.TypeVarType:
		msg := fmt.Sprintf("TODO: generalize types before building .d.ts files, t = %s", t.String())
		panic(msg)
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
		var typeParams []*TypeParam
		params := make([]*Param, len(t.Params))
		for i, param := range t.Params {
			typeAnn := buildTypeAnn(param.Type)
			params[i] = &Param{
				Pattern:  patToPat(param.Pattern),
				Optional: param.Optional,
				TypeAnn:  typeAnn,
			}
		}
		return NewFuncTypeAnn(
			typeParams,
			params,
			buildTypeAnn(t.Return),
			nil,
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
		panic(fmt.Sprintf("unknown type: %s", t))
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
			Value:    buildTypeAnn(elem.Value),
		}
	case *type_sys.MappedElemType:
		typeParam := &IndexParamTypeAnn{
			Name:       elem.TypeParam.Name,
			Constraint: buildTypeAnn(elem.TypeParam.Constraint),
		}
		return &MappedTypeAnn{
			TypeParam: typeParam,
			Name:      nil,
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
			TypeAnn:  typeAnn,
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
			TypeAnn:  typeAnn,
		}
	}

	return FuncTypeAnn{
		TypeParams: nil,
		Params:     params,
		Return:     buildTypeAnn(funcType.Return),
		Throws:     nil,
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
			Value:  key.Str,
			span:   nil,
			source: nil,
		}
	case type_sys.NumObjTypeKeyKind:
		return &NumLit{
			Value:  key.Num,
			span:   nil,
			source: nil,
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
	// case *type_sys.BigIntLit:
	// 	return NewBigIntLit(lit.Value, nil)
	case *type_sys.NullLit:
		return NewNullLit(nil)
	case *type_sys.UndefinedLit:
		return NewUndefinedLit(nil)
	default:
		panic("unknown literal type")
	}
}

func patToPat(pat type_sys.Pat) Pat {
	switch pat := pat.(type) {
	case *type_sys.IdentPat:
		return NewIdentPat(pat.Name, nil, nil)
	case *type_sys.TuplePat:
		elems := make([]Pat, len(pat.Elems))
		for i, elem := range pat.Elems {
			elems[i] = patToPat(elem)
		}
		return NewTuplePat(elems, nil)
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
		panic(fmt.Sprintf("unknown pattern type: %#v", pat))
	}
}
