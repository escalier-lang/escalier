package solver

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// pushPrelude declares an `Array` whose `push` needs a mutable receiver, so a call of it
// reports whether the array it is called on was granted a mutable view.
const pushPrelude = `export declare class Array<T> { push(&mut self, value: T) -> number }
export declare class Promise<T, E = never> { then<U>(&self, f: fn (v: T) -> U) -> Promise<U, E> }`

// inferWithPushPrelude infers src against pushPrelude and the node_modules files given.
func inferWithPushPrelude(t *testing.T, src string, files map[string]string) []string {
	t.Helper()
	tree := map[string]string{"std/prelude.esc": pushPrelude}
	for path, contents := range files {
		tree[path] = contents
	}
	dir := seedStdlib(t, tree)
	res := InferModuleAgainstStdlib(parseModule(t, src), dir, Options{PackageDir: dir})
	return errorMessagesOf(res.Errors)
}

// outerMoveOf returns the OuterMoveError message for a move out of the place source names.
func outerMoveOf(source string) string {
	return (&OuterMoveError{Source: source}).Message()
}

// TestMoveOutOfModuleLevelValue covers moves inside a function body out of a module-level
// binding, and the borrows and copies that read one without moving it.
func TestMoveOutOfModuleLevelValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "bound to a mut local",
			src: `val xs: Array<number> = [1]
fn f() {
	val mut m = xs
	m.push(1)
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "bound to an annotated mut local",
			src: `val xs: Array<number> = [1]
fn f() { val m: mut Array<number> = xs }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "bound to a local",
			src: `val xs: Array<number> = [1]
fn f() {
	val a = xs
	val mut m = a
	m.push(1)
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "a field bound to a local",
			src: `val cfg: {items: Array<number>} = {items: [1]}
fn f() { val a = cfg.items }`,
			want: []string{outerMoveOf("cfg.items")},
		},
		{
			name: "an element bound to a local",
			src: `val xss: Array<Array<number>> = [[1]]
fn f() { val a = xss[0] }`,
			want: []string{outerMoveOf("xss[0]")},
		},
		{
			name: "a branch result bound to a local",
			src: `val xs: Array<number> = [1]
fn f(c: boolean) {
	val ys: Array<number> = [2]
	val a = if c { xs } else { ys }
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "a match arm result returned",
			src: `val xs: Array<number> = [1]
fn f(n: number) -> Array<number> {
	return match n {
		0 => xs,
		_ => [2],
	}
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "wrapped in an object literal",
			src: `val xs: Array<number> = [1]
fn f() { val a = {inner: xs} }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "wrapped in a tuple literal",
			src: `val xs: Array<number> = [1]
fn f() { val a = [xs] }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "spread into a literal",
			src: `val xss: Array<Array<number>> = [[1]]
fn f() { val a = [...xss] }`,
			want: []string{outerMoveOf("xss")},
		},
		{
			name: "returned",
			src: `val xs: Array<number> = [1]
fn g() -> Array<number> { return xs }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "returned as mut",
			src: `val xs: Array<number> = [1]
fn g() -> mut Array<number> { return xs }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "assigned to a local",
			src: `val xs: Array<number> = [1]
fn f() {
	var a: Array<number> = [1]
	a = xs
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "assigned to another module-level binding",
			src: `val xs: Array<number> = [1]
var ys: Array<number> = [2]
fn f() { ys = xs }`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "stored into a field",
			src: `val xs: Array<number> = [1]
fn f() {
	val o: mut {a: Array<number>} = {a: [1]}
	o.a = xs
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "destructured into an owned leaf",
			src: `val cfg: {items: Array<number>, n: number} = {items: [1], n: 1}
fn f() { val {items} = cfg }`,
			want: []string{outerMoveOf("cfg")},
		},
		{
			name: "stored into a capture by a closure",
			src: `val xs: Array<number> = [1]
fn f() {
	var a: Array<number> = [1]
	val g = fn() { a = xs }
	g()
}`,
			want: []string{outerMoveOf("xs")},
		},
		{
			name: "borrowed",
			src: `val xs: Array<number> = [1]
fn f() { val a = &xs }`,
		},
		{
			name: "returned as a borrow",
			src: `val xs: Array<number> = [1]
fn get() -> &Array<number> { return xs }`,
		},
		{
			name: "stored into a borrow field",
			src: `val xs: Array<number> = [1]
fn f() {
	val o: mut {a: &Array<number>} = {a: &xs}
	o.a = xs
}`,
		},
		{
			name: "bound to an annotated borrow",
			src: `val xs: Array<number> = [1]
fn f() { val a: &Array<number> = xs }`,
		},
		{
			name: "a number element copied",
			src: `val xs: Array<number> = [1]
fn f() { val n = xs[0] }`,
		},
		{
			name: "a number field copied",
			src: `val o = {c: 5}
fn f() {
	val n = o.c
	val a = {c: n}
	val mut m = a
	m.c = 3
}`,
		},
		{
			name: "a spread of numbers copied",
			src: `val xs: Array<number> = [1]
fn f() {
	val a: Array<number> = [...xs]
	val mut m = a
	m.push(1)
}`,
		},
		{
			name: "destructured into a copied leaf",
			src: `val cfg: {items: Array<number>, n: number} = {items: [1], n: 1}
fn f() { val {n} = cfg }`,
		},
		{
			name: "a fresh literal",
			src:  `fn f() { val m: mut Array<number> = [1] }`,
		},
		{
			name: "an owned parameter",
			src: `fn f(p: Array<number>) {
	val a = p
	val mut m = a
	m.push(1)
}`,
		},
		{
			name: "a call of a function that builds a fresh value",
			src: `fn make() -> Array<number> {
	val a: Array<number> = [1]
	return a
}
fn h() {
	val mut m = make()
	m.push(1)
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			got := errorMessagesOf(errs)
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// TestClosureMovesCapture covers closures that move a local they capture. The closure takes the
// local where it is written, and a call of the closure moves the binding holding it.
func TestClosureMovesCapture(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "the local used after the closure is written",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val n = a
}`,
			want: []string{"use of moved value 'a'"},
		},
		{
			name: "the closure called twice",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val m = g()
	val n = g()
}`,
			want: []string{"use of moved value 'g'"},
		},
		{
			name: "the closure called in a loop",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() { val b = a }
	for i in [1, 2] { g() }
}`,
			want: []string{"use of moved value 'g'"},
		},
		{
			name: "a nested closure moving a capture of its enclosing closure",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() {
		val h = fn() { val b = a }
	}
	val n = a
}`,
			want: []string{"use of moved value 'a'"},
		},
		{
			name: "one capture stored into another",
			src: `fn f() {
	var a: Array<number> = [1]
	val b: Array<number> = [2]
	val g = fn() { a = b }
	val n = b
}`,
			want: []string{"use of moved value 'b'"},
		},
		{
			name: "the closure called once and its result mutated",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val mut m = g()
	m.push(1)
}`,
		},
		{
			name: "a closure that borrows its capture",
			src: `fn f() {
	val a: Array<number> = [1]
	val g = fn() { val b = &a }
	g()
	g()
	val n = a
}`,
		},
		{
			name: "a fresh literal stored into a capture",
			src: `fn f() {
	var a: Array<number> = [1]
	val g = fn() { a = [2] }
	g()
	g()
	val mut m = a
	m.push(1)
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			got := errorMessagesOf(errs)
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}

// TestScriptMovesLibraryValue asserts that a script function moving a value its library exports
// reports an OuterMoveError.
func TestScriptMovesLibraryValue(t *testing.T) {
	ctx := context.Background()
	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val xs: Array<number> = [1]`}
	module, libParseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, libParseErrors)
	scriptSource := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: `fn h() { return xs }`}
	script, scriptParseErrors := parser.NewParser(ctx, scriptSource).ParseScript()
	require.Empty(t, scriptParseErrors)
	registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, scriptSource.ID: scriptSource})

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)

	_, _, errs := InferScriptInLib(script, lib)
	require.Equal(t, []string{outerMoveOf("xs")}, errorMessagesOf(errs))
}
