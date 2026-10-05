package codegen

import (
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// dts_decl_soltype.go walks a module's declarations and emits `.d.ts` statements from
// solver results, the twin of the type_system-driven walk in dts.go. It carries the
// `FromSol` suffix dts_soltype.go establishes for a counterpart of a function there,
// and goes when internal/type_system does.

// SolNamespace is the solver surface the declaration walk reads at one namespace level,
// an interface so this package keeps reading type representations rather than the checker
// that produced them. Its implementation lives beside the solver backend. Each method
// answers for the namespace itself, never for an enclosing one, so a prelude binding
// under the same spelling does not answer for the module.
type SolNamespace interface {
	// ValueType is the type a name renders as in value position, which for a class is
	// its static side and for an enum variant its constructor, along with the type
	// parameters the declaration wrote. Nothing in this namespace binds the name in that
	// position when ok is false.
	//
	// The parameters come back under the variables the returned type holds for them, so a
	// renderer names them by pointer. TypeScript writes them on a signature rather than
	// beside the value, so the type keeps each one as a variable rather than eliding a
	// slot nothing mentions.
	ValueType(name string) (soltype.Type, []*soltype.TypeParam, bool)

	// DeclaredType is what a name's type declaration stands for, along with the type
	// parameters it quantifies. A class returns its instance members, and an alias, an
	// enum, or an interface returns its body. The type binding holds only a handle
	// carrying the declaration's name, which is why the members are read here.
	DeclaredType(name string) (soltype.Type, []*soltype.TypeParam, bool)

	// Namespace is the namespace a name declares, which is what an enum's variants
	// are reached through.
	Namespace(name string) (SolNamespace, bool)
}

// BuildDefinitionsFromSol builds `.d.ts` definitions from a solver module run, grouping
// declarations by namespace the way the type_system walk does and emitting them in the
// order depGraph typed them. Namespace names are sorted, so one module's output does not
// depend on map order. preludePrefix is the key the prelude's declarations are registered
// under, which tells a reference to a prelude type from a user's own of the same name.
func (b *Builder) BuildDefinitionsFromSol(
	depGraph *dep_graph.DepGraph,
	root SolNamespace,
	preludePrefix string,
) *Module {
	namespaceGroups := make(map[string][]dep_graph.BindingKey)

	var topoBindingKeys []dep_graph.BindingKey
	for _, component := range depGraph.Components {
		topoBindingKeys = append(topoBindingKeys, component...)
	}
	for _, key := range topoBindingKeys {
		namespace := depGraph.GetNamespace(key)
		namespaceGroups[namespace] = append(namespaceGroups[namespace], key)
	}

	var namespaceNames []string
	for namespace := range namespaceGroups {
		namespaceNames = append(namespaceNames, namespace)
	}
	sort.Strings(namespaceNames)

	b.solSymbolKeys = symbolKeysFromSol(depGraph, root, namespaceGroups[""])

	// A class and an enum each hold a type binding and a value binding pointing at one
	// declaration, so the walk would reach the declaration twice without this.
	processedDecls := set.NewSet[ast.Decl]()
	stmts := []Stmt{}

	for _, namespace := range namespaceNames {
		declNS := root
		if namespace != "" {
			// A namespace the walk cannot find leaves its declarations reading the
			// module's own bindings, which is what the type_system walk falls back to.
			if found, ok := findNamespaceFromSol(root, namespace); ok {
				declNS = found
			}
		}

		namespaceStmts := []Stmt{}
		for _, key := range namespaceGroups[namespace] {
			for _, decl := range depGraph.GetDecls(key) {
				if processedDecls.Contains(decl) {
					continue
				}
				processedDecls.Add(decl)

				declStmts := b.buildDeclStmtFromSol(decl, declNS, preludePrefix, namespace == "")
				namespaceStmts = append(namespaceStmts, declStmts...)
			}
		}

		if len(namespaceStmts) == 0 {
			continue
		}
		if namespace == "" {
			stmts = append(stmts, namespaceStmts...)
			continue
		}
		stmts = append(stmts, &DeclStmt{
			Decl:   b.buildNamespaceDecl(namespace, namespaceStmts),
			span:   nil,
			source: nil,
		})
	}

	return &Module{Stmts: stmts}
}

// symbolKeysFromSol maps each unique symbol a module-level `val` binds to that binding's
// name, the map solTypeAnnBuilder.symbolKeys reads. `keys` holds the module's top-level
// bindings in the order the dependency graph typed them. When two bindings hold one
// symbol, the first one in that order names it.
//
// A binding inside a namespace block is left out. A declaration outside the block can
// reach it only through the namespace's path, and only when the block exports it.
func symbolKeysFromSol(depGraph *dep_graph.DepGraph, root SolNamespace, keys []dep_graph.BindingKey) map[int]string {
	symbolKeys := map[int]string{}
	for _, key := range keys {
		for _, decl := range depGraph.GetDecls(key) {
			varDecl, ok := decl.(*ast.VarDecl)
			if !ok {
				continue
			}
			names := ast.FindBindings(varDecl.Pattern).ToSlice()
			sort.Strings(names)
			for _, name := range names {
				t, _, ok := root.ValueType(name)
				if !ok {
					continue
				}
				sym, ok := t.(*soltype.UniqueSymbolType)
				if !ok {
					continue
				}
				if _, named := symbolKeys[sym.ID]; !named {
					symbolKeys[sym.ID] = name
				}
			}
		}
	}
	return symbolKeys
}

// solRenderer returns a renderer for one declaration of the module
// BuildDefinitionsFromSol is walking, which keys a member off a unique symbol through
// the names symbolKeysFromSol found.
func (b *Builder) solRenderer(preludePrefix, companionPrefix string, typeParams []*soltype.TypeParam) *solTypeAnnBuilder {
	render := newSolTypeAnnBuilder(preludePrefix, companionPrefix, typeParams)
	render.symbolKeys = b.solSymbolKeys
	return render
}

// findNamespaceFromSol resolves a dotted namespace path against ns, one segment at a
// time. A path whose every segment resolves returns the namespace it names.
func findNamespaceFromSol(ns SolNamespace, path string) (SolNamespace, bool) {
	if path == "" {
		return ns, true
	}
	current := ns
	for _, part := range strings.Split(path, ".") {
		nested, ok := current.Namespace(part)
		if !ok {
			return nil, false
		}
		current = nested
	}
	return current, true
}

// buildDeclStmtFromSol emits the statements one declaration contributes. isTopLevel
// marks a declaration at the module's own level, which is what carries `declare`. One
// inside a namespace block does not. A declaration whose types the run did not record
// emits nothing rather than a half-written statement, and the solver has already
// reported the fault that left them missing.
func (b *Builder) buildDeclStmtFromSol(
	decl ast.Decl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	switch decl := decl.(type) {
	case *ast.VarDecl:
		return b.buildVarDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	case *ast.FuncDecl:
		return b.buildFuncDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	case *ast.TypeDecl:
		return b.buildTypeDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	case *ast.InterfaceDecl:
		return b.buildInterfaceDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	case *ast.ClassDecl:
		return b.buildClassDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	case *ast.EnumDecl:
		return b.buildEnumDeclFromSol(decl, ns, preludePrefix, isTopLevel)
	default:
		return nil
	}
}

// buildVarDeclFromSol emits one `declare const` per name the declaration's pattern
// binds, in sorted order so a destructuring declaration emits the same way on every
// run. Each name carries its own type, since `val {x, y} = …` binds two.
func (b *Builder) buildVarDeclFromSol(
	decl *ast.VarDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	names := ast.FindBindings(decl.Pattern).ToSlice()
	sort.Strings(names)

	stmts := make([]Stmt, 0, len(names))
	for _, name := range names {
		bindingType, _, ok := ns.ValueType(name)
		if !ok {
			continue
		}
		localName := extractLocalName(name)
		render := b.solRenderer(preludePrefix, localName, nil)

		// A type mentioning `Self` renders `this`, which TypeScript accepts only inside an
		// interface, so the binding's type moves into one and the binding refers to it.
		// `val inc = counter.increment` on a method returning `Self` is such a binding.
		typeAnn := render.render(bindingType)
		var selfStmts []Stmt
		if containsSelfTypeFromSol(bindingType) {
			ifaceName := "__" + localName + "_self__"
			selfStmts = append(selfStmts, &DeclStmt{
				Decl: &TypeDecl{
					Name:       NewIdentifier(ifaceName, nil),
					TypeParams: nil,
					TypeAnn:    interfaceBodyFromSol(typeAnn),
					Interface:  true,
					// Held back from the output's exports on purpose. The name exists
					// to give `this` a body, not for a consumer to write.
					declare: false,
					export:  false,
					span:    nil,
					source:  nil,
				},
				span:   nil,
				source: nil,
			})
			typeAnn = NewRefTypeAnn(ifaceName, nil)
		}

		stmts = append(stmts, companionStmtsFromSol(render)...)
		stmts = append(stmts, selfStmts...)
		stmts = append(stmts, &DeclStmt{
			Decl: &VarDecl{
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
			},
			span:   nil,
			source: nil,
		})
	}
	return stmts
}

// interfaceBodyFromSol wraps a callable type as an interface body, since
// `interface Name { (args): Ret }` is the only interface form a function type has.
// Every other type is already a body.
func interfaceBodyFromSol(typeAnn TypeAnn) TypeAnn {
	fn, ok := typeAnn.(*FuncTypeAnn)
	if !ok {
		return typeAnn
	}
	return NewObjectTypeAnn([]ObjTypeAnnElem{&CallableTypeAnn{Fn: *fn}})
}

// buildFuncDeclFromSol emits a `declare function` from the signature inference gave
// the name. The type parameters come from that signature rather than from the `<…>` the
// declaration wrote, so a parameter an un-annotated function picked up only through
// generalization is declared too.
func (b *Builder) buildFuncDeclFromSol(
	decl *ast.FuncDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	// A declaration error recovery left without params or a body has no signature to
	// emit, and `declare function` needs one.
	if decl.Body == nil && !decl.Declare() {
		return nil
	}
	bindingType, _, ok := ns.ValueType(decl.Name.Name)
	if !ok {
		return nil
	}
	funcType, ok := bindingType.(*soltype.FuncType)
	if !ok {
		return nil
	}

	localName := extractLocalName(decl.Name.Name)
	render := b.solRenderer(preludePrefix, localName, nil)
	sig := render.declFuncTypeAnn(funcType)

	stmts := companionStmtsFromSol(render)
	return append(stmts, &DeclStmt{
		Decl: &FuncDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: sig.TypeParams,
			Params:     sig.Params,
			TypeAnn:    sig.Return,
			Body:       nil,
			declare:    isTopLevel,
			export:     decl.Export(),
			// `.d.ts` declares no function body, so it states no async-ness either.
			async:  false,
			span:   nil,
			source: nil,
		},
		span:   nil,
		source: nil,
	})
}

// buildTypeDeclFromSol emits a type alias from the body registered under the
// declaration's name.
func (b *Builder) buildTypeDeclFromSol(
	decl *ast.TypeDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	body, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || body == nil {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)
	render := b.solRenderer(preludePrefix, localName, typeParams)
	typeAnn := render.render(body)
	declTypeParams := typeParamsFromSol(render, typeParams)

	stmts := companionStmtsFromSol(render)
	return append(stmts, &DeclStmt{
		Decl: &TypeDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: declTypeParams,
			TypeAnn:    typeAnn,
			Interface:  false,
			declare:    isTopLevel,
			export:     decl.Export(),
			span:       nil,
			source:     decl,
		},
		span:   nil,
		source: nil,
	})
}

// buildInterfaceDeclFromSol emits an interface from the body its arms merged into.
func (b *Builder) buildInterfaceDeclFromSol(
	decl *ast.InterfaceDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	body, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || body == nil {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)
	render := b.solRenderer(preludePrefix, localName, typeParams)
	members, extends := interfacePartsFromSol(render.render(body))
	declTypeParams := typeParamsFromSol(render, typeParams)

	stmts := companionStmtsFromSol(render)
	return append(stmts, &DeclStmt{
		Decl: &InterfaceDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: declTypeParams,
			Extends:    extends,
			Members:    members,
			export:     decl.Export(),
			declare:    isTopLevel,
			span:       nil,
			source:     decl,
		},
		span:   nil,
		source: nil,
	})
}

// interfacePartsFromSol splits a rendered interface body into the members it declares
// and the interfaces it extends. An interface extending another is registered as the
// intersection of the parent and the members this one adds, so `interface Employee
// extends Person` arrives as `Person & {employeeId: number}`. Each object arm contributes
// members and every other arm names a parent.
func interfacePartsFromSol(typeAnn TypeAnn) ([]ObjTypeAnnElem, []TypeAnn) {
	switch t := typeAnn.(type) {
	case *ObjectTypeAnn:
		return t.Elems, nil
	case *IntersectionTypeAnn:
		var members []ObjTypeAnnElem
		var extends []TypeAnn
		for _, arm := range t.Types {
			if obj, ok := arm.(*ObjectTypeAnn); ok {
				members = append(members, obj.Elems...)
				continue
			}
			extends = append(extends, arm)
		}
		return members, extends
	default:
		return nil, nil
	}
}

// buildClassDeclFromSol emits a class as the two declarations TypeScript needs for
// one: a type naming the instance members, and a value holding the constructor and
// the statics. A class binds both sorts under one name, and `.d.ts` has no `class`
// form that carries the inferred members.
func (b *Builder) buildClassDeclFromSol(
	decl *ast.ClassDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	instance, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || instance == nil {
		return nil
	}
	staticType, staticParams, ok := ns.ValueType(decl.Name.Name)
	if !ok {
		return nil
	}

	localName := extractLocalName(decl.Name.Name)
	// The two sides render under their own builders, and each seeds the names it mints
	// from a prefix of its own. Both numbering from one prefix would let the instance
	// type and the static side mint the same name for two different bodies.
	instanceRender := b.solRenderer(preludePrefix, localName, typeParams)
	instanceAnn := instanceRender.render(instance)
	instanceTypeParams := typeParamsFromSol(instanceRender, typeParams)

	stmts := companionStmtsFromSol(instanceRender)
	stmts = append(stmts, &DeclStmt{
		Decl: &TypeDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: instanceTypeParams,
			TypeAnn:    instanceAnn,
			Interface:  false,
			declare:    isTopLevel,
			export:     decl.Export(),
			span:       nil,
			source:     decl,
		},
		span:   nil,
		source: nil,
	})

	// The static side leaves the class's parameters for its constructor signature to
	// bind. A `{new (value: T): Box<T>}` naming a `T` nothing binds is not valid
	// TypeScript; the signature has to write `new <T>`.
	staticRender := b.solRenderer(preludePrefix, localName+"_static", nil)
	staticRender.bindOnSignatures(staticParams)
	staticAnn := staticRender.render(staticType)
	stmts = append(stmts, companionStmtsFromSol(staticRender)...)
	return append(stmts, &DeclStmt{
		Decl: &VarDecl{
			Kind: ValKind,
			Decls: []*Declarator{{
				Pattern: NewIdentPat(localName, nil, decl.Name),
				TypeAnn: staticAnn,
				Init:    nil,
			}},
			declare: isTopLevel,
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		},
		span:   nil,
		source: nil,
	})
}

// buildEnumDeclFromSol emits an enum as a namespace holding one type and one constructor
// per variant, beside a type alias naming the union of those variants, so `Color.Hex` is
// both a type and a callable.
//
// An enum with no variant namespace emits nothing at all, matching its twin. The union
// names each variant through that namespace, so emitting the alias without it would
// declare `type Color = Color.Red` against a `Color.Red` nothing declares.
func (b *Builder) buildEnumDeclFromSol(
	decl *ast.EnumDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	variantNS, ok := ns.Namespace(decl.Name.Name)
	if !ok {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)

	var namespaceStmts []Stmt
	for _, elem := range decl.Elems {
		variant, isVariant := elem.(*ast.EnumVariant)
		if !isVariant {
			// An `...Other` spread contributes its own enum's variants, which this walk
			// does not expand. Tracked in #178.
			continue
		}
		namespaceStmts = append(namespaceStmts,
			b.buildEnumVariantFromSol(variant, variantNS, preludePrefix)...)
	}

	var stmts []Stmt
	if len(namespaceStmts) > 0 {
		stmts = append(stmts, &DeclStmt{
			Decl: &NamespaceDecl{
				Name:    NewIdentifier(localName, decl.Name),
				Body:    namespaceStmts,
				export:  decl.Export(),
				declare: isTopLevel,
				span:    nil,
				source:  decl,
			},
			span:   nil,
			source: nil,
		})
	}

	union, typeParams, hasUnion := ns.DeclaredType(decl.Name.Name)
	if !hasUnion || union == nil {
		return stmts
	}
	render := b.solRenderer(preludePrefix, localName, typeParams)
	unionAnn := render.render(union)
	declTypeParams := typeParamsFromSol(render, typeParams)

	stmts = append(stmts, companionStmtsFromSol(render)...)
	return append(stmts, &DeclStmt{
		Decl: &TypeDecl{
			Name:       NewIdentifier(localName, decl.Name),
			TypeParams: declTypeParams,
			TypeAnn:    unionAnn,
			Interface:  false,
			declare:    isTopLevel,
			export:     decl.Export(),
			span:       nil,
			source:     decl,
		},
		span:   nil,
		source: nil,
	})
}

// buildEnumVariantFromSol emits one variant's type and its constructor, both exported
// from the enum's namespace and neither carrying `declare`, which a namespace member
// does not take.
func (b *Builder) buildEnumVariantFromSol(
	variant *ast.EnumVariant,
	variantNS SolNamespace,
	preludePrefix string,
) []Stmt {
	name := variant.Name.Name
	var stmts []Stmt

	if body, typeParams, ok := variantNS.DeclaredType(name); ok && body != nil {
		render := b.solRenderer(preludePrefix, name, typeParams)
		typeAnn := render.render(body)
		declTypeParams := typeParamsFromSol(render, typeParams)
		stmts = append(stmts, companionStmtsFromSol(render)...)
		stmts = append(stmts, &DeclStmt{
			Decl: &TypeDecl{
				Name:       NewIdentifier(name, variant.Name),
				TypeParams: declTypeParams,
				TypeAnn:    typeAnn,
				Interface:  false,
				declare:    false,
				export:     true,
				span:       nil,
				source:     variant,
			},
			span:   nil,
			source: nil,
		})
	}

	if ctorType, _, ok := variantNS.ValueType(name); ok {
		// A prefix of its own, so the variant's type and its constructor cannot mint one
		// name for two bodies.
		render := b.solRenderer(preludePrefix, name+"_ctor", nil)
		ctorAnn := render.render(ctorType)
		stmts = append(stmts, companionStmtsFromSol(render)...)
		stmts = append(stmts, &DeclStmt{
			Decl: &VarDecl{
				Kind: ValKind,
				Decls: []*Declarator{{
					Pattern: NewIdentPat(name, nil, variant.Name),
					TypeAnn: ctorAnn,
					Init:    nil,
				}},
				declare: false,
				export:  true,
				span:    nil,
				source:  variant,
			},
			span:   nil,
			source: nil,
		})
	}

	return stmts
}

// typeParamsFromSol emits a declaration's own `<…>` clause. The names come from the
// renderer, which registered each parameter under the name the declaration wrote, so
// the clause and every reference to it in the body agree.
func typeParamsFromSol(render *solTypeAnnBuilder, typeParams []*soltype.TypeParam) []*TypeParam {
	if len(typeParams) == 0 {
		return nil
	}
	out := make([]*TypeParam, len(typeParams))
	for i, tp := range typeParams {
		var constraint TypeAnn
		if tp.UpperBound != nil {
			constraint = render.typeAnn(tp.UpperBound)
		}
		var defaultType TypeAnn
		if tp.Default != nil {
			defaultType = render.typeAnn(tp.Default)
		}
		out[i] = &TypeParam{Name: tp.Name, Constraint: constraint, Default: defaultType}
	}
	return out
}

// companionStmtsFromSol wraps the declarations a render minted. They carry what
// TypeScript has no inline form for, such as a recursive type's body, so each must be
// emitted before the declaration whose type refers to it.
func companionStmtsFromSol(render *solTypeAnnBuilder) []Stmt {
	companions := render.companionDecls()
	if len(companions) == 0 {
		return nil
	}
	stmts := make([]Stmt, len(companions))
	for i, companion := range companions {
		stmts[i] = &DeclStmt{Decl: companion, span: nil, source: nil}
	}
	return stmts
}
