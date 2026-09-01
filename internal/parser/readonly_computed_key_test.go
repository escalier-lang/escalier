package parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// `readonly [` opens a mapped type and a computed-key property alike, and only what
// follows the brackets tells the two apart. The modifier has to survive the attempt to
// read the member as a mapped type, so assert it on the parsed AST rather than through
// the printer.
func TestReadonlyOnComputedKeyInterfaceMember(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		src      string
		readonly bool
	}{
		{"computed key", "{readonly [Symbol.toStringTag]: string}", true},
		{"computed key, optional", "{readonly [Symbol.toStringTag]?: string}", true},
		{"computed key, no modifier", "{[Symbol.toStringTag]: string}", false},
		{"named key", "{readonly bar: string}", true},
		{"named key, no modifier", "{bar: string}", false},
		{"string key", `{readonly "bar": string}`, true},
		{"computed key on a method's own type", "{readonly [Symbol.species]: fn () -> number}", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.src)
			require.Empty(t, errors)
			obj, ok := typeAnn.(*ast.ObjectTypeAnn)
			require.True(t, ok, "%s should be an object type", tt.src)
			require.Len(t, obj.Elems, 1)
			prop, ok := obj.Elems[0].(*ast.PropertyTypeAnn)
			require.True(t, ok, "%s should be a property", tt.src)
			require.Equal(t, tt.readonly, prop.Readonly)
		})
	}
}

// A computed key and a named key in the same interface body each keep their own
// modifier, and reading the computed member leaves the members after it alone.
func TestReadonlyOnMixedInterfaceMembers(t *testing.T) {
	t.Parallel()
	src := "declare interface Foo {\n" +
		"    readonly [Symbol.toStringTag]: string,\n" +
		"    readonly bar: string,\n" +
		"    baz: string,\n" +
		"}"
	script, errors := parseScriptSrc(t, src)
	require.Empty(t, errors)
	decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.InterfaceDecl)
	require.Len(t, decl.TypeAnn.Elems, 3)

	computed := decl.TypeAnn.Elems[0].(*ast.PropertyTypeAnn)
	require.IsType(t, &ast.ComputedKey{}, computed.Name)
	require.True(t, computed.Readonly)

	named := decl.TypeAnn.Elems[1].(*ast.PropertyTypeAnn)
	require.True(t, named.Readonly)

	plain := decl.TypeAnn.Elems[2].(*ast.PropertyTypeAnn)
	require.False(t, plain.Readonly)
}

// A mapped type is told from a computed-key property by the `for K in Keys` clause that
// closes it. Without that clause the brackets name a computed key, and the member that
// follows has to stay a member of its own rather than be read as the key variable.
func TestBracketWithoutForClauseIsAComputedKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
	}{
		{"readonly, named member next", "{readonly [Symbol.toStringTag]: string, baz: string}"},
		{"no modifier, named member next", "{[Symbol.toStringTag]: string, baz: string}"},
		{"readonly, keyword-named member next", "{readonly [Symbol.toStringTag]: string, catch: string}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.src)
			require.Empty(t, errors)
			obj, ok := typeAnn.(*ast.ObjectTypeAnn)
			require.True(t, ok, "%s should be an object type", tt.src)
			require.Len(t, obj.Elems, 2)
			require.IsType(t, &ast.PropertyTypeAnn{}, obj.Elems[0])
			require.IsType(t, &ast.PropertyTypeAnn{}, obj.Elems[1])
		})
	}
}

// A class body reaches the same members through parseClassElemInner, so it needs its
// own assertions to keep the two paths in agreement.
func TestReadonlyOnComputedKeyClassField(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		member   string
		readonly bool
	}{
		{"computed key", "readonly [Symbol.toStringTag]: string", true},
		{"computed key, no modifier", "[Symbol.toStringTag]: string", false},
		{"named key", "readonly bar: string", true},
		{"named key, no modifier", "bar: string", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := "declare class C {\n    " + tt.member + "\n}"
			script, errors := parseScriptSrc(t, src)
			require.Empty(t, errors)
			decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.ClassDecl)
			require.Len(t, decl.Body, 1)
			field, ok := decl.Body[0].(*ast.FieldElem)
			require.True(t, ok, "%s should be a field", src)
			require.Equal(t, tt.readonly, field.Readonly)
		})
	}
}

// The mapped-type reading of `readonly [` still wins where a mapped type follows.
func TestReadonlyBracketStillOpensMappedTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want ast.MappedModifier
	}{
		{"key remapping", "{readonly [K]: T[K] for K in keyof T}", ast.MMAdd},
		{"index signature shorthand", "{readonly [K: keyof T]: T[K]}", ast.MMAdd},
		{"added modifier", "{+readonly [K]: T[K] for K in keyof T}", ast.MMAdd},
		{"removed modifier", "{-readonly [K]: T[K] for K in keyof T}", ast.MMRemove},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.src)
			require.Empty(t, errors)
			obj, ok := typeAnn.(*ast.ObjectTypeAnn)
			require.True(t, ok, "%s should be an object type", tt.src)
			require.Len(t, obj.Elems, 1)
			mapped, ok := obj.Elems[0].(*ast.MappedTypeAnn)
			require.True(t, ok, "%s should be a mapped type", tt.src)
			require.NotNil(t, mapped.ReadOnly)
			require.Equal(t, tt.want, *mapped.ReadOnly)
		})
	}
}
