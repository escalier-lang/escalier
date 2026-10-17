package codegen

import (
	"sort"
	"strconv"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// TypeScript has no type-constructor parameters, so a module's .d.ts carries them in the URI
// encoding fp-ts uses. Each constructor the module passes as a type-constructor argument gets a
// string key, its URI, in a registry interface of its arity:
//
//	export interface URItoKind1<A0> { "Array": Array<A0>; "Box": Box<A0> }
//	export type URIS1 = keyof URItoKind1<unknown>
//	export type Kind1<F extends URIS1, A0> = URItoKind1<A0>[F]
//
// A parameter `F<_>` renders `F extends URIS1`, an application `F<A>` renders `Kind1<F, A>`, and
// a constructor argument such as `Array` renders its URI `"Array"`. A parameter with a bound
// renders a constraint naming only the URIs whose instance extends the bound.

// kindRegistry collects the constructors one module's .d.ts names as URIs and the arities it
// uses, for kindRegistryStmts to declare.
type kindRegistry struct {
	// entries holds each registered constructor under its URI, in registration order.
	entries []kindEntry
	// uriByIdentity maps a constructor's qualified rendering to the URI it was registered under,
	// and uris holds every URI in use.
	uriByIdentity map[string]string
	uris          set.Set[string]
	// arities holds every arity a parameter, an application, or an entry used.
	arities set.Set[int]
	// reserved holds the names the module declares, which no name the registry mints may take.
	reserved set.Set[string]
}

// kindEntry is one constructor and the URI it is registered under.
type kindEntry struct {
	uri  string
	ctor *soltype.TypeCtorType
}

// newKindRegistry returns an empty registry whose minted names avoid reserved, the names the
// module declares. A nil reserved reserves nothing.
func newKindRegistry(reserved set.Set[string]) *kindRegistry {
	return &kindRegistry{
		uriByIdentity: map[string]string{},
		uris:          set.NewSet[string](),
		arities:       set.NewSet[int](),
		reserved:      reserved,
	}
}

// uriOf registers ctor and returns its URI. The URI is the constructor as soltype prints it, as
// `Array` or `Tagged<string, _>`, with a `#2`-style suffix when a different constructor already
// took that spelling. It reports false for a constructor whose fixed arguments name a type
// variable or a skolem, since a registry entry has no binder for one.
func (r *kindRegistry) uriOf(ctor *soltype.TypeCtorType) (string, bool) {
	for _, f := range ctor.Fixed {
		if f != nil && soltype.HasTypeVarOrSkolem(f) {
			return "", false
		}
	}
	r.useArity(ctor.Arity())
	identity := soltype.PrintQualified(ctor)
	if uri, ok := r.uriByIdentity[identity]; ok {
		return uri, true
	}
	base := soltype.Print(ctor)
	uri := base
	for n := 2; r.uris.Contains(uri); n++ {
		uri = base + "#" + strconv.Itoa(n)
	}
	r.uriByIdentity[identity] = uri
	r.uris.Add(uri)
	r.entries = append(r.entries, kindEntry{uri: uri, ctor: ctor})
	return uri, true
}

// offer registers ctor when the module already names the registry of ctor's arity, and does
// nothing otherwise.
func (r *kindRegistry) offer(ctor *soltype.TypeCtorType) {
	if r.arities.Contains(ctor.Arity()) {
		r.uriOf(ctor)
	}
}

// useArity records that the module names the registry declarations of arity n.
func (r *kindRegistry) useArity(n int) {
	r.arities.Add(n)
}

// name returns base, followed by as many `_` as it takes to avoid every reserved name.
func (r *kindRegistry) name(base string) string {
	name := base
	for r.reserved.Contains(name) {
		name += "_"
	}
	return name
}

// kindNames returns the registry interface, URI-set alias, and lookup alias names for arity n.
func (r *kindRegistry) kindNames(n int) (registry, uris, kind string) {
	s := strconv.Itoa(n)
	return r.name("URItoKind" + s), r.name("URIS" + s), r.name("Kind" + s)
}

// ctorParamConstraint renders the constraint of a type-constructor parameter with arity slots and
// an optional bound, a TypeCtorType. An unbounded parameter renders `URIS1`. A bounded one
// renders the URIs whose instance extends the bound:
//
//	{[K in URIS1]: URItoKind1<unknown>[K] extends Sized<unknown> ? K : never}[URIS1]
func (b *solTypeAnnBuilder) ctorParamConstraint(arity int, bound soltype.Type) TypeAnn {
	registry, uris, _ := b.kinds.kindNames(arity)
	b.kinds.useArity(arity)
	urisRef := NewRefTypeAnn(uris, nil)
	boundCtor, ok := bound.(*soltype.TypeCtorType)
	if !ok {
		return urisRef
	}
	unknowns := make([]soltype.Type, arity)
	unknownAnns := make([]TypeAnn, arity)
	for i := range unknowns {
		unknowns[i] = &soltype.UnknownType{}
		unknownAnns[i] = NewUnknownTypeAnn(nil)
	}
	boundInst, ok := boundCtor.Apply(unknowns)
	if !ok {
		return urisRef
	}
	key := b.kinds.name("K")
	instance := NewIndexTypeAnn(NewRefTypeAnn(registry, unknownAnns), NewRefTypeAnn(key, nil))
	filter := NewCondTypeAnn(instance, b.typeAnn(boundInst), NewRefTypeAnn(key, nil), NewNeverTypeAnn(nil))
	mapped := NewObjectTypeAnn([]ObjTypeAnnElem{&MappedTypeAnn{
		TypeParam: &IndexParamTypeAnn{Name: key, Constraint: urisRef},
		Name:      nil,
		Value:     filter,
		Optional:  nil,
		ReadOnly:  nil,
	}})
	return NewIndexTypeAnn(mapped, NewRefTypeAnn(uris, nil))
}

// typeAppTypeAnn renders an application `F<A>` as `Kind1<F, A>`. A head that is not a parameter
// this render named has no URI to pass, so it renders `unknown`.
func (b *solTypeAnnBuilder) typeAppTypeAnn(t *soltype.TypeAppType) TypeAnn {
	v, ok := t.Head.(*soltype.TypeVarType)
	if !ok {
		return NewUnknownTypeAnn(nil)
	}
	name, named := b.typeParamNames[v]
	if !named {
		return NewUnknownTypeAnn(nil)
	}
	_, _, kind := b.kinds.kindNames(len(t.Args))
	b.kinds.useArity(len(t.Args))
	args := make([]TypeAnn, 0, len(t.Args)+1)
	args = append(args, NewRefTypeAnn(name, nil))
	for _, a := range t.Args {
		args = append(args, b.typeAnn(a))
	}
	return NewRefTypeAnn(kind, args)
}

// typeCtorTypeAnn renders a constructor argument as its URI, a string literal type. A constructor
// with no URI renders `unknown`.
func (b *solTypeAnnBuilder) typeCtorTypeAnn(t *soltype.TypeCtorType) TypeAnn {
	uri, ok := b.kinds.uriOf(t)
	if !ok {
		return NewUnknownTypeAnn(nil)
	}
	return NewLitTypeAnn(NewStrLit(uri, nil))
}

// kindRegistryStmts declares the registry interface, the URI set, and the lookup alias for each
// arity r used, in arity order. render renders each entry's instance. Each declaration is exported
// when export is set and ambient otherwise, matching how the module's own declarations are
// emitted. It returns nil when r used no arity.
func kindRegistryStmts(r *kindRegistry, render *solTypeAnnBuilder, export bool) []Stmt {
	arities := r.arities.ToSlice()
	sort.Ints(arities)
	var stmts []Stmt
	for _, n := range arities {
		registry, uris, kind := r.kindNames(n)
		params := make([]*TypeParam, n)
		vars := make([]soltype.Type, n)
		unknowns := make([]TypeAnn, n)
		argRefs := make([]TypeAnn, n)
		for i := range n {
			name := r.name("A" + strconv.Itoa(i))
			v := &soltype.TypeVarType{ID: -1 - i}
			render.typeParamNames[v] = name
			vars[i] = v
			params[i] = &TypeParam{Name: name, Constraint: nil, Default: nil}
			unknowns[i] = NewUnknownTypeAnn(nil)
			argRefs[i] = NewRefTypeAnn(name, nil)
		}
		var members []ObjTypeAnnElem
		for _, e := range r.entries {
			if e.ctor.Arity() != n {
				continue
			}
			inst, ok := e.ctor.Apply(vars)
			if !ok {
				continue
			}
			members = append(members, &PropertyTypeAnn{
				Name:     NewStrLit(e.uri, nil),
				Optional: false,
				Readonly: false,
				Value:    render.typeAnn(inst),
			})
		}
		head := r.name("F")
		stmts = append(stmts,
			registryTypeDecl(registry, params, NewObjectTypeAnn(members), true, export),
			registryTypeDecl(uris, nil, NewKeyOfTypeAnn(NewRefTypeAnn(registry, unknowns)), false, export),
			registryTypeDecl(kind,
				append([]*TypeParam{{Name: head, Constraint: NewRefTypeAnn(uris, nil), Default: nil}}, params...),
				NewIndexTypeAnn(NewRefTypeAnn(registry, argRefs), NewRefTypeAnn(head, nil)),
				false, export,
			),
		)
	}
	return stmts
}

// registryTypeDecl returns `type name<params> = body`, or an interface when isInterface is set
// and body is an object type. It is exported when export is set and declared ambient otherwise.
func registryTypeDecl(name string, params []*TypeParam, body TypeAnn, isInterface, export bool) Stmt {
	return &DeclStmt{
		Decl: &TypeDecl{
			Name:       NewIdentifier(name, nil),
			TypeParams: params,
			TypeAnn:    body,
			Interface:  isInterface,
			export:     export,
			declare:    !export,
			span:       nil,
			source:     nil,
		},
		span:   nil,
		source: nil,
	}
}
