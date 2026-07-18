package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// AliasDef is the heavy per-alias data the token soltype.AliasType points at.
// inferTypeDecl builds one per `type` declaration and registers it on the Context
// under the alias's dep_graph-qualified name; expandAlias reads Body to unfold a
// reference. Keeping Body out of soltype.AliasType lets the token stay a small,
// cheap-to-compare identity, mirroring the split between ClassType and ClassDef.
type AliasDef struct {
	// TypeParams are the alias's own quantified type parameters in declaration order,
	// resolved from the `<…>` list. nil for a non-generic alias. Instantiation and
	// arity checking over these land in M7 PR2. PR1 registers only non-generic aliases,
	// so this is always nil today.
	TypeParams []*soltype.TypeParam

	// LifetimeParams are the alias's quantified lifetime parameters, the lifetime twin
	// of TypeParams. nil until the lifetime-generic alias form lands in M7 PR4.
	LifetimeParams []*soltype.LifetimeParam

	// Body is the type the alias expands to, the resolved right-hand side of
	// `type Name = Body`. expandAlias returns it so an alias reference subtypes exactly
	// as its body does. M7 stores no variance beside it. A transparent alias expands, so
	// variance is resolved structurally on the body rather than per parameter.
	Body soltype.Type

	// Level is the alias binding's generalize level, kept for the generic-instantiation
	// machinery in later PRs. A non-generic alias has no parameters to freshen, so PR1
	// only records it.
	Level int
}

// expandAlias unfolds an alias reference to the structural type it names, by reading
// the registered AliasDef's Body. It is the standalone helper the subtyping engine
// calls to treat an alias transparently, and the same helper M9's type-level operator
// evaluator reuses to reduce over an alias. PR1 has no type arguments to substitute, so
// it returns Body directly. PR2 substitutes ref.TypeArgs for the AliasDef's type
// parameters here. An unregistered reference yields an ErrorType so a stray reference
// absorbs rather than looping. A well-formed program never produces one, since
// inferTypeDecl registers the alias before binding its name.
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

// inferTypeDecl resolves a non-generic `type X = Body` declaration: it resolves the
// body annotation, registers an AliasDef holding that body, and binds the type name to
// an AliasType token so a later reference resolves and renders under its own name. PR1
// handles only the single, non-recursive decl. The SCC two-pass for recursive and
// mutually recursive aliases is PR3, and type parameters are PR2, so a `<…>` list is
// ignored here and its aliases behave as their bare-name body.
func (c *checker) inferTypeDecl(scope *Scope, lvl int, decl *ast.TypeDecl, ns string) {
	// The alias's dep_graph-qualified name — the namespace joined to the local name, or
	// the bare local name at the root namespace — the same qualified key class and enum
	// registration use, so the registry key and the AliasType token match.
	qname := decl.Name.Name
	if ns != "" {
		qname = ns + "." + decl.Name.Name
	}

	// A missing body is a parser error-recovery case: `type Foo =` with no annotation
	// yields a nil TypeAnn, which the parser already reported. Bind the alias to a fresh
	// var so a later reference still resolves, and skip resolveTypeAnn — passing nil there
	// routes to reportUnsupported(nil), whose error node has no span to render.
	var body soltype.Type = c.freshAt(lvl)
	if decl.TypeAnn != nil {
		if resolved, ok := c.resolveTypeAnn(scope, decl.TypeAnn, lvl); ok {
			body = resolved
		}
		// An unsupported body reported its own error; keep the fresh var so a reference
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
