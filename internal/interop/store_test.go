package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/type_system"
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
	if got == nil || got.Type != fn {
		t.Fatalf("expected free fn lookup to return fn; got %#v", got)
	}
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
	if eff == nil || eff.Type != fn {
		t.Fatalf("expected instance method lookup to return fn; got %#v", eff)
	}
	// Static lookup misses.
	if got := store.Resolve(Path{
		Owner:  ident("Array"),
		Name:   ident("map"),
		Kind:   KindMethod,
		Static: true,
	}); got != nil {
		t.Fatalf("expected static lookup to miss; got %#v", got)
	}
}

func TestResolveNilStoreReturnsNil(t *testing.T) {
	var store *OverrideStore
	if got := store.Resolve(Path{Module: "anything"}); got != nil {
		t.Fatalf("nil store should resolve to nil; got %#v", got)
	}
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
			if got := canonicalNameFromPK(c.in); got != c.want {
				t.Fatalf("canonicalNameFromPK = %q; want %q", got, c.want)
			}
		})
	}
}
