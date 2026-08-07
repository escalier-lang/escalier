package ucs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoreNodesCarryProvenance(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(2, 5, 18)))
	scrutinee := NewRoot(ident("p"), origin)

	terms := map[string]Term{
		"CoreSplit":  &CoreSplit{Scrutinee: scrutinee, Origin: origin},
		"CoreBranch": &CoreBranch{Pattern: wildcardPat(), Origin: origin},
		"CoreGuard":  &CoreGuard{Cond: ident("g"), Origin: origin},
		"CoreBind":   &CoreBind{Name: "x", Source: scrutinee, Origin: origin},
	}

	for name, term := range terms {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, origin, term.Prov())
		})
	}
}

// TestCoreSplitKeepsSourceOrder locks the core's first-match semantics. Branch order
// is source order, and normalization in PR3 is what rewrites it into tests that
// never backtrack.
func TestCoreSplitKeepsSourceOrder(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	scrutinee := NewRoot(ident("p"), origin)

	first := &CoreBranch{Pattern: identPat("a"), Cont: &BodyLeaf{Body: exprBody(num(1))}, Origin: origin}
	second := &CoreBranch{Pattern: wildcardPat(), Cont: &BodyLeaf{Body: exprBody(num(2))}, Origin: origin}
	split := &CoreSplit{Scrutinee: scrutinee, Branches: []*CoreBranch{first, second}, Origin: origin}

	require.Equal(t, []*CoreBranch{first, second}, split.Branches)
	// A `match` leaves Else nil, since a catch-all arm is an ordinary branch here.
	require.Nil(t, split.Else)
}

// TestCoreBranchKeepsItsPatternWhole is the property that separates the core from the
// normalized form. A core branch holds the arm's pattern with its nesting intact, so
// no tag-level flattening has happened yet.
func TestCoreBranchKeepsItsPatternWhole(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(1, 1, 8)))
	pattern := objPat("x", "y")
	branch := &CoreBranch{Pattern: pattern, Origin: origin}

	require.Same(t, pattern, branch.Pattern)
	require.Equal(t, "{x, y}", patString(branch.Pattern))
}
