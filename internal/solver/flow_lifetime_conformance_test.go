package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// flowLifetimeCase is one per-rule conformance case for F3, D9, L1, L2, L3, E4, and D10 in
// planning/ownership_rules/requirements.md. Most programs are the rule's own example, copied
// as the rule writes it.
type flowLifetimeCase struct {
	name string
	src  string
	// values maps a top-level binding to its rendered type. Bindings the map leaves out are not
	// checked.
	values map[string]string
	// want lists every diagnostic the program reports, each with its span. It is empty when the
	// program checks clean.
	want []string
}

// runFlowLifetimeCases checks each case's program against the standard library. It asserts the
// full list of diagnostics, then the rendered type of each binding the case names.
func runFlowLifetimeCases(t *testing.T, cases []flowLifetimeCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			values, errs := inferAgainstTreeWithSpans(t, c.src)
			require.Equal(t, c.want, errs)
			for name, want := range c.values {
				require.Contains(t, values, name)
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// e4Decls declares the module-level closure and the callee that E4's `stored` reads.
const e4Decls = `
	var sink: fn () -> undefined = fn () {}
	declare fn readIt(a: &{x: number}) -> undefined
`

// d10Decls declares the callees the three `setup` programs in D10 share. `onClick` keeps its
// handler for the rest of the program, and `forEachItem` only calls its function during the call.
const d10Decls = `
	declare fn onClick(handler: &'static fn () -> undefined) -> undefined
	declare fn forEachItem(f: fn () -> undefined) -> undefined
`

// TestFlowLifetimeRules covers the per-rule conformance cases for F3, L1, L2, L3, and E4 that
// the current solver gets right. The cases it gets wrong sit in the disabled tests after this
// one, grouped by the issue that enables them.
//
// The E4 programs name the spec's `get` closure `peek`, since `get` is a keyword.
func TestFlowLifetimeRules(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// F3. A return with no written type is owned when it returns a local, and carries the
		// parameter's borrow when it returns one.
		{
			name: "F3/MakeAndSame",
			src: `
				fn make() {
					val p = {x: 1}
					return p    // moves p out, so make returns an owned value
				}

				fn same(p: &{x: number}) {
					return p    // same: fn <'a>(p: &'a {x: number}) -> &'a {x: number}
				}
			`,
			values: map[string]string{
				"make": "fn () -> {x: 1}",
				"same": "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
			},
		},
		// F3. A closure that stays in the body borrows its captures, so `p` stays usable in
		// `local`. A returned closure owns its captures, so `returned` carries no borrow of `p`.
		{
			name: "F3/LocalAndReturned",
			src: `
				fn local() -> number {
					val p = {x: 1}
					val f = fn () { return p.x }    // borrows p
					val a = f()
					return a + p.x                  // p is still usable
				}

				fn returned() {
					val p = {x: 1}
					return fn () { return p.x }     // owns p, since it outlives the frame
				}
			`,
			values: map[string]string{
				"local":    "fn () -> number",
				"returned": "fn () -> fn () -> 1",
			},
		},
		// F3. A binding moves an owned value even when the binding dies first.
		{
			name: "F3/BindingInsideIf",
			src: `
				fn go() -> number {
					val p = {x: 0}
					if true {
						val q = p       // moves p
					}
					return p.x          // error: use of moved value 'p'
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'p'"},
		},
		// L1. A returned borrow may outlive the binding that owns its place. The `remember` half of
		// the rule's example is in TestFlowLifetimeStaticBorrow.
		{
			name: "L1/Make",
			src: `
				fn make() -> &{x: number} {
					val p = {x: 1}
					return &p          // accepted, since the value stays alive for the borrow
				}
			`,
		},
		// L2. Each function below is one function from the rule's example. `wrong` is in
		// TestFlowLifetimeUndeclaredElidedRelation.
		{
			name: "L2/Pick",
			src: `
				fn pick<'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number} {
					return p    // accepted
				}
			`,
		},
		{
			name: "L2/Either",
			src: `
				fn either<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}, first: boolean) -> &'a {x: number} {
					if first {
						return p
					}
					return q    // accepted, since returning q establishes 'b: 'a
				}
			`,
		},
		{
			name: "L2/Unproven",
			src: `
				fn unproven<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number} {
					return p    // rejected, since the body never establishes 'b: 'a
				}
			`,
			want: []string{"2:21-2:27: declared lifetime bound 'b: 'a is not satisfied; the body does not make 'b outlive 'a"},
		},
		{
			name: "L2/Trusted",
			src: `
				declare fn trusted<'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}
			`,
			values: map[string]string{
				"trusted": "fn <'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}",
			},
		},
		// L3. `read` and `same` from the rule's example. `cache` is in TestFlowLifetimeStaticRender.
		{
			name: "L3/ReadAndSame",
			src: `
				fn read(p: &{x: number}) -> number { return p.x }
				// fn (p: &{x: number}) -> number, since the lifetime connects nothing

				fn same(p: &{x: number}) { return p }
				// fn <'a>(p: &'a {x: number}) -> &'a {x: number}, since it ties p to the result
			`,
			values: map[string]string{
				"read": "fn (p: &{x: number}) -> number",
				"same": "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
			},
		},
		// E4. `peek` holds no live borrow between its calls, so the write to `p` is allowed.
		{
			name: "E4/Local",
			src: e4Decls + `
				fn local() -> number {
					val mut p = {x: 1}
					val peek = fn () { return p.x }
					val a = peek()
					p.x = 2       // allowed, since peek holds no live borrow between calls
					return a + peek()
				}
			`,
		},
		// E4. The closure in `sink` may run during any later call, so `readIt(&p)` conflicts with
		// the write the closure performs.
		{
			name: "E4/Stored",
			src: e4Decls + `
				fn stored() {
					val mut p = {x: 1}
					sink = fn () { p.x = 2 }
					readIt(&p)    // error: this call may run 'sink', which writes 'p' while it is borrowed as immutable
				}
			`,
			want: []string{"8:6-8:16: this call may run 'sink', which writes 'p' while it is borrowed as immutable"},
		},
		// E4. A closure that moves a capture moves it at each call, so the second call uses a
		// moved value.
		{
			name: "E4/MovingClosureCalledTwice",
			src: `
				declare fn take(p: {x: number}) -> number

				fn go() -> number {
					val p = {x: 1}
					val f = fn () { return take(p) }
					val a = f()
					return a + f()
				}
			`,
			want: []string{"8:17-8:18: use of moved value 'f'"},
		},
	})
}

// DISABLED until #2260, which infers an unannotated parameter that does not escape as a borrow.
// Today `read` infers as `fn <T0>(p: {x: T0}) -> T0`, an owned parameter, so `read(&b)` reports
// `cannot use borrowed &object as owned object`.
/*
func TestFlowLifetimeNonEscapingParam(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// F3. `store` lets `p` escape into module-level state, so its parameter is owned. `read`
		// only reads `p`, so its parameter is a borrow and the call borrows `b`.
		{
			name: "F3/StoreAndRead",
			src: `
				var sink = {x: 0}

				fn store(p) { sink = p }     // store: fn (p: {x: number}) -> undefined
				fn read(p) { return p.x }    // read: fn (p: &{x: number}) -> number

				fn go() -> number {
					val a = {x: 1}
					store(a)                 // moves a
					val b = {x: 2}
					val n = read(&b)         // borrows b, so b stays usable
					return b.x + n
				}
			`,
			values: map[string]string{
				"store": "fn (p: {x: number}) -> undefined",
				"read":  "fn <T0>(p: &{x: T0}) -> T0",
			},
		},
	})
}
*/

// DISABLED until #2261, which moves the argument when calling an unannotated parameter that
// escapes. Today `store` infers as `fn (p: {x: number}) -> undefined`, yet `store(a)` records no
// move, so the read of `a.x` checks clean.
/*
func TestFlowLifetimeEscapingParamMoves(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// F3. The call to `store` moves `a`, so the later read of `a.x` uses a moved value.
		{
			name: "F3/StoreMovesArgument",
			src: `
				var sink = {x: 0}

				fn store(p) { sink = p }

				fn go() -> number {
					val a = {x: 1}
					store(a)
					return a.x
				}
			`,
			want: []string{"9:13-9:16: use of moved value 'a'"},
		},
	})
}
*/

// DISABLED until #2262, which infers an escaping parameter that the body only reads as a
// `'static` borrow. Today `remember` infers as `fn <T0>(value: T0) -> undefined`, which leaves
// the parameter owned.
/*
func TestFlowLifetimeEscapingReadOnlyParam(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// F3 and D9. `seen` is a `Map` whose value type is inferred, so its type leaves the
		// ownership of `value` open. The body never writes `value`, so the parameter is a
		// `'static` borrow and `opts` stays readable after the call.
		{
			name: "D9/ReadOnlyEscapeIsStaticBorrow",
			src: `
				val mut seen = Map()

				fn remember(value) { seen.set("last", value) }

				fn go() -> number {
					val opts = {retries: 3}
					remember(&opts)
					return opts.retries
				}
			`,
			values: map[string]string{
				"remember": "fn <T0>(value: &'static T0) -> undefined",
			},
		},
	})
}
*/

// DISABLED until #2247, which blocks writes and new immutable borrows after a `'static` borrow.
// Today the write to `p.x` in `remember` checks clean, since the exclusivity check does not keep
// the borrow stored in `cache` live after the store.
/*
func TestFlowLifetimeStaticBorrow(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// L1. The returned borrow in `make` outlives `p`, which is accepted. The borrow stored in
		// `cache` is `'static`, so `cache` can read `p` at any later point and E2 rejects the write.
		{
			name: "L1/MakeAndRemember",
			src: `
				fn make() -> &{x: number} {
					val p = {x: 1}
					return &p          // accepted, since the value stays alive for the borrow
				}

				var cache: &{x: number} = &{x: 0}

				fn remember() {
					val mut p = {x: 1}
					cache = &p         // the borrow's lifetime is 'static
					p.x = 2            // rejected by E2, since cache can still read p
				}
			`,
			want: []string{"12:6-12:9: cannot assign to 'p.x' while it is borrowed as immutable"},
		},
	})
}
*/

// DISABLED until a body that ties a result to a parameter whose lifetime the signature leaves
// unnamed reports the undeclared relation. Not tracked by an issue yet.
//
// Today the solver checks only the relations between lifetimes the signature names. It gives the
// unnamed lifetime of `q` a fresh variable, records the bound the body needs on it, and renders
// `wrong` as `fn <'a, 'b: 'a>(p: &'a {x: number}, q: &'b {x: number}) -> &'a {x: number}` with no
// diagnostic.
/*
func TestFlowLifetimeUndeclaredElidedRelation(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// L2. `wrong` declares that its result lives as long as `p` but returns `q`, so its body
		// needs a relation the signature does not state.
		{
			name: "L2/Wrong",
			src: `
				fn wrong<'a>(p: &'a {x: number}, q: &{x: number}) -> &'a {x: number} {
					return q    // rejected, since the result's tie to q is not declared
				}
			`,
			want: []string{"3:13-3:14: the body requires the lifetime of 'q' to outlive 'a, but the signature does not declare it"},
		},
	})
}
*/

// DISABLED until #2249 and #2269. Today the program reports three errors and renders `cache` as
// `fn (p: &mut {x: number}) -> undefined`:
//
//   - The initializer `&mut {x: 0}` reports `cannot constrain immutable object <: mutable object`
//     and `cannot constrain number <: 0`. No issue tracks this yet.
//   - The store `sink = p` reports `cannot constrain immutable object <: mutable object`. Any store
//     into a module-level `&mut` binding reports it, and the store also moves `p`, which F2 rules
//     out for a borrow.
/*
func TestFlowLifetimeStaticRender(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// L3. A lifetime that is `'static` is rendered, so `cache` shows that `p` escapes to
		// module-level state.
		{
			name: "L3/ReadSameAndCache",
			src: `
				var sink: &mut {x: number} = &mut {x: 0}

				fn read(p: &{x: number}) -> number { return p.x }
				// fn (p: &{x: number}) -> number, since the lifetime connects nothing

				fn same(p: &{x: number}) { return p }
				// fn <'a>(p: &'a {x: number}) -> &'a {x: number}, since it ties p to the result

				fn cache(p: &mut {x: number}) { sink = p }
				// fn (p: &'static mut {x: number}) -> undefined, since p escapes to 'static
			`,
			values: map[string]string{
				"read":  "fn (p: &{x: number}) -> number",
				"same":  "fn <'a>(p: &'a {x: number}) -> &'a {x: number}",
				"cache": "fn (p: &'static mut {x: number}) -> undefined",
			},
		},
	})
}
*/

// DISABLED until #2269, which turns on the single ownership pass. Today the move `val q = p`
// checks clean, and the later call reports `this call runs 'peek', which reads 'p' after it was
// moved` instead.
/*
func TestFlowLifetimeClosureBlocksMove(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// E4. `peek` still holds its borrow of `p` for its later call, so E3 rejects the move.
		{
			name: "E4/Moved",
			src: e4Decls + `
				fn moved() -> number {
					val p = {x: 1}
					val peek = fn () { return p.x }
					val q = p     // error: cannot move 'p' while it is borrowed
					return peek()
				}
			`,
			want: []string{"8:14-8:15: cannot move 'p' while it is borrowed"},
		},
	})
}
*/

// DISABLED until #2264, which keeps a closure's borrows for the lifetime of a function parameter
// that retains it. Today the parameter type `&'static fn () -> undefined` reports
// `Unsupported: borrow of a non-borrowable type` and `onClick` infers as
// `fn <T0>(handler: T0) -> undefined`. The first program also checks clean apart from that error,
// since a closure passed to any function parameter holds its borrows for the call alone.
/*
func TestFlowLifetimeRetainedCallback(t *testing.T) {
	runFlowLifetimeCases(t, []flowLifetimeCase{
		// E4 and D10. `onClick` keeps its handler for the rest of the program, so the handler's
		// mutable borrow of `count` stays live after the call and `render` takes an immutable view
		// a click could change.
		{
			name: "D10/RetainedHandlerConflicts",
			src: d10Decls + `
				declare fn render(view: &{n: number}) -> undefined

				fn setup() {
					val mut count = {n: 0}
					onClick(fn () { count.n = count.n + 1 })
					render(&count)    // error: a click may run the handler, which writes 'count' while it is borrowed as immutable
				}
			`,
			want: []string{"10:6-10:20: this call may run the 'handler' that 'onClick' retains, which writes 'count' while it is borrowed as immutable"},
		},
		// E4 and D10. The handler owns `count`, since `onClick` keeps it past the frame, so the
		// borrow inside the handler lasts for one call.
		{
			name: "D10/HandlerOwnsState",
			src: d10Decls + `
				declare fn render(view: &{n: number}) -> undefined

				fn setup() {
					val mut count = {n: 0}
					onClick(fn () {
						count.n = count.n + 1
						render(&count)    // accepted
					})
				}
			`,
		},
		// E4 and D10. A mutable view makes no promise that the value holds still, and E2 allows
		// several mutable paths at once.
		{
			name: "D10/MutableView",
			src: d10Decls + `
				declare fn render(view: &mut {n: number}) -> undefined

				fn setup() {
					val mut count = {n: 0}
					onClick(fn () { count.n = count.n + 1 })
					render(&mut count)    // accepted
				}
			`,
		},
	})
}
*/
