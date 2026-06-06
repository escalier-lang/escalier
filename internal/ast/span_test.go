package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sp(sl, sc, el, ec, srcID int) Span {
	return NewSpan(Location{Line: sl, Column: sc}, Location{Line: el, Column: ec}, srcID)
}

// ContainsSpan reports whether inner lies entirely within the receiver, requiring
// the same source and both endpoints contained.
func TestSpanContainsSpan(t *testing.T) {
	outer := sp(1, 1, 1, 20, 0)

	t.Run("strictly inside", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(sp(1, 5, 1, 9, 0)))
	})
	t.Run("equal span is contained", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(outer))
	})
	t.Run("flush at the end (inclusive boundary) is contained", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(sp(1, 17, 1, 20, 0)))
	})
	t.Run("starts before is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(sp(1, 0, 1, 9, 0)))
	})
	t.Run("ends after is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(sp(1, 5, 1, 21, 0)))
	})
	t.Run("different source is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(sp(1, 5, 1, 9, 1)))
	})
	t.Run("multi-line containment", func(t *testing.T) {
		multi := sp(1, 1, 5, 10, 0)
		require.True(t, multi.ContainsSpan(sp(2, 1, 4, 3, 0)))
		require.False(t, multi.ContainsSpan(sp(2, 1, 6, 1, 0)))
	})
}
