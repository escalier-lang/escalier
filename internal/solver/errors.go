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
	prov     Provenance // M2.5: type→node index; assigned after Constrain returns (§3.5)
	site     ast.Node   // M2.5: the constraint node n — the use, the fallback when LHS has no entry
}

// FuncArityMismatchError fires on FuncType <: FuncType when the two arities
// differ (M1's exact-function rule; exact-by-default requires the same number
// of params). Holds the full FuncTypes, not just the arities, so consumers can
// report param/return types too. M3 narrows the firing conditions when the
// exactness flag adds the inexact fewer-params-is-subtype arm.
//
// The subject is the RHS call-shape (a fresh FuncType{args, res} minted per call,
// recorded against the CallExpr), so Span() resolves precisely to the call;
// Related() follows the LHS callee to a "defined here" span.
type FuncArityMismatchError struct {
	LHS, RHS *soltype.FuncType
	prov     Provenance // M2.5: type→node index (§3.5)
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when the
// inexact flag is added).
//
// The subject is the LHS tuple literal (recorded by inferTuple); Related() points
// at the RHS expected-source. Not actually reachable in M2.5 — a tuple <: tuple
// constraint needs a tuple sink (annotation/param) resolveTypeAnn does not yet
// produce — so its blame is wired but exercised from M4.
type TupleLengthMismatchError struct {
	LHS, RHS *soltype.TupleType
	prov     Provenance // M2.5: type→node index (§3.5)
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
// not recoverable from a single node (the RHS may require several fields).
type MissingPropertyError struct {
	LHS, RHS *soltype.RecordType
	Name     string
	prov     Provenance // M2.5: type→node index (§3.5)
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
	if e.prov != nil {
		if n, ok := e.prov.NodeFor(e.RHS); ok { // the call-shape → the CallExpr
			return n.Span()
		}
		if n, ok := e.prov.NodeFor(e.LHS); ok { // degrade → the callee function
			return n.Span()
		}
	}
	return ast.Span{}
}
func (e *FuncArityMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.LHS) } // the fn

func (e *TupleLengthMismatchError) Span() ast.Span {
	if e.prov != nil {
		if n, ok := e.prov.NodeFor(e.LHS); ok { // the tuple literal
			return n.Span()
		}
		if n, ok := e.prov.NodeFor(e.RHS); ok { // degrade → the other tuple
			return n.Span()
		}
	}
	return ast.Span{}
}
func (e *TupleLengthMismatchError) Related() []ast.Span { return relatedOf(e.prov, e.RHS) }

func (e *MissingPropertyError) Span() ast.Span {
	if e.prov != nil {
		if f, ok := e.RHS.Field(e.Name); ok { // the field's inner var → the .foo prop ident
			if n, ok := e.prov.NodeFor(f); ok {
				return n.Span()
			}
		}
		if n, ok := e.prov.NodeFor(e.LHS); ok { // unreachable in practice → the receiver
			return n.Span()
		}
	}
	return ast.Span{}
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
func spanOf(p Provenance, op soltype.Type, site ast.Node) ast.Span {
	if p != nil && site != nil {
		if n, ok := p.NodeFor(op); ok && within(site.Span(), n.Span()) {
			return n.Span()
		}
	}
	if site != nil {
		return site.Span()
	}
	return ast.Span{}
}

// within reports whether inner sits inside outer (same source, both of inner's
// endpoints within outer). ast.Span.Contains takes a Location, so a span is
// contained when its Start and End both are.
func within(outer, inner ast.Span) bool {
	return outer.SourceID == inner.SourceID &&
		outer.Contains(inner.Start) && outer.Contains(inner.End)
}

// relatedOf resolves each operand that has an entry to a related span and drops
// the ones that don't (no fallback — a missing related node is simply omitted),
// deduped by span (§3.6).
func relatedOf(p Provenance, ops ...soltype.Type) []ast.Span {
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

func (*UnknownIdentifierError) isSolverError()    {}
func (*NamespaceUsedAsValueError) isSolverError() {}
func (*UnsupportedNodeError) isSolverError()      {}
func (*UnsupportedFeatureError) isSolverError()   {}
func (*BodyDeclNotAllowedError) isSolverError()   {}
func (*MissingInitializerError) isSolverError()   {}
func (*DuplicateDeclarationError) isSolverError() {}
func (*OverloadNotSupportedError) isSolverError() {}

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
