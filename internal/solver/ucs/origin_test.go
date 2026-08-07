package ucs

import (
	"testing"

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
