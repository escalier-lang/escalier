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
	// TypeParams. Always nil today, since alias-typed borrows land with lifetime aliases.
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
	subst := newTypeSubst(def.TypeParams, ref.TypeArgs, def.LifetimeParams, nil)
	return def.Body.Accept(subst, soltype.Positive)
}

// typeDeclEntry pairs a `type` decl with its namespace, so the type-key loop can defer
// alias-body resolution until every sibling class and enum in the component is bound.
type typeDeclEntry struct {
	decl *ast.TypeDecl
	ns   string
}

// inferTypeDecl resolves a `type X = Body` or `type X<T, U = T> = Body`, registers its
// AliasDef, and binds the name to an AliasType handle. A generic alias resolves its type
// parameters into a child scope so the body reads each `T` as one shared var, matching the
// class path. The vars stay symbolic in the stored body, and expandAlias substitutes an
// instance's arguments for them at subtyping time.
func (c *checker) inferTypeDecl(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) {
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

	// A nil TypeAnn is parser error recovery for `type Foo =`, already reported. Bind a
	// fresh var and skip resolveTypeAnn, since a nil annotation has no span to report on.
	var body soltype.Type = c.freshAt(lvl)
	if decl.TypeAnn != nil {
		if resolved, ok := c.resolveTypeAnn(declScope, decl.TypeAnn, lvl); ok {
			body = resolved
		}
		// An unsupported body reported its own error. Keep the fresh var so a reference
		// resolves rather than cascading an unbound-name error, matching the Promise-wrapper
		// recovery in resolveTypeAnn.
	}
	c.ctx.registerAlias(qname, &AliasDef{TypeParams: typeParams, Body: body, Level: lvl - 1})

	t := &soltype.AliasType{Name: qname}
	scope.defineType(qname, TypeBinding{
		Type:    t,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, t)
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
