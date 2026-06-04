package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// checker is the per-inference-run carrier for the M2 constraint-generating
// walk. It wraps M1's *Context (the fresh-var counter + Constrain), threads the
// Info side table that records node→type, and accumulates SolverErrors. It is
// the method receiver for the whole walk.
//
// The walk is a direct recursive switch over ast.Expr/Stmt/Decl, NOT the shared
// ast.Visitor. This is a deliberate deviation from the CLAUDE.md "use the
// existing visitor" convention: constraint generation is bottom-up and
// value-producing (every node synthesizes a soltype.Type), which the visitor —
// shaped for enter/exit transformation with no per-node synthesized value — does
// not model cleanly. The shape matches both the simplesub spike's typeTerm and
// the old checker's inferExpr. See m2-implementation-plan §3.2.
type checker struct {
	ctx  *Context      // M1: freshVar(level), Constrain(lhs, rhs) []SolverError
	info *Info         // M1: node → soltype.Type side table (unexported setType)
	errs []SolverError // accumulated; mirrors the spike's []error threading
}

// newChecker returns a checker with a fresh Context and an empty Info table.
func newChecker() *checker {
	return &checker{ctx: &Context{}, info: NewInfo()}
}

// freshAt allocates a fresh inference variable at the given level. No provenance
// recording in M2 — the Prov side table is deferred to M3+.
func (c *checker) freshAt(lvl int) *soltype.TypeVarType {
	return c.ctx.freshVar(lvl)
}

// constrain asserts lhs <: rhs and stamps the offending node's span onto any
// resulting SolverErrors before accumulating them. M2 does not look provenance
// up from a side table — the span is taken directly from the AST node being
// walked (§3.5).
func (c *checker) constrain(n ast.Node, lhs, rhs soltype.Type) {
	for _, e := range c.ctx.Constrain(lhs, rhs) {
		e.setSpan(n.Span())
		c.errs = append(c.errs, e)
	}
}

// report accumulates a structured error and returns a placeholder type so a
// caller can `return c.report(...)` in value position.
func (c *checker) report(e SolverError) soltype.Type {
	c.errs = append(c.errs, e)
	return &soltype.NeverType{}
}

// recordType records t as the inferred type of n in the Info side table. Wraps
// the unexported setType — which is why the whole M2 walk lives in package
// solver. The AST stays untouched (no InferredType() writes); Info is the single
// source of truth for node→type.
func (c *checker) recordType(n ast.Node, t soltype.Type) {
	c.info.setType(n, t)
}

// inferExpr dispatches on the concrete expression kind. PR-1 wires only the two
// leaf cases (literals, identifiers); every other kind falls through to a clean
// UnsupportedNodeError (never a panic). Later PRs replace fall-through arms with
// real cases: fn/call/block (PR-3), objects/members/tuples (PR-4).
func (c *checker) inferExpr(scope *Scope, lvl int, e ast.Expr) soltype.Type {
	switch e := e.(type) {
	case *ast.LiteralExpr:
		return c.inferLiteral(e)
	case *ast.IdentExpr:
		return c.inferIdent(scope, lvl, e)
	default:
		return c.report(&UnsupportedNodeError{
			errSpan: errSpan{span: e.Span()},
			Kind:    astKind(e),
		})
	}
}
