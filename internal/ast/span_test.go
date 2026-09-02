package ast

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sp(start, end, srcID int) Span {
	return NewSpan(Location{Offset: start}, Location{Offset: end}, srcID)
}

// ContainsSpan reports whether inner lies entirely within the receiver, requiring
// the same source and both endpoints contained.
func TestSpanContainsSpan(t *testing.T) {
	outer := sp(0, 20, 0)

	t.Run("strictly inside", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(sp(4, 9, 0)))
	})
	t.Run("equal span is contained", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(outer))
	})
	t.Run("flush at the end (inclusive boundary) is contained", func(t *testing.T) {
		require.True(t, outer.ContainsSpan(sp(16, 20, 0)))
	})
	t.Run("starts before is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(NewSpan(Location{Offset: -1}, Location{Offset: 9}, 0)))
	})
	t.Run("ends after is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(sp(4, 21, 0)))
	})
	t.Run("different source is not contained", func(t *testing.T) {
		require.False(t, outer.ContainsSpan(sp(4, 9, 1)))
	})
	t.Run("spanning several lines is contained by a span covering them", func(t *testing.T) {
		multi := sp(0, 120, 0)
		require.True(t, multi.ContainsSpan(sp(30, 90, 0)))
		require.False(t, multi.ContainsSpan(sp(30, 121, 0)))
	})
}
