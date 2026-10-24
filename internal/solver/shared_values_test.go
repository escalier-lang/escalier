package solver

import (
	"testing"

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

// TestSharedValueIsNotUpgradedToMut covers values a binding outside the function frame also
// reaches. Moving one into a local leaves the outer binding holding the same object, so
// neither a local read from it nor a call returning it may be granted a mutable view.
func TestSharedValueIsNotUpgradedToMut(t *testing.T) {
	t.Parallel()

	const rejected = "cannot constrain immutable Array<number> <: mutable Array<number>"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a module-level val read directly",
			src: `val xs: Array<number> = [1]
fn f() {
	val mut m = xs
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a module-level val bound to an annotated mut local",
			src: `val xs: Array<number> = [1]
fn f() { val m: mut Array<number> = xs }`,
			want: []string{rejected},
		},
		{
			name: "a module-level val read through a local",
			src: `val xs: Array<number> = [1]
fn f() {
	val a = xs
	val mut m = a
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a module-level val wrapped in a local literal",
			src: `val xs: Array<number> = [1]
fn f() {
	val a = {inner: xs}
	val mut m = a.inner
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a module-level val assigned to a local in a loop",
			src: `val xs: Array<number> = [1]
fn f() {
	var a: Array<number> = [1]
	for i in [1, 2] {
		val mut m = a
		m.push(1)
		a = xs
	}
}`,
			want: []string{rejected, "use of moved value 'a'", "use of moved value 'xs'"},
		},
		{
			name: "an element of a module-level tuple bound by a loop",
			src: `val xs: Array<number> = [1]
val xss = [xs, xs]
fn f() {
	for x in xss {
		val mut m = x
		m.push(1)
	}
}`,
			want: []string{rejected, "use of moved value 'x'"},
		},
		{
			name: "a call of a function that returns a module-level val",
			src: `val xs: Array<number> = [1]
fn g() -> Array<number> { return xs }
fn h() {
	val mut m = g()
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a call of an unannotated function that returns a module-level val",
			src: `val xs: Array<number> = [1]
fn g() { return xs }
fn h() {
	val mut m = g()
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a call result bound to a local first",
			src: `val xs: Array<number> = [1]
fn g() -> Array<number> { return xs }
fn h() {
	val b = g()
	val mut m = b
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a call bound to an annotated mut local",
			src: `val xs: Array<number> = [1]
fn g() -> Array<number> { return xs }
fn h() { val m: mut Array<number> = g() }`,
			want: []string{rejected},
		},
		{
			name: "a module-level val returned as mut",
			src: `val xs: Array<number> = [1]
fn g() -> mut Array<number> { return xs }`,
			want: []string{rejected},
		},
		{
			name: "a closure returning a local it captures",
			src: `fn h() {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val mut m = g()
	m.push(1)
}`,
			want: []string{rejected},
		},
		{
			name: "a fresh literal",
			src:  `fn f() { val m: mut Array<number> = [1] }`,
		},
		{
			name: "a local built from a fresh literal",
			src: `fn f() {
	val a: Array<number> = [1]
	val mut m = a
	m.push(1)
}`,
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
		{
			name: "a call of a function that copies a module-level number into a literal",
			src: `val o = {c: 5}
fn make() {
	val a = o.c
	return {c: a}
}
fn h() {
	val mut m = make()
	m.c = 2
}`,
		},
		{
			name: "a constructor call",
			src: `class Counter {
	count: number,
	constructor(&mut self, count: number) { self.count = count },
	inc(&mut self) -> number { return self.count },
}
fn h() {
	val mut c = Counter(0)
	c.inc()
}`,
		},
		{
			name: "a call of a function that returns a constructed instance",
			src: `class Counter {
	count: number,
	constructor(&mut self, count: number) { self.count = count },
	inc(&mut self) -> number { return self.count },
}
fn make() { return Counter(0) }
fn h() {
	val mut c = make()
	c.inc()
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := inferWithPushPrelude(t, tt.src, nil)
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}
}
