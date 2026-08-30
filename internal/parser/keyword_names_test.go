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

// `fn` and `new` are the two keywords an object type reads as a signature rather
// than a member name, because `fn(…)` and `fn (…)` differ only in whitespace. A
// property keeps the name, and a string key reaches the method.
func TestFnAndNewClaimSignaturesInObjectTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want ast.ObjTypeAnnElem
	}{
		{"fn opens a call signature", "{fn () -> number}", &ast.CallableTypeAnn{}},
		{"fn without a space is still a call signature", "{fn() -> number}", &ast.CallableTypeAnn{}},
		{"new opens a construct signature", "{new () -> number}", &ast.ConstructorTypeAnn{}},
		{"a string key reaches the fn method", `{"fn"() -> number}`, &ast.MethodTypeAnn{}},
		{"a string key reaches the new method", `{"new"() -> number}`, &ast.MethodTypeAnn{}},
		{"another keyword stays a method", "{catch() -> number}", &ast.MethodTypeAnn{}},
		{"fn names a property", "{fn: number}", &ast.PropertyTypeAnn{}},
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

// A class has no call or construct signature to compete with, so `fn` and `new`
// name methods there. This asymmetry with an object type is deliberate.
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

// cannotBind lists the keywords that must never become a binding name. It is
// written out rather than derived from bindingKeywords so the sweeps below have
// an oracle independent of the code under test. Widening bindingKeywords by
// mistake then fails here instead of moving both sides of the comparison
// together.
//
// The first 28 are the words ECMAScript reserves, which codegen cannot emit as
// an identifier: `fn f(in: number)` would lower to `const in = ...`. `undefined`
// is here for a different reason. It is not reserved, but a pattern reads it as
// the value it names, and a binding name must not shift meaning with position.
func cannotBind() map[string]bool {
	words := []string{
		"await", "catch", "class", "do", "else", "enum", "export", "extends",
		"false", "for", "if", "implements", "import", "in", "interface", "new",
		"null", "private", "return", "static", "super", "throw", "true", "try",
		"typeof", "var", "void", "yield",
		"undefined",
	}
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}

// Every keyword the lexer knows is either a binding name or one of the words
// above. A keyword added to the lexer without a decision about which it is
// fails here, which is the reminder to make that decision.
func TestEveryKeywordIsClassifiedForBinding(t *testing.T) {
	t.Parallel()
	reserved := cannotBind()
	for _, keyword := range keywordTexts() {
		require.Equal(t, !reserved[keyword], bindsAsAName(keywords[keyword]),
			"%q: bindingKeywords and the reserved list disagree; add %q to one of them",
			keyword, keyword)
	}
}

// A binding name reaches JavaScript verbatim, so only the keywords JavaScript
// itself accepts as an identifier may name a parameter or a function.
//
// The negative direction checks the parsed pattern rather than the presence of
// an error. `true` and the other literal keywords take the literal-pattern path
// instead of failing outright, and what matters is that neither becomes a name.
func TestKeywordsInBindingPositions(t *testing.T) {
	t.Parallel()
	reserved := cannotBind()

	t.Run("parameter name", func(t *testing.T) {
		t.Parallel()
		for _, keyword := range keywordTexts() {
			src := fmt.Sprintf("declare fn f(%s: number) -> number", keyword)
			script, errors := parseScriptSrc(t, src)
			bound := false
			if len(errors) == 0 {
				fn := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.FuncDecl)
				if identPat, ok := fn.Params[0].Pattern.(*ast.IdentPat); ok {
					bound = identPat.Name == keyword
				}
			}
			require.Equal(t, !reserved[keyword], bound,
				"%s: bound as a parameter name = %v", src, bound)
		}
	})

	t.Run("rest parameter name", func(t *testing.T) {
		t.Parallel()
		for _, keyword := range keywordTexts() {
			src := fmt.Sprintf("declare fn f(...%s: Array<number>) -> number", keyword)
			script, errors := parseScriptSrc(t, src)
			bound := false
			if len(errors) == 0 {
				fn := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.FuncDecl)
				if rest, ok := fn.Params[0].Pattern.(*ast.RestPat); ok {
					if identPat, isIdent := rest.Pattern.(*ast.IdentPat); isIdent {
						bound = identPat.Name == keyword
					}
				}
			}
			require.Equal(t, !reserved[keyword], bound,
				"%s: bound as a rest parameter name = %v", src, bound)
		}
	})

	t.Run("function declaration name", func(t *testing.T) {
		t.Parallel()
		for _, keyword := range keywordTexts() {
			src := fmt.Sprintf("declare fn %s() -> number", keyword)
			script, errors := parseScriptSrc(t, src)
			named := false
			if len(errors) == 0 {
				fn := script.Stmts[0].(*ast.DeclStmt).Decl.(*ast.FuncDecl)
				named = fn.Name.Name == keyword
			}
			require.Equal(t, !reserved[keyword], named,
				"%s: used as the function name = %v", src, named)
		}
	})
}

// A shorthand property is a key and a variable reference at once, so it accepts
// exactly the keywords a binding does. The keyed form accepts every keyword,
// since `{ catch: 1 }` is valid JavaScript.
func TestKeywordShorthandProperties(t *testing.T) {
	t.Parallel()
	reserved := cannotBind()

	t.Run("shorthand takes only binding names", func(t *testing.T) {
		t.Parallel()
		for _, keyword := range keywordTexts() {
			src := fmt.Sprintf("val a = {%s}", keyword)
			_, errors := parseScriptSrc(t, src)
			if !reserved[keyword] {
				require.Empty(t, errors, "%s should parse", src)
				continue
			}
			require.NotEmpty(t, errors, "%s should report", src)
			require.Equal(t, "`"+keyword+
				"` cannot be a shorthand property because it is not a variable name",
				errors[0].Message)
		}
	})

	t.Run("a keyed property takes every keyword", func(t *testing.T) {
		t.Parallel()
		for _, keyword := range keywordTexts() {
			src := fmt.Sprintf("val a = {%s: 1}", keyword)
			_, errors := parseScriptSrc(t, src)
			require.Empty(t, errors, "%s should parse", src)
		}
	})
}

// `get` and `set` mark an accessor only when a name follows them, so the method
// shorthand an object literal rejects stays rejected while the property does not.
func TestGetAndSetInObjectLiterals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"get names a property", "val a = {get: 1}", ""},
		{"set names a property", "val a = {set: 1}", ""},
		{"get is a shorthand property", "val a = {get}", ""},
		{"a getter is still rejected", "val a = {get x() {}}",
			"Method shorthand is not allowed in object literals; use a class instead"},
		{"a setter is still rejected", "val a = {set x(v) {}}",
			"Method shorthand is not allowed in object literals; use a class instead"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, errors := parseScriptSrc(t, tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errors)
				return
			}
			require.NotEmpty(t, errors)
			require.Equal(t, tt.wantErr, errors[0].Message)
		})
	}
}
