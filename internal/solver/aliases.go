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
	// declScope is scope, or a child holding a generic alias's type parameters. The body
	// resolves here so it reads each `T` as the one shared var the def stores.
	declScope *Scope
	// def is the registered AliasDef preBindAlias inserted with a nil Body; inferAliasBody
	// fills its Body once every sibling identity in the component is bound.
	def *AliasDef
}

// preBindAlias resolves an alias's type parameters, registers a shell AliasDef whose Body
// is still nil, and binds the alias name to an AliasType handle — without resolving the
// body. It returns the shell inferAliasBody completes. Registering the name and def up
// front is what lets a sibling alias, or the alias itself, resolve this name while its own
// body is still being walked. expandAlias only runs at subtyping time, after every body in
// the component is filled, so the nil Body is never read during pre-binding.
func (c *checker) preBindAlias(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) *aliasShell {
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

	// Resolve the alias's type parameters into a child scope so a bound, a default, and the
	// body all read a sibling `T` as one shared var. A non-generic alias reuses the
	// enclosing scope.
	declScope := scope
	var typeParams []*soltype.TypeParam
	if len(decl.TypeParams) > 0 {
		declScope = scope.Child()
		typeParams = c.resolveTypeParams(declScope, lvl, decl.TypeParams)
	}

	def := &AliasDef{TypeParams: typeParams, Level: lvl - 1}
	c.ctx.registerAlias(qname, def)

	t := &soltype.AliasType{Name: qname}
	scope.defineType(qname, TypeBinding{
		Type:    t,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, t)

	return &aliasShell{decl: decl, ns: ns, lvl: lvl, declScope: declScope, def: def}
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

	// A nil TypeAnn is parser error recovery for `type Foo =`, already reported. Bind a
	// fresh var and skip resolveTypeAnn, since a nil annotation has no span to report on.
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
}

// buildAliasInstance resolves a use-site reference to a registered alias into an AliasType
// carrying one type argument per parameter. A trailing parameter with a default may be
// omitted, so its argument is filled from the default with the earlier arguments already
// substituted, letting `type Pair<T, U = T>` resolve `Pair<number>` to `Pair<number,
// number>`. Fewer than the required count or more than the total parameter count reports an
// AliasArityMismatchError and recovers, so a downstream reference still resolves.
func (c *checker) buildAliasInstance(scope *Scope, at *soltype.AliasType, ref *ast.TypeRefTypeAnn, lvl int) *soltype.AliasType {
	def, _ := c.ctx.aliasDef(at.Name)
	var params []*soltype.TypeParam
	if def != nil {
		params = def.TypeParams
	}
	total := len(params)
	required := 0
	for _, p := range params {
		if p.Default == nil {
			required++
		}
	}
	got := len(ref.TypeArgs)
	if got < required || got > total {
		c.report(&AliasArityMismatchError{
			Ref:      ref,
			Name:     ast.QualIdentToString(ref.Name),
			Required: required,
			Total:    total,
			Got:      got,
		})
	}
	if total == 0 {
		// A non-generic alias carries no arguments; any that were supplied are reported
		// above. Return the bare handle so the alias still resolves under its name.
		return at
	}
	args := make([]soltype.Type, total)
	for i := range total {
		if i < got {
			if resolved, ok := c.resolveTypeAnn(scope, ref.TypeArgs[i], lvl); ok {
				args[i] = resolved
			} else {
				args[i] = c.freshAt(lvl)
			}
			continue
		}
		if params[i].Default != nil {
			// The default may reference an earlier parameter, as `U = T` does, so substitute
			// the arguments already resolved for parameters before this one.
			subst := newTypeSubst(params[:i], args[:i], nil, nil)
			args[i] = params[i].Default.Accept(subst, soltype.Positive)
		} else {
			// A required argument was omitted, already reported as an arity mismatch. Recover
			// to a fresh var so expansion has one argument per parameter.
			args[i] = c.freshAt(lvl)
		}
	}
	return &soltype.AliasType{Name: at.Name, TypeArgs: args}
}
