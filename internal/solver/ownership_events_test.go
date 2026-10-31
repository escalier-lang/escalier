package solver

import (
	"fmt"
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
// attached to.
type ownershipEventsCase struct {
	name string
	src  string
	fn   string
	want []string
}

// runOwnershipEvents asserts each case's events. Every program checks clean, since emitting
// the events reports nothing.
func runOwnershipEvents(t *testing.T, cases []ownershipEventsCase) {
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fn := tc.fn
			if fn == "" {
				fn = "go"
			}
			table, errs := inferOwnershipEvents(t, tc.src, fn)
			require.Equal(t, tc.want, renderOwnershipEvents(table))
			require.Empty(t, errs)
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
				"b0.1: borrow(&, p, c, '1)",
				"b0.2: copy(c, d)",
				"b0.3: use(p.x, write)",
				"b0.4: use(d.x, read)",
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
				"b0.1: use(n, read)",
				"b0.2: copy(r, b)",
				"b0.4: borrow(&, p, c, '1)",
				"b0.6: move(q, d)",
				"b0.7: use(a, read)",
				"b0.7: use(b.x, read)",
				"b0.7: use(c.x, read)",
				"b0.7: use(p.x, read)",
				"b0.7: use(d.x, read)",
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
				"b0.1: use(store, read)",
				"b0.1: move(a, arg(store, 0))",
				"b0.3: use(read, read)",
				"b0.3: borrow(&, b, arg(read, 0), '1)",
				"b0.4: use(b.x, read)",
				"b0.4: use(n, read)",
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
				"b0.0: move(p, sink)",
				"b0.0: use(sink, write)",
			},
		},
		{
			name: "ReadThroughUnannotatedParam",
			fn:   "read",
			src: `
				fn read(p) { return p.x }
			`,
			want: []string{
				"b0.0: copy(p.x, return)",
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
				"b0.1: borrow(&, p, f, '_)",
				"b0.2: use(f, read)",
				"b0.3: use(a, read)",
				"b0.3: use(p.x, read)",
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
				"b0.1: move(p, return)",
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
				"b0.0: use(line.width, read)",
				"b0.1: use(line.start.x, read)",
				"b0.2: borrow(&, line.start, start, '1)",
				"b0.3: borrow(&, line.start, return, '1)",
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
				"b0.1: move(p.a, a)",
				"b0.1: use(p.n, read)",
				"b0.2: move(a, return)",
				"b0.2: move(p.b, return)",
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
				"b0.0: borrow(&mut, p.a, a, '1)",
				"b0.0: use(p.n, read)",
				"b0.1: use(n, read)",
				"b0.1: use(a.x, write)",
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
				"b0.3: borrow(&, p, o.a, '1)",
				"b0.3: move(q, o.b)",
				"b0.3: move(r, o.c)",
				"b0.4: move(o, return)",
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
				"b0.2: use(c, read)",
				"b3.0: move(p, r)",
				"b4.0: move(q, r)",
				"b2.0: move(r, return)",
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
				"b0.1: move(q, o.a)",
				"b0.1: use(o.a, write)",
				"b0.4: move(s, r)",
				"b0.4: use(r, write)",
				"b0.5: move(r, return)",
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
				"b0.0: borrow(&, c, recv(get), '1)",
				"b0.1: borrow(&mut, c, recv(set), '1)",
				"b0.2: move(c, recv(take))",
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
				"b0.0: use(v.x, read)",
				"b0.0: use(v.x, read)",
				"b0.0: use(n, read)",
				"b0.0: move(p.v, v)",
				"b0.0: use(p.n, read)",
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
				"b3.0: use(v.x, read)",
				"b4.0: use(n, read)",
				"b3.-1: move(p.v, v)",
				"b4.-1: use(p.n, read)",
				"b3.1: move(v, r)",
				"b2.0: move(r, return)",
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
				"b3.0: use(v.x, read)",
				"b3.-1: move(p.v, v)",
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
				"b0.1: borrow(&mut, p, f, '_)",
				"b0.2: use(f, read)",
				"b0.3: move(p, return)",
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
				"b0.1: borrow(&, p, arg(map, 0), '_)",
				"b0.1: borrow(&, xs, recv(map), '1)",
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
				"b0.1: move(p, handler)",
				"b0.1: use(handler, write)",
			},
		},
		{
			name: "NestedClosureFlowsOutOfOuterParam",
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
				"b0.0: borrow(&, p, f, '_)",
				"b0.1: use(f, read)",
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
				"b0.0: use(n, read)",
				"b3.0: use(n, read)",
				"b3.0: move(p, arg(go, 1))",
				"b2.0: move(p, q)",
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
				"b0.0: use(read, read)",
				"b0.0: copy(p, arg(read, 0))",
				"b0.1: use(n, read)",
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
				"b0.0: use(n, read)",
				"b3.0: use(n, read)",
				"b3.0: move(p, arg(stop, 1))",
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
		key := fmt.Sprintf("b%d.%d", at.BlockID, at.StmtIdx)
		for _, ev := range events {
			got[key] = append(got[key], table.renderEvent(ev, ltNames))
		}
	}
	require.Equal(t, map[string][]string{
		"b0.1": {"borrow(&, p, q, '1)"},
		"b0.2": {"use(c, read)"},
		"b3.0": {"copy(q, d)"},
		"b3.1": {"use(p.x, write)"},
		"b3.2": {"use(d.x, read)"},
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
				"b2.-1: move(p.v, v)",
				"b2.0: use(v.x, read)",
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
				"b0.0: move(t.0, a)",
				"b0.0: move(t.1..., rest)",
				"b0.1: move(rest.0, b)",
				"b0.1: move(rest.1, c)",
				"b0.2: move(a, return)",
				"b0.2: move(b, return)",
				"b0.2: move(c, return)",
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
				"b0.1: move(o.a, a)",
				"b0.1: move(o.b, rest.b)",
				"b0.1: move(o.c, rest.c)",
				"b0.2: move(a, return)",
				"b0.2: move(rest, return)",
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
				"b3.-1: move(o.0, v)",
				"b3.0: move(v, r)",
				"b2.0: move(r, return)",
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
				"b0.3: use(c, read)",
				"b3.0: use(take, read)",
				"b3.0: move(p, arg(take, 0))",
				"b3.0: use(y, write)",
				"b4.0: use(take, read)",
				"b4.0: move(q, arg(take, 0))",
				"b4.0: use(y, write)",
				"b2.0: use(y, read)",
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
				"b3.0: use(check, read)",
				"b3.0: borrow(&, v, arg(check, 0), '1)",
				"b4.0: use(n, read)",
				"b3.-1: move(p.v, v)",
				"b4.-1: use(p.n, read)",
				"b2.0: use(r, read)",
			},
		},
	})
}
