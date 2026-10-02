package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// Info is the AST->type side table (à la go/types.Info). The new checker records
// inferred types here rather than mutating ast nodes via InferredType() /
// SetInferredType(). No probe/cleanup discipline in M1 — that arrives with
// Prov/Probe in a later milestone.
type Info struct {
	types map[ast.Node]soltype.Type
	// nsMembers maps a member or index expression that resolved through a namespace
	// to the declaration that bound the member it names. A node's recorded type says
	// what the member is, not where it came from, and codegen needs where: a member
	// read of a namespace whose declaration carries `@js("Math.sin")` lowers to that
	// JavaScript rather than to a property read. soltype has no namespace former to
	// recover this from, so the resolution records it as it happens.
	nsMembers map[ast.Node]ast.Decl
}

// NewInfo returns an empty Info side table ready to record inferred types.
func NewInfo() *Info {
	return &Info{
		types:     map[ast.Node]soltype.Type{},
		nsMembers: map[ast.Node]ast.Decl{},
	}
}

// TypeOf returns the type recorded for n, or nil if none has been set.
func (i *Info) TypeOf(n ast.Node) soltype.Type {
	return i.types[n]
}

// setType records t as the inferred type of n, overwriting any prior entry.
func (i *Info) setType(n ast.Node, t soltype.Type) {
	i.types[n] = t
}

// NamespaceMemberDecl returns the declaration a namespace member read resolved to,
// and reports whether n is such a read. codegen asks so it can lower a member whose
// declaration carries an `@js("…")` decorator.
func (i *Info) NamespaceMemberDecl(n ast.Node) (ast.Decl, bool) {
	decl, ok := i.nsMembers[n]
	return decl, ok
}

// setNamespaceMember records that n resolved to decl through a namespace.
func (i *Info) setNamespaceMember(n ast.Node, decl ast.Decl) {
	i.nsMembers[n] = decl
}
