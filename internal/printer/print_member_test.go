package printer

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// parseOneDecl parses a single declaration out of an `.esc` source
// string.
func parseOneDecl(t *testing.T, src string) ast.Decl {
	t.Helper()
	decls, errs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "member_test.esc", Contents: src})
	require.Empty(t, errs, "parsing %q", src)
	require.Len(t, decls, 1)
	return decls[0]
}

// TestPrintClassElem prints each member of a class one at a time. The
// printed member carries no leading indent and no trailing separator,
// so a caller splicing it into an existing body places both itself.
func TestPrintClassElem(t *testing.T) {
	t.Parallel()
	decl := parseOneDecl(t, `declare class Foo<T> {
    constructor(mut self, length: number),
    readonly length: number,
    static isFoo(value: unknown) -> boolean,
    indexOf(self, item: T) -> number,
    get first(self) -> T,
    set first(mut self, value: T),
}`)
	class, ok := decl.(*ast.ClassDecl)
	require.True(t, ok)

	expected := []string{
		"constructor(mut self, length: number)",
		"readonly length: number",
		"static isFoo(value: unknown) -> boolean",
		"indexOf(self, item: T) -> number",
		"get first(self) -> T",
		"set first(mut self, value: T)",
	}
	require.Len(t, class.Body, len(expected))
	for i, want := range expected {
		got, err := PrintClassElem(class.Body[i], DefaultOptions())
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

// TestPrintObjTypeAnnElem is TestPrintClassElem for interface members.
func TestPrintObjTypeAnnElem(t *testing.T) {
	t.Parallel()
	decl := parseOneDecl(t, `interface Foo<T> {
    readonly length: number,
    name?: string,
    indexOf(item: T) -> number,
    get first() -> T,
    set first(value: T),
}`)
	iface, ok := decl.(*ast.InterfaceDecl)
	require.True(t, ok)
	require.NotNil(t, iface.TypeAnn)

	expected := []string{
		"readonly length: number",
		"name?: string",
		"indexOf(item: T) -> number",
		"get first(self) -> T",
		"set first(mut self, value: T)",
	}
	require.Len(t, iface.TypeAnn.Elems, len(expected))
	for i, want := range expected {
		got, err := PrintObjTypeAnnElem(iface.TypeAnn.Elems[i], DefaultOptions())
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

// TestPrintMemberKeepsDoc checks that a member's retained JSDoc prints
// ahead of it, so splicing a documented member does not drop the doc.
func TestPrintMemberKeepsDoc(t *testing.T) {
	t.Parallel()
	decl := parseOneDecl(t, `declare class Foo {
    /** Counts the items. */
    length: number,
}`)
	class, ok := decl.(*ast.ClassDecl)
	require.True(t, ok)
	require.Len(t, class.Body, 1)

	got, err := PrintClassElem(class.Body[0], DefaultOptions())
	require.NoError(t, err)
	require.Equal(t, "/** Counts the items. */\nlength: number", got)
}

// TestPrintMemberRejectsNil covers the nil guards: both entry points
// take an interface, so a nil member is a caller bug rather than an
// empty print.
//
// A typed nil is the case worth pinning. Every member variant is a
// pointer type, so an interface can hold a nil `*ast.FieldElem`, which
// a plain `== nil` test reports as non-nil. Without the reflect-based
// guard the printer reaches the member's Doc method and panics.
func TestPrintMemberRejectsNil(t *testing.T) {
	t.Parallel()
	classCases := []struct {
		name string
		elem ast.ClassElem
	}{
		{"untyped nil", nil},
		{"typed nil field", (*ast.FieldElem)(nil)},
		{"typed nil method", (*ast.MethodElem)(nil)},
		{"typed nil constructor", (*ast.ConstructorElem)(nil)},
	}
	for _, tc := range classCases {
		t.Run("class/"+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := PrintClassElem(tc.elem, DefaultOptions())
			require.EqualError(t, err, "cannot print a nil class member")
		})
	}

	annCases := []struct {
		name string
		elem ast.ObjTypeAnnElem
	}{
		{"untyped nil", nil},
		{"typed nil property", (*ast.PropertyTypeAnn)(nil)},
		{"typed nil method", (*ast.MethodTypeAnn)(nil)},
	}
	for _, tc := range annCases {
		t.Run("objTypeAnn/"+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := PrintObjTypeAnnElem(tc.elem, DefaultOptions())
			require.EqualError(t, err, "cannot print a nil object type member")
		})
	}
}
