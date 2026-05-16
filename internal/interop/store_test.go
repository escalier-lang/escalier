package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

func ident(name string) *dts_parser.Ident {
	return dts_parser.NewIdent(name, ast.Span{})
}

func TestResolveFreeFunctionAtModuleTop(t *testing.T) {
	fn := type_system.NewFuncType(nil, nil, nil, nil, nil)
	store := NewOverrideStore()
	store.Modules["lodash"] = &ModuleScope{
		Container: Container{
			Free: map[string]*Effective{
				"map": {Type: fn, Source: TierBuiltinOverride},
			},
			Children: map[string]ChildScope{},
		},
	}
	got := store.Resolve(Path{
		Module: "lodash",
		Name:   ident("map"),
		Kind:   KindFree,
	})
	require.NotNil(t, got)
	require.Same(t, fn, got.Type)
}

func TestResolveInstanceMethod(t *testing.T) {
	fn := type_system.NewFuncType(nil, nil, nil, nil, nil)
	store := NewOverrideStore()
	store.Modules[""] = &ModuleScope{
		Container: Container{
			Free: map[string]*Effective{},
			Children: map[string]ChildScope{
				"Array": &ClassScope{
					Instance: &MemberSet{
						Methods: map[string]*Effective{
							"map": {Type: fn, Source: TierBuiltinOverride},
						},
						Getters:    map[string]*Effective{},
						Setters:    map[string]*Effective{},
						Properties: map[string]*Effective{},
					},
					Static: NewMemberSet(),
				},
			},
		},
	}
	eff := store.Resolve(Path{
		Owner:  ident("Array"),
		Name:   ident("map"),
		Kind:   KindMethod,
		Static: false,
	})
	require.NotNil(t, eff)
	require.Same(t, fn, eff.Type)
	// Static lookup misses.
	require.Nil(t, store.Resolve(Path{
		Owner:  ident("Array"),
		Name:   ident("map"),
		Kind:   KindMethod,
		Static: true,
	}))
}

func TestResolveNilStoreReturnsNil(t *testing.T) {
	var store *OverrideStore
	require.Nil(t, store.Resolve(Path{Module: "anything"}))
}

func TestCanonicalNameFromPK(t *testing.T) {
	cases := []struct {
		name string
		in   dts_parser.PropertyKey
		want string
	}{
		{"plain ident", ident("foo"), "foo"},
		{"string literal", &dts_parser.StringLiteral{Value: "foo bar"}, `["foo bar"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, canonicalNameFromPK(c.in))
		})
	}
}
