package codegen

import (
	"sort"
	"strconv"
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
	// its static side and for an enum variant its constructor. A class's parameters arrive
	// as the binder of each constructor and call signature. Nothing in this namespace binds
	// the name in that position when ok is false.
	ValueType(name string) (soltype.Type, bool)

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
// symbols describes the unique symbols the module declares.
func (b *Builder) BuildDefinitionsFromSol(
	depGraph *dep_graph.DepGraph,
	root SolNamespace,
	preludePrefix string,
	symbols soltype.ModuleSymbols,
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

	b.solSymbolKeys = symbolKeysFromSol(depGraph, root, namespaceGroups[""], symbols.Minted)
	b.solSymbolOwners = symbols.Owners
	b.solSymbolAliases = nil
	b.solSymbolAliasNames = map[int]string{}
	b.solModuleNames = moduleNamesFromSol(namespaceGroups)

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

		names := solNames{preludePrefix: preludePrefix, namespace: namespace}
		namespaceStmts := []Stmt{}
		for _, key := range namespaceGroups[namespace] {
			for _, decl := range depGraph.GetDecls(key) {
				if processedDecls.Contains(decl) {
					continue
				}
				processedDecls.Add(decl)

				b.solShadowed = shadowedNamesFromSol(namespaceGroups, namespace)
				declStmts := b.buildDeclStmtFromSol(decl, declNS, names, namespace == "")
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

	// The aliases go first. TypeScript resolves a name anywhere in the file, so the order
	// is only for a reader, who meets each alias before the namespace that uses it.
	return &Module{Stmts: append(symbolAliasStmtsFromSol(b.solSymbolAliases), stmts...)}
}

// solSymbolAlias is one private top-level value that holds a unique symbol under a name
// a namespace block can reach. `name` is the alias and `path` is the root value's path,
// such as `sym` or `i.key`.
type solSymbolAlias struct {
	name string
	path string
}

// symbolAliasStmtsFromSol declares each alias as `declare const name: typeof path`. The
// alias's type is the original symbol, so `typeof name` and a `[name]` key both name it.
// An alias is not exported, since it exists only for this file's namespace blocks.
func symbolAliasStmtsFromSol(aliases []solSymbolAlias) []Stmt {
	stmts := make([]Stmt, 0, len(aliases))
	for _, alias := range aliases {
		stmts = append(stmts, &DeclStmt{
			Decl: &VarDecl{
				Kind: ValKind,
				Decls: []*Declarator{{
					Pattern: NewIdentPat(alias.name, nil, nil),
					TypeAnn: NewTypeOfTypeAnn(convertQualIdentFromSol(alias.path)),
					Init:    nil,
				}},
				declare: true,
				export:  false,
				span:    nil,
				source:  nil,
			},
			span:   nil,
			source: nil,
		})
	}
	return stmts
}

// moduleNamesFromSol returns every name a module declares: each binding's own name and
// each namespace segment, at every level.
func moduleNamesFromSol(namespaceGroups map[string][]dep_graph.BindingKey) set.Set[string] {
	names := set.NewSet[string]()
	for namespace, keys := range namespaceGroups {
		if namespace != "" {
			for _, segment := range strings.Split(namespace, ".") {
				names.Add(segment)
			}
		}
		for _, key := range keys {
			names.Add(extractLocalName(key.Name()))
		}
	}
	return names
}

// shadowedNamesFromSol returns the names that a declaration inside `namespace` reads as a
// namespace member rather than as the module root's binding. That is every member the
// block or an enclosing block declares, including a nested namespace's name. The module
// root shadows nothing.
func shadowedNamesFromSol(namespaceGroups map[string][]dep_graph.BindingKey, namespace string) set.Set[string] {
	shadowed := set.NewSet[string]()
	if namespace == "" {
		return shadowed
	}
	enclosing := enclosingNamespaces(namespace)
	for other, keys := range namespaceGroups {
		if other == "" {
			continue
		}
		for _, outer := range enclosing {
			if other == outer {
				for _, key := range keys {
					shadowed.Add(extractLocalName(key.Name()))
				}
			}
			// A namespace nested in an enclosing block is a member of that block too.
			if rest, found := strings.CutPrefix(other, outer+"."); found {
				shadowed.Add(strings.Split(rest, ".")[0])
			}
		}
	}
	return shadowed
}

// enclosingNamespaces returns `namespace` and every namespace that encloses it, so
// `a.b` gives `a.b` and `a`.
func enclosingNamespaces(namespace string) []string {
	parts := strings.Split(namespace, ".")
	out := make([]string, len(parts))
	for i := range parts {
		out[i] = strings.Join(parts[:i+1], ".")
	}
	return out
}

// symbolKeysForBlock returns the name a renderer in the current block writes for each
// unique symbol. A symbol whose root name a member of the block shadows is written
// through an alias instead, and the alias is recorded the first time a block needs it.
func (b *Builder) symbolKeysForBlock() map[int]string {
	if b.solShadowed == nil || b.solShadowed.Len() == 0 {
		return b.solSymbolKeys
	}
	keys := make(map[int]string, len(b.solSymbolKeys))
	for id, path := range b.solSymbolKeys {
		root, _, _ := strings.Cut(path, ".")
		if !b.solShadowed.Contains(root) {
			keys[id] = path
			continue
		}
		keys[id] = b.symbolAlias(id, path)
	}
	return keys
}

// symbolAlias returns the alias for the symbol with `id`, held by the root value at
// `path`, and records it to be declared. The name avoids every name the module declares
// and every alias already chosen, so no block can shadow it.
func (b *Builder) symbolAlias(id int, path string) string {
	if name, ok := b.solSymbolAliasNames[id]; ok {
		return name
	}
	base := "__" + strings.ReplaceAll(path, ".", "_") + "_symbol"
	name := base
	for i := 2; b.solModuleNames.Contains(name); i++ {
		name = base + "_" + strconv.Itoa(i)
	}
	b.solModuleNames.Add(name)
	b.solSymbolAliasNames[id] = name
	b.solSymbolAliases = append(b.solSymbolAliases, solSymbolAlias{name: name, path: path})
	return name
}

// symbolKeysFromSol maps each unique symbol the module declares to the path a key off it
// is written through, the map solTypeAnnBuilder.symbolKeys reads. `keys` holds the
// module's top-level bindings in the order the dependency graph typed them, and `minted`
// holds the symbols the solver minted for the module.
//
// A symbol whose name starts at a value of the module root keeps the name the solver gave
// it. `declare val sym: unique symbol` gives `sym`. With
// `interface I { readonly key: unique symbol }` and `declare val i: I`, the symbol is
// `i.key`, so a key off it renders `[i.key]`.
//
// Any other symbol is named after the first top-level `val` whose type it is. With
// `declare var v: I`, the solver leaves `v.key` unnamed, since a `var` may be reassigned,
// and `val k = v.key` names it `k`. A symbol named after a binding in a namespace block
// falls back the same way. A declaration outside the block reaches that binding only
// through the namespace's path, and only when the block exports it.
func symbolKeysFromSol(
	depGraph *dep_graph.DepGraph,
	root SolNamespace,
	keys []dep_graph.BindingKey,
	minted []*soltype.UniqueSymbolType,
) map[int]string {
	rootValues := set.NewSet[string]()
	for _, key := range keys {
		if key.IsValueBinding() {
			rootValues.Add(key.Name())
		}
	}
	symbolKeys := map[int]string{}
	for _, sym := range minted {
		head, _, _ := strings.Cut(sym.Name, ".")
		if sym.Name != "" && rootValues.Contains(head) {
			symbolKeys[sym.ID] = sym.Name
		}
	}
	for _, key := range keys {
		for _, decl := range depGraph.GetDecls(key) {
			varDecl, ok := decl.(*ast.VarDecl)
			if !ok {
				continue
			}
			names := ast.FindBindings(varDecl.Pattern).ToSlice()
			sort.Strings(names)
			for _, name := range names {
				t, ok := root.ValueType(name)
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
func (b *Builder) solRenderer(names solNames, companionPrefix string, typeParams []*soltype.TypeParam) *solTypeAnnBuilder {
	render := newSolTypeAnnBuilder(names, companionPrefix, typeParams)
	render.symbolKeys = b.symbolKeysForBlock()
	render.symbolPaths = b.solSymbolKeys
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
	names solNames,
	isTopLevel bool,
) []Stmt {
	switch decl := decl.(type) {
	case *ast.VarDecl:
		return b.buildVarDeclFromSol(decl, ns, names, isTopLevel)
	case *ast.FuncDecl:
		return b.buildFuncDeclFromSol(decl, ns, names, isTopLevel)
	case *ast.TypeDecl:
		return b.buildTypeDeclFromSol(decl, ns, names, isTopLevel)
	case *ast.InterfaceDecl:
		return b.buildInterfaceDeclFromSol(decl, ns, names, isTopLevel)
	case *ast.ClassDecl:
		return b.buildClassDeclFromSol(decl, ns, names, isTopLevel)
	case *ast.EnumDecl:
		return b.buildEnumDeclFromSol(decl, ns, names, isTopLevel)
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
	names solNames,
	isTopLevel bool,
) []Stmt {
	bound := ast.FindBindings(decl.Pattern).ToSlice()
	sort.Strings(bound)

	stmts := make([]Stmt, 0, len(bound))
	for _, name := range bound {
		bindingType, ok := ns.ValueType(name)
		if !ok {
			continue
		}
		localName := extractLocalName(name)
		render := b.solRenderer(names, localName, nil)
		// A binding annotated with an object type declares the symbols its `readonly`
		// properties hold, which are named after the binding.
		if isTopLevel {
			render.valuePath = name
		}

		// A type mentioning `Self` renders `this`, which TypeScript accepts only inside an
		// interface, so the binding's type moves into one and the binding refers to it.
		// `val inc = counter.increment` on a method returning `Self` is such a binding.
		typeAnn := render.render(bindingType)
		// The binding that names a symbol declares it, so it writes `unique symbol`
		// rather than the `typeof sym` that would refer to itself.
		if sym, isSym := bindingType.(*soltype.UniqueSymbolType); isSym && isTopLevel && b.solSymbolKeys[sym.ID] == name {
			typeAnn = NewUniqueSymbolTypeAnn(nil)
		}
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
	names solNames,
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
	render := b.solRenderer(names, localName, nil)
	sig := render.declFuncTypeAnn(funcType)
	// internal/checker gives `void` to a function whose return it infers from a body
	// that returns no value, and `undefined` to one that writes `-> undefined` or
	// returns `undefined` explicitly. soltype has one kind for both, so the source
	// tells them apart.
	// TODO(#1810): Drop this rewrite once internal/checker is retired.
	if _, isUndefined := funcType.Ret.(*soltype.UndefinedType); isUndefined &&
		decl.Return == nil && !returnsValue(decl.Body) {
		sig.Return = NewVoidTypeAnn(nil)
	}

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

// returnsValue reports whether body holds a `return` with an operand. A `return`
// inside a nested function belongs to that function and is not counted. A nil body
// returns no value.
func returnsValue(body *ast.Block) bool {
	if body == nil {
		return false
	}
	finder := &valueReturnFinder{}
	for _, stmt := range body.Stmts {
		stmt.Accept(finder)
		if finder.found {
			return true
		}
	}
	return false
}

// valueReturnFinder records whether the walk reached a `return` with an operand,
// skipping the bodies of nested functions.
type valueReturnFinder struct {
	ast.DefaultVisitor
	found bool
}

func (v *valueReturnFinder) EnterStmt(stmt ast.Stmt) bool {
	if ret, ok := stmt.(*ast.ReturnStmt); ok && ret.Expr != nil {
		v.found = true
	}
	return !v.found
}

func (v *valueReturnFinder) EnterExpr(expr ast.Expr) bool {
	_, isFunc := expr.(*ast.FuncExpr)
	return !v.found && !isFunc
}

func (v *valueReturnFinder) EnterDecl(decl ast.Decl) bool {
	_, isFunc := decl.(*ast.FuncDecl)
	return !v.found && !isFunc
}

// buildTypeDeclFromSol emits a type alias from the body registered under the
// declaration's name.
func (b *Builder) buildTypeDeclFromSol(
	decl *ast.TypeDecl,
	ns SolNamespace,
	names solNames,
	isTopLevel bool,
) []Stmt {
	body, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || body == nil {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)
	render := b.solRenderer(names, localName, typeParams)
	// The alias's body declares the symbols its `readonly` properties hold, which are
	// named after the value the solver chose as their owner. That value may sit at the
	// module root while the alias sits in a namespace block.
	render.valuePath = b.solSymbolOwners[names.qualified(localName)]
	return typeAliasStmtsFromSol(render, localName, decl.Name, decl, decl.Export(), body, typeParams, isTopLevel)
}

// typeAliasStmtsFromSol renders body with render and returns the companion statements its
// symbols need followed by `type localName<typeParams> = body`. nameNode is the source
// identifier the emitted name maps back to, and source the declaration the alias stands for.
func typeAliasStmtsFromSol(
	render *solTypeAnnBuilder,
	localName string,
	nameNode *ast.Ident,
	source ast.Node,
	export bool,
	body soltype.Type,
	typeParams []*soltype.TypeParam,
	isTopLevel bool,
) []Stmt {
	typeAnn := render.render(body)
	declTypeParams := typeParamsFromSol(render, typeParams)

	stmts := companionStmtsFromSol(render)
	return append(stmts, &DeclStmt{
		Decl: &TypeDecl{
			Name:       NewIdentifier(localName, nameNode),
			TypeParams: declTypeParams,
			TypeAnn:    typeAnn,
			Interface:  false,
			declare:    isTopLevel,
			export:     export,
			span:       nil,
			source:     source,
		},
		span:   nil,
		source: nil,
	})
}

// buildInterfaceDeclFromSol emits an interface from the body its arms merged into.
func (b *Builder) buildInterfaceDeclFromSol(
	decl *ast.InterfaceDecl,
	ns SolNamespace,
	names solNames,
	isTopLevel bool,
) []Stmt {
	body, typeParams, ok := ns.DeclaredType(decl.Name.Name)
	if !ok || body == nil {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)
	render := b.solRenderer(names, localName, typeParams)
	// The interface's body declares the symbols its `readonly` properties hold, which
	// are named after the value the solver chose as their owner. That value may sit at
	// the module root while the interface sits in a namespace block.
	render.valuePath = b.solSymbolOwners[names.qualified(localName)]
	interfaceBody, expressible := interfaceShapeFromSol(body)
	if !expressible {
		// A mapped member whose keys TypeScript cannot list in an interface body, such
		// as `[K: keyof T]`, is valid only in a type literal, so the declaration emits
		// as an alias of one.
		return typeAliasStmtsFromSol(render, localName, decl.Name, decl, decl.Export(), body, typeParams, isTopLevel)
	}
	members, extends := interfacePartsFromSol(render.render(interfaceBody))
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
		return interfaceMembers(t.Elems), nil
	case *IntersectionTypeAnn:
		var members []ObjTypeAnnElem
		var extends []TypeAnn
		for _, arm := range t.Types {
			if obj, ok := arm.(*ObjectTypeAnn); ok {
				members = append(members, interfaceMembers(obj.Elems)...)
				continue
			}
			extends = append(extends, arm)
		}
		return members, extends
	default:
		return nil, nil
	}
}

// interfaceShapeFromSol rewrites each mapped member of an interface body into members
// TypeScript accepts in an interface, and reports false when one has no such form.
// body is the registered body, an object or the intersection of the parents and an
// object. A mapped member splits by key:
//
//   - A string or number literal key becomes an ordinary property, with the key put in
//     place of the key variable in the value. `[K: "x" | "y"]?: number` becomes
//     `x?: number` and `y?: number`.
//   - The keys an index signature accepts stay together in one mapped member, which
//     interfaceMembers lowers once rendered. These are `string`, `number`, `symbol`,
//     and a template literal whose interpolations are each `string` or `number`.
//   - Any other key, such as `keyof T`, has no interface form. So does a member that
//     remaps or filters its keys, an index signature whose value names its key or that
//     lacks the `?`, and a literal key the object already declares as a property.
//
// An index signature also has to be the interface's only member. TypeScript holds every
// other member and every inherited one to the signature's value type, so
// `interface L {name: string, [K: string]: number | undefined}` is rejected, where
// Escalier's `{name: string, [K: string]?: number}` describes an object whose other keys
// hold a number.
func interfaceShapeFromSol(body soltype.Type) (soltype.Type, bool) {
	switch t := body.(type) {
	case *soltype.ObjectType:
		rewritten, sigs, ok := interfaceObjectFromSol(t)
		if !ok || (sigs > 0 && len(rewritten.Elems) > 1) {
			return nil, false
		}
		return rewritten, true
	case *soltype.IntersectionType:
		arms := make([]soltype.Type, len(t.Types))
		sigs, members := 0, 0
		for i, arm := range t.Types {
			obj, ok := arm.(*soltype.ObjectType)
			if !ok {
				// A parent contributes members the signature would govern too.
				arms[i] = arm
				members++
				continue
			}
			rewritten, armSigs, ok := interfaceObjectFromSol(obj)
			if !ok {
				return nil, false
			}
			arms[i] = rewritten
			sigs += armSigs
			members += len(rewritten.Elems)
		}
		if sigs > 0 && members > 1 {
			return nil, false
		}
		return &soltype.IntersectionType{Types: arms}, true
	}
	return body, true
}

// interfaceObjectFromSol is interfaceShapeFromSol for one object. It also returns how
// many of the rewritten object's members are mapped members left for an index signature.
func interfaceObjectFromSol(obj *soltype.ObjectType) (*soltype.ObjectType, int, bool) {
	if !soltype.HasMappedElem(obj.Elems) {
		return obj, 0, true
	}
	sigs := 0
	declared := set.NewSet[string]()
	for _, elem := range obj.Elems {
		if prop, ok := elem.(*soltype.PropertyElem); ok {
			declared.Add(prop.Name)
		}
	}
	elems := make([]soltype.ObjTypeElem, 0, len(obj.Elems))
	for _, elem := range obj.Elems {
		mapped, ok := elem.(*soltype.MappedElem)
		if !ok {
			elems = append(elems, elem)
			continue
		}
		if mapped.Name != nil || mapped.Check != nil || mapped.Extends != nil {
			return nil, 0, false
		}
		var indexKeys []soltype.Type
		for _, key := range keyMembers(mapped.Keys) {
			if name, isLit := literalKeyName(key); isLit {
				// A second declaration of a property the object already declares is a
				// duplicate member in an interface.
				if declared.Contains(name) {
					return nil, 0, false
				}
				declared.Add(name)
				elems = append(elems, &soltype.PropertyElem{
					Name:     name,
					Type:     substMappedKey(mapped.Value, mapped.Key, key),
					Optional: mapped.Optional == soltype.ModAdd,
					Readonly: mapped.Readonly == soltype.ModAdd,
				})
				continue
			}
			if !indexSignatureKey(key) {
				return nil, 0, false
			}
			indexKeys = append(indexKeys, key)
		}
		if len(indexKeys) == 0 {
			continue
		}
		// An index signature has no binding for its key, so a value that names the key
		// would name nothing. interfaceMembers lowers only the optional form, the one an
		// uncountable key set takes.
		if mapped.Optional != soltype.ModAdd || namesMappedKey(mapped.Value, mapped.Key) {
			return nil, 0, false
		}
		keys := indexKeys[0]
		if len(indexKeys) > 1 {
			keys = &soltype.UnionType{Types: indexKeys}
		}
		cp := *mapped
		cp.Keys = keys
		elems = append(elems, &cp)
		sigs++
	}
	cp := *obj
	cp.Elems = elems
	return &cp, sigs, true
}

// keyMembers returns the members of a union key set, or the key set itself.
func keyMembers(keys soltype.Type) []soltype.Type {
	if union, ok := keys.(*soltype.UnionType); ok {
		return union.Types
	}
	return []soltype.Type{keys}
}

// literalKeyName returns the property name a string or number literal key stands for.
func literalKeyName(key soltype.Type) (string, bool) {
	lit, ok := key.(*soltype.LitType)
	if !ok {
		return "", false
	}
	switch l := lit.Lit.(type) {
	case *soltype.StrLit:
		return l.Value, true
	case *soltype.NumLit:
		return strconv.FormatFloat(l.Value, 'f', -1, 64), true
	}
	return "", false
}

// indexSignatureKey reports whether TypeScript accepts key as an index signature's key
// type: `string`, `number`, `symbol`, or a template literal whose interpolations are
// each `string` or `number`.
func indexSignatureKey(key soltype.Type) bool {
	switch k := key.(type) {
	case *soltype.PrimType:
		return k.Prim == soltype.StrPrim || k.Prim == soltype.NumPrim || k.Prim == soltype.SymPrim
	case *soltype.TemplateLitType:
		for _, interp := range k.Interps {
			prim, ok := interp.(*soltype.PrimType)
			if !ok || (prim.Prim != soltype.StrPrim && prim.Prim != soltype.NumPrim) {
				return false
			}
		}
		return true
	}
	return false
}

// substMappedKey returns t with every reference to key replaced by with.
func substMappedKey(t soltype.Type, key *soltype.MappedKeyType, with soltype.Type) soltype.Type {
	return t.Accept(&mappedKeySubst{key: key, with: with}, soltype.Positive)
}

// namesMappedKey reports whether t refers to key.
func namesMappedKey(t soltype.Type, key *soltype.MappedKeyType) bool {
	finder := &mappedKeyFinder{key: key}
	t.Accept(finder, soltype.Positive)
	return finder.found
}

// mappedKeyFinder is the visitor namesMappedKey runs.
type mappedKeyFinder struct {
	key   *soltype.MappedKeyType
	found bool
}

func (v *mappedKeyFinder) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if ref, ok := t.(*soltype.MappedKeyType); ok && ref.ID == v.key.ID {
		v.found = true
	}
	return soltype.EnterResult{SkipChildren: v.found}
}

func (v *mappedKeyFinder) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// mappedKeySubst is the visitor substMappedKey runs.
type mappedKeySubst struct {
	key  *soltype.MappedKeyType
	with soltype.Type
}

func (v *mappedKeySubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	// Matched by id, so a nested mapped member writing the same name stays separate.
	if ref, ok := t.(*soltype.MappedKeyType); ok && ref.ID == v.key.ID {
		return soltype.EnterResult{Type: v.with, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (v *mappedKeySubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// interfaceMembers returns elems with each optional mapped member over its key set
// rewritten as an index signature, since TypeScript accepts no mapped member in an
// interface body. interfaceShapeFromSol leaves only mapped members whose keys an index
// signature accepts. The `?` becomes `| undefined` on the value, so
// `interface D {[K: string]?: number}` emits `[K: string]: number | undefined`.
// Every other member is returned unchanged.
func interfaceMembers(elems []ObjTypeAnnElem) []ObjTypeAnnElem {
	out := make([]ObjTypeAnnElem, len(elems))
	for i, elem := range elems {
		mapped, ok := elem.(*MappedTypeAnn)
		if !ok || mapped.Name != nil || mapped.Optional == nil || *mapped.Optional != MMAdd {
			out[i] = elem
			continue
		}
		out[i] = &IndexSignatureTypeAnn{
			KeyName:  mapped.TypeParam.Name,
			KeyType:  mapped.TypeParam.Constraint,
			Value:    NewUnionTypeAnn([]TypeAnn{mapped.Value, NewUndefinedTypeAnn(nil)}),
			Readonly: mapped.ReadOnly != nil && *mapped.ReadOnly == MMAdd,
		}
	}
	return out
}

// buildClassDeclFromSol emits a class as the two declarations TypeScript needs for
// one: a type naming the instance members, and a value holding the constructor and
// the statics. A class binds both sorts under one name, and `.d.ts` has no `class`
// form that carries the inferred members.
func (b *Builder) buildClassDeclFromSol(
	decl *ast.ClassDecl,
	ns SolNamespace,
	names solNames,
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
	instanceRender := b.solRenderer(names, localName, typeParams)
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

	// The static side binds the class's parameters on each constructor and call signature,
	// which carry them as their own binder, so the value renders
	// `{new <T>(value: T): Box<T>}` and the `T` is bound where TypeScript needs it.
	staticRender := b.solRenderer(names, localName+"_static", nil)
	// A `static readonly` field declares the symbol it holds, which is named after the
	// class, as in `C.key`.
	if isTopLevel {
		staticRender.valuePath = localName
	}
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
	names solNames,
	isTopLevel bool,
) []Stmt {
	variantNS, ok := ns.Namespace(decl.Name.Name)
	if !ok {
		return nil
	}
	localName := extractLocalName(decl.Name.Name)

	// The variants are emitted inside a namespace named after the enum, nested in the one
	// the enum itself is emitted into.
	variantNames := names.within(localName)
	var namespaceStmts []Stmt
	for _, elem := range decl.Elems {
		variant, isVariant := elem.(*ast.EnumVariant)
		if !isVariant {
			// An `...Other` spread contributes its own enum's variants, which this walk
			// does not expand. Tracked in #178.
			continue
		}
		namespaceStmts = append(namespaceStmts,
			b.buildEnumVariantFromSol(variant, variantNS, variantNames)...)
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
	render := b.solRenderer(names, localName, typeParams)
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
	names solNames,
) []Stmt {
	name := variant.Name.Name
	var stmts []Stmt

	if body, typeParams, ok := variantNS.DeclaredType(name); ok && body != nil {
		render := b.solRenderer(names, name, typeParams)
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
		render := b.solRenderer(names, name+"_ctor", nil)
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
