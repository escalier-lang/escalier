package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// AliasDef is the heavy per-alias data the AliasType handle points at, keyed in the
// Context registry by qualified name, mirroring the ClassType/ClassDef split.
type AliasDef struct {
	// TypeParams are the alias's own type parameters. Always nil today, since generic
	// aliases are not yet supported.
	TypeParams []*soltype.TypeParam

	// LifetimeParams are the alias's quantified lifetime parameters, the lifetime twin of
	// TypeParams. Always nil today, for the same reason.
	LifetimeParams []*soltype.LifetimeParam

	// Body is the type the alias expands to, the right-hand side of `type Name = Body`.
	// No variance is stored: a transparent alias expands, so variance follows the body.
	Body soltype.Type

	// Level is the alias binding's generalize level. A non-generic alias has no parameters
	// to freshen, so it is recorded but not otherwise consulted.
	Level int
}

// expandAlias unfolds an alias reference to its registered AliasDef Body, the shared
// transparent-alias helper. An unregistered reference yields an ErrorType so it absorbs.
func (c *Context) expandAlias(ref *soltype.AliasType) soltype.Type {
	def, ok := c.aliasDef(ref.Name)
	if !ok || def.Body == nil {
		return &soltype.ErrorType{}
	}
	return def.Body
}

// typeDeclEntry pairs a `type` decl with its namespace, so the type-key loop can defer
// alias-body resolution until every sibling class and enum in the component is bound.
type typeDeclEntry struct {
	decl *ast.TypeDecl
	ns   string
}

// inferTypeDecl resolves a non-generic `type X = Body`, registers its AliasDef, and binds
// the name to an AliasType handle. A generic alias is reported as unsupported.
func (c *checker) inferTypeDecl(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) {
	if len(decl.TypeParams) > 0 {
		// A generic alias needs substitution and arity checking that are not implemented.
		// Report it and bind nothing, so a reference fails as an unknown type.
		c.reportUnsupportedFeature(decl, "generic type alias")
		return
	}

	// The alias's dep_graph-qualified name is the namespace joined to the local name, or
	// the bare local name at the root namespace, the same qualified key class and enum
	// registration use, so the registry key and the AliasType handle match.
	qname := decl.Name.Name
	if ns != "" {
		qname = ns + "." + decl.Name.Name
	}

	// A nil TypeAnn is parser error recovery for `type Foo =`, already reported. Bind a
	// fresh var and skip resolveTypeAnn, since a nil annotation has no span to report on.
	var body soltype.Type = c.freshAt(lvl)
	if decl.TypeAnn != nil {
		if resolved, ok := c.resolveTypeAnn(scope, decl.TypeAnn, lvl); ok {
			body = resolved
		}
		// An unsupported body reported its own error. Keep the fresh var so a reference
		// resolves rather than cascading an unbound-name error, matching the Promise-wrapper
		// recovery in resolveTypeAnn.
	}
	c.ctx.registerAlias(qname, &AliasDef{Body: body, Level: lvl - 1})

	t := &soltype.AliasType{Name: qname}
	scope.defineType(qname, TypeBinding{
		Type:    t,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, t)
}
