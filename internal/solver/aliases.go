package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// AliasDef is the heavy per-alias data the token soltype.AliasType points at.
// inferTypeDecl builds one per `type` declaration and registers it on the Context under
// the alias's dep_graph-qualified name. expandAlias reads Body to unfold a reference.
// Keeping Body out of soltype.AliasType lets the token stay a small, cheap-to-compare
// identity, mirroring the split between ClassType and ClassDef.
type AliasDef struct {
	// TypeParams are the alias's own quantified type parameters in declaration order.
	// Always nil today: generic aliases are not yet supported, so only non-generic
	// aliases are registered.
	TypeParams []*soltype.TypeParam

	// LifetimeParams are the alias's quantified lifetime parameters, the lifetime twin of
	// TypeParams. Always nil today, for the same reason.
	LifetimeParams []*soltype.LifetimeParam

	// Body is the type the alias expands to, the resolved right-hand side of
	// `type Name = Body`. expandAlias returns it so an alias reference subtypes exactly
	// as its body does. No variance is stored beside it. A transparent alias expands, so
	// variance is resolved structurally on the body rather than per parameter.
	Body soltype.Type

	// Level is the alias binding's generalize level. A non-generic alias has no parameters
	// to freshen, so it is recorded but not otherwise consulted.
	Level int
}

// expandAlias unfolds an alias reference to the structural type it names, by reading the
// registered AliasDef's Body. It is a standalone helper the subtyping engine calls to
// treat an alias transparently, kept separate from constrain so other callers can reuse
// the same unfolding. There are no type arguments to substitute, so it returns Body
// directly. An unregistered reference yields an ErrorType so a stray reference absorbs
// rather than looping. A well-formed program never produces one, since inferTypeDecl
// registers the alias before binding its name.
func (c *Context) expandAlias(ref *soltype.AliasType) soltype.Type {
	def, ok := c.aliasDef(ref.Name)
	if !ok || def.Body == nil {
		return &soltype.ErrorType{}
	}
	return def.Body
}

// typeDeclEntry pairs a `type` decl with its dep_graph namespace, so the module
// type-key loop can defer alias-body resolution until every class token and enum union
// in the component is bound. It is the alias counterpart of the enumShell the enum
// two-pass carries between its passes.
type typeDeclEntry struct {
	decl *ast.TypeDecl
	ns   string
}

// inferTypeDecl infers a `type X = Body` declaration. It resolves the body annotation,
// registers an AliasDef holding that body, and binds the type name to an AliasType token
// so a reference resolves and renders under its own name. Only a non-generic alias is
// supported. A declaration carrying type parameters reports an unsupported-feature
// diagnostic and binds nothing, because argument substitution and arity checking are not
// implemented.
func (c *checker) inferTypeDecl(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) {
	if len(decl.TypeParams) > 0 {
		// A generic alias needs argument substitution and arity checking that are not
		// implemented. Report the declaration and bind nothing, so a reference fails as an
		// unknown type rather than resolving to a half-built alias whose body references
		// unbound parameters.
		c.reportUnsupportedFeature(decl, "generic type alias")
		return
	}

	// The alias's dep_graph-qualified name is the namespace joined to the local name, or
	// the bare local name at the root namespace, the same qualified key class and enum
	// registration use, so the registry key and the AliasType token match.
	qname := decl.Name.Name
	if ns != "" {
		qname = ns + "." + decl.Name.Name
	}

	// A missing body is a parser error-recovery case. `type Foo =` with no annotation
	// yields a nil TypeAnn, which the parser already reported. Bind the alias to a fresh
	// var so a later reference still resolves, and skip resolveTypeAnn. Passing nil there
	// routes to reportUnsupported(nil), whose error node has no span to render.
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

	token := &soltype.AliasType{Name: qname}
	scope.defineType(qname, TypeBinding{
		Type:    token,
		Sources: []provenance.Provenance{&ast.NodeProvenance{Node: decl}},
	})
	c.recordType(decl.Name, token)
}
