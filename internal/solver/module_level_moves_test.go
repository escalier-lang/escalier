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
	return messagesWithSpan(t, res.Errors)
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
fn f() -> undefined {
	val mut m = xs
	m.push(1)
}`,
			want: []string{"3:14-3:16: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "bound to an annotated mut local",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val mut m: Array<number> = xs }`,
			want: []string{"2:50-2:52: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "bound to a local",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	val a = xs
	val mut m = a
	m.push(1)
}`,
			want: []string{"3:10-3:12: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "a field bound to a local",
			src: `val cfg: {items: Array<number>} = {items: [1]}
fn f() -> undefined { val a = cfg.items }`,
			want: []string{"2:31-2:40: cannot move module-level value 'cfg.items' inside a function. Borrow it with `&cfg.items`"},
		},
		{
			name: "an element bound to a local",
			src: `val xss: Array<Array<number>> = [[1]]
fn f() -> undefined { val a = xss[0] }`,
			want: []string{"2:31-2:37: cannot move module-level value 'xss[0]' inside a function. Borrow it with `&xss[0]`"},
		},
		{
			name: "a branch result bound to a local",
			src: `val xs: Array<number> = [1]
fn f(c: boolean) -> undefined {
	val ys: Array<number> = [2]
	val a = if c { xs } else { ys }
}`,
			want: []string{"4:17-4:19: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
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
			want: []string{"4:8-4:10: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "wrapped in an object literal",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val a = {inner: xs} }`,
			want: []string{"2:39-2:41: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "wrapped in a tuple literal",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val a = [xs] }`,
			want: []string{"2:32-2:34: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "spread into a literal",
			src: `val xss: Array<Array<number>> = [[1]]
fn f() -> undefined { val a = [...xss] }`,
			want: []string{"2:35-2:38: cannot move module-level value 'xss' inside a function. Borrow it with `&xss`"},
		},
		{
			// Two leaves move out of one source, which is reported once.
			name: "destructured into two owned leaves",
			src: `val t: [Array<number>, Array<number>] = [[1], [2]]
fn f() -> undefined { val [a, b] = t }`,
			want: []string{"2:36-2:37: cannot move module-level value 't' inside a function. Borrow it with `&t`"},
		},
		{
			name: "returned",
			src: `val xs: Array<number> = [1]
fn g() -> Array<number> { return xs }`,
			want: []string{"2:34-2:36: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "assigned to a local",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	var a: Array<number> = [1]
	a = xs
}`,
			want: []string{"4:6-4:8: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "assigned to another module-level binding",
			src: `val xs: Array<number> = [1]
var ys: Array<number> = [2]
fn f() -> undefined { ys = xs }`,
			want: []string{"3:28-3:30: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "stored into a field",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	val mut o: {a: Array<number>} = {a: [1]}
	o.a = xs
}`,
			want: []string{"4:8-4:10: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "stored into an element",
			src: `val xs: Array<number> = [1]
fn f(i: number) -> undefined {
	val mut a: Array<Array<number>> = [[1]]
	a[i] = xs
}`,
			want: []string{"4:9-4:11: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "stored through a setter",
			src: `val xs: Array<number> = [1]
class Box {
	items: Array<number>,
	constructor(&mut self) { self.items = [] },
	set all(&mut self, v: Array<number>) { self.items = v },
}
fn f() -> undefined {
	val mut b = Box()
	b.all = xs
}`,
			want: []string{"9:10-9:12: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "a tuple of arrays spread into a literal",
			src: `val t: [Array<number>, Array<number>] = [[1], [2]]
fn f() -> undefined { val a = [...t] }`,
			want: []string{"2:35-2:36: cannot move module-level value 't' inside a function. Borrow it with `&t`"},
		},
		{
			name: "destructured into an owned leaf",
			src: `val cfg: {items: Array<number>, n: number} = {items: [1], n: 1}
fn f() -> undefined { val {items} = cfg }`,
			want: []string{"2:37-2:40: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			name: "stored into a capture by a closure",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	var a: Array<number> = [1]
	val g = fn() { a = xs }
	g()
}`,
			want: []string{"4:21-4:23: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			name: "borrowed",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val a = &xs }`,
		},
		{
			name: "returned as a borrow",
			src: `val xs: Array<number> = [1]
fn get() -> &Array<number> { return &xs }`,
		},
		{
			name: "stored into a borrow field",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	val mut o: {a: &Array<number>} = {a: &xs}
	o.a = &xs
}`,
		},
		{
			name: "bound to an annotated borrow",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val a: &Array<number> = &xs }`,
		},
		{
			name: "a number element copied",
			src: `val xs: Array<number> = [1]
fn f() -> undefined { val n = xs[0] }`,
		},
		{
			name: "a number field copied",
			src: `val o = {c: 5}
fn f() -> undefined {
	val n = o.c
	val a = {c: n}
	val mut m = a
	m.c = 3
}`,
		},
		{
			name: "a spread of numbers copied",
			src: `val xs: Array<number> = [1]
fn f() -> undefined {
	val a: Array<number> = [...xs]
	val mut m = a
	m.push(1)
}`,
		},
		{
			name: "a tuple of numbers spread into a literal",
			src: `val t: [number, number] = [1, 2]
fn f() -> undefined { val a = [...t] }`,
			want: nil,
		},
		{
			// A class name reads the class, which is no value the module owns.
			name: "a class read in a function",
			src: `class Box { x: number }
fn f() -> undefined { val b = Box }`,
		},
		{
			// B holds the class itself, so reading it moves no value the module owns.
			name: "a module-level alias of a class read in a function",
			src: `class Box { x: number }
val B = Box
fn f() -> undefined { val c = B }`,
		},
		{
			name: "destructured into a copied leaf",
			src: `val cfg: {items: Array<number>, n: number} = {items: [1], n: 1}
fn f() -> undefined { val {n} = cfg }`,
		},
		{
			name: "a fresh literal",
			src:  `fn f() -> undefined { val mut m: Array<number> = [1] }`,
		},
		{
			name: "an owned parameter",
			src: `fn f(p: Array<number>) -> undefined {
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
fn h() -> undefined {
	val mut m = make()
	m.push(1)
}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestMissingBorrowOfModuleLevelValue asserts that a module-level value written where a borrow
// is expected reports only that the borrow is missing. The fix is to write `&`, which also stops
// the move, so a ModuleLevelMoveError on the same expression would blame one fault twice.
func TestMissingBorrowOfModuleLevelValue(t *testing.T) {
	t.Parallel()

	const borrow = "this value is borrowed by its destination, so write the borrow: `&`"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a declaration",
			src: `val xs: Array<number> = [1]
fn f() { val b: &Array<number> = xs }`,
			want: []string{"2:34-2:36: " + borrow},
		},
		{
			name: "a return",
			src: `val xs: Array<number> = [1]
fn f() -> &Array<number> { return xs }`,
			want: []string{"2:35-2:37: " + borrow},
		},
		{
			name: "a field store",
			src: `val xs: Array<number> = [1]
fn f(o: &mut {r: &Array<number>}) { o.r = xs }`,
			want: []string{"2:43-2:45: " + borrow},
		},
		{
			name: "a call argument",
			src: `val xs: Array<number> = [1]
declare fn take(x: &Array<number>) -> undefined
fn f() -> undefined { take(xs) }`,
			want: []string{"3:28-3:30: this argument is borrowed by the callee, so write the borrow: `&`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestMoveOutOfModuleLevelValueIntoCall covers calls inside a function body that pass a
// module-level binding as an argument or as a method's receiver. A parameter or receiver that
// takes ownership moves the binding, which reports a ModuleLevelMoveError. One that borrows
// reads it.
func TestMoveOutOfModuleLevelValueIntoCall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// `take` owns its parameter, so the call moves `cfg`.
			name: "passed to an owned parameter",
			src: `val cfg = {x: 1}
declare fn take(d: {x: number}) -> undefined
fn g() -> undefined {
	take(cfg)
}`,
			want: []string{"4:7-4:10: cannot move module-level value 'cfg' inside a function. Borrow it with `&cfg`"},
		},
		{
			// Only the field moves, so the error names `cfg.items`.
			name: "a field passed to an owned parameter",
			src: `val cfg: {items: Array<number>} = {items: [1]}
declare fn take(xs: Array<number>) -> undefined
fn g() -> undefined {
	take(cfg.items)
}`,
			want: []string{"4:7-4:16: cannot move module-level value 'cfg.items' inside a function. Borrow it with `&cfg.items`"},
		},
		{
			// The call moves the result of each branch, so the error blames `xs` inside the `if`.
			name: "a branch result passed to an owned parameter",
			src: `val xs: Array<number> = [1]
declare fn take(xs: Array<number>) -> undefined
fn g(c: boolean) -> undefined {
	take(if c { xs } else { [2] })
}`,
			want: []string{"4:14-4:16: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			// A method's owned parameter moves its argument the way a function's does.
			name: "passed to a method's owned parameter",
			src: `val xs: Array<number> = [1]
class Box {
	items: Array<number>,
	constructor(&mut self) { self.items = [] },
	put(&mut self, v: Array<number>) -> undefined { self.items = v },
}
fn g() -> undefined {
	val mut b = Box()
	b.put(xs)
}`,
			want: []string{"9:8-9:10: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			// `g`'s parameter type is not settled at the recursive call, so the argument's flow waits
			// until `g`'s body settles `p` as owned. This case infers `g` and calls it in one
			// source, since `g` calls itself.
			name: "passed to a recursive call's unannotated parameter",
			src: `val xs: Array<number> = [1]
fn g(p, c: boolean) -> number {
	val q = p
	if c { return g(xs, false) }
	return 1
}`,
			want: []string{"4:18-4:20: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			// The closure's body moves `p`, which settles `h`'s parameter as owned. This case infers
			// the closure and calls it in one source. `h` renders as `fn (p: unknown) -> undefined`,
			// and a call to a callee declared with that signature moves nothing. The move comes
			// from the parameter's moved flag, which the rendered signature does not show.
			name: "passed to a closure's unannotated parameter",
			src: `val xs: Array<number> = [1]
fn g() -> undefined {
	val h = fn(p) { val q = p }
	h(xs)
}`,
			want: []string{"4:4-4:6: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"},
		},
		{
			// `consume` takes `self` by value, so the call moves `tok`.
			name: "the receiver of a method that takes self",
			src: `class Token {
	constructor(&mut self) {},
	consume(self) -> number { return 1 },
}
val tok = Token()
fn f() -> number { return tok.consume() }`,
			want: []string{"6:27-6:30: cannot move module-level value 'tok' inside a function. The method 'consume' takes `self` by value, so calling it moves 'tok'"},
		},
		{
			name: "passed to a borrow parameter",
			src: `val cfg = {x: 1}
declare fn peek(d: &{x: number}) -> undefined
fn g() -> undefined {
	peek(&cfg)
}`,
		},
		{
			name: "a number field passed to an owned parameter",
			src: `val cfg = {x: 1}
declare fn take(n: number) -> undefined
fn g() -> undefined {
	take(cfg.x)
}`,
		},
		{
			name: "the receiver of a method that borrows self",
			src: `class Token {
	constructor(&mut self) {},
	peek(&self) -> number { return 1 },
}
val tok = Token()
fn f() -> number { return tok.peek() }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
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
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val n = a
}`,
			want: []string{"4:10-4:11: use of moved value 'a'"},
		},
		{
			name: "the closure called twice",
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val m = g()
	val n = g()
}`,
			want: []string{"5:10-5:11: use of moved value 'g'"},
		},
		{
			name: "the closure called in a loop",
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() { val b = a }
	for i in [1, 2] { g() }
}`,
			want: []string{"4:20-4:21: use of moved value 'g'"},
		},
		{
			name: "a nested closure moving a capture of its enclosing closure",
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() {
		val h = fn() { val b = a }
	}
	val n = a
}`,
			want: []string{"6:10-6:11: use of moved value 'a'"},
		},
		{
			name: "one capture stored into another",
			src: `fn f() -> undefined {
	var a: Array<number> = [1]
	val b: Array<number> = [2]
	val g = fn() { a = b }
	val n = b
}`,
			want: []string{"5:10-5:11: use of moved value 'b'"},
		},
		{
			name: "a closure moving two fields of one capture",
			src: `fn f() -> undefined {
	val a: {p: Array<number>, q: Array<number>} = {p: [1], q: [2]}
	val g = fn() {
		val x = a.p
		val y = a.q
	}
	val n = a
}`,
			want: []string{"7:10-7:11: use of moved value 'a'"},
		},
		{
			name: "the closure called once and its result mutated",
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() -> Array<number> { return a }
	val mut m = g()
	m.push(1)
}`,
		},
		{
			name: "a closure that borrows its capture",
			src: `fn f() -> undefined {
	val a: Array<number> = [1]
	val g = fn() { val b = &a }
	g()
	g()
	val n = a
}`,
		},
		{
			name: "a fresh literal stored into a capture",
			src: `fn f() -> undefined {
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
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestScriptMovesLibraryValue asserts that a script function moving a value its library exports
// reports a ModuleLevelMoveError.
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
	require.Equal(t, []string{"1:17-1:19: cannot move module-level value 'xs' inside a function. Borrow it with `&xs`"}, messagesWithSpan(t, errs))
}
