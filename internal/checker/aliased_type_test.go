package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// typeParamRef builds the form a type parameter takes inside an alias body: a
// TypeRefType named after the parameter, which SubstituteTypeParams matches by name.
func typeParamRef(name string) type_system.Type {
	return type_system.NewTypeRefType(nil, name, nil)
}

// pairAlias builds `type <name><params...> = [elems...]`, a tuple body being the
// shortest thing with a predictable rendering.
func pairAlias(elems []type_system.Type, paramNames ...string) *type_system.TypeAlias {
	params := make([]*type_system.TypeParam, len(paramNames))
	for i, paramName := range paramNames {
		params[i] = &type_system.TypeParam{Name: paramName}
	}
	return &type_system.TypeAlias{
		Type:       type_system.NewTupleType(nil, elems...),
		TypeParams: params,
	}
}

func TestAliasedType(t *testing.T) {
	t.Parallel()

	numAndParam := []type_system.Type{type_system.NewNumPrimType(nil), typeParamRef("N")}

	tests := map[string]struct {
		ref *type_system.TypeRefType
		// want is the rendering of the aliased type, or "" when nil is expected.
		want string
	}{
		"NoAliasAttached": {
			// The error-recovery shape: inferTypeAnn could not resolve the name and
			// already reported an UnknownTypeError, so there is nothing to reach for.
			ref:  type_system.NewTypeRefType(nil, "Missing", nil),
			want: "",
		},
		"NonGenericAlias": {
			// type S = [number, string]
			ref: type_system.NewTypeRefType(nil, "S", pairAlias([]type_system.Type{
				type_system.NewNumPrimType(nil),
				type_system.NewStrPrimType(nil),
			})),
			want: "[number, string]",
		},
		"GenericAliasSubstitutesItsArgument": {
			// type G<N> = [number, N], referenced as G<boolean>
			ref: type_system.NewTypeRefType(nil, "G", pairAlias(numAndParam, "N"),
				type_system.NewBoolPrimType(nil)),
			want: "[number, boolean]",
		},
		"GenericAliasWithNoArgumentsIsLeftAlone": {
			// type G<N> = [number, N], referenced as a bare G. Substitution is skipped
			// entirely, so the body still names its own parameter.
			ref:  type_system.NewTypeRefType(nil, "G", pairAlias(numAndParam, "N")),
			want: "[number, N]",
		},
		"FewerArgumentsThanParametersSubstitutesThePrefix": {
			// type P<A, B> = [A, B], referenced as P<string>. createTypeParamSubstitutions
			// pairs through Zip, which stops at the shorter side, so B keeps its name.
			ref: type_system.NewTypeRefType(nil, "P",
				pairAlias([]type_system.Type{typeParamRef("A"), typeParamRef("B")}, "A", "B"),
				type_system.NewStrPrimType(nil)),
			want: "[string, B]",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resolved := aliasedType(test.ref)
			if test.want == "" {
				require.Nil(t, resolved)
				return
			}
			require.NotNil(t, resolved)
			require.Equal(t, test.want, resolved.String())
		})
	}
}

// aliasedType hands back the alias's own Type when there is nothing to substitute,
// rather than a copy. A caller that mutates the result — setting provenance, say —
// reaches every other use of that alias, so one that intends to must copy first.
func TestAliasedTypeSharesTheAliasBodyWhenNothingIsSubstituted(t *testing.T) {
	t.Parallel()

	alias := pairAlias([]type_system.Type{type_system.NewNumPrimType(nil)})
	ref := type_system.NewTypeRefType(nil, "S", alias)

	require.Same(t, alias.Type, aliasedType(ref))
}
