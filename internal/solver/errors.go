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
// slot — is fine, so only this direction errors.
type OptionalPropertyError struct {
	Sub, Super *soltype.ObjectType
	Name       string
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// MutabilityMismatchError fires on RefType <: RefType when the sub is an immutable
// borrow but the super is mutable: writing through the mutable target would mutate a
// value the source only lent out as read-only, so an immutable reference cannot fill
// a mutable slot. The reverse — a mutable source decaying to an immutable target — is
// fine, so only this direction errors. It also fires for `{x} <: mut {x}`, where the
// bare source is wrapped as an immutable view before re-dispatch.
type MutabilityMismatchError struct {
	Sub, Super *soltype.RefType
	prov       NodeResolver // M2.5: type→node index (§3.5)
	site       ast.Node     // M2.5: constraint node fallback
}

// BorrowEscapeError fires when a borrow outlives the slot it flows into: a borrowed
// value (Lt != nil) constrained against an owned slot (Lt == nil), either a bare
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
// operand that infers to an INEXACT tuple ([number, ...]). An inexact tuple has
// unknown length, so an element written after the spread lands at an unknown
// position and the result tuple's shape cannot be pinned. M4 splices exact tuples
// only. The variadic-tail forms such as [number, ...Array<number>] defer to M7/M9.
// Spread is the offending spread element and carries the blame span. Operand is
// the inexact tuple it inferred to.
type InexactTupleSpreadError struct {
	Spread  *ast.ArraySpreadExpr
	Operand *soltype.TupleType
}

func (*CannotConstrainError) isSolverError()     {}
func (*FuncArityMismatchError) isSolverError()   {}
func (*TupleLengthMismatchError) isSolverError() {}
func (*SpreadNotTupleError) isSolverError()      {}
func (*InexactTupleSpreadError) isSolverError()  {}
func (*MissingPropertyError) isSolverError()     {}
func (*InexactIntoExactError) isSolverError()    {}
func (*ExtraPropertyError) isSolverError()       {}
func (*ExtraElementError) isSolverError()        {}
func (*OptionalPropertyError) isSolverError()    {}
func (*MutabilityMismatchError) isSolverError()  {}
func (*BorrowEscapeError) isSolverError()        {}

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

func (e *InexactIntoExactError) Span() ast.Span {
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *InexactIntoExactError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

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
	// The escaping borrow is the subject; blame it, degrade to the slot it escaped
	// into, else the site.
	return spanOfFirst(e.prov, e.site, e.Sub, e.Super)
}
func (e *BorrowEscapeError) Related() []ast.Span { return relatedOf(e.prov, e.Super) }

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

func (*UnknownIdentifierError) isSolverError()            {}
func (*NamespaceUsedAsValueError) isSolverError()         {}
func (*UnknownNamespaceMemberError) isSolverError()       {}
func (*DynamicNamespaceIndexError) isSolverError()        {}
func (*InvalidAssignmentTargetError) isSolverError()      {}
func (*CannotAssignToImmutableError) isSolverError()      {}
func (*TooManyArgsError) isSolverError()                  {}
func (*NotEnoughArgsError) isSolverError()                {}
func (*UnsupportedNodeError) isSolverError()              {}
func (*UnsupportedFeatureError) isSolverError()           {}
func (*BodyDeclNotAllowedError) isSolverError()           {}
func (*MissingInitializerError) isSolverError()           {}
func (*DuplicateDeclarationError) isSolverError()         {}
func (*NoMatchingOverloadError) isSolverError()           {}
func (*UnannotatedRecursiveOverloadError) isSolverError() {}
func (*DuplicateOverloadError) isSolverError()            {}
func (*AwaitOutsideAsyncError) isSolverError()            {}
func (*ReturnOutsideFunctionError) isSolverError()        {}
func (*AsyncReturnNotPromiseError) isSolverError()        {}
func (*NonExhaustiveMatchError) isSolverError()           {}

func (e *NonExhaustiveMatchError) Span() ast.Span      { return e.Match.Span() }
func (e *NonExhaustiveMatchError) Related() []ast.Span { return nil }
func (e *NonExhaustiveMatchError) Message() string {
	return "match is not exhaustive; add a catch-all branch"
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
	return "cannot spread an inexact tuple into a tuple"
}

func (e *MissingPropertyError) Message() string {
	return "object is missing property: " + e.Name
}

func (e *InexactIntoExactError) Message() string {
	return "cannot constrain inexact object <: exact object"
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
		describe(e.Sub.Inner), describe(e.Super.Inner))
}

func (e *BorrowEscapeError) Message() string {
	return fmt.Sprintf("borrowed value %s does not live long enough to satisfy %s",
		describe(e.Sub), describe(e.Super))
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
	case *soltype.NeverType:
		return "never"
	case *soltype.UnknownType:
		return "unknown"
	case *soltype.ErrorType:
		return "error"
	case *soltype.UnionType:
		return joinDescribe(t.Types, " | ")
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
