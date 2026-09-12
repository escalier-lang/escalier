package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// preludeClassesOf settles the classes the rules single out over a seeded
// pseudo-package tree and returns the checker that recorded them. Each name is
// empty when the tree declares no such class.
func preludeClassesOf(t *testing.T, files map[string]string) *checker {
	t.Helper()
	c := newChecker()
	c.source = StdlibSource(seedStdlib(t, files))
	c.preludeScope()
	return c
}

// Each class is read from the prelude package, so a rule reaches it with nothing
// imported. What is recorded is the registry key rather than the written name,
// since two packages may each declare an `Array` and the rules compare against
// one of them.
func TestResolvePreludeClassesNeedNoImport(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{
		"std/prelude.esc": `
			export declare class Array<T> {
				at(self, index: number) -> T | undefined,
			}
			export declare class Promise<T, E = never> {
				catch<U>(self, onrejected: fn (reason: E) -> U) -> Promise<T | U>,
			}
		`,
	})
	require.Equal(t, "import:std:prelude.Array", c.ctx.arrayClass)
	require.Equal(t, "import:std:prelude.Promise", c.ctx.promiseClass)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A tree that does not declare a class the rules name is a broken standard library
// rather than a configuration to degrade into, so the run reports it once. The rows
// cover the three ways a declaration falls short of what the rules read.
func TestAPreludeMissingAClassIsReported(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name:  "NoPreludeAtAll",
			files: map[string]string{},
			want: []string{
				"the standard library declares no class `Array`, which the checker needs",
				"the standard library declares no class `Promise`, which the checker needs",
			},
		},
		{
			name:  "APreludeDeclaringNeither",
			files: map[string]string{"std/prelude.esc": `export val unrelated: number = 1`},
			want: []string{
				"the standard library declares no class `Array`, which the checker needs",
				"the standard library declares no class `Promise`, which the checker needs",
			},
		},
		{
			// A stub is nothing a rule can read an element or a payload off, so a name
			// bound to one is as good as absent.
			name: "ANameBoundToSomethingOtherThanAClass",
			files: map[string]string{"std/prelude.esc": `
				export type Array = number
				export type Promise = number
			`},
			want: []string{
				"the standard library binds `Array` to something other than a class, so the checker cannot read it",
				"the standard library binds `Promise` to something other than a class, so the checker cannot read it",
			},
		},
		{
			// The rules read `Array<T>` and `Promise<T, E>`, so a class declaring another
			// count answers a different question than the one they ask.
			name: "AClassOfTheWrongArity",
			files: map[string]string{"std/prelude.esc": `
				export declare class Array<T, U> { at(self, index: number) -> T, tag(self) -> U }
				export declare class Promise<T> { then<U>(self, f: fn (v: T) -> U) -> Promise<U> }
			`},
			want: []string{
				"the standard library declares `Array` with 2 type parameter(s), but the checker reads 1",
				"the standard library declares `Promise` with 1 type parameter(s), but the checker reads 2",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := preludeClassesOf(t, tt.files)
			require.Equal(t, tt.want, errorMessagesOf(c.errs))
			require.Empty(t, c.ctx.arrayClass)
			require.Empty(t, c.ctx.promiseClass)
		})
	}
}

// The report names the standard library rather than a line in the file under inference,
// since the fault is upstream of anything that file wrote.
func TestAMissingPreludeClassBlamesNoSpan(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{})
	require.NotEmpty(t, c.errs)
	require.Equal(t, ast.Span{}, c.errs[0].Span())
	require.Empty(t, c.errs[0].Related())
}

// arrayForPromiseTests is the `Array` a tree needs beside the `Promise` under test, so
// the run reports nothing about a class these rows are not about.
const arrayForPromiseTests = `
export declare class Array<T> { at(self, index: number) -> T | undefined }
`

// promiseOf carries the declaration's own parameter defaults, so what the printer elides
// is what a reference omitting the argument resolves to. A run whose `Promise` takes
// other than two parameters is not the shape the async rules build, so it resolves
// nothing and an `async fn` degrades the way it does with no `Promise` at all.
func TestPromiseOfFollowsTheDeclaredShape(t *testing.T) {
	tests := []struct {
		name string
		decl string
		want string
	}{
		{
			// The rejection slot defaults to `never` and holds `never`, an absence the
			// reader is not owed.
			name: "AnEmptySlotDefaultingToNeverIsElided",
			decl: `export declare class Promise<T, E = never> { then<U>(self, f: fn (v: T) -> U) -> Promise<U, E> }`,
			want: "fn () -> Promise<1>",
		},
		{
			// `unknown` is what a reference omitting the argument would resolve to, so
			// hiding the `never` this promise actually carries would misreport it.
			name: "ASlotDefaultingToSomethingElseIsShown",
			decl: `export declare class Promise<T, E = unknown> { then<U>(self, f: fn (v: T) -> U) -> Promise<U, E> }`,
			want: "fn () -> Promise<1, never>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := inferAgainstExactStdlib(t, `val f = async fn () { return 1 }`,
				map[string]string{"std/prelude.esc": tt.decl + arrayForPromiseTests})

			require.Empty(t, errorMessagesOf(res.Errors))
			require.Equal(t, tt.want, soltype.Print(inferredValueType(t, res.Scope, "f")))
		})
	}
}
