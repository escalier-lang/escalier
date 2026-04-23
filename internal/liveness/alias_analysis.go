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

	// Property access: aliases the property's source (Phase 7);
	// for now treat as unknown
	case *ast.MemberExpr:
		return AliasSource{Kind: AliasSourceUnknown}
	case *ast.IndexExpr:
		return AliasSource{Kind: AliasSourceUnknown}

	// Conditionals: aliases all branches (Phase 7);
	// for now treat as unknown
	case *ast.IfElseExpr:
		return AliasSource{Kind: AliasSourceUnknown}
	case *ast.IfLetExpr:
		return AliasSource{Kind: AliasSourceUnknown}
	case *ast.MatchExpr:
		return AliasSource{Kind: AliasSourceUnknown}

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
