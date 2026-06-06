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
	prov Prov          // M2.5: soltype.Type → Origin (leaf FromAST only), the inverse of info
	errs []SolverError // accumulated; mirrors the spike's []error threading
}

// newChecker returns a checker with a fresh Context, an empty Info table, and an
// empty Prov side table.
func newChecker() *checker {
	return &checker{ctx: &Context{}, info: NewInfo(), prov: Prov{}}
}

// freshAt allocates a fresh inference variable at the given level. Provenance for
// a fresh var is recorded by its construction site (recordProv), not here.
func (c *checker) freshAt(lvl int) *soltype.TypeVarType {
	return c.ctx.freshVar(lvl)
}

// constrain asserts lhs <: rhs and, for each resulting constraint error, hands it
// the provenance table (and, for CannotConstrainError, the constraint node n as a
// blame fallback) so its own Span()/Related() can resolve per-operand blame
// through Prov on demand (§3.5). The engine itself never touches Prov — the fields
// are assigned here, after Constrain returns, so the hot loop stays off the table
// (the perf invariant, §3.9). Bridge errors never flow through here; they
// self-blame from their own node.
func (c *checker) constrain(n ast.Node, lhs, rhs soltype.Type) {
	for _, e := range c.ctx.Constrain(lhs, rhs) {
		switch err := e.(type) {
		case *CannotConstrainError:
			err.prov, err.site = c.prov, n // the only kind that still needs a site fallback
		case *TupleLengthMismatchError:
			err.prov = c.prov
		case *MissingPropertyError:
			err.prov = c.prov
		case *FuncArityMismatchError:
			err.prov = c.prov
		}
		c.errs = append(c.errs, e)
	}
}

// report accumulates a structured error and returns a placeholder type so a
// caller can `return c.report(...)` in value position.
func (c *checker) report(e SolverError) soltype.Type {
	c.errs = append(c.errs, e)
	return &soltype.NeverType{}
}

// reportUnsupported records an UnsupportedNodeError for an AST node whose kind is
// outside the M2 subset. The node self-blames: both the span and the rendered kind
// (astKind) come from it. When the unsupported thing is a child carried by its
// parent — an object property's computed key, a function's destructuring pattern —
// pass that child node directly (it embeds ast.Node and carries its own, narrower
// span). Returns the never placeholder so a caller can `return c.reportUnsupported(n)`
// in value position.
func (c *checker) reportUnsupported(node ast.Node) soltype.Type {
	return c.report(&UnsupportedNodeError{Node: node})
}

// reportUnsupportedFeature records an UnsupportedFeatureError: the node's KIND is
// supported but a feature of it is not (optional chaining on a MemberExpr, type
// params on a FuncExpr). The node carries the blame span; feature names what is
// unsupported, since astKind would name the supported parent.
func (c *checker) reportUnsupportedFeature(node ast.Node, feature string) soltype.Type {
	return c.report(&UnsupportedFeatureError{Node: node, Feature: feature})
}

// recordType records t as the inferred type of n in the Info side table. Wraps
// the unexported setType — which is why the whole M2 walk lives in package
// solver. The AST stays untouched (no InferredType() writes); Info is the single
// source of truth for node→type.
func (c *checker) recordType(n ast.Node, t soltype.Type) {
	c.info.setType(n, t)
}

// inferExpr dispatches on the concrete expression kind. PR-1 wired the two leaf
// cases (literals, identifiers); PR-3 adds the function/application walk
// (FuncExpr, CallExpr — the block/statement walk they drive lives in
// infer_stmt.go); PR-4 adds tuples, object literals, and member access. Every
// remaining kind falls through to a clean UnsupportedNodeError (never a panic).
func (c *checker) inferExpr(scope *Scope, lvl int, e ast.Expr) soltype.Type {
	switch e := e.(type) {
	case *ast.LiteralExpr:
		return c.inferLiteral(e)
	case *ast.IdentExpr:
		return c.inferIdent(scope, lvl, e)
	case *ast.FuncExpr:
		return c.inferFuncExpr(scope, lvl, e)
	case *ast.CallExpr:
		return c.inferCall(scope, lvl, e)
	case *ast.TupleExpr:
		return c.inferTuple(scope, lvl, e)
	case *ast.ObjectExpr:
		return c.inferObject(scope, lvl, e)
	case *ast.MemberExpr:
		return c.inferMember(scope, lvl, e)
	default:
		return c.reportUnsupported(e)
	}
}
