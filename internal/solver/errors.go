package solver

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// SolverError is the sealed interface for constraint-solving failures. Each
// concrete struct carries typed references to the offending soltype.Type values
// (not just a rendered string) so LSP/tooling consumers can inspect what the
// error refers to — e.g. navigate to a type's declaration — without reparsing
// the message. Modeled on internal/checker/error.go's shape.
//
// M2.5 makes every Span() node-derived: bridge kinds self-blame from the AST node
// they carry, and constraint kinds resolve their offending operand to its minting
// node through the Prov side table (per-operand blame, §3.5). Related() exposes
// secondary contributing nodes (the expected-source alongside the actual-source;
// the prior declaration alongside the duplicate). errSpan/setSpan are gone — no
// error is stamped after the fact.
type SolverError interface {
	isSolverError()
	Message() string
	Span() ast.Span      // ALWAYS derived from a primary ast.Node
	Related() []ast.Span // node-derived; empty unless the kind carries related nodes
}

// CannotConstrainError fires when a non-variable sub/super pair fails to match:
// prim/prim mismatch, lit/lit mismatch, lit/prim mismatch, and the generic
// "no rule applies" fall-through at the end of constrain.
//
// Sub is the "actual" value, Super the "expected"; blame follows Sub to its minting
// node (when that node lies inside the constraint site — the containment guard
// that keeps identifier-flow blame on the use, not the definition, §3.8) and falls
// back to site otherwise. This is the only constraint kind that keeps a site
// fallback: its actual-value operand can legitimately resolve outside the
// constraint (an ident's definition) or not at all (a Void result).
type CannotConstrainError struct {
	Sub, Super soltype.Type
	prov       NodeResolver // M2.5: type→node index; assigned after Constrain returns (§3.5)
	site       ast.Node     // M2.5: the constraint node n — the use, the fallback when Sub has no entry
}

// FuncArityMismatchError fires on FuncType <: FuncType when the two arities
// differ (M1's exact-function rule; exact-by-default requires the same number
// of params). Holds the full FuncTypes, not just the arities, so consumers can
// report param/return types too. M3 narrows the firing conditions when the
// exactness flag adds the inexact fewer-params-is-subtype arm.
//
// The subject is the super call-shape (a fresh FuncType{args, res} minted per call,
// recorded against the CallExpr), so Span() resolves precisely to the call;
// Related() follows the sub callee to a "defined here" span. site is the
// constraint node, used as a coarse fallback when neither operand resolves (e.g.
// once M3 produces higher-order FuncArity errors whose super isn't a recorded
// call-shape) so blame never degrades to the zero span.
type FuncArityMismatchError struct {
	Sub, Super *soltype.FuncType
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback when no operand resolves
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when the
// inexact flag is added).
//
// The subject is the sub tuple literal (recorded by inferTuple); Related() points
// at the super expected-source. Not actually reachable in M2.5 — a tuple <: tuple
// constraint needs a tuple sink (annotation/param) resolveTypeAnn does not yet
// produce — so its blame is wired but exercised from M4. site is the constraint
// node, the coarse fallback when neither tuple resolves (e.g. an M4 tuple
// annotation whose elements aren't recorded) so blame never degrades to the zero
// span.
type TupleLengthMismatchError struct {
	Sub, Super *soltype.TupleType
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback when no operand resolves
}

// MissingPropertyError fires on ObjectType <: ObjectType when the super requires a
// property the sub lacks — the object analogue of FuncArityMismatchError /
// TupleLengthMismatchError. It is the failure behind a field read on an object
// without that property (recv.foo where recv has no foo): the walk constrains
// recv <: {foo: fresh, ...}, and the absent property surfaces here. Holds both
// objects plus the missing name so consumers can inspect what was required.
//
// The subject is the property's inner result var (Super.Prop(Name)), minted by
// inferMember and recorded against the .prop identifier — so Span() blames the
// member's prop (.foo), not the receiver. Name stays: the absent property name is
// not recoverable from a single node (the super may require several properties).
// site is the constraint node, the coarse fallback when the property var has no
// entry — reachable for a concrete object <: object requirement whose property
// types are coalesced/annotation-minted (and therefore not recorded by
// inferMember); for member access it never fires, but it keeps blame off the zero
// span.
type MissingPropertyError struct {
	Sub, Super *soltype.ObjectType
	Name       string
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback when the property var has no entry
}

// InexactIntoExactError fires on ObjectType <: ObjectType when the super is exact
// but the sub is inexact: an inexact source carries an open `...` tail of unknown
// properties, so it cannot satisfy an exact target that fixes its member set.
type InexactIntoExactError struct {
	Sub, Super *soltype.ObjectType
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// InexactTupleIntoExactError is the tuple twin of InexactIntoExactError. An
// inexact tuple `[T, ...]` carries an open tail of unknown trailing elements,
// so it cannot satisfy an exact tuple target `[T]` whose length is fixed,
// even when the declared element prefixes match.
type InexactTupleIntoExactError struct {
	Sub, Super *soltype.TupleType
	prov       NodeResolver
	site       ast.Node
}

// InexactUnionIntoExactError is the union twin of InexactIntoExactError. An
// inexact union `A | B | ...` carries an open tail of unknown additional
// members, so it cannot flow into a closed target. A closed target is either
// an exact union or any non-union concrete the open tail could violate. The
// base form lands in M6 PR2. The flag itself and the parser surface for
// `A | B | ...` land in PR4. Until then the rule fires only against an
// internally-built inexact union.
type InexactUnionIntoExactError struct {
	Sub   *soltype.UnionType
	Super soltype.Type
	prov  NodeResolver
	site  ast.Node
}

// ExtraPropertyError fires on ObjectType <: ObjectType when the super is exact and
// the sub carries a property the super does not declare — width is rejected against
// an exact target. One error fires per extra property, carrying its name.
type ExtraPropertyError struct {
	Sub, Super *soltype.ObjectType
	Name       string
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// ExtraElementError is the tuple analogue of ExtraPropertyError, fired by the
// construction-site excess check (A3): a tuple LITERAL checked against a tuple
// annotation may not carry elements beyond the target's declared set, even when
// the target is inexact (`[number, ...]`). It is the parallel of the direct-call
// extra-arg lint: an inexact tail tolerates extra elements from a non-literal
// source through width subtyping, but a literal spells out its elements, so an
// extra one is a construction error. One error fires per excess element, carrying
// its index. It is reported from the walk, not from the tuple constrain arm.
type ExtraElementError struct {
	Sub, Super *soltype.TupleType
	Index      int
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: the excess element node fallback
}

// OptionalPropertyError fires on ObjectType <: ObjectType when a property is
// optional on the sub (source) but required on the super (target): the source may omit
// the property, so it cannot satisfy a target that requires it present (the object
// analogue of TypeScript's "Property 'x' is optional in type … but required in
// type …"). The converse — a required source property filling an optional target
// property — is fine, so only this direction errors.
type OptionalPropertyError struct {
	Sub, Super *soltype.ObjectType
	Name       string
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// MutabilityMismatchError fires on RefType <: RefType when the sub is an immutable
// borrow but the super is mutable: writing through the mutable target would mutate a
// value the source only lent out as read-only, so an immutable reference cannot fill
// a mutable target. The reverse — a mutable source decaying to an immutable target — is
// fine, so only this direction errors. It also fires for `{x} <: mut {x}`, where the
// bare source is wrapped as an immutable view before re-dispatch.
type MutabilityMismatchError struct {
	Sub, Super *soltype.RefType
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// BorrowEscapeError fires when a borrow outlives the destination it flows into: a borrowed
// value (Lt != nil) constrained against an owned destination (Lt == nil), either a bare
// supertype or a RefType super with no lifetime. The firing path is INERT in C2 —
// every RefType carries Lt == nil until the lifetime sort lands (D1) and borrows
// originate (D2) — so the struct is wired now and exercised from D2.
//
// It is intentionally UNTESTED until then, and currently unconstructible: soltype.Lifetime
// has no concrete implementors, so no non-nil Lt exists to drive any firing branch.
// The Message format and the describe-the-whole-borrow choice are first observed in
// D2; coverage of Message/Span/Related is deferred to that PR.
type BorrowEscapeError struct {
	Sub   *soltype.RefType
	Super soltype.Type
	prov  NodeResolver // M2.5: type→node index (§3.5)
	site  ast.Node     // M2.5: constraint node fallback
}

// SpreadNotTupleError fires when a tuple-literal spread element ([...xs]) has an
// operand that does not infer to a tuple. M4 handles only the concrete-literal
// splice: the operand's element types are copied into the literal in order, so it
// must be a TupleType. A spread of any other type, such as an Array (M7), cannot
// be spliced statically. Two type-level cousins defer to M7/M9: a tuple-spread
// type over an abstract operand [...P, x], and a typed variadic tail
// [number, ...Array<number>]. Spread is the offending spread element and carries
// the blame span. Operand is the type it inferred to.
type SpreadNotTupleError struct {
	Spread  *ast.ArraySpreadExpr
	Operand soltype.Type
}

// InexactTupleSpreadError fires when a tuple-literal spread element ([...xs]) has an
// operand that infers to an INEXACT tuple ([number, ...]) and is not the last
// element of the literal. An inexact tuple has unknown length, so an element written
// after the spread would land at an unknown position and the result tuple's shape
// could not be pinned. A trailing inexact spread is allowed and makes the whole
// result inexact. The variadic-tail forms such as [number, ...Array<number>] defer
// to M7/M9. Spread is the offending spread element and carries the blame span.
// Operand is the inexact tuple it inferred to.
type InexactTupleSpreadError struct {
	Spread  *ast.ArraySpreadExpr
	Operand *soltype.TupleType
}

// MutFieldError fires when a `mut T` annotation appears as an object property
// value or a tuple element inside a non-mut container — `{a: mut {x}}` or
// `[mut {x}]`. An owned-mutable cell nested inside an immutable container is
// misleading: the enclosing container's immutability already reaches into the
// field, so the field is not actually writable, and interior mutability (#618) is
// the proper mechanism for the case the user wants. A borrow field (`&T`,
// `&mut T`) is a reference to external storage, not an interior owned-mutable
// cell, so it stays legal. The annotation node carries the blame span.
type MutFieldError struct {
	Ann *ast.MutableTypeAnn
}

// ReadonlyFieldError fires on a literal field-assignment `obj.f = …` whose target
// field is declared `readonly`. The assignment-site check lives in
// inferMemberAssign, which catches the case directly so the diagnostic blames the
// write expression and the message names the assignment outright.
type ReadonlyFieldError struct {
	Field string
	site  ast.Node // the BinaryExpr assignment, which carries the blame span
}

// ReadonlyFieldSubtypeError fires when a readonly source field flows into a
// non-readonly target field through structural subtyping under a mutable borrow:
// a `mut {readonly a: T}` cannot fill a `mut {a: T}` target, since the target view
// would otherwise let a holder write through and break the source's readonly
// contract. The check sits in the ObjectType <: ObjectType arm under the
// mut-context flag, the contravariant write view of a mutable borrow, and blames
// whatever site triggered the constraint, typically the call argument or return
// that flows the value out.
type ReadonlyFieldSubtypeError struct {
	Field string
	site  ast.Node
}

// ClassIntoExactObjectError fires when a class instance flows into an exact
// structural object target — `val foo: {x: number, y: number} = Point(1, 2)`. A
// non-final class may have subclasses that add members, so an instance carries an
// open tail of unknown properties an exact target cannot tolerate. A final class is
// exact instead, so it projects an exact body and is checked structurally rather than
// rejected here (exact-types §2.6). Holds the class instance and the object target.
type ClassIntoExactObjectError struct {
	Sub   *soltype.ClassType
	Super *soltype.ObjectType
	prov  NodeResolver
	site  ast.Node
}

// StructuralIntoClassError fires when a structural object flows into a class-typed
// target — `self.item = {x: 0}` where `item` is typed by a class. Nominal identity is
// declared, not structural, so an object literal that happens to carry a class's
// fields is still not that class. Holds the object source and the class target.
type StructuralIntoClassError struct {
	Sub   *soltype.ObjectType
	Super *soltype.ClassType
	prov  NodeResolver
	site  ast.Node
}

// NonClassSuperError fires when a name in an `extends` or `implements` clause resolves
// to a binding that is not a class — a type parameter, say. Only a class can be
// extended or implemented, so the edge is rejected rather than silently dropped. An
// unbound super name stays silent until M7's general TypeRef resolution reports the
// undefined name centrally. Ref carries the blame span and Name the rendered reference.
type NonClassSuperError struct {
	Ref  *ast.TypeRefTypeAnn
	Name string
}

// CannotExtendFinalClassError fires when an `extends` clause names a final class. A
// final class has no subclasses (exact-types §2.6), so it cannot be a superclass. Ref
// carries the blame span and Name the rendered reference.
type CannotExtendFinalClassError struct {
	Ref  *ast.TypeRefTypeAnn
	Name string
}

// VarianceMismatchError fires when a type parameter's declared `in`/`out`/`in out`
// modifier disagrees with the variance inferred from the class body — a parameter
// written `out` (covariant) that the body actually uses contravariantly. A written
// modifier is checked against the inferred variance rather than trusted, so a mismatch
// is rejected rather than silently overriding it. Name is the parameter, Declared the
// modifier's variance, Inferred the variance measured from the body, and Class the blame
// node.
type VarianceMismatchError struct {
	Name     string
	Declared Variance
	Inferred Variance
	Class    ast.Node
}

func (*CannotConstrainError) isSolverError()        {}
func (*MutFieldError) isSolverError()               {}
func (*ReadonlyFieldError) isSolverError()          {}
func (*ReadonlyFieldSubtypeError) isSolverError()   {}
func (*FuncArityMismatchError) isSolverError()      {}
func (*TupleLengthMismatchError) isSolverError()    {}
func (*SpreadNotTupleError) isSolverError()         {}
func (*InexactTupleSpreadError) isSolverError()     {}
func (*MissingPropertyError) isSolverError()        {}
func (*InexactIntoExactError) isSolverError()       {}
func (*InexactTupleIntoExactError) isSolverError()  {}
func (*InexactUnionIntoExactError) isSolverError()  {}
func (*ExtraPropertyError) isSolverError()          {}
func (*ExtraElementError) isSolverError()           {}
func (*OptionalPropertyError) isSolverError()       {}
func (*MutabilityMismatchError) isSolverError()     {}
func (*BorrowEscapeError) isSolverError()           {}
func (*ClassIntoExactObjectError) isSolverError()   {}
func (*StructuralIntoClassError) isSolverError()    {}
func (*NonClassSuperError) isSolverError()          {}
func (*CannotExtendFinalClassError) isSolverError() {}
func (*VarianceMismatchError) isSolverError()       {}

// --- Per-operand blame (§3.5): each constraint kind follows its operands through
// Prov on demand, falling back to its own site (where it keeps one) ---

func (e *CannotConstrainError) Span() ast.Span      { return spanOf(e.prov, e.Sub, e.site) }
func (e *CannotConstrainError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *FuncArityMismatchError) Span() ast.Span {
	// super call-shape → the CallExpr; degrade → the callee function; else the site.
	return spanOfFirst(e.prov, e.site, e.Super, e.Sub)
}
func (e *FuncArityMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.Sub) } // the fn

func (e *TupleLengthMismatchError) Span() ast.Span {
	// sub tuple literal → degrade to the other tuple → else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *TupleLengthMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *MissingPropertyError) Span() ast.Span {
	// The property's inner var → the .foo prop ident; degrade to the receiver;
	// else the site. The property-var arm is the only one reachable from member
	// access (which always records it); the receiver/site arms cover the concrete
	// object <: object case where the property type may be unrecorded.
	ops := make([]soltype.Type, 0, 2)
	if p, ok := e.Super.Prop(e.Name); ok {
		ops = append(ops, p.Type)
	}
	ops = append(ops, e.Sub)
	return spanOfFirst(e.prov, e.site, ops...)
}
func (e *MissingPropertyError) Related() []ast.Span { return relatedOf(e.prov, e.Sub) } // the receiver

func (e *InexactTupleIntoExactError) Span() ast.Span {
	return spanOf(e.prov, e.Sub, e.site)
}
func (e *InexactTupleIntoExactError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *InexactIntoExactError) Span() ast.Span {
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *InexactIntoExactError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *InexactUnionIntoExactError) Span() ast.Span {
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *InexactUnionIntoExactError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *ExtraPropertyError) Span() ast.Span {
	// The extra property lives on the sub (source); blame it, degrade to the super
	// (target), else the site.
	ops := make([]soltype.Type, 0, 2)
	if p, ok := e.Sub.Prop(e.Name); ok {
		ops = append(ops, p.Type)
	}
	ops = append(ops, e.Sub)
	return spanOfFirst(e.prov, e.site, ops...)
}
func (e *ExtraPropertyError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *ExtraElementError) Span() ast.Span {
	// The excess element lives on the sub (source) at Index; blame it, degrade to
	// the super (target), else the site.
	ops := make([]soltype.Type, 0, 2)
	if e.Index >= 0 && e.Index < len(e.Sub.Elems) {
		ops = append(ops, e.Sub.Elems[e.Index])
	}
	return spanOfFirst(e.prov, e.site, ops...)
}
func (e *ExtraElementError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *OptionalPropertyError) Span() ast.Span {
	// The optional property lives on the sub (source); blame it, degrade to the super
	// (target), else the site (same shape as ExtraPropertyError).
	ops := make([]soltype.Type, 0, 2)
	if p, ok := e.Sub.Prop(e.Name); ok {
		ops = append(ops, p.Type)
	}
	ops = append(ops, e.Sub)
	return spanOfFirst(e.prov, e.site, ops...)
}
func (e *OptionalPropertyError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *MutabilityMismatchError) Span() ast.Span {
	// The immutable source borrow is the actual value; blame it, degrade to the
	// mutable target, else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *MutabilityMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *BorrowEscapeError) Span() ast.Span {
	// The escaping borrow is the subject; blame it, degrade to the destination it escaped
	// into, else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *BorrowEscapeError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *ClassIntoExactObjectError) Span() ast.Span {
	// The class instance is the source value; blame it, degrade to the object target,
	// else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *ClassIntoExactObjectError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *StructuralIntoClassError) Span() ast.Span {
	// The object literal is the source value; blame it, degrade to the class target,
	// else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *StructuralIntoClassError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

func (e *NonClassSuperError) Span() ast.Span      { return e.Ref.Span() }
func (e *NonClassSuperError) Related() []ast.Span { return nil }

func (e *CannotExtendFinalClassError) Span() ast.Span      { return e.Ref.Span() }
func (e *CannotExtendFinalClassError) Related() []ast.Span { return nil }

func (e *VarianceMismatchError) Span() ast.Span      { return e.Class.Span() }
func (e *VarianceMismatchError) Related() []ast.Span { return nil }

// spanOf blames op's own source node when that node lies *within* the constraint
// site, and the site itself otherwise (or when op has no entry). The containment
// guard is the M2.5 fix for identifier-flow blame: for f("hi") the operand ("hi")
// is minted inside the call, so the narrower operand wins; for val a: number = x
// the operand (x's type) traces to x's *definition*, which is NOT inside the use
// site, so the use (site) wins — an ident-use error points at the use, not the
// definition (§3.8). In M3+, NodeFor chases interior Origin edges to the nearest
// AST leaf; in M2.5 it is a single lookup.
func spanOf(p NodeResolver, op soltype.Type, site ast.Node) ast.Span {
	if p != nil && site != nil {
		if n, ok := p.NodeFor(op); ok && site.Span().ContainsSpan(n.Span()) {
			return n.Span()
		}
	}
	return spanOfNode(site)
}

// spanOfFirst returns the span of the first operand that resolves through p, in
// the order given, falling back to the site's span (and finally the zero span
// when there is no site). Unlike spanOf it applies no containment guard — its
// callers' subject operands are minted *at* the constraint (a call-shape, a tuple
// literal, a member's field var), so the first resolved operand is already the
// narrowest blame. The site fallback keeps blame off the zero span when an
// operand is unrecorded (a degrade path reachable from M4).
func spanOfFirst(p NodeResolver, site ast.Node, ops ...soltype.Type) ast.Span {
	if p != nil {
		for _, op := range ops {
			if n, ok := p.NodeFor(op); ok {
				return n.Span()
			}
		}
	}
	return spanOfNode(site)
}

// spanOfNode returns site.Span(), or the zero span when site is nil. The zero
// span only arises for a hand-built error with no site; every error the walk
// produces carries one.
func spanOfNode(site ast.Node) ast.Span {
	if site != nil {
		return site.Span()
	}
	return ast.Span{}
}

// relatedOf resolves each operand that has an entry to a related span and drops
// the ones that don't (no fallback — a missing related node is simply omitted),
// deduped by span (§3.6).
func relatedOf(p NodeResolver, ops ...soltype.Type) []ast.Span {
	if p == nil {
		return nil
	}
	var spans []ast.Span
	for _, op := range ops {
		if n, ok := p.NodeFor(op); ok {
			spans = appendUnique(spans, n.Span())
		}
	}
	return spans
}

// appendUnique appends s unless an equal span is already present. ast.Span is a
// plain comparable value (Start/End Location of ints + SourceID), so == suffices.
func appendUnique(spans []ast.Span, s ast.Span) []ast.Span {
	for _, e := range spans {
		if e == s {
			return spans
		}
	}
	return append(spans, s)
}

// --- M2 bridge errors (born in the walk with the offending ast.Node in hand,
// so they self-blame: Span() is the node's own span, no post-hoc stamping) ---

// UnknownIdentifierError fires when a value-position identifier resolves to no
// binding in the scope chain.
type UnknownIdentifierError struct {
	Ident *ast.IdentExpr
}

// NamespaceUsedAsValueError fires when a path expression resolves to a namespace
// in value position. Namespaces are a separate binding sort and never flow as
// values. M4 moves the rejection off inferIdent to the value-position consumer
// (demandValue): a namespace is legal in the object position of a member/index
// chain, so this fires for both a bare `f(Foo)` and a partial chain `f(A.B)` where
// the chain stops at a namespace. Node is the offending path expression (the blame
// span); NS is the namespace it resolved to.
type NamespaceUsedAsValueError struct {
	Node ast.Expr
	NS   *Namespace
}

// UnknownNamespaceMemberError fires when a namespace member access (Foo.bar or the
// constant-keyed Foo["bar"]) names a member the namespace does not declare —
// neither a value nor a nested namespace. Node is the member/index expression (the
// blame span); NS is the namespace; Name is the absent member.
type UnknownNamespaceMemberError struct {
	Node ast.Expr
	NS   *Namespace
	Name string
}

// DynamicNamespaceIndexError fires when a namespace is indexed by a non-constant
// key (Foo[k]). A namespace member is resolved statically, so an index into one
// must be a constant string literal — Foo["bar"], the bracket form of Foo.bar.
// Index is the offending index expression (the blame span); NS is the namespace.
type DynamicNamespaceIndexError struct {
	Index ast.Expr
	NS    *Namespace
}

// UnsupportedNodeError is the M2-subset guard: an AST node whose KIND is outside
// the M2 walk's coverage (kind = astKind(Node)). Unlike BodyDeclNotAllowedError
// this is a temporary scope gate, not a permanent language rule — later milestones
// widen coverage and remove arms. The "the node is fine, a FEATURE of it isn't"
// case (e.g. optional chaining on a supported MemberExpr) is UnsupportedFeatureError.
type UnsupportedNodeError struct {
	Node ast.Node
}

// UnsupportedFeatureError is the sibling of UnsupportedNodeError for the case
// where the node kind IS supported but a feature of it is not — e.g. a generic
// function (the FuncExpr is fine, type params are M3) or optional chaining (the
// MemberExpr is fine, recv?.foo is M6). The node carries the blame span; Feature
// names what is unsupported (not derivable from astKind, which would name the
// supported parent).
type UnsupportedFeatureError struct {
	Node    ast.Node
	Feature string
}

// BodyDeclNotAllowedError fires when a statement-level declaration in a function
// body is anything other than a VarDecl. This is a permanent language rule
// (§3.2), not the temporary subset gate above: body decls are VarDecl-only.
type BodyDeclNotAllowedError struct {
	Decl ast.Decl
}

// MissingInitializerError fires when a `val`/`var` declaration has no
// initializer. M2 has no way to bind such a name yet — annotation-only binding
// needs TypeAnn→soltype resolution that lands in a later PR — so this is its own
// kind rather than a generic UnsupportedNodeError: it marks an annotation-driven
// binding the walk can't infer, not an AST node shape outside the subset.
type MissingInitializerError struct {
	Decl *ast.VarDecl
}

// DuplicateDeclarationError fires when a top-level `val`/`var` rebinds a name
// already declared in the module scope. Unlike a function (whose repeated
// top-level declarations are overloads, supported from PR-3), a variable may be
// declared only once per scope; the first binding is kept.
//
// Decl is the rejected redeclaration (the blame span); Previous is the kept first
// declaration, surfaced via Related() as "previously declared here". Name is the
// resolved binding-key name — retained alongside the nodes because it may be a
// qualified key name distinct from the decl's local identifier, and the message
// wants the canonical name (§3.4 caveat).
type DuplicateDeclarationError struct {
	Decl, Previous ast.Decl
	Name           string
}

// NoMatchingOverloadError fires when a call to an overloaded name (PR6) matches
// none of the overload set's arms — every candidate either disagreed on arity or
// failed to accept the supplied argument types. It replaces M2's interim
// OverloadNotSupportedError (overloading is now real).
//
// It is a BRIDGE error: born in resolveOverload with the *ast.CallExpr in hand, so
// it self-blames (Span() is the call) and relates the callee expression. Candidates
// holds the overload arms (declaration order) so the message can list the
// signatures that were tried.
type NoMatchingOverloadError struct {
	Call       *ast.CallExpr
	Candidates []TypeScheme
}

// UnannotatedRecursiveOverloadError fires when an overloaded function participates
// in a mutually-recursive group (a dep-graph component with more than one binding)
// without fully-annotated overload signatures (PR6). Fixed-point iteration over
// overload choices is not guaranteed to converge under subtyping, so the overload
// set must be ground before the group is inferred; self-recursion (a singleton
// component) is softer and does not trip this. The binding degrades to its first
// arm so a later reference still resolves.
//
// It is a BRIDGE error: born in checkOverloadAnnotations with the offending arm's
// declaration in hand, so it self-blames (Span() is the first unannotated arm).
// Name is the overloaded binding's name for the message.
type UnannotatedRecursiveOverloadError struct {
	Decl ast.Decl
	Name string
}

// DuplicateOverloadError fires when two arms of an overload set (PR6) are
// indistinguishable for dispatch: they share an arity and have pointwise-equal
// parameter types, so no call could ever select one over the other. An overload set
// compiles to a single runtime function that dispatches on argument types, so two
// arms accepting exactly the same arguments cannot be told apart at codegen.
//
// It is a BRIDGE error: born in the overload-set builder (module.go) with the
// colliding arms in hand, so it self-blames the duplicate (later) arm and relates the
// earlier arm it collides with. Name is the overloaded binding's name for the message.
type DuplicateOverloadError struct {
	Decl, Previous ast.Decl
	Name           string
}

// TooManyArgsError fires when a DIRECT call supplies more arguments than the
// (concrete) callee declares — the #677 §4.2.3 extra-arg lint, which rejects
// too-many for exact AND inexact callees alike (an inexact function tolerates
// extras only as a callback, not at a visible call site). It is the uniform
// too-many message; the surviving FuncArityMismatchError covers "too few /
// required" and the callback-subtyping accept-set failures.
//
// It is a BRIDGE error: born in inferCall with the *ast.CallExpr in hand, so it
// self-blames (Span() is the call's own span) and relates the callee expression —
// no Prov stamping. Fn is the resolved callee FuncType, retained so the message can
// report the declared parameter count.
type TooManyArgsError struct {
	Call *ast.CallExpr
	Fn   *soltype.FuncType
}

// NotEnoughArgsError fires when a DIRECT call supplies fewer arguments than the
// (concrete) callee REQUIRES — the symmetric twin of TooManyArgsError. `required`
// is the count of non-trailing-optional params (requiredCount), so an optional
// trailing param may be omitted without tripping it. Like TooManyArgsError it is a
// call-site lint over a concrete callee; a deferred (var) callee's too-few still
// surfaces from the accept-set gate as FuncArityMismatchError.
//
// It is a BRIDGE error: born in inferCall with the *ast.CallExpr in hand, so it
// self-blames (Span() is the call) and relates the callee expression. Fn is the
// resolved callee FuncType, retained so the message can report the required count.
type NotEnoughArgsError struct {
	Call *ast.CallExpr
	Fn   *soltype.FuncType
}

// AwaitOutsideAsyncError fires when an `await` expression appears outside the
// body of an `async fn` (the walk's rule, not the type rule — per M3's plan,
// "Awaiting outside an `async` function is rejected by the AST walk, not by the
// type rule"). The argument is still walked (so any errors inside it surface),
// but the await contributes a `never` placeholder so callers don't cascade.
//
// EnclosingFn is the (non-async) function the await sits in, when there is one —
// the function the user would mark `async` to fix the error, surfaced via
// Related(). It is nil when the await is at module top-level (no enclosing fn).
type AwaitOutsideAsyncError struct {
	Await       *ast.AwaitExpr
	EnclosingFn ast.Node
}

// ReturnOutsideFunctionError fires when a `return` statement is reached outside
// any function body — e.g. inside an `if` that is part of a top-level `val`
// initializer. Symmetric to AwaitOutsideAsyncError: the walk rejects it rather
// than silently dropping the return point (a return collected against no enclosing
// function would otherwise vanish).
type ReturnOutsideFunctionError struct {
	Return *ast.ReturnStmt
}

// ForAwaitOutsideAsyncError fires when a `for await (x in xs)` loop appears
// outside the body of an `async fn`. Like AwaitOutsideAsyncError it is a WALK
// rejection, not a type-rule failure: the iterable and body are still walked so
// their own errors surface. EnclosingFn is the non-async function the loop sits
// in, surfaced via Related() as the function to mark `async`; it is nil at module
// top-level where there is no enclosing function.
type ForAwaitOutsideAsyncError struct {
	Loop        *ast.ForInStmt
	EnclosingFn ast.Node
}

// NotIterableError fires when the operand of `for (x in xs)` is not iterable, or
// the operand of `for await (x in xs)` is not async-iterable. M5 resolves the
// iteration protocol structurally over the types the solver can represent: a
// tuple yields the union of its elements, and a union of tuples yields the union
// of their element types. Array<T> and the `[Symbol.iterator]` protocol land in
// M7, so every other operand is reported here, as is every operand of a
// `for await` since no async iterable is representable yet. Await selects the
// message.
type NotIterableError struct {
	Iterable ast.Expr
	Type     soltype.Type
	Await    bool
}

// AsyncReturnNotPromiseError fires when an `async fn` declares a return annotation
// that is not a `Promise<…>`. An async function's external type is always
// `Promise<T>`, so the annotation NAMES that Promise; a bare type
// (`async fn () -> number`) is rejected — write `-> Promise<number>`, or
// `-> Promise<_>` to let the checker infer the inner from the body.
//
// Like AwaitOutsideAsyncError it is a WALK rejection, not a type-rule failure:
// born in inferFunc (asyncReturn) with the annotation and function nodes in hand,
// so it self-blames from the annotation's span and relates the function via
// Related() (the signature the user would fix).
type AsyncReturnNotPromiseError struct {
	Return ast.TypeAnn // the offending (non-Promise) return annotation
	Fn     ast.Node    // the enclosing async function, surfaced via Related()
}

// InvalidAssignmentTargetError fires when the left-hand side of an assignment
// (`a = expr`) is not an assignable place. M3's only assignable target is an
// IdentExpr resolving to a `var` binding; a literal, call, or any other non-place
// target is rejected here. A member or index target such as `obj.x = …` is instead
// reported as an UnsupportedFeatureError: it is a valid place whose type rule needs
// object/array types (M4), not a fundamentally invalid target.
//
// It is a BRIDGE error: born in inferAssign with the offending target node in hand,
// so it self-blames (Span() is the target's own span); it carries no related node.
type InvalidAssignmentTargetError struct {
	Target ast.Expr // the non-place assignment target (blame span)
}

// CannotAssignToImmutableError fires when a reassignment (`a = expr`) targets a
// binding that is not a `var` — a `val`, a function, a parameter, or a prelude
// binding. The binding-level mutability gate (PR8); `mut`-field / alias / lifetime
// mutability is M4.
//
// It is a BRIDGE error: born in inferAssign with the assignment node in hand, so
// it self-blames the whole assignment (Span()). Decl is the introducing
// declaration — surfaced via Related() as the "declared immutable here" span,
// resolved for a `val`/`fn` decl AND for a parameter (its pattern) — or nil for a
// prelude binding that carries no source node. Name is the target identifier, for
// the message.
type CannotAssignToImmutableError struct {
	Assign *ast.BinaryExpr // the assignment (blame span)
	Name   string          // the target binding's name
	Decl   ast.Node        // the introducing decl, related; nil for prelude bindings
}

// NonExhaustiveMatchError fires when a `match` expression does not cover every
// value its scrutinee can take, so a value could fall through every arm. In M4 the
// coverage decision reads only the scrutinee's structural exactness. An exact object
// or tuple scrutinee is covered by a structural arm matching its shape. An inexact
// scrutinee carries an open tail of unknown values, so it requires a catch-all arm.
// A catch-all is an unguarded wildcard `_` or identifier pattern. Union-scrutinee
// exhaustiveness is M6 and enum exhaustiveness is M5, and both extend this same form.
//
// It is a bridge error born in inferMatch with the match node in hand, so it
// self-blames the whole match through Span and carries no related node.
type NonExhaustiveMatchError struct {
	Match *ast.MatchExpr
}

// MixedOwnershipError fires when an inferred union or intersection has a borrowed
// member beside an owned one, such as `{x: number} | &{y: number}`, which has no
// single owned-or-borrowed verdict. It blames the inference join where it forms.
type MixedOwnershipError struct {
	Node ast.Node
}

func (*UnknownIdentifierError) isSolverError()              {}
func (*NamespaceUsedAsValueError) isSolverError()           {}
func (*UnknownNamespaceMemberError) isSolverError()         {}
func (*DynamicNamespaceIndexError) isSolverError()          {}
func (*InvalidAssignmentTargetError) isSolverError()        {}
func (*CannotAssignToImmutableError) isSolverError()        {}
func (*TooManyArgsError) isSolverError()                    {}
func (*NotEnoughArgsError) isSolverError()                  {}
func (*UnsupportedNodeError) isSolverError()                {}
func (*UnsupportedFeatureError) isSolverError()             {}
func (*BodyDeclNotAllowedError) isSolverError()             {}
func (*MissingInitializerError) isSolverError()             {}
func (*DuplicateDeclarationError) isSolverError()           {}
func (*NoMatchingOverloadError) isSolverError()             {}
func (*UnannotatedRecursiveOverloadError) isSolverError()   {}
func (*DuplicateOverloadError) isSolverError()              {}
func (*AwaitOutsideAsyncError) isSolverError()              {}
func (*ForAwaitOutsideAsyncError) isSolverError()           {}
func (*NotIterableError) isSolverError()                    {}
func (*ReturnOutsideFunctionError) isSolverError()          {}
func (*AsyncReturnNotPromiseError) isSolverError()          {}
func (*NonExhaustiveMatchError) isSolverError()             {}
func (*MixedOwnershipError) isSolverError()                 {}
func (*MutLeafThroughSharedBorrowError) isSolverError()     {}
func (*MissingSelfReceiverError) isSolverError()            {}
func (*MethodOverloadReceiverMismatchError) isSolverError() {}
func (*MultipleConstructorsError) isSolverError()           {}
func (*FieldInitializerNotAllowedError) isSolverError()     {}
func (*SubclassConstructorRequiredError) isSolverError()    {}
func (*WriteOnlyPropertyError) isSolverError()              {}
func (*SetterArityError) isSolverError()                    {}
func (*RecursiveMethodAnnotationError) isSolverError()      {}
func (*FieldNotInitializedError) isSolverError()            {}
func (*ReadBeforeInitError) isSolverError()                 {}
func (*MethodCallBeforeInitError) isSolverError()           {}
func (*InstancePatternNotClassError) isSolverError()        {}
func (*ExtractorPatternNotCtorError) isSolverError()        {}
func (*ExtractorPatternArityError) isSolverError()          {}
func (*AliasArityMismatchError) isSolverError()             {}

// MissingSelfReceiverError fires when a non-static instance method, getter, or
// setter omits its `self` receiver. Such a member cannot read the instance, so the
// receiver is required.
type MissingSelfReceiverError struct {
	Name string
	Elem ast.ClassElem
}

func (e *MissingSelfReceiverError) Span() ast.Span      { return e.Elem.Span() }
func (e *MissingSelfReceiverError) Related() []ast.Span { return nil }
func (e *MissingSelfReceiverError) Message() string {
	return "Instance member '" + e.Name + "' must declare a `self` receiver as its first parameter."
}

// MethodOverloadReceiverMismatchError fires when the arms of an overloaded method disagree
// on their `self` receiver mutability, as when one arm declares `self` and another `mut
// self`. Overload resolution dispatches on the value arguments rather than the receiver, and
// the receiver-mutability check reads one representative arm, so every arm must agree on
// whether it needs a mutable receiver. Elem is the offending arm.
type MethodOverloadReceiverMismatchError struct {
	Name string
	Elem ast.ClassElem
}

func (e *MethodOverloadReceiverMismatchError) Span() ast.Span      { return e.Elem.Span() }
func (e *MethodOverloadReceiverMismatchError) Related() []ast.Span { return nil }
func (e *MethodOverloadReceiverMismatchError) Message() string {
	return "Overloaded method '" + e.Name + "' must use the same `self` receiver mutability in every arm."
}

// WriteOnlyPropertyError fires when a setter-only member is read, as in `val v =
// c.value` where `value` is declared only with `set value(...)`. A setter defines a
// write, not a readable value.
type WriteOnlyPropertyError struct {
	Name string
	Site ast.Node
}

func (e *WriteOnlyPropertyError) Span() ast.Span      { return e.Site.Span() }
func (e *WriteOnlyPropertyError) Related() []ast.Span { return nil }
func (e *WriteOnlyPropertyError) Message() string {
	return "Property '" + e.Name + "' is write-only; it has a setter but no getter or field to read."
}

// InstancePatternNotClassError fires when the name in an instance pattern `Name { ... }`
// resolves to no class — either the name is unbound or it names a value or type parameter
// rather than a class. An instance pattern deconstructs a class instance through the named
// class's member view, so the name must be a class.
type InstancePatternNotClassError struct {
	Pat  *ast.InstancePat
	Name string
}

func (e *InstancePatternNotClassError) Span() ast.Span      { return e.Pat.Span() }
func (e *InstancePatternNotClassError) Related() []ast.Span { return nil }
func (e *InstancePatternNotClassError) Message() string {
	return "`" + e.Name + "` does not name a class and cannot be used as an instance pattern."
}

// ExtractorPatternNotCtorError fires when the name in an extractor pattern `Name(...)`
// resolves to no constructor — the name is unbound, or its value is not callable as a
// constructor. An extractor pattern deconstructs a value through the named constructor's
// parameters, so the name must resolve to a constructor.
type ExtractorPatternNotCtorError struct {
	Pat  *ast.ExtractorPat
	Name string
}

func (e *ExtractorPatternNotCtorError) Span() ast.Span      { return e.Pat.Span() }
func (e *ExtractorPatternNotCtorError) Related() []ast.Span { return nil }
func (e *ExtractorPatternNotCtorError) Message() string {
	return "`" + e.Name + "` is not a constructor and cannot be used as an extractor pattern."
}

// ExtractorPatternArityError fires when an extractor pattern `Name(a, b, ...)` supplies
// a different number of sub-patterns than the constructor has parameters. Each sub-pattern
// binds one constructor parameter, so the counts must match.
type ExtractorPatternArityError struct {
	Pat      *ast.ExtractorPat
	Name     string
	Expected int
	Got      int
}

func (e *ExtractorPatternArityError) Span() ast.Span      { return e.Pat.Span() }
func (e *ExtractorPatternArityError) Related() []ast.Span { return nil }
func (e *ExtractorPatternArityError) Message() string {
	return fmt.Sprintf("extractor pattern `%s` expects %d arguments but got %d", e.Name, e.Expected, e.Got)
}

// AliasArityMismatchError fires when a generic-alias reference `Name<…>` supplies fewer
// than the required number of type arguments or more than the total parameter count. A
// trailing parameter with a default is optional, so the valid count is the range from
// Required to Total, where Required counts the parameters with no default. The message
// states a single count when every parameter is required and a range when a default makes
// one optional.
type AliasArityMismatchError struct {
	Ref      *ast.TypeRefTypeAnn
	Name     string
	Required int
	Total    int
	Got      int
}

func (e *AliasArityMismatchError) Span() ast.Span      { return e.Ref.Span() }
func (e *AliasArityMismatchError) Related() []ast.Span { return nil }
func (e *AliasArityMismatchError) Message() string {
	if e.Required == e.Total {
		return fmt.Sprintf("type alias `%s` expects %d type arguments but got %d", e.Name, e.Total, e.Got)
	}
	return fmt.Sprintf("type alias `%s` expects between %d and %d type arguments but got %d", e.Name, e.Required, e.Total, e.Got)
}

// MultipleConstructorsError fires on the second and any later `constructor` block in
// one class; a class declares at most one.
type MultipleConstructorsError struct {
	Ctor *ast.ConstructorElem
}

func (e *MultipleConstructorsError) Span() ast.Span      { return e.Ctor.Span() }
func (e *MultipleConstructorsError) Related() []ast.Span { return nil }
func (e *MultipleConstructorsError) Message() string {
	return "Multiple constructors per class are not yet supported."
}

// FieldInitializerNotAllowedError fires when an instance field declares a `= expr`
// initializer. Instance fields are assigned in the constructor body; only static
// fields may use the initializer form.
type FieldInitializerNotAllowedError struct {
	Field *ast.FieldElem
}

func (e *FieldInitializerNotAllowedError) Span() ast.Span      { return e.Field.Span() }
func (e *FieldInitializerNotAllowedError) Related() []ast.Span { return nil }
func (e *FieldInitializerNotAllowedError) Message() string {
	name, _ := objKeyName(e.Field.Name)
	return "Field '" + name + "' cannot have a `= expr` initializer; only static fields may use this form. Initialize instance fields in the constructor body."
}

// SubclassConstructorRequiredError fires when a class with an `extends` clause
// declares no `constructor` block. Synthesizing one would silently skip the required
// `super(...)` call, so the constructor must be written explicitly.
type SubclassConstructorRequiredError struct {
	Decl *ast.ClassDecl
}

func (e *SubclassConstructorRequiredError) Span() ast.Span      { return e.Decl.Name.Span() }
func (e *SubclassConstructorRequiredError) Related() []ast.Span { return nil }
func (e *SubclassConstructorRequiredError) Message() string {
	return "Subclasses must declare an explicit `constructor` block; constructor synthesis is not supported for classes with an `extends` clause."
}

// SetterArityError fires when a setter declares other than exactly one value parameter
// beyond its `self` receiver. A setter's single parameter is the value being assigned,
// so `set x(self)` and `set x(self, a, b)` are both malformed.
type SetterArityError struct {
	Name  string
	Elem  *ast.SetterElem
	Count int // value parameters declared, excluding `self`
}

func (e *SetterArityError) Span() ast.Span      { return e.Elem.Span() }
func (e *SetterArityError) Related() []ast.Span { return nil }
func (e *SetterArityError) Message() string {
	return "Setter '" + e.Name + "' must declare exactly one value parameter; found " + strconv.Itoa(e.Count) + "."
}

// RecursiveMethodAnnotationError fires when a group of mutually recursive methods
// in one class has no return-type annotation anywhere in the cycle. A method's
// signature is pre-declared before its body is walked so a sibling call resolves,
// but an un-annotated return in a cycle stays an inference variable that the cycle
// cannot ground on its own. An annotation on any member of the cycle breaks it, so
// every member reports until one is annotated. This mirrors the recursion gate
// top-level overloaded functions use (UnannotatedRecursiveOverloadError).
type RecursiveMethodAnnotationError struct {
	Name  string
	Elem  *ast.MethodElem
	Group []string // the mutually recursive method names, sorted lexicographically for a stable message
}

func (e *RecursiveMethodAnnotationError) Span() ast.Span      { return e.Elem.Span() }
func (e *RecursiveMethodAnnotationError) Related() []ast.Span { return nil }
func (e *RecursiveMethodAnnotationError) Message() string {
	return "Mutually recursive method '" + e.Name + "' must declare a return type; the cycle " + strings.Join(e.Group, ", ") + " has no annotated return to ground it."
}

// FieldNotInitializedError fires when a constructor body has a reachable exit on
// which one or more required instance fields are still uninitialized. FieldNames
// lists the missing fields in sorted order, so the message is deterministic.
type FieldNotInitializedError struct {
	FieldNames []string
	Ctor       *ast.ConstructorElem
}

func (e *FieldNotInitializedError) Span() ast.Span      { return e.Ctor.Fn.Body.Span }
func (e *FieldNotInitializedError) Related() []ast.Span { return nil }
func (e *FieldNotInitializedError) Message() string {
	if len(e.FieldNames) == 1 {
		return "Field '" + e.FieldNames[0] + "' is not initialized on every path through the constructor."
	}
	return "Fields " + quoteJoin(e.FieldNames) + " are not initialized on every path through the constructor."
}

// ReadBeforeInitError fires when a constructor reads `self.f` on a path where field
// `f` has not yet been assigned. It blames the read.
type ReadBeforeInitError struct {
	FieldName string
	Read      ast.Node
}

func (e *ReadBeforeInitError) Span() ast.Span      { return e.Read.Span() }
func (e *ReadBeforeInitError) Related() []ast.Span { return nil }
func (e *ReadBeforeInitError) Message() string {
	return "Field 'self." + e.FieldName + "' is read before it has been initialized."
}

// MethodCallBeforeInitError fires when a constructor calls a method on `self` on a
// path where one or more required fields are not yet assigned. The called method may
// read any field, so every required field must be initialized first. MissingFields
// lists the still-unassigned fields in sorted order.
type MethodCallBeforeInitError struct {
	MissingFields []string
	Call          ast.Node
}

func (e *MethodCallBeforeInitError) Span() ast.Span      { return e.Call.Span() }
func (e *MethodCallBeforeInitError) Related() []ast.Span { return nil }
func (e *MethodCallBeforeInitError) Message() string {
	return "Cannot call a method on `self` before all required fields are initialized; missing " + quoteJoin(e.MissingFields) + "."
}

// quoteJoin renders names as a comma-separated list, each single-quoted, for a
// multi-field diagnostic.
func quoteJoin(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	return strings.Join(quoted, ", ")
}

func (e *MixedOwnershipError) Span() ast.Span      { return spanOfNode(e.Node) }
func (e *MixedOwnershipError) Related() []ast.Span { return nil }
func (e *MixedOwnershipError) Message() string {
	return "a union or intersection mixes owned and borrowed members. Make ownership uniform first. Clone the borrowed member to own it, or borrow the owned member."
}

func (e *NonExhaustiveMatchError) Span() ast.Span      { return e.Match.Span() }
func (e *NonExhaustiveMatchError) Related() []ast.Span { return nil }
func (e *NonExhaustiveMatchError) Message() string {
	return "match is not exhaustive; add a catch-all branch"
}

// MutLeafThroughSharedBorrowError fires when a destructuring pattern marks a leaf
// `mut` while the scrutinee is an immutable `&` borrow. A `mut` leaf projects mutable
// access to the value it binds, which cannot be obtained through a shared borrow, so the leaf is
// rejected. It self-blames the leaf's pattern node.
type MutLeafThroughSharedBorrowError struct {
	Node ast.Node
}

func (e *MutLeafThroughSharedBorrowError) Span() ast.Span      { return e.Node.Span() }
func (e *MutLeafThroughSharedBorrowError) Related() []ast.Span { return nil }
func (e *MutLeafThroughSharedBorrowError) Message() string {
	return "cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"
}

// LifetimeBoundNotSatisfiedError fires when a function body does not establish an
// outlives relation its signature declares. A declared `<'a: 'b>` asserts 'a outlives
// 'b, and the body must prove that from its borrows, joins, and stores. When the
// inferred lifetime graph does not prove it, the declared bound is unfounded and
// rejected here. Sub and Super are the two lifetime names written without the leading
// `'`, so `<'a: 'b>` gives Sub "a" and Super "b".
//
// It is a BRIDGE error: born in inferFunc with the LifetimeParam binder in hand, so it
// self-blames the `'a: 'b` binder through Span and carries no related node.
type LifetimeBoundNotSatisfiedError struct {
	Sub   string
	Super string
	Param *ast.LifetimeParam
}

func (*LifetimeBoundNotSatisfiedError) isSolverError()        {}
func (e *LifetimeBoundNotSatisfiedError) Span() ast.Span      { return e.Param.Span() }
func (e *LifetimeBoundNotSatisfiedError) Related() []ast.Span { return nil }
func (e *LifetimeBoundNotSatisfiedError) Message() string {
	return fmt.Sprintf(
		"declared lifetime bound '%s: '%s is not satisfied; the body does not make '%s outlive '%s",
		e.Sub, e.Super, e.Sub, e.Super)
}

// UndeclaredLifetimeError fires when a signature uses a named lifetime that its own
// `<…>` quantifier list does not bind. A `&'x` borrow or a bound's right-hand side
// names `'x`, but no `<'x>` binder introduces it, so the name is a forgotten
// declaration or a typo. Either way it is a hard error the author must resolve. Name is
// the used name without the leading `'`.
//
// The clause shapes only the hint, not the severity. With no `<…>` clause the message
// prompts adding one. With a clause that binds other names the message suggests the
// nearest declared siblings by edit distance when one is close, since the miss is likely
// a typo, and otherwise prompts declaring the name. Recovery mints a fresh lifetime
// through namedLifetime so the signature stays well-formed and later checks proceed.
type UndeclaredLifetimeError struct {
	Name        string
	Suggestions []string // nearest declared siblings by edit distance; empty when none is close
	hasClause   bool     // whether the signature carries any `<…>` binder at all
	span        ast.Span
}

func (*UndeclaredLifetimeError) isSolverError()        {}
func (e *UndeclaredLifetimeError) Span() ast.Span      { return e.span }
func (e *UndeclaredLifetimeError) Related() []ast.Span { return nil }

func (e *UndeclaredLifetimeError) Message() string {
	msg := "lifetime '" + e.Name + " is used but not declared"
	switch {
	case !e.hasClause:
		msg += "; add `<'" + e.Name + ">` to the enclosing function signature"
	case len(e.Suggestions) > 0:
		msg += "; did you mean '" + strings.Join(e.Suggestions, " or '") + "?"
	default:
		// A clause exists but no declared name is close enough to be a likely typo, so
		// point at declaring the name rather than guessing a correction.
		msg += "; add `<'" + e.Name + ">` to the signature's lifetime list"
	}
	return msg
}

// DuplicateLifetimeParamError fires when a signature's `<…>` list binds the same
// lifetime name more than once, as in `<'a, 'a>`. The repeat binds nothing new and is
// almost certainly a typo, so it is a hard error. Name is the repeated name without the
// leading `'`. Param is the redundant binder, the blame span; First is the kept binder,
// surfaced through Related.
type DuplicateLifetimeParamError struct {
	Name  string
	Param *ast.LifetimeParam
	First *ast.LifetimeParam
}

func (*DuplicateLifetimeParamError) isSolverError()        {}
func (e *DuplicateLifetimeParamError) Span() ast.Span      { return e.Param.Span() }
func (e *DuplicateLifetimeParamError) Related() []ast.Span { return []ast.Span{e.First.Span()} }
func (e *DuplicateLifetimeParamError) Message() string {
	return "lifetime parameter '" + e.Name + " is declared more than once"
}

// UnusedLifetimeParamError fires when a signature declares a `<'a>` binder that no
// `&'a` borrow and no bound right-hand side references. The program is well-typed; the
// binder is dead weight. Name is the declared name without the leading `'`. It is the
// symmetric companion of UndeclaredLifetimeError — the same signature scan read the
// other way, binders with no use rather than uses with no binder — and is always a
// warning.
type UnusedLifetimeParamError struct {
	Name  string
	Param *ast.LifetimeParam
}

func (*UnusedLifetimeParamError) isSolverError()        {}
func (e *UnusedLifetimeParamError) Span() ast.Span      { return e.Param.Span() }
func (e *UnusedLifetimeParamError) Related() []ast.Span { return nil }
func (e *UnusedLifetimeParamError) IsWarning() bool     { return true }
func (e *UnusedLifetimeParamError) Message() string {
	return "lifetime parameter '" + e.Name + " is declared but never used"
}

func (e *UnknownIdentifierError) Span() ast.Span      { return e.Ident.Span() }
func (e *UnknownIdentifierError) Related() []ast.Span { return nil }
func (e *UnknownIdentifierError) Message() string {
	return "Unknown identifier: " + e.Ident.Name
}

func (e *NamespaceUsedAsValueError) Span() ast.Span      { return e.Node.Span() }
func (e *NamespaceUsedAsValueError) Related() []ast.Span { return nil }
func (e *NamespaceUsedAsValueError) Message() string {
	return "Namespace used as a value: " + e.NS.Name
}

func (e *UnknownNamespaceMemberError) Span() ast.Span      { return e.Node.Span() }
func (e *UnknownNamespaceMemberError) Related() []ast.Span { return nil }
func (e *UnknownNamespaceMemberError) Message() string {
	return "Namespace " + e.NS.Name + " has no member: " + e.Name
}

func (e *DynamicNamespaceIndexError) Span() ast.Span      { return e.Index.Span() }
func (e *DynamicNamespaceIndexError) Related() []ast.Span { return nil }
func (e *DynamicNamespaceIndexError) Message() string {
	return "Namespace " + e.NS.Name + " can only be indexed by a constant string"
}

func (e *InvalidAssignmentTargetError) Span() ast.Span      { return e.Target.Span() }
func (e *InvalidAssignmentTargetError) Related() []ast.Span { return nil }
func (e *InvalidAssignmentTargetError) Message() string {
	return "Invalid assignment target: " + astKind(e.Target)
}

func (e *CannotAssignToImmutableError) Span() ast.Span { return e.Assign.Span() }
func (e *CannotAssignToImmutableError) Related() []ast.Span {
	// Point at the introducing declaration (the "declared immutable here" span) when
	// there is one — a `val`/`fn` decl or a parameter's pattern; a prelude binding
	// carries no source node, so the related list is then empty.
	if e.Decl == nil {
		return nil
	}
	return []ast.Span{e.Decl.Span()}
}
func (e *CannotAssignToImmutableError) Message() string {
	return "Cannot assign to immutable binding: " + e.Name
}

func (e *TooManyArgsError) Span() ast.Span { return e.Call.Span() }
func (e *TooManyArgsError) Related() []ast.Span {
	// Span() uses the call's own span and never derefs Callee; Related() does, so
	// guard a nil Callee (not produced by the parser, but possible in a hand-built
	// AST) to uphold the "never panic on malformed AST" guarantee.
	if e.Call.Callee == nil {
		return nil
	}
	return []ast.Span{e.Call.Callee.Span()}
}
func (e *TooManyArgsError) Message() string {
	return fmt.Sprintf("Too many arguments: expected at most %d, but got %d",
		len(e.Fn.Params), len(e.Call.Args))
}

func (e *NotEnoughArgsError) Span() ast.Span { return e.Call.Span() }
func (e *NotEnoughArgsError) Related() []ast.Span {
	if e.Call.Callee == nil {
		return nil
	}
	return []ast.Span{e.Call.Callee.Span()}
}
func (e *NotEnoughArgsError) Message() string {
	return fmt.Sprintf("Not enough arguments: expected at least %d, but got %d",
		requiredCount(e.Fn), len(e.Call.Args))
}

func (e *UnsupportedNodeError) Span() ast.Span      { return e.Node.Span() }
func (e *UnsupportedNodeError) Related() []ast.Span { return nil }
func (e *UnsupportedNodeError) Message() string {
	return "Unsupported: " + astKind(e.Node)
}

func (e *UnsupportedFeatureError) Span() ast.Span      { return e.Node.Span() }
func (e *UnsupportedFeatureError) Related() []ast.Span { return nil }
func (e *UnsupportedFeatureError) Message() string {
	return "Unsupported: " + e.Feature
}

func (e *BodyDeclNotAllowedError) Span() ast.Span      { return e.Decl.Span() }
func (e *BodyDeclNotAllowedError) Related() []ast.Span { return nil }
func (e *BodyDeclNotAllowedError) Message() string {
	return "Declaration not allowed in function body: " + astKind(e.Decl)
}

func (e *MissingInitializerError) Span() ast.Span      { return e.Decl.Span() }
func (e *MissingInitializerError) Related() []ast.Span { return nil }
func (e *MissingInitializerError) Message() string {
	if name, ok := varName(e.Decl); ok {
		return "Variable declaration requires an initializer: " + name
	}
	return "Variable declaration requires an initializer"
}

func (e *DuplicateDeclarationError) Span() ast.Span      { return e.Decl.Span() }
func (e *DuplicateDeclarationError) Related() []ast.Span { return []ast.Span{e.Previous.Span()} }
func (e *DuplicateDeclarationError) Message() string {
	return "Duplicate declaration: " + e.Name
}

func (e *NoMatchingOverloadError) Span() ast.Span { return e.Call.Span() }
func (e *NoMatchingOverloadError) Related() []ast.Span {
	if e.Call.Callee == nil {
		return nil
	}
	return []ast.Span{e.Call.Callee.Span()}
}
func (e *NoMatchingOverloadError) Message() string {
	var sb strings.Builder
	sb.WriteString("No matching overload for this call")
	for _, s := range e.Candidates {
		sb.WriteString("\n  ")
		sb.WriteString(renderScheme(s))
	}
	return sb.String()
}

func (e *UnannotatedRecursiveOverloadError) Span() ast.Span      { return e.Decl.Span() }
func (e *UnannotatedRecursiveOverloadError) Related() []ast.Span { return nil }
func (e *UnannotatedRecursiveOverloadError) Message() string {
	return "Overloaded function in a recursive group must have fully-annotated signatures: " + e.Name
}

func (e *DuplicateOverloadError) Span() ast.Span      { return e.Decl.Span() }
func (e *DuplicateOverloadError) Related() []ast.Span { return []ast.Span{e.Previous.Span()} }
func (e *DuplicateOverloadError) Message() string {
	return "Overload arms must have distinguishable parameter types: " + e.Name
}

func (e *AwaitOutsideAsyncError) Span() ast.Span { return e.Await.Span() }
func (e *AwaitOutsideAsyncError) Related() []ast.Span {
	// Point at the enclosing function (the one to make `async`) when there is one;
	// empty at module top-level.
	if e.EnclosingFn != nil {
		return []ast.Span{e.EnclosingFn.Span()}
	}
	return nil
}
func (e *AwaitOutsideAsyncError) Message() string {
	return "await can only be used inside an async function"
}

func (e *ReturnOutsideFunctionError) Span() ast.Span      { return e.Return.Span() }
func (e *ReturnOutsideFunctionError) Related() []ast.Span { return nil }
func (e *ReturnOutsideFunctionError) Message() string {
	return "return can only be used inside a function"
}

func (e *ForAwaitOutsideAsyncError) Span() ast.Span { return e.Loop.Span() }
func (e *ForAwaitOutsideAsyncError) Related() []ast.Span {
	// Point at the enclosing function to mark `async` when there is one; empty at
	// module top-level, mirroring AwaitOutsideAsyncError.
	if e.EnclosingFn != nil {
		return []ast.Span{e.EnclosingFn.Span()}
	}
	return nil
}
func (e *ForAwaitOutsideAsyncError) Message() string {
	return "for await can only be used inside an async function"
}

func (e *NotIterableError) Span() ast.Span      { return e.Iterable.Span() }
func (e *NotIterableError) Related() []ast.Span { return nil }
func (e *NotIterableError) Message() string {
	if e.Await {
		return describe(e.Type) + " is not an async iterable"
	}
	return describe(e.Type) + " is not iterable"
}

func (e *AsyncReturnNotPromiseError) Span() ast.Span { return e.Return.Span() }
func (e *AsyncReturnNotPromiseError) Related() []ast.Span {
	// Point at the enclosing async function (the signature to fix). Guard a nil Fn
	// to uphold the "never panic on malformed AST" guarantee, even though inferFunc
	// always supplies the function node.
	if e.Fn == nil {
		return nil
	}
	return []ast.Span{e.Fn.Span()}
}
func (e *AsyncReturnNotPromiseError) Message() string {
	return "async function return type must be a Promise; write Promise<...> or Promise<_>"
}

func (e *CannotConstrainError) Message() string {
	return fmt.Sprintf("cannot constrain %s <: %s", describe(e.Sub), describe(e.Super))
}

func (e *FuncArityMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain function of arity %d <: function of arity %d",
		len(e.Sub.Params), len(e.Super.Params))
}

func (e *TupleLengthMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain tuple of length %d <: tuple of length %d",
		len(e.Sub.Elems), len(e.Super.Elems))
}

func (e *SpreadNotTupleError) Span() ast.Span      { return e.Spread.Span() }
func (e *SpreadNotTupleError) Related() []ast.Span { return nil }
func (e *SpreadNotTupleError) Message() string {
	return "cannot spread " + describe(e.Operand) + " into a tuple"
}

func (e *InexactTupleSpreadError) Span() ast.Span      { return e.Spread.Span() }
func (e *InexactTupleSpreadError) Related() []ast.Span { return nil }
func (e *InexactTupleSpreadError) Message() string {
	return "cannot spread an inexact tuple except as the last element"
}

func (e *MissingPropertyError) Message() string {
	return "object is missing property: " + e.Name
}

func (e *InexactIntoExactError) Message() string {
	return "cannot constrain inexact object <: exact object"
}

func (e *InexactTupleIntoExactError) Message() string {
	return "cannot constrain inexact tuple <: exact tuple"
}

func (e *InexactUnionIntoExactError) Message() string {
	return fmt.Sprintf("cannot constrain %s <: %s", describe(e.Sub), describe(e.Super))
}

func (e *ExtraPropertyError) Message() string {
	return "object has extra property: " + e.Name
}

func (e *ExtraElementError) Message() string {
	return "tuple has extra element at index " + strconv.Itoa(e.Index)
}

func (e *OptionalPropertyError) Message() string {
	return "object property is optional but required: " + e.Name
}

func (e *MutabilityMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain immutable %s <: mutable %s",
		describeBorrowInner(e.Sub.Inner), describeBorrowInner(e.Super.Inner))
}

func (e *ClassIntoExactObjectError) Message() string {
	return fmt.Sprintf("cannot constrain class %s <: exact object", describe(e.Sub))
}

func (e *StructuralIntoClassError) Message() string {
	return fmt.Sprintf("cannot constrain object <: class %s", describe(e.Super))
}

func (e *NonClassSuperError) Message() string {
	return fmt.Sprintf("`%s` does not name a class and cannot be extended or implemented.", e.Name)
}

func (e *CannotExtendFinalClassError) Message() string {
	return fmt.Sprintf("Cannot extend `%s`; it is a final class and has no subclasses.", e.Name)
}

func (e *VarianceMismatchError) Message() string {
	return fmt.Sprintf("type parameter `%s` is declared %s but is actually %s",
		e.Name, e.Declared, e.Inferred)
}

// describeBorrowInner renders the pointee of a borrow for a diagnostic. An immutable
// field read routes its result through a fresh field-read variable (fieldReadBorrow),
// so the borrow's inner can be an inference variable whose raw `t{N}` name means
// nothing to a user. When it is, resolve it to a concrete bound so the message names
// the actual shape — `immutable object`, not `immutable t3`. Prefer a lower bound, the
// value that flowed in; fall back to an upper bound, then to the bare variable when it
// carries no concrete bound.
func describeBorrowInner(t soltype.Type) string {
	if v, ok := t.(*soltype.TypeVarType); ok {
		if b, ok := concreteBoundOf(v); ok {
			return describe(b)
		}
	}
	return describe(t)
}

// concreteBoundOf returns a non-variable bound of v, preferring lower bounds, or
// ok=false when every bound is itself a variable or v has none. It never recurses
// through a variable bound, so it cannot loop on a cyclic bound graph.
func concreteBoundOf(v *soltype.TypeVarType) (soltype.Type, bool) {
	for _, b := range v.LowerBounds {
		if _, isVar := b.(*soltype.TypeVarType); !isVar {
			return b, true
		}
	}
	for _, b := range v.UpperBounds {
		if _, isVar := b.(*soltype.TypeVarType); !isVar {
			return b, true
		}
	}
	return nil, false
}

func (e *BorrowEscapeError) Message() string {
	return fmt.Sprintf("borrowed value %s does not live long enough to satisfy %s",
		describe(e.Sub), describe(e.Super))
}

func (e *MutFieldError) Span() ast.Span      { return e.Ann.Span() }
func (e *MutFieldError) Related() []ast.Span { return nil }
func (e *MutFieldError) Message() string {
	return "owned-mutable field annotation is not allowed; the enclosing context decides mutability — wrap the whole annotation in `mut` to make this field writable, or use interior mutability"
}

func (e *ReadonlyFieldError) Span() ast.Span      { return spanOfNode(e.site) }
func (e *ReadonlyFieldError) Related() []ast.Span { return nil }
func (e *ReadonlyFieldError) Message() string {
	return fmt.Sprintf("cannot assign to readonly property: %s", e.Field)
}

func (e *ReadonlyFieldSubtypeError) Span() ast.Span      { return spanOfNode(e.site) }
func (e *ReadonlyFieldSubtypeError) Related() []ast.Span { return nil }
func (e *ReadonlyFieldSubtypeError) Message() string {
	return fmt.Sprintf("readonly field %s cannot satisfy a writable field requirement", e.Field)
}

// describe renders a RAW, uncoalesced type for in-flight error messages (t0,
// function, number). Distinct from soltype.Print, which renders coalesced
// output as user-facing Escalier syntax (see m1-implementation-plan §2.2). It
// lives in solver because it walks bound-carrying variables, and its wording
// matches the spike's verbatim so test assertions stay stable.
func describe(t soltype.Type) string {
	switch t := t.(type) {
	case *soltype.PrimType:
		return primName(t.Prim)
	case *soltype.LitType:
		switch l := t.Lit.(type) {
		case *soltype.StrLit:
			return strconv.Quote(l.Value)
		case *soltype.NumLit:
			// JavaScript numbers are IEEE 754 doubles, and NumLit.Value is a
			// float64, so render at 64-bit precision — bitSize 32 would round
			// through float32 and misrender values beyond float32's range/mantissa
			// (e.g. 0.123456789, 16777217). Note codegen/printer.go still uses
			// bitSize 32 here, which is the same latent bug on the emit path.
			return strconv.FormatFloat(l.Value, 'f', -1, 64)
		case *soltype.BoolLit:
			return strconv.FormatBool(l.Value)
		}
		return "?"
	case *soltype.FuncType:
		return "function"
	case *soltype.TupleType:
		return "tuple"
	case *soltype.ObjectType:
		return "object"
	case *soltype.ClassType:
		// A class instance renders nominally under its display name, `Point` or
		// `Box<number>`, so a diagnostic naming it matches the printer's surface form.
		return soltype.Print(t)
	case *soltype.AliasType:
		// An alias reference renders under its own name, `Point` or `Box<number>`, so a
		// diagnostic naming it matches the printer's surface form rather than the expanded
		// body the constraint actually compares.
		return soltype.Print(t)
	case *soltype.PromiseType:
		// Rendered STRUCTURALLY (Promise<inner>), unlike the nominal function/tuple/
		// object above. That is deliberate and consistent with the Union/Intersection
		// arms below, which also recurse: a Promise's single type argument is compact
		// and informative (`Promise<number>`), whereas a function/tuple/record would
		// be verbose spelled out, so those stay nominal.
		return "Promise<" + describe(t.Inner) + ">"
	case *soltype.RefType:
		// A borrow renders with its `mut` prefix over the nominal inner (`mut object`),
		// recursing like the Promise arm. The lifetime is deliberately NOT rendered: D2
		// attaches lifetimes, so Lt may be non-nil here, but a raw `'l{id}` in a
		// diagnostic is noise. Naming lands in D4; until then an escape message reads
		// `mut object` without naming which borrow escaped.
		prefix := ""
		if t.Mut {
			prefix = "mut "
		}
		return prefix + describe(t.Inner)
	case *soltype.Void:
		return "void"
	case *soltype.NullType:
		return "null"
	case *soltype.UndefinedType:
		return "undefined"
	case *soltype.NeverType:
		return "never"
	case *soltype.UnknownType:
		return "unknown"
	case *soltype.ErrorType:
		return "error"
	case *soltype.UnionType:
		s := joinDescribe(t.Types, " | ")
		if t.Inexact {
			// An inexact union has an open tail. Append the marker so a
			// diagnostic naming the union matches the printer's surface form.
			return s + " | ..."
		}
		return s
	case *soltype.IntersectionType:
		return joinDescribe(t.Types, " & ")
	case *soltype.TypeVarType:
		return "t" + strconv.Itoa(t.ID)
	}
	return "?"
}

func joinDescribe(types []soltype.Type, sep string) string {
	parts := make([]string, len(types))
	for i, t := range types {
		parts[i] = describe(t)
	}
	return strings.Join(parts, sep)
}

// primName maps a Prim to the surface name used in raw error messages.
func primName(p soltype.Prim) string {
	switch p {
	case soltype.NumPrim:
		return "number"
	case soltype.StrPrim:
		return "string"
	case soltype.BoolPrim:
		return "boolean"
	}
	return "?"
}

// primOf maps a Lit concrete to the Prim it is a literal of: *NumLit -> NumPrim,
// *StrLit -> StrPrim, *BoolLit -> BoolPrim. Used by the LitType <: PrimType arm
// of constrain (a literal is a subtype of its primitive).
func primOf(lit soltype.Lit) soltype.Prim {
	switch lit.(type) {
	case *soltype.NumLit:
		return soltype.NumPrim
	case *soltype.StrLit:
		return soltype.StrPrim
	case *soltype.BoolLit:
		return soltype.BoolPrim
	}
	panic(fmt.Sprintf("primOf: unhandled Lit %T", lit))
}
