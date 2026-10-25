package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestModuleLevelValuePassedToOwnedParam covers a call that passes an argument to an owned
// parameter. The call moves the argument into the callee, so a module-level value is rejected
// there, and an owned local is consumed by it.
func TestModuleLevelValuePassedToOwnedParam(t *testing.T) {
	t.Parallel()

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
			want: []string{"6:15-6:17: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed to a function that returns it",
			src: `val xs: Array<number> = [1, 2]
fn id(x: Array<number>) -> Array<number> { return x }
fn f() {
	val mut m = id(xs)
	m.push(3)
}`,
			want: []string{"4:17-4:19: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed to a constructor that stores it",
			src: `val xs: Array<number> = [1, 2]
class Holder {
	items: Array<number>,
	constructor(&mut self, items: Array<number>) { self.items = items },
}
fn f() { val h = Holder(xs) }`,
			want: []string{"6:25-6:27: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
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
			want: []string{"9:8-9:10: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed through an if branch",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {
	val mut m = x
	m.push(3)
}
fn f(c: boolean) { take(if c { xs } else { [1] }) }`,
			want: []string{"6:32-6:34: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a local read from a module-level val",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {}
fn f() {
	val a = xs
	take(a)
}`,
			want: []string{"4:10-4:12: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "a field of a module-level val",
			src: `val o: {items: Array<number>} = {items: [1]}
fn take(x: Array<number>) {}
fn f() { take(o.items) }`,
			want: []string{"3:15-3:22: cannot move module-level value 'o.items' into an owned parameter. Make the parameter a borrow and pass `&o.items`"},
		},
		{
			name: "a call returning a module-level val",
			src: `val xs: Array<number> = [1, 2]
fn g() -> Array<number> { return xs }
fn take(x: Array<number>) {}
fn f() { take(g()) }`,
			want: []string{"2:34-2:36: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "a capture passed inside a closure",
			src: `fn take(x: Array<number>) {}
fn f() {
	val a: Array<number> = [1]
	val g = fn() { take(a) }
	g()
	val b = a
}`,
			want: []string{"6:10-6:11: use of moved value 'a'"},
		},
		{
			name: "a module-level val passed to a rest parameter of owned elements",
			src: `val xs: Array<number> = [1, 2]
fn takeAll(...items: Array<Array<number>>) {}
fn f() { takeAll([1], xs) }`,
			want: []string{"3:23-3:25: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed to an overload arm",
			src: `val xs: Array<number> = [1, 2]
declare fn take(x: Array<number>) -> undefined
declare fn take(x: string) -> undefined
fn f() { take(xs) }`,
			want: []string{"4:15-4:17: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed twice",
			src: `val xs: Array<number> = [1, 2]
fn take(x: Array<number>) {}
fn f() {
	take(xs)
	take(xs)
}`,
			want: []string{"4:7-4:9: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`", "5:7-5:9: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "a module-level val passed to an owned-mutable parameter",
			src: `val xs: Array<number> = [1, 2]
fn take(x: mut Array<number>) { x.push(3) }
fn f() { take(xs) }`,
			want: []string{"3:15-3:17: cannot move module-level value 'xs' into an owned parameter. Make the parameter a borrow and pass `&xs`"},
		},
		{
			name: "an owned local used after the call consumes it",
			src: `fn take(x: Array<number>) {}
fn f() {
	val a: Array<number> = [1]
	take(a)
	val b = a
}`,
			want: []string{"5:10-5:11: use of moved value 'a'"},
		},
		{
			name: "an owned parameter passed on and used again",
			src: `fn take(x: Array<number>) {}
fn f(p: Array<number>) {
	take(p)
	take(p)
}`,
			want: []string{"4:7-4:8: use of moved value 'p'"},
		},
		{
			name: "a module-level val borrowed for a borrow parameter",
			src: `val xs: Array<number> = [1, 2]
fn look(x: &Array<number>) {}
fn f() { look(&xs) }`,
			want: nil,
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
			want: nil,
		},
		{
			name: "a module-level number and string",
			src: `val n: number = 1
val s: string = "a"
fn take(x: number, y: string) {}
fn f() { take(n, s) }`,
			want: nil,
		},
		{
			name: "a number read off a module-level val",
			src: `val o = {c: 5}
fn take(x: number) {}
fn f() { take(o.c) }`,
			want: nil,
		},
		{
			name: "a fresh literal",
			src: `fn take(x: Array<number>) {}
fn f() { take([1, 2]) }`,
			want: nil,
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
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, inferWithPushPrelude(t, tt.src, nil))
		})
	}
}

// TestModuleLevelValuePassedToNpmOwnedParam covers calls into an npm package's types. A
// `.d.ts` gives a parameter no ownership meaning, so none of these calls moves the
// module-level val it passes to a parameter the package declares owned.
func TestModuleLevelValuePassedToNpmOwnedParam(t *testing.T) {
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
		{name: "a function bound to a local", body: `val s = lod.sum
	val n = s(xs)`},
		{name: "a generic function", body: `val n = lod.wrap(o)`},
		{name: "a constructor", body: `val b = lod.Box(xs)`},
		{name: "an instance method", body: `val mut b = lod.Box([3])
	b.put(xs)`},
		{name: "a static method", body: `val b = lod.Box.of(xs)`},
		{name: "a static method read by index", body: `val b = lod.Box["of"](xs)`},
		{name: "an instance method read by index", body: `val mut b = lod.Box([3])
	b["put"](xs)`},
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
			require.Empty(t, messagesWithSpan(t, res.Errors))
		})
	}
}

// TestScriptValuePassedToOwnedParam covers a script's top-level value passed to an owned
// parameter. A script's top-level bindings are locals, so a closure that passes one to an
// owned parameter takes it when the closure is created rather than reporting a
// ModuleLevelMoveError.
func TestScriptValuePassedToOwnedParam(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a closure passing the value to an owned param",
			src: `val ys: Array<number> = [1]
fn take(x: Array<number>) {}
val g = fn() { take(ys) }
g()`,
			want: nil,
		},
		{
			name: "a use of the value after a closure takes it",
			src: `val ys: Array<number> = [1]
fn take(x: Array<number>) {}
val g = fn() { take(ys) }
g()
ys`,
			want: []string{"5:1-5:3: use of moved value 'ys'"},
		},
		{
			name: "a second call of a closure that moves the value",
			src: `val ys: Array<number> = [1]
fn take(x: Array<number>) {}
val g = fn() { take(ys) }
g()
g()`,
			want: []string{"5:1-5:2: use of moved value 'g'"},
		},
		{
			name: "a closure passing a borrow of the value",
			src: `val ys: Array<number> = [1]
fn read(x: &Array<number>) -> number { return x.length }
val g = fn() -> number { return read(&ys) }
g()
g()
ys`,
			want: nil,
		},
		{
			name: "a use of the value after a top-level call takes it",
			src: `val ys: Array<number> = [1]
fn take(x: Array<number>) {}
take(ys)
ys`,
			want: []string{"4:1-4:3: use of moved value 'ys'"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferScriptSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
