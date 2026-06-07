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

// CannotConstrainError fires when a non-variable LHS/RHS pair fails to match:
// prim/prim mismatch, lit/lit mismatch, lit/prim mismatch, and the generic
// "no rule applies" fall-through at the end of constrain.
//
// LHS is the "actual" value, RHS the "expected"; blame follows LHS to its minting
// node (when that node lies inside the constraint site — the containment guard
// that keeps identifier-flow blame on the use, not the definition, §3.8) and falls
// back to site otherwise. This is the only constraint kind that keeps a site
// fallback: its actual-value operand can legitimately resolve outside the
// constraint (an ident's definition) or not at all (a Void result).
type CannotConstrainError struct {
	LHS, RHS soltype.Type
	prov     NodeResolver // M2.5: type→node index; assigned after Constrain returns (§3.5)
	site     ast.Node     // M2.5: the constraint node n — the use, the fallback when LHS has no entry
}

// FuncArityMismatchError fires on FuncType <: FuncType when the two arities
// differ (M1's exact-function rule; exact-by-default requires the same number
// of params). Holds the full FuncTypes, not just the arities, so consumers can
// report param/return types too. M3 narrows the firing conditions when the
// exactness flag adds the inexact fewer-params-is-subtype arm.
//
// The subject is the RHS call-shape (a fresh FuncType{args, res} minted per call,
// recorded against the CallExpr), so Span() resolves precisely to the call;
// Related() follows the LHS callee to a "defined here" span. site is the
// constraint node, used as a coarse fallback when neither operand resolves (e.g.
// once M3 produces higher-order FuncArity errors whose RHS isn't a recorded
// call-shape) so blame never degrades to the zero span.
type FuncArityMismatchError struct {
	LHS, RHS *soltype.FuncType
	prov     NodeResolver // M2.5: type→node index (§3.5)
	site     ast.Node     // M2.5: constraint node fallback when no operand resolves
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when the
// inexact flag is added).
//
// The subject is the LHS tuple literal (recorded by inferTuple); Related() points
// at the RHS expected-source. Not actually reachable in M2.5 — a tuple <: tuple
// constraint needs a tuple sink (annotation/param) resolveTypeAnn does not yet
// produce — so its blame is wired but exercised from M4. site is the constraint
// node, the coarse fallback when neither tuple resolves (e.g. an M4 tuple
// annotation whose elements aren't recorded) so blame never degrades to the zero
// span.
type TupleLengthMismatchError struct {
	LHS, RHS *soltype.TupleType
	prov     NodeResolver // M2.5: type→node index (§3.5)
	site     ast.Node     // M2.5: constraint node fallback when no operand resolves
}

// MissingPropertyError fires on RecordType <: RecordType when the RHS requires a
// field the LHS lacks — the record analogue of FuncArityMismatchError /
// TupleLengthMismatchError. It is the failure behind a field read on a record
// without that field (recv.foo where recv has no foo): the walk constrains
// recv <: {foo: fresh}, and the absent field surfaces here. Holds both records
// plus the missing name so consumers can inspect what was required.
//
// The subject is the field's inner result var (RHS.Field(Name)), minted by
// inferMember and recorded against the .prop identifier — so Span() blames the
// member's prop (.foo), not the receiver. Name stays: the absent field name is
// not recoverable from a single node (the RHS may require several fields). site
// is the constraint node, the coarse fallback when the field var has no entry —
// reachable once M4 builds concrete record <: record requirements whose field
// types are coalesced/annotation-minted (and therefore not recorded by
// inferMember); until then it never fires, but it keeps blame off the zero span.
type MissingPropertyError struct {
	LHS, RHS *soltype.RecordType
	Name     string
	prov     NodeResolver // M2.5: type→node index (§3.5)
	site     ast.Node     // M2.5: constraint node fallback when the field var has no entry
}

func (*CannotConstrainError) isSolverError()     {}
func (*FuncArityMismatchError) isSolverError()   {}
func (*TupleLengthMismatchError) isSolverError() {}
func (*MissingPropertyError) isSolverError()     {}

// --- Per-operand blame (§3.5): each constraint kind follows its operands through
// Prov on demand, falling back to its own site (where it keeps one) ---

func (e *CannotConstrainError) Span() ast.Span      { return spanOf(e.prov, e.LHS, e.site) }
func (e *CannotConstrainError) Related() []ast.Span { return relatedOf(e.prov, e.RHS) }

func (e *FuncArityMismatchError) Span() ast.Span {
	// RHS call-shape → the CallExpr; degrade → the callee function; else the site.
	return spanOfFirst(e.prov, e.site, e.RHS, e.LHS)
}
func (e *FuncArityMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.LHS) } // the fn

func (e *TupleLengthMismatchError) Span() ast.Span {
	// LHS tuple literal → degrade to the other tuple → else the site.
	return spanOfFirst(e.prov, e.site, e.LHS, e.RHS)
}
func (e *TupleLengthMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.RHS) }

func (e *MissingPropertyError) Span() ast.Span {
	// The field's inner var → the .foo prop ident; degrade to the receiver; else
	// the site. The field-var arm is the only one reachable in M2.5 (member
	// access always records it); the receiver/site arms cover the M4 concrete
	// record <: record case where the field type may be unrecorded.
	ops := make([]soltype.Type, 0, 2)
	if f, ok := e.RHS.Field(e.Name); ok {
		ops = append(ops, f)
	}
	ops = append(ops, e.LHS)
	return spanOfFirst(e.prov, e.site, ops...)
}
func (e *MissingPropertyError) Related() []ast.Span { return relatedOf(e.prov, e.LHS) } // the receiver

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

// NamespaceUsedAsValueError fires when an identifier resolves to a namespace
// rather than a value. Namespaces are a separate binding sort and never flow as
// values; in M2 a namespace name in value position can only fail (qualified
// member access — Foo.bar — is M4).
type NamespaceUsedAsValueError struct {
	Ident *ast.IdentExpr
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

// OverloadNotSupportedError fires when a name has more than one top-level
// FuncDecl. Function overloading needs the overload-intersection representation
// that lands in M3; M2 keeps the first declaration (so the binding stays
// callable with that signature) and reports each extra arm, rather than merging
// the arms into the same var — which yields an uncallable union-of-functions
// binding whose every call fails with an opaque `function | function` mismatch.
//
// Decl/Previous/Name mirror DuplicateDeclarationError.
type OverloadNotSupportedError struct {
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

func (*UnknownIdentifierError) isSolverError()    {}
func (*NamespaceUsedAsValueError) isSolverError() {}
func (*TooManyArgsError) isSolverError()          {}
func (*NotEnoughArgsError) isSolverError()        {}
func (*UnsupportedNodeError) isSolverError()      {}
func (*UnsupportedFeatureError) isSolverError()   {}
func (*BodyDeclNotAllowedError) isSolverError()   {}
func (*MissingInitializerError) isSolverError()   {}
func (*DuplicateDeclarationError) isSolverError() {}
func (*OverloadNotSupportedError) isSolverError() {}
func (*AwaitOutsideAsyncError) isSolverError()      {}
func (*ReturnOutsideFunctionError) isSolverError()  {}
func (*AsyncReturnNotPromiseError) isSolverError()  {}

func (e *UnknownIdentifierError) Span() ast.Span      { return e.Ident.Span() }
func (e *UnknownIdentifierError) Related() []ast.Span { return nil }
func (e *UnknownIdentifierError) Message() string {
	return "Unknown identifier: " + e.Ident.Name
}

func (e *NamespaceUsedAsValueError) Span() ast.Span      { return e.Ident.Span() }
func (e *NamespaceUsedAsValueError) Related() []ast.Span { return nil }
func (e *NamespaceUsedAsValueError) Message() string {
	return "Namespace used as a value: " + e.Ident.Name
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
	return "Unsupported in M2: " + astKind(e.Node)
}

func (e *UnsupportedFeatureError) Span() ast.Span      { return e.Node.Span() }
func (e *UnsupportedFeatureError) Related() []ast.Span { return nil }
func (e *UnsupportedFeatureError) Message() string {
	return "Unsupported in M2: " + e.Feature
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

func (e *OverloadNotSupportedError) Span() ast.Span      { return e.Decl.Span() }
func (e *OverloadNotSupportedError) Related() []ast.Span { return []ast.Span{e.Previous.Span()} }
func (e *OverloadNotSupportedError) Message() string {
	return "Function overloads are not supported in M2: " + e.Name
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
	return fmt.Sprintf("cannot constrain %s <: %s", describe(e.LHS), describe(e.RHS))
}

func (e *FuncArityMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain function of arity %d <: function of arity %d",
		len(e.LHS.Params), len(e.RHS.Params))
}

func (e *TupleLengthMismatchError) Message() string {
	return fmt.Sprintf("cannot constrain tuple of length %d <: tuple of length %d",
		len(e.LHS.Elems), len(e.RHS.Elems))
}

func (e *MissingPropertyError) Message() string {
	return "object is missing property: " + e.Name
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
	case *soltype.RecordType:
		return "object"
	case *soltype.PromiseType:
		// Rendered STRUCTURALLY (Promise<inner>), unlike the nominal function/tuple/
		// object above. That is deliberate and consistent with the Union/Intersection
		// arms below, which also recurse: a Promise's single type argument is compact
		// and informative (`Promise<number>`), whereas a function/tuple/record would
		// be verbose spelled out, so those stay nominal.
		return "Promise<" + describe(t.Inner) + ">"
	case *soltype.Void:
		return "void"
	case *soltype.NeverType:
		return "never"
	case *soltype.UnknownType:
		return "unknown"
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
