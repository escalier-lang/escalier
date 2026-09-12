package solver

import (
	"testing"

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

// A prelude declaring neither class leaves both names empty and reports nothing.
// The rules that read them decline, and a program that never mentions an array or
// a promise is owed no diagnostic about one.
func TestResolvePreludeClassesDeclineWithoutTheDeclarations(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{
		"std/prelude.esc": `export val unrelated: number = 1`,
	})
	require.Empty(t, c.ctx.arrayClass)
	require.Empty(t, c.ctx.promiseClass)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A tree with no prelude at all degrades the same way. The solver's own tests
// infer against no stdlib, so a report here would land on every one of them.
func TestResolvePreludeClassesDeclineWithNoPrelude(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{})
	require.Empty(t, c.ctx.arrayClass)
	require.Empty(t, c.ctx.promiseClass)
	require.Empty(t, errorMessagesOf(c.errs))
}

// A name bound as something other than a class is not one of these classes. The
// prelude seeds several opaque stubs, and a stub is nothing a rule can read an
// element or a payload off.
func TestResolvePreludeClassesDeclineANonClassBinding(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{
		"std/prelude.esc": `
			export type Array = number
			export type Promise = number
		`,
	})
	require.Empty(t, c.ctx.arrayClass)
	require.Empty(t, c.ctx.promiseClass)
}

// promiseOf declines when the run settled no `Promise`, which is what keeps the
// async rules from minting a reference to a class the tree does not declare.
func TestPromiseOfDeclinesWithNoPromiseClass(t *testing.T) {
	t.Parallel()

	c := preludeClassesOf(t, map[string]string{})
	_, resolved := c.ctx.promiseOf(&soltype.PrimType{Prim: soltype.NumPrim}, nil)
	require.False(t, resolved)
}

// An `async fn` inferred against a tree with no `Promise` reads as the body's own
// type. There is nothing to wrap the value in, and a signature naming the value is
// wrong but legible where a half-formed promise would not be.
func TestAsyncFnWithoutAPromiseClassReadsAsItsBody(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `val f = async fn () { return 5 }`, map[string]string{})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, "fn () -> 5", soltype.Print(inferredValueType(t, res.Scope, "f")))
}

// The body's raise survives the same degradation. An `async fn` normally moves it into
// the promise's rejection slot, and with no promise to hold it the signature keeps it
// rather than reading as a function that raises nothing.
func TestAsyncFnWithoutAPromiseClassKeepsItsRaise(t *testing.T) {
	t.Parallel()

	res := inferAgainstStdlib(t, `val f = async fn () { throw "boom" }`, map[string]string{})

	require.Empty(t, errorMessagesOf(res.Errors))
	require.Equal(t, `fn () -> never throws "boom"`,
		soltype.Print(inferredValueType(t, res.Scope, "f")))
}
