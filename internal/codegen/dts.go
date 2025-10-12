package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	type_sys "github.com/escalier-lang/escalier/internal/type_system"
)

// declIDs must be sorted according to reverse topological order based on the
// the strongly connected components of the dependency graph.  The reason why
// we pass this in is because we don't want to compute the strongly connected
// components more than once and BuildTopLevelDecls needs this information as well.
func (b *Builder) BuildDefinitions(
	depGraph *dep_graph.DepGraph,
	moduleNS *type_sys.Namespace,
) *Module {
	// Group declarations by namespace
	namespaceGroups := make(map[string][]dep_graph.DeclID)

	var topoDeclIDs []dep_graph.DeclID
	for _, component := range depGraph.Components {
		topoDeclIDs = append(topoDeclIDs, component...)
	}

	// Group declarations by their namespace
	for _, declID := range topoDeclIDs {
		namespace, exists := depGraph.GetDeclNamespace(declID)
		if !exists {
			namespace = "" // Default to root namespace
		}
		namespaceGroups[namespace] = append(namespaceGroups[namespace], declID)
	}

	// Build statements for each namespace
	stmts := []Stmt{}

	// Sort namespace names for consistent output
	var namespaceNames []string
	for namespace := range namespaceGroups {
		namespaceNames = append(namespaceNames, namespace)
	}
	sort.Strings(namespaceNames)

	for _, namespace := range namespaceNames {
		declIDs := namespaceGroups[namespace]

		if namespace == "" {
			// Root namespace declarations go directly to module level
			for _, declID := range declIDs {
				decl, exists := depGraph.GetDecl(declID)
				if !exists {
					continue
				}

				declStmts := b.buildDeclStmt(decl, moduleNS, true)
				if len(declStmts) != 0 {
					stmts = append(stmts, declStmts...)
				}
			}
		} else {
			// Non-root namespace declarations go inside namespace blocks
			namespaceStmts := []Stmt{}
			for _, declID := range declIDs {
				decl, exists := depGraph.GetDecl(declID)
				if !exists {
					continue
				}

				// Find the nested namespace in moduleNS based on the namespace string
				nestedNS := findNamespace(moduleNS, namespace)
				if nestedNS == nil {
					// If the nested namespace doesn't exist, fall back to the module namespace
					nestedNS = moduleNS
				}
				declStmts := b.buildDeclStmt(decl, nestedNS, false)
				if len(declStmts) != 0 {
					namespaceStmts = append(namespaceStmts, declStmts...)
				}
			}

			if len(namespaceStmts) > 0 {
				namespaceDecl := b.buildNamespaceDecl(namespace, namespaceStmts)
				stmts = append(stmts, &DeclStmt{
					Decl:   namespaceDecl,
					span:   nil,
					source: nil,
				})
			}
		}
	}

	return &Module{Stmts: stmts}
}

// buildDeclStmt creates a DeclStmt for a given declaration
func (b *Builder) buildDeclStmt(decl ast.Decl, namespace *type_sys.Namespace, isTopLevel bool) []Stmt {
	switch decl := decl.(type) {
	case *ast.VarDecl:
		keys := ast.FindBindings(decl.Pattern).ToSlice()
		sort.Strings(keys)

		stmts := make([]Stmt, 0, len(keys))
		for _, name := range keys {
			binding := namespace.Values[name]
			if binding == nil {
				continue
			}

			localName := extractLocalName(name)
			bindingType := type_sys.Prune(binding.Type)

			var ifaceStmt *DeclStmt = nil
			typeAnn := b.buildTypeAnn(bindingType)
			if containsSelfTypeRef(bindingType) {
				ifaceName := fmt.Sprintf("__%s_self__", localName)
				ifaceType := replaceSelfWithThis(bindingType)
				ifaceTypeAnn := b.buildTypeAnn(ifaceType)
				ifaceDecl := &TypeDecl{
					Name:       NewIdentifier(ifaceName, nil),
					TypeParams: nil,
					TypeAnn:    ifaceTypeAnn,
					Interface:  true,
					declare:    false, // intentionally not exported, even at the top level
					export:     false,
					span:       nil,
					source:     nil,
				}
				ifaceStmt = &DeclStmt{
					Decl:   ifaceDecl,
					span:   nil,
					source: nil,
				}
				typeAnn = NewRefTypeAnn(ifaceName, nil)
			}

			varDecl := &VarDecl{
				Kind: VariableKind(decl.Kind),
				Decls: []*Declarator{{
					Pattern: NewIdentPat(localName, nil, nil),
					TypeAnn: typeAnn,
					Init:    nil,
				}},
				declare: isTopLevel,
				export:  decl.Export(),
				span:    nil,
				source:  nil,
			}

			if ifaceStmt != nil {
				stmts = append(stmts, ifaceStmt)
			}
			stmts = append(stmts, &DeclStmt{
				Decl:   varDecl,
				span:   nil,
				source: nil,
			})
		}

		return stmts

	case *ast.FuncDecl:
		// For function declarations, the binding is stored with the function name
		binding := namespace.Values[decl.Name.Name]
		if binding == nil {
			return nil
		}

		funcType, ok := binding.Type.(*type_sys.FuncType)
		if !ok {
			return nil
		}

		localName := extractLocalName(decl.Name.Name)

		// Build type parameters from the declaration
		typeParams := make([]*TypeParam, len(decl.TypeParams))
		for i, param := range decl.TypeParams {
			var constraint TypeAnn
			if param.Constraint != nil {
				t := param.Constraint.InferredType()
				if t != nil {
					constraint = b.buildTypeAnn(t)
				}
			}
			var default_ TypeAnn
			if param.Default != nil {
				t := param.Default.InferredType()
				if t != nil {
					default_ = b.buildTypeAnn(t)
				}
			}

			typeParams[i] = &TypeParam{
				Name:       param.Name,
				Constraint: constraint,
				Default:    default_,
			}
		}

		fnDecl := &FuncDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: typeParams,
			Params:     b.funcTypeToParams(funcType),
			// TODO: Use the type annotation if there is one and if not
			// fallback to the inferred return type from the binding.
			TypeAnn: b.buildTypeAnn(funcType.Return),
			Body:    nil,
			declare: isTopLevel, // Only add declare modifier for root namespace
			export:  decl.Export(),
			async:   decl.Async,
			span:    nil,
			source:  nil,
		}
		return []Stmt{
			&DeclStmt{
				Decl:   fnDecl,
				span:   nil,
				source: nil,
			},
		}

	case *ast.TypeDecl:
		typeParams := make([]*TypeParam, len(decl.TypeParams))
		for i, param := range decl.TypeParams {
			var constraint TypeAnn
			if param.Constraint != nil {
				t := param.Constraint.InferredType()
				if t != nil {
					constraint = b.buildTypeAnn(t)
				}
			}
			var default_ TypeAnn
			if param.Default != nil {
				t := param.Default.InferredType()
				if t != nil {
					default_ = b.buildTypeAnn(t)
				}
			}

			typeParams[i] = &TypeParam{
				Name:       param.Name,
				Constraint: constraint,
				Default:    default_,
			}
		}

		typeAnnType := decl.TypeAnn.InferredType()
		if typeAnnType == nil {
			return nil
		}

		localName := extractLocalName(decl.Name.Name)

		typeDecl := &TypeDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: typeParams,
			TypeAnn:    b.buildTypeAnn(typeAnnType),
			Interface:  false,
			declare:    isTopLevel, // Only add declare modifier for root namespace
			export:     decl.Export(),
			span:       nil,
			source:     nil,
		}
		return []Stmt{
			&DeclStmt{
				Decl:   typeDecl,
				span:   nil,
				source: nil,
			},
		}

	case *ast.ClassDecl:
		// For class declarations, generate separate type and constructor declarations
		typeAlias := namespace.Types[decl.Name.Name]
		if typeAlias == nil {
			return nil
		}

		t := type_sys.Prune(typeAlias.Type)

		// Classes are represented as nominal ObjectTypes
		objType, ok := t.(*type_sys.ObjectType)
		if !ok || !objType.Nominal {
			return nil
		}

		localName := extractLocalName(decl.Name.Name)
		var stmts []Stmt

		// Generate instance type declaration using the inferred object type
		instanceTypeAnn := b.buildTypeAnn(objType)

		instanceTypeDecl := &TypeDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: nil, // TODO: handle generic classes
			TypeAnn:    instanceTypeAnn,
			Interface:  false,
			declare:    isTopLevel,
			export:     decl.Export(),
			span:       nil,
			source:     decl,
		}

		stmts = append(stmts, &DeclStmt{
			Decl:   instanceTypeDecl,
			span:   nil,
			source: nil,
		})

		staticTypeBinding := namespace.Values[decl.Name.Name]
		if staticTypeBinding == nil {
			return nil
		}

		staticType, ok := type_sys.Prune(staticTypeBinding.Type).(*type_sys.ObjectType)
		if !ok {
			return nil
		}

		staticTypeAnn := b.buildTypeAnn(staticType)

		staticVarDecl := &VarDecl{
			Kind: ValKind,
			Decls: []*Declarator{{
				Pattern: NewIdentPat(localName, nil, decl.Name),
				TypeAnn: staticTypeAnn,
				Init:    nil,
			}},
			declare: isTopLevel,
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		}

		stmts = append(stmts, &DeclStmt{
			Decl:   staticVarDecl,
			span:   nil,
			source: nil,
		})

		return stmts
	default:
		return nil
	}
}

// buildNamespaceDecl creates a namespace declaration with nested namespaces if needed
func (b *Builder) buildNamespaceDecl(namespace string, stmts []Stmt) Decl {
	parts := strings.Split(namespace, ".")

	// Start from the innermost namespace and work outward
	currentStmts := stmts

	// Build nested namespace declarations from inside out
	for i := len(parts) - 1; i >= 0; i-- {
		namespaceName := parts[i]

		namespaceDecl := &NamespaceDecl{
			Name:    NewIdentifier(namespaceName, nil),
			Body:    currentStmts,
			export:  false,  // Namespaces in .d.ts files are typically not exported at the individual level
			declare: i == 0, // Only the outermost namespace is declared
			span:    nil,
			source:  nil,
		}

		// Wrap this namespace declaration in a DeclStmt for the next level
		currentStmts = []Stmt{&DeclStmt{
			Decl:   namespaceDecl,
			span:   nil,
			source: nil,
		}}
	}

	// Return the outermost namespace declaration
	if len(currentStmts) > 0 {
		if declStmt, ok := currentStmts[0].(*DeclStmt); ok {
			return declStmt.Decl
		}
	}

	// Fallback: create a simple namespace with the full name
	return &NamespaceDecl{
		Name:    NewIdentifier(namespace, nil),
		Body:    stmts,
		export:  false,
		declare: true,
		span:    nil,
		source:  nil,
	}
}

func (b *Builder) buildTypeAnn(t type_sys.Type) TypeAnn {
	switch t := type_sys.Prune(t).(type) {
	case *type_sys.TypeVarType:
		msg := fmt.Sprintf("TODO: generalize types before building .d.ts files, t = %s", t.String())
		panic(msg)
	case *type_sys.TypeRefType:
		typeArgs := make([]TypeAnn, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			typeArgs[i] = b.buildTypeAnn(arg)
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
		case type_sys.SymbolPrim:
			return NewSymbolTypeAnn(nil)
		case type_sys.BigIntPrim:
			panic("TODO: typeToTypeAnn - handle BigIntPrim")
		default:
			panic("typeToTypeAnn: unknown primitive type")
		}
	case *type_sys.AnyType:
		return NewAnyTypeAnn(nil)
	case *type_sys.LitType:
		return NewLitTypeAnn(litToLit(t.Lit))
	case *type_sys.RegexType:
		// For regex types, convert to string type in .d.ts files
		return NewStringTypeAnn(nil)
	case *type_sys.UniqueSymbolType:
		return NewUniqueSymbolTypeAnn(nil)
	case *type_sys.UnknownType:
		return NewUnknownTypeAnn(nil)
	case *type_sys.NeverType:
		return NewNeverTypeAnn(nil)
	case *type_sys.GlobalThisType:
		panic("TODO: implement GlobalThisType")
	case *type_sys.FuncType:
		typeParams := make([]*TypeParam, len(t.TypeParams))
		for i, tp := range t.TypeParams {
			var constraint TypeAnn
			var defaultType TypeAnn
			if tp.Constraint != nil {
				constraint = b.buildTypeAnn(tp.Constraint)
			}
			if tp.Default != nil {
				defaultType = b.buildTypeAnn(tp.Default)
			}
			typeParams[i] = &TypeParam{
				Name:       tp.Name,
				Constraint: constraint,
				Default:    defaultType,
			}
		}
		params := make([]*Param, len(t.Params))
		for i, param := range t.Params {
			typeAnn := b.buildTypeAnn(param.Type)
			params[i] = &Param{
				Pattern:  patToPat(param.Pattern),
				Optional: param.Optional,
				TypeAnn:  typeAnn,
			}
		}
		return NewFuncTypeAnn(
			typeParams,
			params,
			b.buildTypeAnn(t.Return),
			nil,
			nil,
		)
	case *type_sys.ObjectType:
		elems := make([]ObjTypeAnnElem, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = b.buildObjTypeAnnElem(elem, t.SymbolKeyMap)
		}
		return NewObjectTypeAnn(elems)
	case *type_sys.TupleType:
		elems := make([]TypeAnn, len(t.Elems))
		for i, elem := range t.Elems {
			elems[i] = b.buildTypeAnn(elem)
		}
		return NewTupleTypeAnn(elems)
	case *type_sys.RestSpreadType:
		panic("TODO: implement RestSpreadType")
	case *type_sys.UnionType:
		types := make([]TypeAnn, len(t.Types))
		for i, type_ := range t.Types {
			types[i] = b.buildTypeAnn(type_)
		}
		return NewUnionTypeAnn(types)
	case *type_sys.IntersectionType:
		types := make([]TypeAnn, len(t.Types))
		for i, type_ := range t.Types {
			types[i] = b.buildTypeAnn(type_)
		}
		return NewIntersectionTypeAnn(types)
	case *type_sys.KeyOfType:
		return NewKeyOfTypeAnn(b.buildTypeAnn(t.Type))
	case *type_sys.IndexType:
		return NewIndexTypeAnn(
			b.buildTypeAnn(t.Target),
			b.buildTypeAnn(t.Index),
		)
	case *type_sys.CondType:
		return NewCondTypeAnn(
			b.buildTypeAnn(t.Check),
			b.buildTypeAnn(t.Extends),
			b.buildTypeAnn(t.Then),
			b.buildTypeAnn(t.Else),
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
			types[i] = b.buildTypeAnn(type_)
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
	case *type_sys.MutableType:
		return b.buildTypeAnn(t.Type) // Mutable types are treated the same in .d.ts files
	default:
		panic(fmt.Sprintf("unknown type: %s", t))
	}
}

func (b *Builder) buildObjTypeAnnElem(elem type_sys.ObjTypeElem, symbolExprMap map[int]any) ObjTypeAnnElem {
	switch elem := elem.(type) {
	case *type_sys.CallableElemType:
		return &CallableTypeAnn{
			Fn: b.buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.ConstructorElemType:
		return &ConstructorTypeAnn{
			Fn: b.buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.MethodElemType:
		return &MethodTypeAnn{
			Name: b.buildTypeAnnObjKey(elem.Name, symbolExprMap),
			Fn:   b.buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.GetterElemType:
		return &GetterTypeAnn{
			Name: b.buildTypeAnnObjKey(elem.Name, symbolExprMap),
			Fn:   b.buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.SetterElemType:
		return &SetterTypeAnn{
			Name: b.buildTypeAnnObjKey(elem.Name, symbolExprMap),
			Fn:   b.buildFuncTypeAnn(elem.Fn),
		}
	case *type_sys.PropertyElemType:
		return &PropertyTypeAnn{
			Name:     b.buildTypeAnnObjKey(elem.Name, symbolExprMap),
			Optional: elem.Optional,
			Readonly: elem.Readonly,
			Value:    b.buildTypeAnn(elem.Value),
		}
	case *type_sys.MappedElemType:
		typeParam := &IndexParamTypeAnn{
			Name:       elem.TypeParam.Name,
			Constraint: b.buildTypeAnn(elem.TypeParam.Constraint),
		}
		return &MappedTypeAnn{
			TypeParam: typeParam,
			Name:      nil,
			Value:     b.buildTypeAnn(elem.Value),
			Optional:  mapMappedModifier(elem.Optional),
			ReadOnly:  mapMappedModifier(elem.ReadOnly),
		}
	case *type_sys.RestSpreadElemType:
		return &RestSpreadTypeAnn{
			Value: b.buildTypeAnn(elem.Value),
		}
	default:
		panic("unknown object type element")
	}
}

func (b *Builder) funcTypeToParams(fnType *type_sys.FuncType) []*Param {
	params := make([]*Param, len(fnType.Params))
	for i, param := range fnType.Params {
		typeAnn := b.buildTypeAnn(param.Type)
		params[i] = &Param{
			Pattern:  patToPat(param.Pattern),
			Optional: param.Optional,
			TypeAnn:  typeAnn,
		}
	}
	return params
}

func (b *Builder) buildFuncTypeAnn(funcType *type_sys.FuncType) FuncTypeAnn {
	params := b.funcTypeToParams(funcType)

	// Build type parameters
	var typeParams []*TypeParam
	if len(funcType.TypeParams) > 0 {
		typeParams = make([]*TypeParam, len(funcType.TypeParams))
		for i, param := range funcType.TypeParams {
			var constraint TypeAnn
			if param.Constraint != nil {
				constraint = b.buildTypeAnn(param.Constraint)
			}
			var default_ TypeAnn
			if param.Default != nil {
				default_ = b.buildTypeAnn(param.Default)
			}

			typeParams[i] = &TypeParam{
				Name:       param.Name,
				Constraint: constraint,
				Default:    default_,
			}
		}
	}

	return FuncTypeAnn{
		TypeParams: typeParams,
		Params:     params,
		Return:     b.buildTypeAnn(funcType.Return),
		Throws:     nil,
		span:       nil,
		source:     nil,
	}
}

func (b *Builder) buildTypeAnnObjKey(key type_sys.ObjTypeKey, symbolExprMap map[int]any) ObjKey {
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
		e := symbolExprMap[key.Sym]
		expr, _ := b.buildExpr(e.(ast.Expr), nil)
		return &ComputedKey{
			Expr:   expr,
			span:   nil,
			source: nil,
		}
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

// extractLocalName extracts the local name from a qualified name by removing the namespace prefix
func extractLocalName(qualifiedName string) string {
	if lastDot := strings.LastIndex(qualifiedName, "."); lastDot != -1 {
		return qualifiedName[lastDot+1:]
	}
	return qualifiedName
}

// findNamespace navigates through nested namespaces to find the target namespace
// based on a dot-separated namespace string (e.g., "Foo.Bar")
func findNamespace(rootNS *type_sys.Namespace, namespaceStr string) *type_sys.Namespace {
	if namespaceStr == "" {
		return rootNS
	}

	parts := strings.Split(namespaceStr, ".")
	currentNS := rootNS

	for _, part := range parts {
		if nestedNS, exists := currentNS.Namespaces[part]; exists {
			currentNS = nestedNS
		} else {
			// Namespace part not found, return nil
			return nil
		}
	}

	return currentNS
}
