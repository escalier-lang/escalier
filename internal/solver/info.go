package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
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
	// methodReads holds each member read that resolved to a method of an instance, an
	// object type or an interface, which runs against the value it was read from. Codegen
	// binds such a read to its receiver when it travels as a value.
	// The read's recorded type cannot say this, since the value a method read yields is
	// its signature with the receiver already stripped.
	methodReads set.Set[ast.Node]
	// decls maps an identifier read to the node that bound the name it read. That
	// node is the binding's first source, which is a declaration such as a
	// *ast.VarDecl or *ast.FuncDecl, or a pattern for a parameter or a
	// destructured leaf. A read of a binding with no source, such as one the
	// prelude seeded, has no entry.
	decls map[*ast.IdentExpr]ast.Node
	// ctx is the Context of the run that recorded this table, or nil for a table built
	// by NewInfo. MemberObject reads the class and alias registries it holds.
	ctx *Context
}

// NewInfo returns an empty Info side table ready to record inferred types.
func NewInfo() *Info {
	return &Info{
		types:       map[ast.Node]soltype.Type{},
		nsMembers:   map[ast.Node]ast.Decl{},
		methodReads: set.NewSet[ast.Node](),
		decls:       map[*ast.IdentExpr]ast.Node{},
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

// DeclOf returns the node that bound the name e read, and reports whether e
// resolved to a binding that records one.
func (i *Info) DeclOf(e *ast.IdentExpr) (ast.Node, bool) {
	decl, ok := i.decls[e]
	return decl, ok
}

// setDecl records that the identifier read e resolved to the binding decl introduced.
func (i *Info) setDecl(e *ast.IdentExpr, decl ast.Node) {
	i.decls[e] = decl
}

// IsMethodRead reports whether the member read n resolved to a method that runs against
// the value it was read from.
func (i *Info) IsMethodRead(n ast.Node) bool {
	return i.methodReads.Contains(n)
}

// setMethodRead records that the member read n resolved to such a method.
func (i *Info) setMethodRead(n ast.Node) {
	i.methodReads.Add(n)
}

// ResolvedTypeOf returns the type recorded for n with any inference variable resolved
// to the shape it settled on, or nil when none was recorded.
//
// TypeOf returns what the walk recorded at the moment it reached n, which for an
// expression is usually an inference variable rather than a type a caller can switch
// on. `val p = Point(5)` records the call as `t7`, not as the object carrying Point's
// constructor. Resolving reads that variable's lower bounds, so the caller sees what
// flows out of the expression.
//
// Resolve only once a run has finished. Reading a variable's bounds is sound at any
// point, but constraints added afterwards would make an earlier answer stale, and a
// caller that tests a value against a literal adds that literal as a further lower
// bound. Everything asking this is downstream of inference, where no constraint is
// left to add.
func (i *Info) ResolvedTypeOf(n ast.Node) soltype.Type {
	t := i.types[n]
	if t == nil {
		return nil
	}
	return groundedCarrier(t)
}

// Expand returns t with each alias and `typeof` on the outside expanded and each borrow
// on the outside peeled, until none is left. `type Shape = A | B` expands to `A | B`.
// A table built by NewInfo holds no registries and returns t unchanged.
func (i *Info) Expand(t soltype.Type) soltype.Type {
	if i.ctx == nil || t == nil {
		return t
	}
	return i.ctx.expandedCarrier(t)
}

// LendsMut reports whether the value recorded for n lends mutable access, which a
// `&mut self` member and a setter need from their receiver. It reads the type as
// recorded, before any coalescing, because a binding's read records a variable whose
// lower bounds carry the borrow. Every one of those bounds has to be a `mut` borrow,
// since the value at run time may be any of them.
func (i *Info) LendsMut(n ast.Node) bool {
	t := i.types[n]
	return t != nil && lendsMut(t)
}

// MemberObject returns an object type whose elements are the members a value of type t
// exposes, answered against the registries of the run that recorded this table. It
// reads t the way a member read off a value of type t does:
//
//   - A class instance yields its own members and the ones it inherits, at the
//     instance's type arguments.
//   - An alias, an interface, or a `typeof` yields the type it expands to.
//   - A primitive, a literal, or a unique symbol yields its wrapper class's instance,
//     such as `String` for `"abc"`.
//   - A tuple yields the `Array` instance holding the union of its elements.
//   - An object type, a class value's statics included, yields itself.
//   - A borrow yields what it borrows, and an inference variable yields what its
//     bounds settled on.
//
// Every inference variable in the returned members is replaced by the bounds it settled
// on, and the object is the caller's to keep or change.
//
// It returns false for any other type, such as a union, an intersection, a function,
// `never`, or `unknown`. It also returns false for an object built from a mapped type
// or a spread, which names its members only once reduced, and for a table built by
// NewInfo.
//
// Call it only once the run has finished. A library run and every bin/ script checked
// against it share one set of registries, and a script's check rewrites the entries
// the previous check of that script made. A caller must not call it on any of those
// tables while such a check runs.
func (i *Info) MemberObject(t soltype.Type) (*soltype.ObjectType, bool) {
	if i.ctx == nil || t == nil {
		return nil, false
	}
	return i.ctx.memberObject(t)
}

// DisplayTypeOf returns the type recorded for n coalesced to the form a person reads,
// or nil when none was recorded. Every inference variable is replaced by the bounds it
// settled on, except a type parameter generalization kept, which soltype.PrintAsScheme
// names. `val x = 5` displays `x` as `5` where TypeOf may return a variable. Call it only
// once a run has finished, for the reason ResolvedTypeOf gives.
func (i *Info) DisplayTypeOf(n ast.Node) soltype.Type {
	t := i.types[n]
	if t == nil {
		return nil
	}
	return coalesce(t, soltype.Positive)
}
