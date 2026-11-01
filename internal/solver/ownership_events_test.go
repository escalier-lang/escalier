package solver

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// inferOwnershipEvents infers src against the standard library and returns the ownership
// events of the body of the top-level function declaration named fn, along with every
// diagnostic the program reports. It asserts that every provisional flow was settled.
func inferOwnershipEvents(t *testing.T, src, fn string) (*ownershipEvents, []string) {
	t.Helper()

	module := parseModule(t, src)
	res := InferModuleAgainstStdlib(module, benchTree, Options{})
	require.Empty(t, res.checker.pendingFlows)

	msgs := make([]string, len(res.Errors))
	for i, e := range res.Errors {
		msgs[i] = e.Message()
	}

	var decl *ast.FuncDecl
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, d := range ns.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name != nil && fd.Name.Name == fn {
				decl = fd
			}
		}
		return true
	})
	require.NotNil(t, decl)

	table := res.Info.ownershipEventsOf(decl)
	require.NotNil(t, table)
	return table, msgs
}

// ownershipEventsCase is one program whose ownership events a test asserts. fn names the
// function whose body's events are listed, and defaults to `go`. want lists every event of
// that body in the order the walk emitted them, each prefixed with the CFG node it is
// attached to. errs lists every diagnostic the program reports, and is nil for a program
// that checks clean.
type ownershipEventsCase struct {
	name string
	src  string
	fn   string
	want []string
	errs []string
}

// runOwnershipEvents asserts each case's events and diagnostics. Emitting the events reports
// nothing, so a diagnostic comes from the rest of the checker.
func runOwnershipEvents(t *testing.T, cases []ownershipEventsCase) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := tc.fn
			if fn == "" {
				fn = "go"
			}

			table, errs := inferOwnershipEvents(t, tc.src, fn)
			require.Equal(t, tc.want, renderOwnershipEvents(table))
			if tc.errs == nil {
				require.Empty(t, errs)
				return
			}
			require.Equal(t, tc.errs, errs)
		})
	}
}

// TestOwnershipEventsCheckingModel covers the programs planning/ownership_rules/requirements.md
// annotates with their outcomes: the example in the checking model section, the F2 example, the
// F3 examples, and the F4 example without its rejected line.
func TestOwnershipEventsCheckingModel(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "CheckingModelExample",
			src: `
				fn go() -> number {
					val mut p = {x: 0}
					val c = &p
					val d = c
					p.x = 1
					return d.x
				}
			`,
			want: []string{
				"b0.1: borrow(&, `p`, `c`, '1)", // `val c = &p` borrows p immutably into c
				"b0.2: copy(`c`, `d`)",          // `val d = c` copies the immutable borrow
				"b0.3: use(`p.x`, write)",       // `p.x = 1`
				"b0.4: use(`d.x`, read)",        // `return d.x` reads a `number`
			},
		},
		{
			name: "F2Outcomes",
			src: `
				fn go(r: &{x: number}) -> number {
					val n = 1
					val a = n
					val b = r
					val p = {x: 1}
					val c = &p
					val q = {x: 2}
					val d = q
					return a + b.x + c.x + p.x + d.x
				}
			`,
			want: []string{
				"b0.1: use(`n`, read)",          // `val a = n` only reads n, since a `number` is a value type
				"b0.2: copy(`r`, `b`)",          // `val b = r` copies the borrow parameter r
				"b0.4: borrow(&, `p`, `c`, '1)", // `val c = &p`
				"b0.6: move(`q`, `d`)",          // `val d = q` moves the owned object q
				"b0.7: use(`a`, read)",          // `a` in the return
				"b0.7: use(`b.x`, read)",        // `b.x` in the return
				"b0.7: use(`c.x`, read)",        // `c.x` in the return
				"b0.7: use(`p.x`, read)",        // `p.x` in the return
				"b0.7: use(`d.x`, read)",        // `d.x` in the return
			},
		},
		{
			name: "ArgumentIntoEscapingParam",
			src: `
				var sink = {x: 0}
				fn store(p) { sink = p }
				fn read(p) { return p.x }
				fn go() -> number {
					val a = {x: 1}
					store(a)
					val b = {x: 2}
					val n = read(&b)
					return b.x + n
				}
			`,
			want: []string{
				"b0.1: use(`store`, read)",                 // `store(a)` reads the callee `store`
				"b0.1: move(`a`, arg(`store`, 0))",         // `store(a)` moves a, since `store` stores its parameter into `sink`
				"b0.3: use(`read`, read)",                  // `read(&b)` reads the callee `read`
				"b0.3: borrow(&, `b`, arg(`read`, 0), '1)", // `read(&b)` passes the written borrow `&b`
				"b0.4: use(`b.x`, read)",                   // `b.x` in the return
				"b0.4: use(`n`, read)",                     // `n` in the return
			},
		},
		{
			name: "ModuleLevelStore",
			fn:   "store",
			src: `
				var sink = {x: 0}
				fn store(p) { sink = p }
			`,
			want: []string{
				"b0.0: move(`p`, `sink`)",  // `sink = p` moves p, so F3 settles p as owned
				"b0.0: use(`sink`, write)", // `sink = p` writes the module binding
			},
		},
		{
			name: "ReadThroughUnannotatedParam",
			fn:   "read",
			src: `
				fn read(p) { return p.x }
			`,
			want: []string{
				"b0.0: copy(`p.x`, return)", // `return p.x` copies out of p, which F3 settles as a borrow since the body never moves it
			},
		},
		{
			name: "LocalClosureBorrowsCapture",
			src: `
				fn go() -> number {
					val p = {x: 1}
					val f = fn () { return p.x }
					val a = f()
					return a + p.x
				}
			`,
			want: []string{
				"b0.1: borrow(&, `p`, `f`, '_)", // `val f = fn ...` captures p, and f is never returned, so the capture is a borrow
				"b0.2: use(`f`, read)",          // `f()` reads the closure
				"b0.3: use(`a`, read)",          // `a` in the return
				"b0.3: use(`p.x`, read)",        // `p.x` in the return
			},
		},
		{
			name: "ReturnedClosureOwnsCapture",
			src: `
				fn go() {
					val p = {x: 1}
					return fn () { return p.x }
				}
			`,
			want: []string{
				"b0.1: move(`p`, return)", // `return fn ...` returns the closure, so it owns its capture p
			},
		},
		{
			name: "ReadsThroughBorrow",
			src: `
				class Point { x: number, y: number }
				class Line { start: Point, width: number }
				fn go(line: &Line) -> &Point {
					val w = line.width
					val x = line.start.x
					val {start} = line
					return &line.start
				}
			`,
			want: []string{
				"b0.0: use(`line.width`, read)",              // `val w = line.width` reads a `number`
				"b0.1: use(`line.start.x`, read)",            // `val x = line.start.x` reads a `number`
				"b0.2: borrow(&, `line.start`, `start`, '1)", // `val {start} = line` binds a borrow, since the pattern reads through the borrow `line`
				"b0.3: borrow(&, `line.start`, return, '1)",  // `return &line.start`
			},
		},
	})
}

// TestOwnershipEventsFlowSites covers each kind of flow site F1 lists, and the uses around
// them.
func TestOwnershipEventsFlowSites(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "DestructureOwned",
			src: `
				fn go() {
					val p = {a: {x: 1}, b: {y: 2}, n: 3}
					val {a, n} = p
					return [a, p.b]
				}
			`,
			want: []string{
				"b0.1: move(`p.a`, `a`)",      // `val {a, n} = p` moves the object field a out of p
				"b0.1: use(`p.n`, read)",      // `val {a, n} = p` only reads the `number` field n
				"b0.2: move(`a`, return.0)",   // `a` in `return [a, p.b]`
				"b0.2: move(`p.b`, return.1)", // `p.b` in `return [a, p.b]` moves the field out of p
			},
		},
		{
			name: "PatternOverMutableBorrow",
			src: `
				fn go(p: &mut {a: {x: number}, n: number}) {
					val {a, n} = p
					a.x = n
				}
			`,
			want: []string{
				"b0.0: borrow(&mut, `p.a`, `a`, '1)", // `val {a, n} = p` binds a as `&mut`, since p is a `&mut` borrow
				"b0.0: use(`p.n`, read)",             // `val {a, n} = p` only reads the `number` field n
				"b0.1: use(`n`, read)",               // `a.x = n` reads n
				"b0.1: use(`a.x`, write)",            // `a.x = n` writes through a
			},
		},
		{
			name: "LiteralElementsAndSpread",
			src: `
				fn go() {
					val p = {x: 1}
					val q = {x: 2}
					val r = {x: 3}
					val o = {a: &p, b: q, c: [r], ...{y: 4}}
					return o
				}
			`,
			want: []string{
				"b0.3: borrow(&, `p`, `o.a`, '1)", // `a: &p` in the literal bound to o
				"b0.3: move(`q`, `o.b`)",          // `b: q` moves the owned q into the literal
				"b0.3: move(`r`, `o.c.0`)",        // `c: [r]` moves r into element 0 of the nested tuple
				"b0.4: move(`o`, return)",         // `return o`
			},
		},
		{
			name: "BranchValues",
			src: `
				fn go(c: boolean) {
					val p = {x: 1}
					val q = {x: 2}
					val r = if c { p } else { q }
					return r
				}
			`,
			want: []string{
				"b0.2: use(`c`, read)",    // the condition of `if c`
				"b3.0: move(`p`, `r`)",    // the consequent `{ p }` moves p into r
				"b4.0: move(`q`, `r`)",    // the alternative `{ q }` moves q into r
				"b2.0: move(`r`, return)", // `return r` after the branches join
			},
		},
		{
			name: "Stores",
			src: `
				fn go(mut o: {a: {x: number}}) {
					val q = {x: 1}
					o.a = q
					var r = {x: 2}
					val s = {x: 3}
					r = s
					return r
				}
			`,
			want: []string{
				"b0.1: move(`q`, `o.a`)",  // `o.a = q` moves q, since the field a holds an owned object
				"b0.1: use(`o.a`, write)", // `o.a = q` writes the field
				"b0.4: move(`s`, `r`)",    // `r = s` moves s into r
				"b0.4: use(`r`, write)",   // `r = s` writes r
				"b0.5: move(`r`, return)", // `return r`
			},
		},
		{
			name: "MethodReceivers",
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
					set(&mut self) -> undefined { self.x = 1 },
					take(self) -> number { return self.x },
				}
				fn go(mut c: C) {
					c.get()
					c.set()
					c.take()
				}
			`,
			want: []string{
				"b0.0: borrow(&, `c`, arg(`get`, self), '1)",    // `c.get()` borrows c for the `&self` receiver
				"b0.1: borrow(&mut, `c`, arg(`set`, self), '2)", // `c.set()` borrows c mutably for the `&mut self` receiver, under a lifetime of its own
				"b0.2: move(`c`, arg(`take`, self))",            // `c.take()` moves c into the owned `self` receiver
			},
		},
		{
			name: "MatchArmsInReturn",
			src: `
				fn go(p: {v: {x: number}, n: number}) {
					return match p {
						{v} if v.x > 0 => v.x,
						{n} => n,
					}
				}
			`,
			want: []string{
				"b0.0: use(`v.x`, read)", // the guard `v.x > 0`
				"b0.0: use(`v.x`, read)", // the first arm's body `v.x`
				"b0.0: use(`n`, read)",   // the second arm's body `n`
				"b0.0: move(`p.v`, `v`)", // the pattern `{v}` moves the field v out of p
				"b0.0: use(`p.n`, read)", // the pattern `{n}` only reads the `number` field n
			},
		},
		{
			name: "MatchArmBlocks",
			src: `
				fn go(p: {v: {x: number}, n: number}) {
					val r = match p {
						{v} if v.x > 0 => {
							v
						},
						{n} => {
							{x: n}
						},
					}
					return r
				}
			`,
			want: []string{
				"b3.0: use(`v.x`, read)",     // the guard `v.x > 0`
				"b4.0: use(`n`, read)",       // `{x: n}` in the second arm reads n
				"b3.entry: move(`p.v`, `v`)", // the pattern `{v}` binds v on entry to the first arm
				"b4.entry: use(`p.n`, read)", // the pattern `{n}` reads the `number` field n on entry to the second arm
				"b3.1: move(`v`, `r`)",       // the first arm's tail `v` moves into r
				"b2.0: move(`r`, return)",    // `return r` after the arms join
			},
		},
		{
			// A callee typed as an object with one call signature resolves to that signature,
			// so the argument flows by the signature's owned parameter.
			name: "CallThroughCallSignature",
			src: `
				fn go(f: {(p: {x: number}) -> undefined}) {
					val o = {x: 1}
					f(o)
				}
			`,
			want: []string{
				"b0.1: use(`f`, read)",         // `f(o)` reads the callee f
				"b0.1: move(`o`, arg(`f`, 0))", // `f(o)` moves o into the call signature's owned parameter p
			},
		},
		{
			// Each call of one method borrows the receiver under its own lifetime, though both
			// calls go through the same declared `self`.
			name: "ReceiverBorrowPerCall",
			src: `
				class C {
					x: number,
					get(&self) -> number { return self.x },
				}
				fn go(c: C) -> number {
					val a = c.get()
					val b = c.get()
					return a + b
				}
			`,
			want: []string{
				"b0.0: borrow(&, `c`, arg(`get`, self), '1)", // the first `c.get()` borrows c
				"b0.1: borrow(&, `c`, arg(`get`, self), '2)", // the second `c.get()` borrows c again, under a new lifetime
				"b0.2: use(`a`, read)",                       // `a + b` reads a
				"b0.2: use(`b`, read)",                       // `a + b` reads b
			},
		},
		{
			name: "IfVal",
			src: `
				fn go(p: {v: {x: number}} | undefined) {
					if val {v} = p {
						return v.x
					}
					return 0
				}
			`,
			want: []string{
				"b3.0: use(`v.x`, read)",     // `return v.x` in the consequent
				"b3.entry: move(`p.v`, `v`)", // `if val {v} = p` binds v on entry to the consequent
			},
		},
	})
}

// TestOwnershipEventsClosureCaptures covers closure captures, which F3 decides from where the
// closure flows.
func TestOwnershipEventsClosureCaptures(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "ClosureWritingCaptureBorrowsMutably",
			src: `
				fn go() {
					val mut p = {x: 1}
					val f = fn () { p.x = 2 }
					f()
					return p
				}
			`,
			want: []string{
				"b0.1: borrow(&mut, `p`, `f`, '_)", // `val f = fn () { p.x = 2 }` writes p and is never returned, so it borrows p mutably
				"b0.2: use(`f`, read)",             // `f()` reads the closure
				"b0.3: move(`p`, return)",          // `return p`
			},
		},
		{
			name: "ClosureArgumentBorrowsCapture",
			src: `
				fn go(xs: Array<number>) {
					val p = {x: 1}
					return xs.map(fn (v, i, a) { return v + p.x })
				}
			`,
			want: []string{
				"b0.1: borrow(&, `p`, arg(`map`, 0), '_)",     // the closure passed to `xs.map` captures p and is not returned, so it borrows p
				"b0.1: borrow(&, `xs`, arg(`map`, self), '1)", // `xs.map(...)` borrows xs for the `&self` receiver of `map`
			},
		},
		{
			name: "ClosureStoredInModuleOwnsCapture",
			src: `
				var handler: fn () -> number = fn () { return 0 }
				fn go() {
					val p = {x: 1}
					handler = fn () { return p.x }
				}
			`,
			want: []string{
				"b0.1: move(`p`, `handler`)",  // `handler = fn ...` stores the closure in a module binding, so it owns p
				"b0.1: use(`handler`, write)", // `handler = fn ...` writes the module binding
			},
		},
		{
			name: "ClosureReturnedThroughLocalOwnsCapture",
			src: `
				fn go(p) {
					val f = fn () {
						val q = p
						return q
					}
					return f
				}
			`,
			want: []string{
				"b0.0: move(`p`, `f`)", // `val f = fn ...` captures p, and `return f` lets the closure escape, so it owns p
				"b0.1: use(`f`, read)", // `return f` only reads f, since a closure is a value type
			},
		},
		{
			name: "ClosureReturnedInLiteralOwnsCapture",
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					return {h: f}
				}
			`,
			want: []string{
				"b0.1: move(`p`, `f`)", // `val f = fn ...` captures p, and `return {h: f}` lets the closure escape inside the literal, so it owns p
				"b0.2: use(`f`, read)", // `{h: f}` only reads f, since a closure is a value type
			},
		},
		{
			name: "ClosureReturnedThroughLiteralLocalOwnsCapture",
			src: `
				fn go() {
					val p = {x: 1}
					val f = fn () { return p.x }
					val o = {h: f}
					return o
				}
			`,
			want: []string{
				"b0.1: move(`p`, `f`)",    // `val o = {h: f}` stores the closure in o, and `return o` lets it escape, so it owns p
				"b0.2: use(`f`, read)",    // `{h: f}` only reads f, since a closure is a value type
				"b0.3: move(`o`, return)", // `return o`
			},
		},
	})
}

// TestOwnershipEventsProvisionalFlows covers flows whose ownership F3 settles only once a body
// is walked. Each becomes the event the settled ownership gives.
func TestOwnershipEventsProvisionalFlows(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "RecursiveCallIsProvisional",
			src: `
				fn go(n: number, p) {
					if n > 0 {
						go(n - 1, p)
					}
					val q = p
				}
			`,
			want: []string{
				"b0.0: use(`n`, read)",          // the condition `n > 0`
				"b3.0: use(`n`, read)",          // `n - 1` in the recursive call
				"b3.0: move(`p`, arg(`go`, 1))", // `go(n - 1, p)` moves p, since `val q = p` settles p as owned
				"b2.0: move(`p`, `q`)",          // `val q = p`
			},
		},
		{
			name: "FlowOutOfUnannotatedParamIsProvisional",
			src: `
				fn read(p: &{x: number}) -> number { return p.x }
				fn go(p) {
					val n = read(p)
					return n
				}
			`,
			want: []string{
				"b0.0: use(`read`, read)",         // `read(p)` reads the callee `read`
				"b0.0: copy(`p`, arg(`read`, 0))", // `read(p)` copies p, which settles as a borrow since the body only passes it to a `&` parameter
				"b0.1: use(`n`, read)",            // `return n` reads a `number`
			},
		},
		{
			name: "MutualRecursion",
			src: `
				fn go(n: number, p) {
					if n > 0 {
						stop(n - 1, p)
					}
				}
				fn stop(n: number, q) {
					go(n, q)
					val r = q
				}
			`,
			want: []string{
				"b0.0: use(`n`, read)",            // the condition `n > 0`
				"b3.0: use(`n`, read)",            // `n - 1` in the call to `stop`
				"b3.0: move(`p`, arg(`stop`, 1))", // `stop(n - 1, p)` moves p, since `val r = q` in `stop` settles q as owned
			},
		},
	})
}

// TestOwnershipEventsByNode asserts that byNode groups the events of the checking model's
// example by the CFG node each is attached to, keeping each node's events in the order the
// walk emitted them.
func TestOwnershipEventsByNode(t *testing.T) {
	table, errs := inferOwnershipEvents(t, `
		fn go(c: boolean) -> number {
			val mut p = {x: 0}
			val q = &p
			if c {
				val d = q
				p.x = 1
				return d.x
			}
			return 0
		}
	`, "go")
	require.Empty(t, errs)
	got := map[string][]string{}
	ltNames := map[soltype.Lifetime]string{}
	for at, events := range table.byNode() {
		key := renderStmtRef(at)
		for _, ev := range events {
			got[key] = append(got[key], table.renderEvent(ev, ltNames))
		}
	}
	require.Equal(t, map[string][]string{
		"b0.1": {"borrow(&, `p`, `q`, '1)"},
		"b0.2": {"use(`c`, read)"},
		"b3.0": {"copy(`q`, `d`)"},
		"b3.1": {"use(`p.x`, write)"},
		"b3.2": {"use(`d.x`, read)"},
	}, got)
}

// TestOwnershipEventsPatterns covers the patterns F1 makes flow sites: a `val … else`, a
// tuple pattern with a rest element, an object pattern with a rest element, and an extractor.
// Each part flows out of its own place, so no place moves twice.
func TestOwnershipEventsPatterns(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "ValElse",
			src: `
				fn go(p: {v: {x: number}} | undefined) -> number {
					val {v} = p else { return 0 }
					return v.x
				}
			`,
			want: []string{
				"b2.entry: move(`p.v`, `v`)", // `val {v} = p else ...` binds v on entry to the code after the `else`
				"b2.0: use(`v.x`, read)",     // `return v.x`
			},
		},
		{
			name: "TupleWithRest",
			src: `
				fn go(t: [{x: number}, {y: number}, {z: number}]) {
					val [a, ...rest] = t
					val [b, c] = rest
					return [a, b, c]
				}
			`,
			want: []string{
				"b0.0: move(`t.0`, `a`)",       // `val [a, ...rest] = t` moves element 0 into a
				"b0.0: move(`t.1...`, `rest`)", // `...rest` moves every element from 1 on
				"b0.1: move(`rest.0`, `b`)",    // `val [b, c] = rest` moves element 0 into b
				"b0.1: move(`rest.1`, `c`)",    // `val [b, c] = rest` moves element 1 into c
				"b0.2: move(`a`, return.0)",    // `a` in `return [a, b, c]`
				"b0.2: move(`b`, return.1)",    // `b` in `return [a, b, c]`
				"b0.2: move(`c`, return.2)",    // `c` in `return [a, b, c]`
			},
		},
		{
			name: "ObjectWithRest",
			src: `
				fn go() {
					val o = {a: {x: 1}, b: {y: 2}, c: {z: 3}}
					val {a, ...rest} = o
					return [a, rest]
				}
			`,
			want: []string{
				"b0.1: move(`o.a`, `a`)",       // `val {a, ...rest} = o` moves the field a into a
				"b0.1: move(`o.b`, `rest.b`)",  // `...rest` takes the field b, which the pattern does not name
				"b0.1: move(`o.c`, `rest.c`)",  // `...rest` takes the field c, which the pattern does not name
				"b0.2: move(`a`, return.0)",    // `a` in `return [a, rest]`
				"b0.2: move(`rest`, return.1)", // `rest` in `return [a, rest]`
			},
		},
		{
			name: "Extractor",
			src: `
				enum Opt { Some(v: {x: number}), None }
				fn go(o: Opt) {
					val r = match o {
						Opt.Some(v) => v,
						Opt.None => ({x: 0}),
					}
					return r
				}
			`,
			want: []string{
				"b3.entry: move(`o.0`, `v`)", // `Opt.Some(v)` binds the payload v on entry to the arm
				"b3.0: move(`v`, `r`)",       // the arm body `v` moves into r
				"b2.0: move(`r`, return)",    // `return r`
			},
		},
	})
}

// TestOwnershipEventsMatchArmNodes asserts that the events of a match arm attach to the CFG
// node of that arm. An arm whose body is an expression runs in a statement of its own, as a
// guard does. The walk types a guard once per path that reaches it, and its events are
// emitted once.
func TestOwnershipEventsMatchArmNodes(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "ArmBodies",
			src: `
				fn take(p: {x: number}) -> number { return p.x }
				fn go(c: boolean) {
					val p = {x: 1}
					val q = {x: 2}
					var y = 0
					val r = match c {
						true => { y = take(p) },
						false => y = take(q),
					}
					return y
				}
			`,
			want: []string{
				"b0.3: use(`c`, read)",            // the target of `match c`
				"b3.0: use(`take`, read)",         // `take(p)` in the first arm reads the callee
				"b3.0: move(`p`, arg(`take`, 0))", // `take(p)` moves p, since `take` declares an owned parameter
				"b3.0: use(`y`, write)",           // `y = take(p)` writes y
				"b4.0: use(`take`, read)",         // `take(q)` in the second arm reads the callee
				"b4.0: move(`q`, arg(`take`, 0))", // `take(q)` moves q into the owned parameter
				"b4.0: use(`y`, write)",           // `y = take(q)` writes y
				"b2.0: use(`y`, read)",            // `return y` reads a `number`
			},
		},
		{
			name: "Guard",
			src: `
				fn check(p: &{ok: boolean}) -> boolean { return p.ok }
				fn go(p: {v: {ok: boolean}, n: number}) -> number {
					val r = match p {
						{v} if check(&v) => 1,
						{n} => n,
					}
					return r
				}
			`,
			want: []string{
				"b3.0: use(`check`, read)",                  // the guard `check(&v)` reads the callee
				"b3.0: borrow(&, `v`, arg(`check`, 0), '1)", // the guard passes the written borrow `&v` to `check`
				"b4.0: use(`n`, read)",                      // the second arm's body `n`
				"b3.entry: move(`p.v`, `v`)",                // the pattern `{v}` moves the field v on entry to the first arm
				"b4.entry: use(`p.n`, read)",                // the pattern `{n}` reads the `number` field n on entry to the second arm
				"b2.0: use(`r`, read)",                      // `return r` reads a `number`
			},
		},
	})
}

// TestOwnershipEventsUnionSources covers sources whose type is a union. A union with a
// reference-shaped member moves like that member, and a union whose reference-shaped members
// are borrows copies like a borrow.
func TestOwnershipEventsUnionSources(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "OwnedOrNull",
			src: `
				fn go(p: {x: number} | null) {
					val q = p
					return q
				}
			`,
			want: []string{
				"b0.0: move(`p`, `q`)",    // `val q = p` moves, since `{x: number} | null` has an owned member
				"b0.1: move(`q`, return)", // `return q`
			},
		},
		{
			name: "BorrowOrUndefined",
			src: `
				fn go(r: &{x: number} | undefined) {
					val q = r
					return q
				}
			`,
			want: []string{
				"b0.0: copy(`r`, `q`)",    // `val q = r` copies, since the union's only reference-shaped member is a borrow
				"b0.1: copy(`q`, return)", // `return q` copies the borrow on
			},
		},
	})
}

// TestOwnershipEventsBranchInsideExpression asserts that a branch the CFG builder leaves
// inside an expression attaches its events to the statement holding the expression. The
// `if` in a tuple element gets no blocks of its own, and the `log()` statement inside it has
// no CFG node, so the read of `log`, the moves out of the branches into `x.0`, and the
// borrow into `x.1` all attach to the binding of `x`.
func TestOwnershipEventsBranchInsideExpression(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "IfInTupleElement",
			src: `
				fn log() {}
				fn go(c: boolean) {
					val p = {x: 1}
					val q = {x: 2}
					val r = {x: 3}
					val x = [if c { log()
						p } else { q }, &r]
					return x
				}
			`,
			want: []string{
				"b0.3: use(`c`, read)",            // the condition of the `if` in the tuple element
				"b0.3: use(`log`, read)",          // `log()` inside the consequent
				"b0.3: move(`p`, `x.0`)",          // the consequent's tail `p` moves into element 0 of x
				"b0.3: move(`q`, `x.0`)",          // the alternative's tail `q` moves into element 0 of x
				"b0.3: borrow(&, `r`, `x.1`, '1)", // `&r` in element 1
				"b0.4: move(`x`, return)",         // `return x`
			},
		},
	})
}

// TestOwnershipEventsBorrowTypes covers sources and destinations whose type is a borrow
// spelled through an alias or a union. An alias stands for the borrow it expands to. A union
// borrows `&mut` only when every borrow member is `&mut`.
func TestOwnershipEventsBorrowTypes(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "AliasOfBorrowBinding",
			src: `
				type R = &{x: number}
				fn go(s: R) {
					val p: R = s
					return p
				}
			`,
			want: []string{
				"b0.0: copy(`s`, `p`)",    // `val p: R = s` copies, since R names a borrow
				"b0.1: copy(`p`, return)", // `return p` copies the borrow on
			},
		},
		{
			name: "AliasOfBorrowRestField",
			src: `
				type R = &{x: number}
				fn go(o: {a: {x: number}, b: R}) {
					val {a, ...rest} = o
					return [a, rest]
				}
			`,
			want: []string{
				"b0.0: move(`o.a`, `a`)",               // `val {a, ...rest} = o` moves the owned field a into a
				"b0.0: borrow(&, `o.b`, `rest.b`, '1)", // `...rest` takes the field b, whose type R names a borrow, so it borrows again
				"b0.1: move(`a`, return.0)",            // `a` in `return [a, rest]`
				"b0.1: move(`rest`, return.1)",         // `rest` in `return [a, rest]`
			},
		},
		{
			name: "MixedMutabilityUnion",
			src: `
				fn go(r: &mut {v: {x: number}} | &{v: {x: number}} | undefined) -> number {
					val {v} = r else { return 0 }
					return v.x
				}
			`,
			want: []string{
				"b2.entry: borrow(&, `r.v`, `v`, '1)", // `val {v} = r else ...` borrows immutably, since one member of r's type is `&`
				"b2.0: use(`v.x`, read)",              // `return v.x`
			},
		},
	})
}

// TestOwnershipEventsFieldWriteThroughOwnedBound asserts that a field store takes the
// ownership of the field the receiver declares when the receiver's read is a variable bounded
// below by an owned value. A module-level binding read inside a function is such a variable.
// The store of the owned q into the borrow field f is a borrow, and the checker reports the
// missing `&`.
func TestOwnershipEventsFieldWriteThroughOwnedBound(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "ModuleLevelReceiver",
			src: `
				val base = {x: 1}
				var mut h: {f: &{x: number}} = {f: &base}
				fn go() {
					val q = {x: 1}
					h.f = q
				}
			`,
			want: []string{
				"b0.1: borrow(&, `q`, `h.f`, '1)", // `h.f = q` borrows q, since h declares f as a borrow
				"b0.1: use(`h.f`, write)",         // `h.f = q` writes h.f
			},
			errs: []string{"this value is borrowed by its destination, so write the borrow: `&`"},
		},
	})
}

// TestOwnershipEventsBracketPlace asserts that a place segment whose name is not an
// identifier renders in bracket notation, so `o["a.b"]` does not read as the nested `o.a.b`.
func TestOwnershipEventsBracketPlace(t *testing.T) {
	runOwnershipEvents(t, []ownershipEventsCase{
		{
			name: "DottedKey",
			src: `
				fn go() {
					val o = {"a.b": {x: 1}}
					val q = o["a.b"]
					return q
				}
			`,
			want: []string{
				"b0.1: move(`o[\"a.b\"]`, `q`)", // `val q = o["a.b"]` moves the field named `a.b`
				"b0.2: move(`q`, return)",       // `return q`
			},
		},
	})
}

// TestOwnershipEventsTupleParts covers which tuple patterns record the elements they name
// in tupleParts. A pattern that names every element of a fixed-length tuple records its
// parts. A pattern too short for the tuple, a pattern over an array, and two patterns that
// split one place differently record that the place's moves never merge. want lists the
// entry of each place, with the parts joined by spaces, or `none` for a place whose moves
// never merge.
func TestOwnershipEventsTupleParts(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "EveryElement",
			src: `
				fn go() {
					val b = [{v: 1}, {v: 2}]
					val [x, y] = b
				}
			`,
			want: []string{"0 1"},
		},
		{
			name: "RestElement",
			src: `
				fn go() {
					val b = [{v: 1}, {v: 2}, {v: 3}]
					val [x, ...rest] = b
				}
			`,
			want: []string{"0 1..."},
		},
		{
			name: "TooShort",
			src: `
				fn go() {
					val b = [{v: 1}, {v: 2}]
					val [x] = b
				}
			`,
			want: []string{"none"},
		},
		{
			name: "Array",
			src: `
				fn go(arr: Array<{v: number}>) {
					val [x, y] = arr
				}
			`,
			want: []string{"none"},
		},
		{
			name: "MatchArmsSplitDifferently",
			src: `
				fn go(t: [{v: number}, {v: number}, {v: number}]) {
					val n = match t {
						[a, b, c] => 1,
						[a, ...rest] => 2,
					}
				}
			`,
			want: []string{"none"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			table, _ := inferOwnershipEvents(t, tc.src, "go")
			var got []string
			for _, parts := range table.tupleParts {
				if parts == nil {
					got = append(got, "none")
					continue
				}
				names := make([]string, len(parts))
				for i, seg := range parts {
					names[i] = seg.name
				}
				got = append(got, strings.Join(names, " "))
			}
			require.Equal(t, tc.want, got)
		})
	}
}
