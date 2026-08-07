package ucs

import (
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

func TestAtRecordsTheSurfaceNode(t *testing.T) {
	node := arm(span(3, 5, 20))
	origin := At(OriginIfVal, node)

	require.Equal(t, OriginIfVal, origin.Kind)
	require.Same(t, node, origin.Node)
	require.False(t, origin.Synthetic)
}

func TestInventedRecordsNoSurfaceNode(t *testing.T) {
	origin := Invented(OriginValElse)

	require.Equal(t, OriginValElse, origin.Kind)
	require.Nil(t, origin.Node)
	require.True(t, origin.Synthetic)
}

// TestAtWithoutANodeIsSynthetic checks that a missing surface node produces a
// synthetic origin rather than one that claims a node and panics on read. Both a
// plain nil and a nil pointer stored in the interface are covered, since only the
// first is caught by `== nil`.
func TestAtWithoutANodeIsSynthetic(t *testing.T) {
	var typedNil *ast.MatchCase

	for name, origin := range map[string]Origin{
		"untyped nil": At(OriginMatchArm, nil),
		"typed nil":   At(OriginMatchArm, typedNil),
	} {
		t.Run(name, func(t *testing.T) {
			require.True(t, origin.Synthetic)
			require.Nil(t, origin.Node)
			_, ok := origin.SourceSpan()
			require.False(t, ok)
		})
	}
}

func TestSourceSpanReadsTheSurfaceNode(t *testing.T) {
	node := arm(span(4, 3, 21))
	found, ok := At(OriginMatchArm, node).SourceSpan()

	require.True(t, ok)
	require.Equal(t, span(4, 3, 21), found)
}

// TestSpanOfToleratesATypedNil keeps the printer and any diagnostic reader total. A
// nil *ast.MatchCase stored in a Spanned field compares non-nil, so a plain nil check
// would let it through and panic inside Span().
func TestSpanOfToleratesATypedNil(t *testing.T) {
	var typedNil *ast.MatchCase

	_, ok := SpanOf(typedNil)
	require.False(t, ok)

	_, ok = SpanOf(nil)
	require.False(t, ok)
}

func TestOriginKindString(t *testing.T) {
	tests := []struct {
		kind OriginKind
		want string
	}{
		{OriginMatchArm, "match arm"},
		{OriginIfVal, "if val"},
		{OriginValElse, "val else"},
		{OriginGuard, "guard"},
		{OriginKind(99), "unknown origin"},
	}

	for _, test := range tests {
		require.Equal(t, test.want, test.kind.String())
	}
}

// TestScrutineeCarriesProvenance checks that a Scrutinee exposes its Origin through
// Term, the accessor diagnostics read. Each IR node type is checked the same way as
// it is added.
func TestScrutineeCarriesProvenance(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(2, 5, 18)))
	var term Term = NewRoot(ident("p"), origin)

	require.Equal(t, origin, term.Prov())
}

// TestInventedFromKeepsAProvenanceChain covers the reason Cause exists. A synthetic
// node carries no span, and the IR has no parent pointers, so without the chain a
// diagnostic about an invented node could only recover a position by threading the
// enclosing origin down a walk. Following Cause recovers it from the node alone.
func TestInventedFromKeepsAProvenanceChain(t *testing.T) {
	decl := arm(span(3, 1, 40))
	declOrigin := At(OriginValElse, decl)
	tail := InventedFrom(OriginValElse, declOrigin)

	require.True(t, tail.Synthetic)
	require.Nil(t, tail.Node)
	require.NotNil(t, tail.Cause)
	require.Equal(t, declOrigin, *tail.Cause)

	// The synthetic node blames nothing itself.
	_, ok := tail.SourceSpan()
	require.False(t, ok)

	// Following the chain reaches the declaration that produced it.
	found, ok := tail.NearestSpan()
	require.True(t, ok)
	require.Equal(t, span(3, 1, 40), found)
}

// A chain several links long still resolves, which is what a tail minted while
// lowering another minted node produces.
func TestNearestSpanFollowsSeveralLinks(t *testing.T) {
	outer := At(OriginMatchArm, arm(span(1, 1, 8)))
	mid := InventedFrom(OriginGuard, outer)
	inner := InventedFrom(OriginValElse, mid)

	found, ok := inner.NearestSpan()
	require.True(t, ok)
	require.Equal(t, span(1, 1, 8), found)
	require.Equal(t, OriginValElse, inner.Kind, "the chain carries a span, not the kind")
}

// A chain that ends at a causeless Invented names no position, rather than inventing
// one the user cannot see.
func TestNearestSpanMissesWhenTheChainHasNoRealNode(t *testing.T) {
	root := Invented(OriginIfVal)
	derived := InventedFrom(OriginValElse, root)

	_, ok := derived.NearestSpan()
	require.False(t, ok)

	_, ok = root.NearestSpan()
	require.False(t, ok)
}

// NearestSpan on a real origin is its own span, so a caller can read it uniformly
// without first asking whether the origin is synthetic.
func TestNearestSpanOnARealOrigin(t *testing.T) {
	found, ok := At(OriginMatchArm, arm(span(2, 5, 18))).NearestSpan()
	require.True(t, ok)
	require.Equal(t, span(2, 5, 18), found)
}

// Cause is exported, so a caller can assign a cycle by hand. Diagnostics must stay
// total, so the walk stops on a link it has already followed instead of spinning.
func TestNearestSpanToleratesACauseCycle(t *testing.T) {
	loop := Invented(OriginGuard)
	loop.Cause = &loop

	done := make(chan bool, 1)
	go func() {
		_, ok := loop.NearestSpan()
		done <- ok
	}()

	select {
	case ok := <-done:
		require.False(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("NearestSpan did not terminate on a cyclic cause chain")
	}
}

// A long chain still resolves. Nothing bounds the walk by length, so a legitimate
// chain does not lose its span for being deep — only a repeated link stops it.
func TestNearestSpanFollowsALongChain(t *testing.T) {
	origin := At(OriginMatchArm, arm(span(7, 2, 30)))
	for range 200 {
		origin = InventedFrom(OriginValElse, origin)
	}

	found, ok := origin.NearestSpan()
	require.True(t, ok)
	require.Equal(t, span(7, 2, 30), found)
}

// Two links sharing one *Origin are a diamond, not a cycle. A walk down a single
// path visits each link once, so the seen set must not cut the chain short here.
func TestNearestSpanToleratesASharedCause(t *testing.T) {
	shared := At(OriginIfVal, arm(span(4, 1, 12)))
	left := InventedFrom(OriginValElse, shared)
	right := InventedFrom(OriginGuard, shared)
	right.Cause = left.Cause // both links now point at the same *Origin

	found, ok := right.NearestSpan()
	require.True(t, ok)
	require.Equal(t, span(4, 1, 12), found)
}
