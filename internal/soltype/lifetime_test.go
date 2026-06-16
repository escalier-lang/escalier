package soltype

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Test 4 — ContainsLifetime's two equality regimes: a LifetimeVar matches by
// pointer identity, but 'static matches by VALUE because origination sites mint a
// fresh &StaticLifetime{} per call and they all denote the one lattice bottom.
func TestContainsLifetime(t *testing.T) {
	a := &LifetimeVar{ID: 0}
	b := &LifetimeVar{ID: 1}
	static1 := &StaticLifetime{}
	static2 := &StaticLifetime{}

	// A LifetimeVar matches by pointer identity.
	require.True(t, ContainsLifetime([]Lifetime{a, b}, a))
	require.False(t, ContainsLifetime([]Lifetime{a}, b), "a different var does not match")
	require.False(t, ContainsLifetime([]Lifetime{a}, &LifetimeVar{ID: 0}),
		"vars match by pointer, not by ID — a distinct var with the same ID is a different lifetime")

	// 'static matches by value: two distinct instances match, and the Static
	// singleton matches a fresh instance either way round.
	require.True(t, ContainsLifetime([]Lifetime{static1}, static2), "distinct 'static instances match by value")
	require.True(t, ContainsLifetime([]Lifetime{Static}, &StaticLifetime{}), "the singleton matches a fresh 'static")
	require.True(t, ContainsLifetime([]Lifetime{a, static1}, Static), "Static is found among mixed bounds")

	// No false positives.
	require.False(t, ContainsLifetime([]Lifetime{a, b}, static1), "'static does not match a var-only bound list")
	require.False(t, ContainsLifetime(nil, a), "empty bounds contain nothing")
}

// IsStaticLifetime distinguishes the lattice bottom from a variable, including the
// canonical Static singleton.
func TestIsStaticLifetime(t *testing.T) {
	require.True(t, IsStaticLifetime(&StaticLifetime{}))
	require.True(t, IsStaticLifetime(Static))
	require.False(t, IsStaticLifetime(&LifetimeVar{ID: 0}))
}
