package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
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
				"map": {Type: fn, Source: dts_to_esc.TierBuiltinOverride},
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
							"map": {Type: fn, Source: dts_to_esc.TierBuiltinOverride},
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

// An Origin carries the file its span indexes into so a diagnostic can name a
// line. Without one there is no text to resolve the offset against.
func TestOriginLine(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // The memoized line map is built on first use.
	source := &ast.Source{ID: 0, Path: "override.esc", Contents: "val a = 1\nval b = 2\nval c = 3\n"}

	tests := []struct {
		name   string
		origin Origin
		want   int
	}{
		{
			name: "resolves the line the span starts on",
			//nolint: exhaustruct // Kind defaults to the zero value, unused here.
			origin: Origin{
				FilePath: "override.esc",
				Span:     ast.NewSpan(ast.Location{Offset: 24}, ast.Location{Offset: 25}, 0),
				Source:   source,
			},
			want: 3,
		},
		{
			name: "the first line",
			//nolint: exhaustruct // Kind defaults to the zero value, unused here.
			origin: Origin{
				FilePath: "override.esc",
				Span:     ast.NewSpan(ast.Location{Offset: 0}, ast.Location{Offset: 3}, 0),
				Source:   source,
			},
			want: 1,
		},
		{
			name: "reports 0 without a source",
			//nolint: exhaustruct // Kind defaults to the zero value, unused here.
			origin: Origin{
				FilePath: "override.esc",
				Span:     ast.NewSpan(ast.Location{Offset: 24}, ast.Location{Offset: 25}, 0),
			},
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.origin.Line())
		})
	}
}

// NewOriginSite stamps both halves onto every Origin an extraction produces,
// so the locator a diagnostic prints and the line it resolves stay together.
func TestOriginSiteStampsTheSource(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // The memoized line map is built on first use.
	source := &ast.Source{ID: 0, Path: "override.esc", Contents: "val a = 1\nval b = 2\n"}
	site := NewOriginSite("builtin:/override.esc", source)

	origin := site.originAt(ast.NewSpan(ast.Location{Offset: 10}, ast.Location{Offset: 13}, 0))
	require.Equal(t, OverrideFile, origin.Kind)
	require.Equal(t, "builtin:/override.esc", origin.FilePath)
	require.Same(t, source, origin.Source)
	require.Equal(t, 2, origin.Line())
}
