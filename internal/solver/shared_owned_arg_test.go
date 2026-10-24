package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSharedValuePassedToOwnedParam covers a call that passes an argument to an owned
// parameter. The call moves the argument into the callee, so a shared value is rejected there,
// and an owned local is consumed by it.
func TestSharedValuePassedToOwnedParam(t *testing.T) {
	t.Parallel()

	const movedXs = "cannot move shared value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a module-level val passed to a function that upgrades it",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {
	val mut m = x
	m.push(3)
}
fn f() { take(xs) }`,
			want: []string{movedXs},
		},
		{
			name: "a module-level val passed to a function that returns it",
			src: `val xs: Array<number> = [1, 2]
fn id(x: Array<number>) -> Array<number> { return x }
fn f() {
	val mut m = id(xs)
	m.push(3)
}`,
			want: []string{movedXs},
		},
		{
			name: "a module-level val passed to a constructor that stores it",
			src: `val xs: Array<number> = [1, 2]
class Holder {
	items: Array<number>,
	constructor(&mut self, items: Array<number>) { self.items = items },
}
fn f() { val h = Holder(xs) }`,
			want: []string{movedXs},
		},
		{
			name: "a module-level val passed to a mut self method that stores it",
			src: `val xs: Array<number> = [1, 2]
class Holder {
	items: Array<number>,
	constructor(&mut self) { self.items = [] },
	set(&mut self, items: Array<number>) { self.items = items },
}
fn f() {
	val mut h = Holder()
	h.set(xs)
}`,
			want: []string{movedXs},
		},
		{
			name: "a local read from a module-level val",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {}
fn f() {
	val a = xs
	take(a)
}`,
			want: []string{"cannot move shared value 'a' into an owned parameter. Make the parameter a borrow and pass `&a`"},
		},
		{
			name: "a field of a module-level val",
			src: `val o: {items: Array<number>} = {items: [1]}
fn take(x: Array<number>) {}
fn f() { take(o.items) }`,
			want: []string{"cannot move shared value 'o.items' into an owned parameter. Make the parameter a borrow and pass `&o.items`"},
		},
		{
			name: "a call returning a module-level val",
			src: `val xs: Array<number> = [1, 2]
fn g() -> Array<number> { return xs }
fn take(x: Array<number>) {}
fn f() { take(g()) }`,
			want: []string{"cannot move a shared value into an owned parameter. Make the parameter a borrow and borrow the argument with `&`"},
		},
		{
			name: "a capture passed inside a closure",
			src: `fn take(x: Array<number>) {}
fn f() {
	val a: Array<number> = [1]
	val g = fn() { take(a) }
	g()
}`,
			want: []string{"cannot move shared value 'a' into an owned parameter. Make the parameter a borrow and pass `&a`"},
		},
		{
			name: "a module-level val passed to a rest parameter of owned elements",
			src: `val xs: Array<number> = [1, 2]
fn takeAll(...items: Array<Array<number>>) {}
fn f() { takeAll([1], xs) }`,
			want: []string{movedXs},
		},
		{
			name: "a module-level val passed to an overload arm",
			src: `val xs: Array<number> = [1, 2]
declare fn take(x: Array<number>) -> undefined
declare fn take(x: string) -> undefined
fn f() { take(xs) }`,
			want: []string{movedXs},
		},
		{
			name: "a module-level val passed twice",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {}
fn f() {
	take(xs)
	take(xs)
}`,
			want: []string{movedXs, movedXs},
		},
		{
			name: "a module-level val passed to an owned-mutable parameter",
			src: `val xs: Array<number> = [1, 2]
fn take(x: mut Array<number>) { x.push(3) }
fn f() { take(xs) }`,
			want: []string{"cannot constrain immutable Array<number> <: mutable Array<number>"},
		},
		{
			// The upgrade check reports this spelling twice. The case asserts that no
			// SharedOwnedArgError joins those reports.
			name: "a module-level val passed to an owned-mutable parameter spelled by an alias",
			src: `val xs: Array<number> = [1, 2]
type M = mut Array<number>
fn take(x: M) { x.push(3) }
fn f() { take(xs) }`,
			want: []string{
				"cannot constrain immutable Array<number> <: mutable Array<number>",
				"cannot constrain immutable Array<number> <: mutable Array<number>",
			},
		},
		{
			name: "an owned local used after the call consumes it",
			src: `fn take(x: Array<number>) {}
fn f() {
	val a: Array<number> = [1]
	take(a)
	val b = a
}`,
			want: []string{"use of moved value 'a'"},
		},
		{
			name: "an owned parameter passed on and used again",
			src: `fn take(x: Array<number>) {}
fn f(p: Array<number>) {
	take(p)
	take(p)
}`,
			want: []string{"use of moved value 'p'"},
		},
		{
			name: "a module-level val borrowed for a borrow parameter",
			src: `val xs: Array<number> = [1, 2]
fn look(x: &Array<number>) {}
fn f() { look(&xs) }`,
		},
		{
			name: "a module-level val borrowed for a borrow method parameter",
			src: `val xs: Array<number> = [1, 2]
class Reader {
	constructor(&mut self) {},
	read(&self, items: &Array<number>) {},
}
fn f() {
	val r = Reader()
	r.read(&xs)
}`,
		},
		{
			name: "a module-level number and string",
			src: `val n: number = 1
val s: string = "a"
fn take(x: number, y: string) {}
fn f() { take(n, s) }`,
		},
		{
			name: "a number read off a module-level val",
			src: `val o = {c: 5}
fn take(x: number) {}
fn f() { take(o.c) }`,
		},
		{
			name: "a fresh literal",
			src: `fn take(x: Array<number>) {}
fn f() { take([1, 2]) }`,
		},
		{
			name: "an owned local",
			src: `fn take(x: Array<number>) {
	val mut m = x
	m.push(3)
}
fn f() {
	val a: Array<number> = [1]
	take(a)
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

// TestSharedValuePassedToNpmOwnedParam covers calls into an npm package's types. A `.d.ts`
// gives a parameter no ownership meaning, so none of these calls rejects the module-level val
// it passes to a parameter the package declares owned.
func TestSharedValuePassedToNpmOwnedParam(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		"node_modules/lod/index.d.ts": `export declare function sum(xs: number[]): number;
export declare function wrap<T>(o: {items: T[]}): T;
export declare class Box {
	constructor(items: number[]);
	put(items: number[]): void;
	static of(items: number[]): Box;
}
export interface Coll { add(items: number[]): void }
export declare function make(): Coll;`,
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "a function", body: `val n = lod.sum(xs)`},
		{name: "a generic function", body: `val n = lod.wrap(o)`},
		{name: "a constructor", body: `val b = lod.Box(xs)`},
		{name: "an instance method", body: `val mut b = lod.Box([3])
	b.put(xs)`},
		{name: "a static method", body: `val b = lod.Box.of(xs)`},
		{name: "an interface method", body: `val c = lod.make()
	c.add(xs)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := `import "lod" as lod
val xs: Array<number> = [1, 2]
val o = {items: xs}
fn f() {
	` + tt.body + `
}`
			res, _ := inferAgainstNodeModules(t, src, files)
			require.Empty(t, errorMessagesOf(res.Errors))
		})
	}
}
