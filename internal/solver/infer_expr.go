package solver

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferLiteral types a literal expression as its singleton soltype.LitType and
// records it in Info. M1's soltype.Lit set is num/str/bool only; the remaining
// ast literal kinds (regex, bigint, null, undefined) fall through to the M2
// subset guard until later milestones extend soltype.Lit (§ soltype/type.go
// Prim/Lit note).
func (c *checker) inferLiteral(e *ast.LiteralExpr) soltype.Type {
	var lit soltype.Lit
	switch l := e.Lit.(type) {
	case *ast.NumLit:
		lit = &soltype.NumLit{Value: l.Value}
	case *ast.StrLit:
		lit = &soltype.StrLit{Value: l.Value}
	case *ast.BoolLit:
		lit = &soltype.BoolLit{Value: l.Value}
	default:
		return c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: e.Span()},
			Kind:    litKind(e.Lit),
		})
	}
	t := &soltype.LitType{Lit: lit}
	c.recordType(e, t)
	return t
}

// inferIdent resolves a value-position identifier through the scope chain — the
// production form of the spike's *Var case crossed with design-notes §"The
// constraint-generating AST walk". In M2 (monomorphic, no schemes) it returns
// the binding's type directly; M3 slots instantiate() in once schemes exist.
//
// A namespace name in value position can only fail in M2: there is no legal
// namespace-member position yet (MemberExpr is value-only and there is no
// IndexExpr), so raising NamespaceUsedAsValueError on any namespace name is
// correct here. M4 moves that error to the value-position consumer once
// qualified Foo.bar access lands.
func (c *checker) inferIdent(scope *Scope, lvl int, e *ast.IdentExpr) soltype.Type {
	if b, ok := scope.GetValue(e.Name); ok {
		c.recordType(e, b.Type)
		return b.Type
	}
	if _, ok := scope.GetNamespace(e.Name); ok {
		return c.report(&NamespaceUsedAsValueError{
			errSpan: errSpan{span: e.Span()},
			Name:    e.Name,
		})
	}
	return c.report(&UnknownIdentifierError{
		errSpan: errSpan{span: e.Span()},
		Name:    e.Name,
	})
}

// exprKind returns a short surface name for an expression node, used in the M2
// subset-guard error message. It strips the leading "*ast." from the Go type
// name so e.g. *ast.BinaryExpr renders as "BinaryExpr".
func exprKind(e ast.Expr) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", e), "*ast.")
}

// litKind returns a short surface name for a literal node, used when a literal
// kind is outside M1's soltype.Lit set.
func litKind(l ast.Lit) string {
	return strings.TrimPrefix(fmt.Sprintf("%T", l), "*ast.")
}
