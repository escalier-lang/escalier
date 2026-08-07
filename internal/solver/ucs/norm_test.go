package ucs

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestNormNodesCarryProvenance(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(2, 5, 18)))
	scrutinee := NewRoot(ident("p"), origin)

	terms := map[string]Term{
		"NormSplit":  &NormSplit{Scrutinee: scrutinee, Origin: origin},
		"NormBranch": &NormBranch{Test: &LitTest{Lit: ast.NewNumber(1, ast.Span{})}, Origin: origin},
		"NormGuard":  &NormGuard{Cond: ident("g"), Origin: origin},
		"NormBind":   &NormBind{Name: "x", Source: scrutinee, Origin: origin},
	}

	for name, term := range terms {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, origin, term.Prov())
		})
	}
}

// TestNormBranchTestsOneTagLevel is the property that separates the normalized form
// from the core. Where a core branch holds `Line { start: {x, y} }` whole, a
// normalized branch tests the `Line` tag alone and hands the nested shape to an inner
// split over a projected sub-scrutinee.
func TestNormBranchTestsOneTagLevel(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	line := NewRoot(ident("l"), origin)
	start := line.Project(FieldStep{Name: "start"}, origin)

	inner := &NormSplit{
		Scrutinee: start,
		Branches: []*NormBranch{{
			Test:   &ObjectTest{Keys: keys("x", "y")},
			Cont:   &BodyLeaf{Body: exprBody(num(1))},
			Origin: origin,
		}},
		Origin: origin,
	}
	outer := &NormSplit{
		Scrutinee: line,
		Branches: []*NormBranch{{
			Test:   &ClassTest{Name: ast.NewIdentifier("Line", ast.Span{})},
			Cont:   inner,
			Origin: origin,
		}},
		Origin: origin,
	}

	require.Equal(t, "Line", testString(outer.Branches[0].Test))
	require.Equal(t, "{x, y}", testString(inner.Branches[0].Test))
	require.Equal(t, "l.start", inner.Scrutinee.String())
	// Sharing the parent pointer is what keeps `l` evaluated once.
	require.Same(t, outer.Scrutinee, inner.Scrutinee.Parent)
}

// TestNormGuardNamesItsFailureContinuation is what removes the backtracking. A
// CoreGuard has no Default and relies on the enclosing split's branch order; a
// NormGuard states where a failed test goes.
func TestNormGuardNamesItsFailureContinuation(t *testing.T) {
	origin := At(OriginGuard, arm(span(1, 1, 8)))
	fallthru := &BodyLeaf{Body: exprBody(num(2)), Origin: origin}
	guard := &NormGuard{
		Cond:    ident("g"),
		Cont:    &BodyLeaf{Body: exprBody(num(1)), Origin: origin},
		Default: fallthru,
		Origin:  origin,
	}

	require.Same(t, fallthru, guard.Default)
}
