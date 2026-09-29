package checker

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// compareBySubtype runs its subtype query under a zero Context on purpose, so
// every lookup reached from it has to answer "not found" rather than panic.
func TestNilScopeResolvesNothing(t *testing.T) {
	t.Parallel()
	var scope *Scope
	require.Nil(t, scope.GetValue("anything"))
	require.Nil(t, scope.GetTypeAlias("anything"))
	require.Nil(t, scope.getNamespace("anything"))
}
