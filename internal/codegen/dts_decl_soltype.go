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

// SolNamespace is the solver surface the declaration walk reads at one namespace
// level. It is an interface so this package keeps reading type representations alone,
// as the type_system walk does, rather than the checker that produced them. One
// implementation lives beside the solver backend that fills it.
//
// Each method answers for the namespace itself and never for an enclosing one, so a
// prelude binding under the same spelling does not answer for the module.
type SolNamespace interface {
	// ValueType is the type a name renders as in value position, which for a class is
	// its static side and for an enum variant its constructor. Nothing in this
	// namespace binds the name in that position when ok is false.
	ValueType(name string) (soltype.Type, bool)

	// DeclaredType is what a name's type declaration stands for, along with the type
	// parameters it quantifies. A class returns its instance members, and an alias, an
	// enum, or an interface returns its body.
	//
	// A type declaration binds a handle in the type map, which is a reference carrying
	// the declaration's name and nothing about its contents. A walk that has to spell
	// the members out asks for them here rather than reading that binding.
	DeclaredType(name string) (soltype.Type, []*soltype.TypeParam, bool)

	// Namespace is the namespace a name declares, which is what an enum's variants
	// are reached through.
	Namespace(name string) (SolNamespace, bool)
}

// BuildDefinitionsFromSol builds `.d.ts` definitions from a solver module run.
// depGraph is the graph that run walked, so declarations emit in the order inference
// typed them. root is the module's own top level, and preludePrefix is the key the
// prelude package's declarations are registered under, which tells a reference to a
// prelude type from a user's own type of the same name.
//
// The namespace grouping matches the type_system walk. Root-level declarations emit at
// module level, and each other namespace's emit inside a namespace block, with the
// namespace names sorted so one module's output does not depend on map order.
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
// inside a namespace block does not.
//
// A declaration whose types the run did not record emits nothing rather than a
// half-written statement. The solver reports the fault that left them missing, so
// emitting nothing here costs no diagnostic.
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
		bindingType, ok := ns.ValueType(name)
		if !ok {
			continue
		}
		localName := extractLocalName(name)
		render := newSolTypeAnnBuilder(preludePrefix, localName, nil)

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
	bindingType, ok := ns.ValueType(decl.Name.Name)
	if !ok {
		return nil
	}
	funcType, ok := bindingType.(*soltype.FuncType)
	if !ok {
		return nil
	}

	localName := extractLocalName(decl.Name.Name)
	render := newSolTypeAnnBuilder(preludePrefix, localName, nil)
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
	render := newSolTypeAnnBuilder(preludePrefix, localName, typeParams)
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
	render := newSolTypeAnnBuilder(preludePrefix, localName, typeParams)
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
// between its braces and the interfaces it extends.
//
// An interface that extends another is registered as the intersection of the parent and
// the members this one adds, so `interface Employee extends Person { employeeId: number }`
// arrives as `Person & {employeeId: number}`. Each arm that rendered as an object
// contributes its members, and every other arm names a parent. A body that is one object
// extends nothing, and one that rendered as anything else declares no members.
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
	staticType, ok := ns.ValueType(decl.Name.Name)
	if !ok {
		return nil
	}

	localName := extractLocalName(decl.Name.Name)
	// The two sides render under their own builders, and each seeds the names it mints
	// from a prefix of its own. Both numbering from one prefix would let the instance
	// type and the static side mint the same name for two different bodies.
	instanceRender := newSolTypeAnnBuilder(preludePrefix, localName, typeParams)
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
	staticRender := newSolTypeAnnBuilder(preludePrefix, localName+"_static", nil)
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

// buildEnumDeclFromSol emits an enum as a namespace holding one type and one
// constructor per variant, beside a type alias naming the union of those variants.
// `Color.Hex` is then both a type and a callable, and `Color` is the union a value of
// the enum inhabits.
func (b *Builder) buildEnumDeclFromSol(
	decl *ast.EnumDecl,
	ns SolNamespace,
	preludePrefix string,
	isTopLevel bool,
) []Stmt {
	localName := extractLocalName(decl.Name.Name)

	var namespaceStmts []Stmt
	if variantNS, ok := ns.Namespace(decl.Name.Name); ok {
		for _, elem := range decl.Elems {
			variant, ok := elem.(*ast.EnumVariant)
			if !ok {
				// An `...Other` spread contributes its own enum's variants, which this
				// walk does not expand. Tracked in #178.
				continue
			}
			namespaceStmts = append(namespaceStmts,
				b.buildEnumVariantFromSol(variant, variantNS, preludePrefix)...)
		}
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

	union, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || union == nil {
		return stmts
	}
	render := newSolTypeAnnBuilder(preludePrefix, localName, typeParams)
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
		render := newSolTypeAnnBuilder(preludePrefix, name, typeParams)
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

	if ctorType, ok := variantNS.ValueType(name); ok {
		// A prefix of its own, so the variant's type and its constructor cannot mint one
		// name for two bodies.
		render := newSolTypeAnnBuilder(preludePrefix, name+"_ctor", nil)
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
		if tp.Constraint != nil {
			constraint = render.typeAnn(tp.Constraint)
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
