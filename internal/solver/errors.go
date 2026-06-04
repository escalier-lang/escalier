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
// M2 adds Span() ast.Span. The constraint kinds (CannotConstrainError, …) are
// still constructed span-free inside the engine (constrain.go has no AST node
// in hand); the M2 walk stamps the offending node's span onto each returned
// error via the unexported setSpan at the constrain call site (see
// (*checker).constrain). The M2 bridge kinds below carry their span from
// construction.
type SolverError interface {
	isSolverError()
	Message() string
	Span() ast.Span
	setSpan(ast.Span)
}

// errSpan is the embeddable span carrier shared by every SolverError kind. It
// supplies Span()/setSpan so a span-free engine error can be stamped after the
// fact and a bridge error can be built with its span in place.
type errSpan struct{ span ast.Span }

func (e *errSpan) Span() ast.Span     { return e.span }
func (e *errSpan) setSpan(s ast.Span) { e.span = s }

// CannotConstrainError fires when a non-variable LHS/RHS pair fails to match:
// prim/prim mismatch, lit/lit mismatch, lit/prim mismatch, and the generic
// "no rule applies" fall-through at the end of constrain.
type CannotConstrainError struct {
	errSpan
	LHS, RHS soltype.Type
}

// FuncArityMismatchError fires on FuncType <: FuncType when the two arities
// differ (M1's exact-function rule; exact-by-default requires the same number
// of params). Holds the full FuncTypes, not just the arities, so consumers can
// report param/return types too. M3 narrows the firing conditions when the
// exactness flag adds the inexact fewer-params-is-subtype arm.
type FuncArityMismatchError struct {
	errSpan
	LHS, RHS *soltype.FuncType
}

// TupleLengthMismatchError fires on TupleType <: TupleType with different
// lengths (M1's exact-tuple case; M4 may narrow the firing conditions when the
// inexact flag is added).
type TupleLengthMismatchError struct {
	errSpan
	LHS, RHS *soltype.TupleType
}

// MissingPropertyError fires on RecordType <: RecordType when the RHS requires a
// field the LHS lacks — the record analogue of FuncArityMismatchError /
// TupleLengthMismatchError. It is the failure behind a field read on a record
// without that field (recv.foo where recv has no foo): the walk constrains
// recv <: {foo: fresh}, and the absent field surfaces here. Holds both records
// plus the missing name so consumers can inspect what was required.
type MissingPropertyError struct {
	errSpan
	LHS, RHS *soltype.RecordType
	Name     string
}

func (*CannotConstrainError) isSolverError()     {}
func (*FuncArityMismatchError) isSolverError()   {}
func (*TupleLengthMismatchError) isSolverError() {}
func (*MissingPropertyError) isSolverError()     {}

// --- M2 bridge errors (carry their span from construction) ---

// UnknownIdentifierError fires when a value-position identifier resolves to no
// binding in the scope chain.
type UnknownIdentifierError struct {
	errSpan
	Name string
}

// NamespaceUsedAsValueError fires when an identifier resolves to a namespace
// rather than a value. Namespaces are a separate binding sort and never flow as
// values; in M2 a namespace name in value position can only fail (qualified
// member access — Foo.bar — is M4).
type NamespaceUsedAsValueError struct {
	errSpan
	Name string
}

// UnsupportedNodeError is the M2-subset guard: an AST node outside the M2 walk's
// coverage. Unlike BodyDeclNotAllowedError this is a temporary scope gate, not a
// permanent language rule — later milestones widen coverage and remove arms.
type UnsupportedNodeError struct {
	errSpan
	Kind string
}

// BodyDeclNotAllowedError fires when a statement-level declaration in a function
// body is anything other than a VarDecl. This is a permanent language rule
// (§3.2), not the temporary subset gate above: body decls are VarDecl-only.
type BodyDeclNotAllowedError struct {
	errSpan
	Kind string
}

// MissingInitializerError fires when a `val`/`var` declaration has no
// initializer. M2 has no way to bind such a name yet — annotation-only binding
// needs TypeAnn→soltype resolution that lands in a later PR — so this is its own
// kind rather than a generic UnsupportedNodeError: it marks an annotation-driven
// binding the walk can't infer, not an AST node shape outside the subset.
type MissingInitializerError struct {
	errSpan
	Name string // the bound name when the pattern is an IdentPat; "" otherwise
}

// DuplicateDeclarationError fires when a top-level `val`/`var` rebinds a name
// already declared in the module scope. Unlike a function (whose repeated
// top-level declarations are overloads, supported from PR-3), a variable may be
// declared only once per scope; the first binding is kept.
type DuplicateDeclarationError struct {
	errSpan
	Name string
}

func (*UnknownIdentifierError) isSolverError()    {}
func (*NamespaceUsedAsValueError) isSolverError() {}
func (*UnsupportedNodeError) isSolverError()      {}
func (*BodyDeclNotAllowedError) isSolverError()   {}
func (*MissingInitializerError) isSolverError()   {}
func (*DuplicateDeclarationError) isSolverError() {}

func (e *UnknownIdentifierError) Message() string {
	return "Unknown identifier: " + e.Name
}

func (e *MissingInitializerError) Message() string {
	if e.Name != "" {
		return "Variable declaration requires an initializer: " + e.Name
	}
	return "Variable declaration requires an initializer"
}

func (e *DuplicateDeclarationError) Message() string {
	return "Duplicate declaration: " + e.Name
}

func (e *NamespaceUsedAsValueError) Message() string {
	return "Namespace used as a value: " + e.Name
}

func (e *UnsupportedNodeError) Message() string {
	return "Unsupported in M2: " + e.Kind
}

func (e *BodyDeclNotAllowedError) Message() string {
	return "Declaration not allowed in function body: " + e.Kind
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
