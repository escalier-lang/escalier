package liveness

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// ProjectionStep names one step from a root variable to a leaf slot inside
// a freshly-constructed composite value. The empty-path case (a leaf with
// no steps) means the leaf is the root itself — this is today's "this
// expression aliases VarID X" behavior.
type ProjectionStep interface{ projectionStep() }

// ElementOf is the step into an array literal element ([a, b]).
type ElementOf struct{}

// PropertyOf is the step into an object property ({k: a}).
type PropertyOf struct{ Key string }

// IndexOf is the step into a fixed tuple slot.
type IndexOf struct{ Index int }

// AwaitOf is the step through a Promise<T> value (await p).
type AwaitOf struct{}

// CastOf is the step through a type cast (p as T) — pass-through.
type CastOf struct{}

func (ElementOf) projectionStep()  {}
func (PropertyOf) projectionStep() {}
func (IndexOf) projectionStep()    {}
func (AwaitOf) projectionStep()    {}
func (CastOf) projectionStep()     {}

// AliasLeaf is one (root, path) pair contributing a lifetime to a specific
// slot of a surrounding fresh container.
type AliasLeaf struct {
	RootVarID VarID
	// Path is the sequence of projection steps from the root into the
	// freshly-constructed container surrounding this expression. An empty
	// path means the leaf is the root itself (the legacy single-var case).
	Path []ProjectionStep
}

// AliasSourceKind describes the kind of alias source an expression
// represents. Derived from the (Leaves, Fresh) shape — kept as a typed
// value so call sites can switch on it.
type AliasSourceKind int

const (
	AliasSourceFresh    AliasSourceKind = iota // new value, no alias
	AliasSourceVariable                        // aliases a specific variable
	AliasSourceMultiple                        // aliases one of several variables (conditional)
	AliasSourceUnknown                         // cannot determine statically
)

// AliasSource describes where a value comes from for alias tracking
// purposes. As of Phase 8.9, an alias source is a *set* of leaves — each
// leaf names a root variable plus the projection path from that root into
// the freshly-constructed container that wraps the expression. A direct
// variable reference is a single leaf with empty path; `[a, b]` is two
// leaves rooted at `a` and `b` with `[ElementOf]` paths.
type AliasSource struct {
	Leaves []AliasLeaf
	// Fresh is true iff the expression produces a freshly-constructed value
	// with no aliasing leaves. Distinguishes "definitely fresh" from
	// "unknown" (the zero value, with no leaves and Fresh==false).
	Fresh bool
}

// Kind returns the legacy alias-source kind derived from the leaves.
func (s AliasSource) Kind() AliasSourceKind {
	switch len(s.Leaves) {
	case 0:
		if s.Fresh {
			return AliasSourceFresh
		}
		return AliasSourceUnknown
	case 1:
		return AliasSourceVariable
	default:
		return AliasSourceMultiple
	}
}

// VarIDs returns the deduplicated list of root variable IDs across all
// leaves, in leaf order. Provided for callers that only care about the
// flat root set (e.g. existing alias-set merging in the checker).
func (s AliasSource) VarIDs() []VarID {
	if len(s.Leaves) == 0 {
		return nil
	}
	seen := set.NewSet[VarID]()
	out := make([]VarID, 0, len(s.Leaves))
	for _, leaf := range s.Leaves {
		if seen.Contains(leaf.RootVarID) {
			continue
		}
		seen.Add(leaf.RootVarID)
		out = append(out, leaf.RootVarID)
	}
	return out
}

// freshSource is a small constructor for the common "definitely fresh"
// case so call sites stay readable.
func freshSource() AliasSource { return AliasSource{Fresh: true} }

// unknownSource is the zero value — kept as a constructor for parity with
// freshSource so the intent is explicit at the call site.
func unknownSource() AliasSource { return AliasSource{} }

// rootSource builds an AliasSource for a single direct variable
// reference (empty projection path).
func rootSource(id VarID) AliasSource {
	return AliasSource{Leaves: []AliasLeaf{{RootVarID: id}}}
}

// DetermineAliasSource examines an expression and returns its alias source.
// When the expression is an IdentExpr, the VarID is read directly from the
// node (set by the rename pass in Phase 2).
func DetermineAliasSource(expr ast.Expr) AliasSource {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		if e.VarID > 0 {
			return rootSource(VarID(e.VarID))
		}
		// Non-local variable (VarID <= 0) — treat as unknown since we
		// can't track aliases across function boundaries.
		return unknownSource()

	// Fresh values: literals, object/array construction, function expressions
	case *ast.LiteralExpr:
		return freshSource()
	case *ast.ObjectExpr:
		return freshSource()
	case *ast.TupleExpr:
		return freshSource()
	case *ast.FuncExpr:
		return freshSource()
	case *ast.TemplateLitExpr:
		return freshSource()
	case *ast.TaggedTemplateLitExpr:
		return freshSource()
	case *ast.JSXElementExpr:
		return freshSource()
	case *ast.JSXFragmentExpr:
		return freshSource()

	// Function calls: treat as fresh for now (Phase 8 adds lifetime-based tracking)
	case *ast.CallExpr:
		return freshSource()

	// Unary/binary operations produce fresh primitive values
	case *ast.UnaryExpr:
		return freshSource()
	case *ast.BinaryExpr:
		return freshSource()

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
		return unknownSource()
	case *ast.MatchExpr:
		return determineMatchAliasSource(e)

	// Do expressions, try-catch: complex control flow, treat as unknown
	case *ast.DoExpr:
		return unknownSource()
	case *ast.TryCatchExpr:
		return unknownSource()

	// Throw/yield don't produce values that get assigned
	case *ast.ThrowExpr:
		return freshSource()
	case *ast.YieldExpr:
		return unknownSource()

	// Array spread
	case *ast.ArraySpreadExpr:
		return freshSource()

	// Error expression
	case *ast.ErrorExpr:
		return unknownSource()

	default:
		return unknownSource()
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
// deduplicating leaves across branches by root VarID. Returns a merged
// AliasSource. Per-branch projection paths are preserved on each leaf;
// duplicate roots keep the first leaf's path.
func collectBranchSources(exprs []ast.Expr) AliasSource {
	seen := set.NewSet[VarID]()
	var leaves []AliasLeaf
	allFresh := true

	for _, expr := range exprs {
		if expr == nil {
			// A branch with no result expression — treat as unknown.
			return unknownSource()
		}
		source := DetermineAliasSource(expr)
		if len(source.Leaves) > 0 {
			allFresh = false
			for _, leaf := range source.Leaves {
				if seen.Contains(leaf.RootVarID) {
					continue
				}
				seen.Add(leaf.RootVarID)
				leaves = append(leaves, leaf)
			}
		} else if !source.Fresh {
			// Unknown — treat like fresh for alias purposes. We can't
			// determine what this branch aliases, but that's no reason
			// to discard alias info from the branches we DO know about.
			allFresh = false
		}
	}

	if len(leaves) == 0 {
		if allFresh {
			return freshSource()
		}
		return unknownSource()
	}
	return AliasSource{Leaves: leaves}
}

// determineConditionalAliasSource determines alias sources for an if-else
// expression by collecting sources from both branches.
func determineConditionalAliasSource(expr *ast.IfElseExpr) AliasSource {
	consExpr := blockResultExpr(expr.Cons)
	altExpr := blockOrExprResultExpr(expr.Alt)

	// If there's no alt branch, the else produces undefined (a fresh
	// value). Only the consequent may contribute alias sources.
	if expr.Alt == nil {
		return collectBranchSources([]ast.Expr{consExpr})
	}

	return collectBranchSources([]ast.Expr{consExpr, altExpr})
}

// determineMatchAliasSource determines alias sources for a match expression
// by collecting sources from all case bodies.
func determineMatchAliasSource(expr *ast.MatchExpr) AliasSource {
	if len(expr.Cases) == 0 {
		return unknownSource()
	}

	branchExprs := make([]ast.Expr, len(expr.Cases))
	for i, matchCase := range expr.Cases {
		branchExprs[i] = blockOrExprResultExpr(&matchCase.Body)
	}

	return collectBranchSources(branchExprs)
}
