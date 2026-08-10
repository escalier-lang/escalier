package set

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOnce(t *testing.T) {
	seen := NewSet[int]()
	calls := []int{}

	first := Once(seen, 1, func() string { return "repeat" }, func() string {
		calls = append(calls, 1)
		return "fresh"
	})
	require.Equal(t, "fresh", first)

	second := Once(seen, 1, func() string { return "repeat" }, func() string {
		calls = append(calls, 1)
		return "fresh"
	})
	require.Equal(t, "repeat", second)
	require.Equal(t, []int{1}, calls)
	require.True(t, seen.Contains(1))
}

// A key Once ran fn for stays in seen after fn returns, so a sibling branch reaching the same
// key takes the onRepeat path rather than walking it a second time.
func TestOnceKeepsKeyAfterFnReturns(t *testing.T) {
	seen := NewSet[int]()
	Once(seen, 1, func() int { return 0 }, func() int { return 0 })
	require.True(t, seen.Contains(1))

	repeats := 0
	Once(seen, 1, func() int { repeats++; return 0 }, func() int { return 0 })
	require.Equal(t, 1, repeats)
}

func TestOnceDo(t *testing.T) {
	seen := NewSet[string]()
	visits := []string{}
	visit := func(key string) {
		OnceDo(seen, key, func() { visits = append(visits, key) })
	}

	visit("a")
	visit("b")
	visit("a")

	require.Equal(t, []string{"a", "b"}, visits)
}

func TestOnPath(t *testing.T) {
	seen := NewSet[int]()

	result := OnPath(seen, 1, func() string { return "cycle" }, func() string {
		require.True(t, seen.Contains(1), "key should be on the path while fn runs")
		return "fresh"
	})

	require.Equal(t, "fresh", result)
	require.False(t, seen.Contains(1), "key should be off the path once fn returns")
}

// Re-entering a key from inside its own fn is a cycle, while reaching the same key from a
// sibling branch that starts after the first one finished is not.
func TestOnPathSeparatesCyclesFromRepeats(t *testing.T) {
	seen := NewSet[int]()
	cycles := 0
	walks := 0

	var walk func(depth int) int
	walk = func(depth int) int {
		return OnPath(seen, 1, func() int { cycles++; return -1 }, func() int {
			walks++
			if depth > 0 {
				return walk(depth - 1)
			}
			return depth
		})
	}

	require.Equal(t, -1, walk(1), "the inner call re-enters key 1 and closes the cycle")
	require.Equal(t, 1, cycles)
	require.Equal(t, 1, walks)

	require.Equal(t, 0, walk(0), "a later independent walk runs fn again")
	require.Equal(t, 1, cycles)
	require.Equal(t, 2, walks)
}

func TestOnPathRemovesKeyOnPanic(t *testing.T) {
	seen := NewSet[int]()

	require.Panics(t, func() {
		OnPath(seen, 1, func() int { return 0 }, func() int { panic("boom") })
	})
	require.False(t, seen.Contains(1))
}

func TestTable(t *testing.T) {
	table := NewTable[string, int]()
	calls := 0

	first := table.Do("a", func() int { return -1 }, func() int { calls++; return 7 })
	require.Equal(t, 7, first)

	second := table.Do("a", func() int { return -1 }, func() int { calls++; return 9 })
	require.Equal(t, 7, second, "a settled key replays its stored value")
	require.Equal(t, 1, calls)
}

// A key asked for again from inside its own fn is mid-derivation and takes the onCycle path.
// Once that fn returns, the key is settled and later asks replay the stored value.
func TestTableCycle(t *testing.T) {
	table := NewTable[string, int]()
	inner := 0

	outer := table.Do("a", func() int { return -1 }, func() int {
		inner = table.Do("a", func() int { return -1 }, func() int { return 100 })
		return 7
	})

	require.Equal(t, -1, inner)
	require.Equal(t, 7, outer)
	require.Equal(t, 7, table.Do("a", func() int { return -1 }, func() int { return 100 }))
}

// A zero Table works without NewTable, so a struct field of type Table needs no constructor.
func TestTableZeroValue(t *testing.T) {
	var table Table[string, int]
	require.Equal(t, 7, table.Do("a", func() int { return -1 }, func() int { return 7 }))
	require.Equal(t, 7, table.Do("a", func() int { return -1 }, func() int { return 9 }))
}

// A settled entry replays even when its value is the zero value, so a walk that legitimately
// produces nil is not mistaken for one still in progress.
func TestTableSettlesZeroValue(t *testing.T) {
	table := NewTable[string, *int]()
	calls := 0

	require.Nil(t, table.Do("a", func() *int { return nil }, func() *int { calls++; return nil }))
	require.Nil(t, table.Do("a", func() *int { return nil }, func() *int { calls++; return nil }))
	require.Equal(t, 1, calls)
}
