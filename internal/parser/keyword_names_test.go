package parser

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// keywordTexts returns the source text of every keyword the lexer recognizes,
// sorted. The sweeps below run over this rather than a list written out by hand,
// so a keyword added to the lexer's table is covered without editing this file.
func keywordTexts() []string {
	texts := make([]string, 0, len(keywords))
	for text := range keywords {
		texts = append(texts, text)
	}
	sort.Strings(texts)
	return texts
}

// parseTypeAnnSrc parses one type annotation and returns it with the errors the
// parse reported.
func parseTypeAnnSrc(t *testing.T, src string) (ast.TypeAnn, []*Error) {
	t.Helper()
	p := NewParser(context.Background(), &ast.Source{Path: "test.esc", Contents: src})
	return p.typeAnn(), p.errors
}

// parseScriptSrc parses one script and returns its statements with the errors
// the parse reported.
func parseScriptSrc(t *testing.T, src string) (*ast.Script, []*Error) {
	t.Helper()
	p := NewParser(context.Background(), &ast.Source{Path: "test.esc", Contents: src})
	script, errors := p.ParseScript()
	return script, errors
}

// A keyword names a member of an object type the way an identifier does. The
// TypeScript lib surface relies on this for `Promise.prototype.catch`,
// `String.prototype.match`, and `Intl`'s `symbol` property, among others.
func TestKeywordsNameObjectTypeMembers(t *testing.T) {
	t.Parallel()
	forms := []struct{ name, tmpl string }{
		{"property", "{%s: number}"},
		{"optional property", "{%s?: number}"},
		{"readonly property", "{readonly %s: number}"},
		{"method", "{%s() -> number}"},
		{"getter", "{get %s() -> number}"},
		{"setter", "{set %s(v: number)}"},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			for _, keyword := range keywordTexts() {
				src := fmt.Sprintf(form.tmpl, keyword)
				typeAnn, errors := parseTypeAnnSrc(t, src)
				require.Empty(t, errors, "%s should parse", src)
				require.NotNil(t, typeAnn, "%s should produce a type annotation", src)
			}
		})
	}
}

// The same holds in a class body, which routes through parseClassElemInner
// rather than objTypeAnnElemInner and so needs its own sweep.
func TestKeywordsNameClassMembers(t *testing.T) {
	t.Parallel()
	forms := []struct{ name, tmpl string }{
		{"field", "declare class C {\n    %s: number\n}"},
		{"static field", "declare class C {\n    static %s: number\n}"},
		{"readonly field", "declare class C {\n    readonly %s: number\n}"},
		{"method", "declare class C {\n    %s(self) -> number\n}"},
		{"getter", "declare class C {\n    get %s(self) -> number\n}"},
		{"setter", "declare class C {\n    set %s(mut self, v: number)\n}"},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			t.Parallel()
			for _, keyword := range keywordTexts() {
				src := fmt.Sprintf(form.tmpl, keyword)
				_, errors := parseScriptSrc(t, src)
				require.Empty(t, errors, "%s should parse", src)
			}
		})
	}
}

// `new` is the one keyword an object type reads as a signature rather than a member name,
// because `new(…)` and `new (…)` differ only in whitespace. A property keeps the name, and a
// string key reaches the method.
func TestNewClaimsTheConstructSignatureInObjectTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want ast.ObjTypeAnnElem
	}{
		{"new opens a construct signature", "{new () -> number}", &ast.ConstructorTypeAnn{}},
		{"a string key reaches the new method", `{"new"() -> number}`, &ast.MethodTypeAnn{}},
		{"another keyword stays a method", "{catch() -> number}", &ast.MethodTypeAnn{}},
		{"new names an optional property", "{new?: number}", &ast.PropertyTypeAnn{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.src)
			require.Empty(t, errors)
			obj, ok := typeAnn.(*ast.ObjectTypeAnn)
			require.True(t, ok, "%s should be an object type", tt.src)
			require.Len(t, obj.Elems, 1)
			require.IsType(t, tt.want, obj.Elems[0])
		})
	}
}

// `fn` and `new` name methods in a class body. A class writes its construct signature as
// `constructor` and its call signature as a bare parameter list, so neither word competes with
// anything there.
func TestFnAndNewNameClassMethods(t *testing.T) {
	t.Parallel()
	for _, keyword := range []string{"fn", "new"} {
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			src := fmt.Sprintf("declare class C {\n    %s(self) -> number\n}", keyword)
			script, errors := parseScriptSrc(t, src)
			require.Empty(t, errors)
			decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.ClassDecl)
			require.Len(t, decl.Body, 1)
			method, ok := decl.Body[0].(*ast.MethodElem)
			require.True(t, ok, "%s should be a method", src)
			require.Equal(t, keyword, method.Name.(*ast.IdentExpr).Name)
		})
	}
}

// A member opening with `(` or `<` is a call signature, in a class body and in an object type
// alike. No other member may start with either token, so the parameter list alone identifies it
// and nothing has to be reserved. `constructor` keeps its name because JavaScript gives it one —
// `Foo.constructor` reaches the same member — and a call signature has no such handle.
func TestABareParameterListOpensACallSignature(t *testing.T) {
	t.Parallel()
	t.Run("InAClassBody", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			src  string
			want ast.ClassElem
		}{
			{"a parameter list opens one", "declare class C {\n    () -> number\n}", &ast.CallableElem{}},
			{"so does a type-parameter list", "declare class C {\n    <T>(v: T) -> T\n}", &ast.CallableElem{}},
			// Every word keeps its meaning as a member name, since none is reserved.
			{"fn names a method", "declare class C {\n    fn(self) -> number\n}", &ast.MethodElem{}},
			{"callable names a method", "declare class C {\n    callable(self) -> number\n}", &ast.MethodElem{}},
			{"callable names a field", "declare class C {\n    callable: number\n}", &ast.FieldElem{}},
			{"constructor still opens a constructor", "class C {\n    constructor(mut self) {}\n}", &ast.ConstructorElem{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				script, errors := parseScriptSrc(t, tt.src)
				require.Empty(t, errors)
				decl := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.ClassDecl)
				require.Len(t, decl.Body, 1)
				require.IsType(t, tt.want, decl.Body[0])
			})
		}
	})

	t.Run("InAnObjectType", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			src  string
			want ast.ObjTypeAnnElem
		}{
			{"a parameter list opens one", "{(a: number, b: string) -> boolean}", &ast.CallableTypeAnn{}},
			{"so does a type-parameter list", "{<T>(v: T) -> T}", &ast.CallableTypeAnn{}},
			{"beside an ordinary member", "{(a: number) -> boolean, tag: string}", &ast.CallableTypeAnn{}},
			// `fn` is no longer reserved here either, so it names a member like any word.
			{"fn names a method", "{fn(x: number) -> number}", &ast.MethodTypeAnn{}},
			{"fn names a property", "{fn: number}", &ast.PropertyTypeAnn{}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				typeAnn, errors := parseTypeAnnSrc(t, tt.src)
				require.Empty(t, errors)
				obj, ok := typeAnn.(*ast.ObjectTypeAnn)
				require.True(t, ok, "%s should be an object type", tt.src)
				require.IsType(t, tt.want, obj.Elems[0])
			})
		}
	})
}

// A call signature declares a shape rather than an implementation, and it is reached through
// the class value rather than an instance. Each rejection says which of the two it is.
func TestAClassCallSignatureRejectsWhatItCannotCarry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "AReceiver",
			src:  "declare class C {\n    (self) -> number\n}",
			want: "call signatures cannot have a `self` receiver",
		},
		{
			name: "ABody",
			src:  "class C {\n    () -> number { 1 }\n}",
			want: "call signatures cannot have a body",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, errors := parseScriptSrc(t, tt.src)
			require.Len(t, errors, 1)
			require.Equal(t, tt.want, errors[0].Message)
		})
	}
}
