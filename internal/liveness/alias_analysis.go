package liveness

import "github.com/escalier-lang/escalier/internal/ast"

// AliasSourceKind describes the kind of alias source an expression represents.
type AliasSourceKind int

const (
	AliasSourceFresh    AliasSourceKind = iota // new value, no alias
	AliasSourceVariable                        // aliases a specific variable
	AliasSourceMultiple                        // aliases one of several variables (conditional)
	AliasSourceUnknown                         // cannot determine statically
)

// AliasSource describes where a value comes from for alias tracking purposes.
type AliasSource struct {
	Kind   AliasSourceKind
	VarIDs []VarID // empty for Fresh/Unknown, one for Variable, multiple for Multiple
}

// DetermineAliasSource examines an expression and returns its alias source.
// When the expression is an IdentExpr, the VarID is read directly from the
// node (set by the rename pass in Phase 2).
func DetermineAliasSource(expr ast.Expr) AliasSource {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID > 0 {
			return AliasSource{Kind: AliasSourceVariable, VarIDs: []VarID{VarID(e.VarID)}}
		}
		// Non-local variable (VarID <= 0) — treat as unknown since we
		// can't track aliases across function boundaries.
		return AliasSource{Kind: AliasSourceUnknown}

	// Fresh values: literals, object/array construction, function expressions
	case *ast.LiteralExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.ObjectExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.TupleExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.FuncExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.TemplateLitExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.TaggedTemplateLitExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.JSXElementExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.JSXFragmentExpr:
		return AliasSource{Kind: AliasSourceFresh}

	// Function calls: treat as fresh for now (Phase 8 adds lifetime-based tracking)
	case *ast.CallExpr:
		return AliasSource{Kind: AliasSourceFresh}

	// Unary/binary operations produce fresh primitive values
	case *ast.UnaryExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.BinaryExpr:
		return AliasSource{Kind: AliasSourceFresh}

	// Type cast: the alias source is the inner expression
	case *ast.TypeCastExpr:
		return DetermineAliasSource(e.Expr)

	// Await: the alias source is the inner expression
	case *ast.AwaitExpr:
		return DetermineAliasSource(e.Arg)

	// Property access: the value aliases the object's source.
	// We treat it as aliasing the object variable (conservative).
	case *ast.MemberExpr:
		return DetermineAliasSource(e.Object)
	case *ast.IndexExpr:
		return DetermineAliasSource(e.Object)

	// Conditionals: aliases all branches (Phase 7.4).
	case *ast.IfElseExpr:
		return determineConditionalAliasSource(e)
	case *ast.IfLetExpr:
		return AliasSource{Kind: AliasSourceUnknown}
	case *ast.MatchExpr:
		return determineMatchAliasSource(e)

	// Do expressions, try-catch: complex control flow, treat as unknown
	case *ast.DoExpr:
		return AliasSource{Kind: AliasSourceUnknown}
	case *ast.TryCatchExpr:
		return AliasSource{Kind: AliasSourceUnknown}

	// Throw/yield don't produce values that get assigned
	case *ast.ThrowExpr:
		return AliasSource{Kind: AliasSourceFresh}
	case *ast.YieldExpr:
		return AliasSource{Kind: AliasSourceUnknown}

	// Array spread
	case *ast.ArraySpreadExpr:
		return AliasSource{Kind: AliasSourceFresh}

	// Error expression
	case *ast.ErrorExpr:
		return AliasSource{Kind: AliasSourceUnknown}

	default:
		return AliasSource{Kind: AliasSourceUnknown}
	}
}

// blockResultExpr returns the result expression of a block (the last
// statement if it's an ExprStmt), or nil if the block is empty or ends
// with a non-expression statement.
func blockResultExpr(b ast.Block) ast.Expr {
	if len(b.Stmts) == 0 {
		return nil
	}
	if exprStmt, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt); ok {
		return exprStmt.Expr
	}
	return nil
}

// blockOrExprResultExpr returns the result expression from a BlockOrExpr.
func blockOrExprResultExpr(boe *ast.BlockOrExpr) ast.Expr {
	if boe == nil {
		return nil
	}
	if boe.Expr != nil {
		return boe.Expr
	}
	if boe.Block != nil {
		return blockResultExpr(*boe.Block)
	}
	return nil
}

// collectBranchSources collects alias sources from a list of expressions,
// deduplicating VarIDs across branches. Returns a merged AliasSource.
func collectBranchSources(exprs []ast.Expr) AliasSource {
	var allVarIDs []VarID
	seen := make(map[VarID]bool)
	allFresh := true

	for _, expr := range exprs {
		if expr == nil {
			// A branch with no result expression — treat as unknown
			return AliasSource{Kind: AliasSourceUnknown}
		}
		source := DetermineAliasSource(expr)
		switch source.Kind {
		case AliasSourceVariable:
			allFresh = false
			for _, id := range source.VarIDs {
				if !seen[id] {
					seen[id] = true
					allVarIDs = append(allVarIDs, id)
				}
			}
		case AliasSourceMultiple:
			allFresh = false
			for _, id := range source.VarIDs {
				if !seen[id] {
					seen[id] = true
					allVarIDs = append(allVarIDs, id)
				}
			}
		case AliasSourceFresh:
			// Fresh branch — doesn't contribute alias IDs
		default:
			// Unknown — cannot determine statically
			return AliasSource{Kind: AliasSourceUnknown}
		}
	}

	if len(allVarIDs) == 0 {
		if allFresh {
			return AliasSource{Kind: AliasSourceFresh}
		}
		return AliasSource{Kind: AliasSourceUnknown}
	}
	if len(allVarIDs) == 1 {
		return AliasSource{Kind: AliasSourceVariable, VarIDs: allVarIDs}
	}
	return AliasSource{Kind: AliasSourceMultiple, VarIDs: allVarIDs}
}

// determineConditionalAliasSource determines alias sources for an if-else
// expression by collecting sources from both branches.
func determineConditionalAliasSource(expr *ast.IfElseExpr) AliasSource {
	consExpr := blockResultExpr(expr.Cons)
	altExpr := blockOrExprResultExpr(expr.Alt)

	// If there's no alt branch, this is a statement-like if, not an
	// expression that produces a value to alias.
	if expr.Alt == nil {
		return AliasSource{Kind: AliasSourceUnknown}
	}

	return collectBranchSources([]ast.Expr{consExpr, altExpr})
}

// determineMatchAliasSource determines alias sources for a match expression
// by collecting sources from all case bodies.
func determineMatchAliasSource(expr *ast.MatchExpr) AliasSource {
	if len(expr.Cases) == 0 {
		return AliasSource{Kind: AliasSourceUnknown}
	}

	branchExprs := make([]ast.Expr, len(expr.Cases))
	for i, matchCase := range expr.Cases {
		branchExprs[i] = blockOrExprResultExpr(&matchCase.Body)
	}

	return collectBranchSources(branchExprs)
}
