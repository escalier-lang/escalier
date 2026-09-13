package dts_to_esc

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every package the partition knows carries a tier. A `web:*` package
// added without a webTiers entry would otherwise route and convert while
// no tier check could say anything about its imports.
func TestEveryPackageHasATier(t *testing.T) {
	for _, uri := range PackageList() {
		_, ok := TierOf(uri)
		require.True(t, ok, "%s has no tier", uri)
	}
}

// webTiers names only packages the partition holds, so a renamed package
// cannot leave a stale entry behind that silently assigns nothing.
func TestEveryTieredPackageExists(t *testing.T) {
	for _, table := range []map[string]Tier{webTiers, stdTiers} {
		for uri := range table {
			_, ok := PackageForURI(uri)
			require.True(t, ok, "the tier table names %s, which the partition does not hold", uri)
		}
	}
}

// The tiers order by how much a package assumes about where it runs, and
// a `std:*` package assumes least. An import edge is checked by comparing
// these, so the order is what the check means.
func TestTiersAreOrdered(t *testing.T) {
	require.Less(t, TierLanguage, TierCore)
	require.Less(t, TierCore, TierPortable)
	require.Less(t, TierPortable, TierBrowser)
}

func TestTierNames(t *testing.T) {
	require.Equal(t, "language", TierLanguage.String())
	require.Equal(t, "core", TierCore.String())
	require.Equal(t, "portable", TierPortable.String())
	require.Equal(t, "browser", TierBrowser.String())
}

// Each tier holds what it is meant to. The membership is the input to
// every check built on it, so it is pinned rather than left implicit.
func TestTierMembership(t *testing.T) {
	require.Equal(t, []string{"web:core"}, PackagesInTier(TierCore))

	require.Equal(t, []string{
		"std:wasm",
		"web:compression", "web:crypto", "web:encoding", "web:fetch",
		"web:file", "web:performance", "web:streams", "web:url",
		"web:websocket",
	}, PackagesInTier(TierPortable))

	require.Equal(t, []string{
		"web:cache", "web:dom", "web:indexeddb", "web:payments",
		"web:push", "web:service_worker", "web:storage", "web:web_audio",
		"web:web_codecs", "web:web_rtc", "web:webauthn", "web:webgl",
		"web:workers",
	}, PackagesInTier(TierBrowser))

	// Every remaining package is a `std:*` one. The reverse does not hold:
	// `std:wasm` is portable, because the scheme names a package and the tier
	// says which runtimes carry it.
	for _, uri := range PackagesInTier(TierLanguage) {
		require.True(t, strings.HasPrefix(uri, "std:"), "%s is not a std package", uri)
	}
	require.NotEmpty(t, PackagesInTier(TierLanguage))
}
