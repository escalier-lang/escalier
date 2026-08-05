package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// AliasDef is the heavy per-alias data the AliasType handle points at, keyed in the
// Context registry by qualified name, mirroring the ClassType/ClassDef split.
type AliasDef struct {
	// TypeParams are the alias's own type parameters, the `<T, U = T>` of a generic alias,
	// in declaration order so expansion substitutes arguments positionally.
	TypeParams []*soltype.TypeParam

	// LifetimeParams are the alias's quantified lifetime parameters, the lifetime twin of
	// TypeParams, in declaration order so expansion substitutes lifetime arguments
	// positionally. expandAlias maps each one to a reference's positional LifetimeArg. The
	// slice stays empty until a `type` declaration parses a lifetime parameter binder.
	LifetimeParams []*soltype.LifetimeParam

	// Body is the type the alias expands to, the right-hand side of `type Name = Body`.
	// No variance is stored: a transparent alias expands, so variance follows the body.
	Body soltype.Type

	// Level is the alias binding's generalize level. The type-parameter vars stay symbolic
	// in the body and are substituted at expansion, so the level is recorded but not
	// otherwise consulted.
	Level int

	// NotProductive marks an alias checkProductive rejected for recursion that emits no
	// structure. Such an alias names no type, so the evaluator refuses to expand it at all and
	// leaves the operator over it symbolic, and constrain lets it absorb the way an ErrorType
	// operand does. That keeps a diagnostic naming the alias showing the arguments the source
	// wrote, and it stops the rejection from cascading into a reduction that would run until a
	// budget cut it off.
	//
	// checkProductive sets it in the loop that immediately follows body resolution for the
	// alias's dep_graph component, with no inference in between. That ordering is what makes
	// the flag safe to read: until a body is filled, expandAlias yields ErrorType for the
	// alias and expandAliasGuarded declines to expand it, so no reduction can reach a
	// resolved body before the flag is set.
	NotProductive bool

	// PhantomParams holds one entry per TypeParam, true when no argument passed to that
	// parameter can appear in the type an instantiation denotes. markPhantomParams computes it,
	// and internAlias drops a phantom parameter's argument when it renders an alias reference's
	// canonical identity, so two references differing only in those arguments intern to one
	// representative. See phantom.go for what makes a parameter phantom.
	//
	// It is nil for an alias no fixed point ran over, which is an enum's synthesized alias. A
	// nil slice marks nothing phantom, so every argument stays in the identity key.
	PhantomParams []bool

	// Enum marks the alias preBindEnum synthesizes for an `enum` declaration, whose body is
	// the union of the enum's variant handles. A reference to an enum resolves through the
	// alias path, so this is what lets its arity diagnostics name it an enum. It is false for
	// an alias a `type` declaration wrote.
	Enum bool
}

// expandAlias unfolds an alias reference to its registered AliasDef Body, the shared
// transparent-alias helper. A generic reference substitutes its TypeArgs for the
// AliasDef's type-parameter vars, minting a fresh body per expansion. A non-generic one
// returns the stored Body directly. An unregistered reference yields an ErrorType so it
// absorbs.
func (c *Context) expandAlias(ref *soltype.AliasType) soltype.Type {
	def, ok := c.aliasDef(ref.Name)
	if !ok || def.Body == nil {
		return &soltype.ErrorType{}
	}
	if len(def.TypeParams) == 0 && len(def.LifetimeParams) == 0 {
		return def.Body
	}
	subst := newTypeSubst(def.TypeParams, ref.TypeArgs, def.LifetimeParams, ref.LifetimeArgs)
	return def.Body.Accept(subst, soltype.Positive)
}

// aliasShell carries an alias's pre-bound state from preBindAlias to inferAliasBody: the
// registered AliasDef whose Body the body pass fills, and the scope its body resolves in.
// Pre-binding every alias identity in a dep_graph type-key component before any body
// resolves is what lets a self or mutual reference find the sibling already bound, so
// `type A = {b: B}` / `type B = {a: A}` resolve each other.
type aliasShell struct {
	decl *ast.TypeDecl
	ns   string
	lvl  int
	// qname is the alias's dep_graph-qualified name. It is the key its AliasDef is
	// registered under, and the Name every AliasType handle pointing at it carries.
	// checkProductive matches a body's references against it to find the recursion cycles.
	qname string
	// declScope is scope, or a child holding a generic alias's type parameters. The body
	// resolves here so it reads each `T` as the one shared var the def stores.
	declScope *Scope
	// namedLts is the alias's own named-lifetime scope, populated by preBindAlias when it
	// resolves the `<'a>` parameters. inferAliasBody installs it so a `&'a` in the body reads
	// the same lifetime variable the parameter binder minted. It is nil for an alias with no
	// lifetime parameters, which gives the body a fresh scope.
	namedLts map[string]*soltype.LifetimeVar
	// def is the registered AliasDef preBindAlias inserted with a nil Body; inferAliasBody
	// fills its Body once every sibling identity in the component is bound.
	def *AliasDef
	// paramsClean is true when preBindAlias resolved the alias's `<…>` list with no
	// diagnostic. A bound or default that fails to resolve is left nil, taking the
	// occurrences it held with it, so `type Foo<T, U: Nope<T>> = number` keeps no record
	// that U's bound wrote T.
	paramsClean bool
	// declClean is paramsClean and the same answer for the body, the errorWindow rule
	// applied to an alias. `type M<T> = {f(x: T) -> T}` drops the unsupported method and
	// with it every occurrence of T, so reportPhantomParams reads this before warning that
	// T is unused.
	declClean bool
}

// preBindAlias resolves an alias's type parameters, registers a shell AliasDef whose Body
// is still nil, and binds the alias name to an AliasType handle — without resolving the
// body. It returns the shell inferAliasBody completes. Registering the name and def up
// front is what lets a sibling alias, or the alias itself, resolve this name while its own
// body is still being walked. expandAlias only runs at subtyping time, after every body in
// the component is filled, so the nil Body is never read during pre-binding.
func (c *checker) preBindAlias(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) *aliasShell {
	quiet := c.errorWindow()

	// The four intrinsic string operators — Uppercase, Lowercase, Capitalize, Uncapitalize — are
	// built-in type operators, not aliases a user may shadow. Reject a `type Uppercase<T> = …`
	// declaration and skip binding it, so a `Uppercase<…>` reference resolves to the built-in
	// operator rather than the user's definition.
	if _, reserved := stringIntrinsics[decl.Name.Name]; reserved {
		c.report(&ReservedTypeNameError{Decl: decl})
		return nil
	}

	// An alias-body type reference resolves against the alias's own namespace first, the
	// same qualified-first resolution a class or enum body uses, so a namespaced alias
	// resolves a bare sibling reference. Save and restore around type-parameter resolution.
	prevNS := c.classNamespace
	c.classNamespace = ns
	defer func() { c.classNamespace = prevNS }()

	// The alias's dep_graph-qualified name is the namespace joined to the local name, or
	// the bare local name at the root namespace, the same qualified key class and enum
	// registration use, so the registry key and the AliasType handle match.
	qname := decl.Name.Name
	if ns != "" {
		qname = ns + "." + decl.Name.Name
	}

	// Resolve the alias's parameters in a fresh named-lifetime scope so a lifetime parameter
	// and every `&'a` in the body share one lifetime variable, and hand that scope to
	// inferAliasBody. Saving and restoring keeps one alias's `'a` independent of a sibling's
	// in the same component.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = nil

	// Resolve the alias's type parameters into a child scope so a bound, a default, and the
	// body all read a sibling `T` as one shared var. A non-generic alias reuses the
	// enclosing scope.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(decl.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, decl.TypeParams)
	}
	lifetimeParams := c.resolveAliasLifetimeParams(lvl, decl.LifetimeParams)
	aliasNamedLts := c.namedLifetimes
	c.namedLifetimes = savedNamedLts

	def := &AliasDef{TypeParams: typeParams, LifetimeParams: lifetimeParams, Level: lvl - 1}
	c.ctx.registerAlias(qname, def)

	t := &soltype.AliasType{Name: qname}
	scope.defineType(qname, TypeBinding{
		Type:    t,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, t)

	return &aliasShell{
		decl:        decl,
		ns:          ns,
		lvl:         lvl,
		qname:       qname,
		declScope:   declScope,
		namedLts:    aliasNamedLts,
		def:         def,
		paramsClean: quiet(),
	}
}

// resolveAliasLifetimeParams mints one lifetime variable per `<'a, ...>` parameter through
// namedLifetime, so a `&'a` in the body resolved under the same scope reaches that variable.
func (c *checker) resolveAliasLifetimeParams(lvl int, ltParams []*ast.LifetimeParam) []*soltype.LifetimeParam {
	if len(ltParams) == 0 {
		return nil
	}
	out := make([]*soltype.LifetimeParam, len(ltParams))
	for i, p := range ltParams {
		var bounds []soltype.Lifetime
		for _, b := range p.Bounds {
			bounds = append(bounds, c.boundLifetime(b.Name, lvl))
		}
		out[i] = &soltype.LifetimeParam{Name: "'" + p.Name, Var: c.namedLifetime(p.Name, lvl), Bounds: bounds}
	}
	return out
}

// inferAliasBody resolves a pre-bound alias's body and stores it on the shell's AliasDef.
// It runs after every alias, enum, and class identity in the recursive group is pre-bound,
// so a body naming a sibling — or the alias itself — resolves. The type-parameter vars stay
// symbolic in the stored body, and expandAlias substitutes an instance's arguments for them
// at subtyping time.
func (c *checker) inferAliasBody(sh *aliasShell) {
	prevNS := c.classNamespace
	c.classNamespace = sh.ns
	defer func() { c.classNamespace = prevNS }()

	// Install the alias's own named-lifetime scope, populated by preBindAlias, so a `&'a` in
	// the body reads the lifetime variable its `<'a>` binder minted. A non-lifetime-generic
	// alias carries a nil scope, which gives its body a fresh one independent of any sibling.
	savedNamedLts := c.namedLifetimes
	c.namedLifetimes = sh.namedLts
	defer func() { c.namedLifetimes = savedNamedLts }()

	// A nil TypeAnn is parser error recovery for `type Foo =`, already reported. Bind a
	// fresh var and skip resolveTypeAnn, since a nil annotation has no span to report on.
	quiet := c.errorWindow()
	var body soltype.Type = c.freshAt(sh.lvl)
	if sh.decl.TypeAnn != nil {
		if resolved, ok := c.resolveTypeAnn(sh.declScope, sh.decl.TypeAnn, sh.lvl); ok {
			body = resolved
		}
		// An unsupported body reported its own error. Keep the fresh var so a reference
		// resolves rather than cascading an unbound-name error, matching the Promise-wrapper
		// recovery in resolveTypeAnn.
	}
	sh.def.Body = body
	sh.declClean = sh.paramsClean && sh.decl.TypeAnn != nil && quiet()
}

// buildAliasInstance resolves a use-site reference to a registered alias into an AliasType
// carrying one type argument per type parameter and one lifetime argument per lifetime
// parameter. A mismatch on either sort reports and recovers with fresh arguments. An enum
// reaches this too, since an enum name binds to an alias over its variant union, so the kind
// the diagnostics render comes off the AliasDef rather than being assumed.
func (c *checker) buildAliasInstance(scope *Scope, at *soltype.AliasType, ref *ast.TypeRefTypeAnn, lvl int) *soltype.AliasType {
	def, _ := c.ctx.aliasDef(at.Name)
	var params []*soltype.TypeParam
	var ltParams []*soltype.LifetimeParam
	kind := AliasDeclKind
	if def != nil {
		params = def.TypeParams
		ltParams = def.LifetimeParams
		if def.Enum {
			kind = EnumDeclKind
		}
	}
	ltArgs := c.resolveAliasLifetimeArgs(ref, kind, ltParams, lvl)
	args := c.resolveTypeArgs(scope, ref, kind, params, arityOfParams(params), lvl)
	if len(args) == 0 {
		// A non-generic alias carries no type arguments; any that were supplied are reported
		// by resolveTypeArgs. Return a handle carrying only the lifetime arguments, or the bare
		// handle when there are none, so the alias still resolves under its name.
		if len(ltArgs) == 0 {
			return at
		}
		return &soltype.AliasType{Name: at.Name, LifetimeArgs: ltArgs}
	}
	c.checkTypeArgBounds(params, args, ltParams, ltArgs, ref)
	return &soltype.AliasType{Name: at.Name, TypeArgs: args, LifetimeArgs: ltArgs}
}

// resolveAliasLifetimeArgs resolves a reference's `<'a, ...>` lifetime arguments and checks
// their count against the alias's lifetime parameters, which have no default. A mismatch
// reports a LifetimeArgArityMismatchError and recovers with fresh lifetimes.
func (c *checker) resolveAliasLifetimeArgs(ref *ast.TypeRefTypeAnn, kind TypeDeclKind, ltParams []*soltype.LifetimeParam, lvl int) []soltype.Lifetime {
	total := len(ltParams)
	got := len(ref.LifetimeArgs)
	if got != total {
		c.report(&LifetimeArgArityMismatchError{
			Ref:      ref,
			Kind:     kind,
			Name:     ast.QualIdentToString(ref.Name),
			Expected: total,
			Got:      got,
		})
	}
	if total == 0 {
		return nil
	}
	args := make([]soltype.Lifetime, total)
	for i := range total {
		if i < got {
			args[i] = c.useLifetimeArg(ref.LifetimeArgs[i], lvl)
		} else {
			// A missing lifetime argument was reported above. Recover with a fresh lifetime so
			// expansion substitutes one per parameter.
			args[i] = c.ctx.freshLifetime(lvl)
		}
	}
	return args
}

// useLifetimeArg resolves one written lifetime argument. A named `'a` shares the variable that
// name denotes at the reference site, and `'static` maps to the static lifetime, matching the
// bound-resolution rule. An unexpected node recovers to a fresh lifetime.
func (c *checker) useLifetimeArg(node ast.LifetimeAnnNode, lvl int) soltype.Lifetime {
	if n, ok := node.(*ast.LifetimeAnn); ok {
		return c.boundLifetime(n.Name, lvl)
	}
	return c.ctx.freshLifetime(lvl)
}
