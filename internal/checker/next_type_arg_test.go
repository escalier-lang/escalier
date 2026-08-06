package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// nextTypeArg walks alias to alias looking for a generator, so a chain that loops back
// on itself would hand it the same aliases forever. `type A = B` paired with
// `type B = A` is the shortest such chain. It names no generator, so the walk ends and
// the send type is inferred.
func TestNextTypeArgTerminatesOnACyclicAliasChain(t *testing.T) {
	t.Parallel()

	aliasA := &type_system.TypeAlias{}
	aliasB := &type_system.TypeAlias{}
	aliasA.Type = type_system.NewTypeRefType(nil, "B", aliasB)
	aliasB.Type = type_system.NewTypeRefType(nil, "A", aliasA)

	require.Nil(t, nextTypeArg(type_system.NewTypeRefType(nil, "A", aliasA), generatorTypeNames))
}

// A chain that reaches one alias twice at different instantiations is not a cycle, so
// the walk has to run through it rather than stopping at the repeated alias. Here
// `type W<T> = T` is traversed twice, with `Generator<…>` and then with its own body,
// before the generator turns up.
func TestNextTypeArgFollowsOneAliasAtTwoInstantiations(t *testing.T) {
	t.Parallel()

	identity := &type_system.TypeAlias{
		Type:       typeParamRef("T"),
		TypeParams: []*type_system.TypeParam{{Name: "T"}},
	}
	gen := type_system.NewTypeRefType(nil, "Generator", nil,
		type_system.NewNumPrimType(nil),
		type_system.NewStrPrimType(nil),
		type_system.NewBoolPrimType(nil),
	)
	inner := type_system.NewTypeRefType(nil, "W", identity, gen)
	outer := type_system.NewTypeRefType(nil, "W", identity, inner)

	require.Equal(t, "boolean", nextTypeArg(outer, generatorTypeNames).String())
}
