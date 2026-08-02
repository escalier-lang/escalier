package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A `try` block collects what it raises into a sink of its own, so an exception the catch
// arms handle never reaches the enclosing function's clause. A catch-all handles the whole
// caught union, tail included, which is what lets a function with no `throws` clause wrap a
// call to a throwing one.
func TestInferTryCatchHandlesWithACatchAll(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "CatchAllLetsAClauselessFunctionThrow",
			src:  `fn f() { try { throw "boom" } catch { e => 0 } }`,
			want: "fn () -> void",
		},
		{
			name: "CatchAllLetsAClauselessFunctionCallAThrowingCallee",
			src: `
				fn a() throws string { throw "boom" }
				fn f() { try { a() } catch { e => 0 } }
			`,
			want: "fn () -> void",
		},
		{
			// A catch arm body is walked against the enclosing sink, not the nested one, so
			// an exception it raises propagates the way any other exit does.
			name: "AnArmBodyRaisesIntoTheEnclosingClause",
			src:  `fn f() throws _ { try { throw "boom" } catch { e => throw 5 } }`,
			want: "fn () -> never throws 5",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// The arm's own `throw` has no clause to reach, so it is rejected at the throw
			// exactly as it would be outside a `try`.
			name:     "AnArmBodyStillNeedsAClause",
			src:      `fn f() { try { throw "boom" } catch { e => throw 5 } }`,
			wantErrs: []string{"1:50-1:51: cannot constrain 5 <: never"},
		},
	})
}

// The caught union is open. Any call can raise something no signature predicted, so the
// members the try block is known to raise are only the part the type system can name.
func TestInferTryCatchBindsAnInexactUnion(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// The arm returns the caught binding, so the function's return type is the
			// caught type.
			name: "CaughtBindingCarriesAnOpenTail",
			src:  `fn f() { try { throw "boom" } catch { e => { return e } } }`,
			want: `fn () -> "boom" | ...`,
		},
		{
			name: "CaughtBindingUnionsEveryRaisedType",
			src: `
				fn f(c: boolean) {
					try {
						if c { throw "boom" } else { throw 5 }
					} catch {
						e => { return e }
					}
				}
			`,
			want: `fn (c: boolean) -> 5 | "boom" | ...`,
		},
		{
			// A block with no known exceptional exit still binds something, since the tail
			// is what remains. `unknown` is that tail on its own.
			name: "CaughtBindingOfAQuietBlockIsUnknown",
			src: `
				fn f() {
					try {
						val x = 1
					} catch {
						e => { return e }
					}
				}
			`,
			want: "fn () -> unknown",
		},
	})
}

// Without a catch-all the arms leave part of the caught union unhandled, and the compiler
// rethrows it rather than reporting the arms non-exhaustive. What reaches the enclosing
// clause is the members no arm covers, plus the open tail.
func TestInferTryCatchRethrowsWhatTheArmsLeave(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "UncoveredMemberReachesTheClause",
			src: `
				fn f(c: boolean) throws _ {
					try {
						if c { throw "boom" } else { throw 5 }
					} catch {
						5 => 0
					}
				}
			`,
			want: `fn (c: boolean) -> void throws "boom" | ...`,
		},
		{
			// Covering every named member leaves only the tail, so the clause is `unknown`.
			// Nothing short of a catch-all closes an open union.
			name: "CoveringEveryMemberStillLeavesTheTail",
			src:  `fn f() throws _ { try { throw "boom" } catch { "boom" => 0 } }`,
			want: "fn () -> void throws unknown",
		},
		{
			// A guarded arm can always fail its guard, so it covers nothing and its member
			// is rethrown alongside the tail.
			name: "AGuardedArmCoversNothing",
			src:  `fn f() throws _ { try { throw "boom" } catch { "boom" if true => 0 } }`,
			want: `fn () -> void throws "boom" | ...`,
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// A clause-less function raises `never`, which is closed, so both the rethrown
			// member and the open tail are rejected against it.
			name: "RethrowNeedsAClause",
			src:  `fn f() { try { throw "boom" } catch { 5 => 0 } }`,
			wantErrs: []string{
				`1:10-1:45: cannot constrain "boom" | ... <: never`,
				`1:22-1:28: cannot constrain "boom" <: never`,
			},
		},
		{
			name:     "TheBareTailNeedsAClauseToo",
			src:      `fn f() { try { throw "boom" } catch { "boom" => 0 } }`,
			wantErrs: []string{"1:10-1:50: cannot constrain unknown <: never"},
		},
	})
}

// A `try` with no `catch` clause inspects nothing, so it is transparent: the block's
// throws reach the enclosing clause unchanged, with no caught binding and no open tail.
func TestInferTryWithoutCatchIsTransparent(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "ThrowsPassThroughUnwidened",
			src:  `fn f() throws string { try { throw "boom" } }`,
			want: "fn () -> never throws string",
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "AClauselessFunctionIsStillRejected",
			src:      `fn f() { try { throw "boom" } }`,
			wantErrs: []string{`1:22-1:28: cannot constrain "boom" <: never`},
		},
	})
}

// A `try` inside another `try`'s block installs its own sink over the outer one, so the
// inner form's leftovers reach the OUTER arms rather than the function's clause.
func TestInferTryCatchNests(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "InnerRemainderReachesTheOuterArms",
			src: `
				fn f(c: boolean) {
					try {
						try {
							if c { throw "boom" } else { throw 5 }
						} catch {
							5 => 0
						}
					} catch {
						e => { return e }
					}
				}
			`,
			want: `fn (c: boolean) -> "boom" | ...`,
		},
		{
			// The outer catch-all closes what the inner form left over, so the function
			// still needs no clause.
			name: "AnOuterCatchAllAbsorbsTheInnerRemainder",
			src: `
				fn f() {
					try {
						try { throw "boom" } catch { 5 => 0 }
					} catch {
						e => 1
					}
				}
			`,
			want: "fn () -> void",
		},
		{
			// A nested function owns its own throws sink, so a `try` in the enclosing body
			// never collects what the inner function raises.
			name: "ANestedFunctionsThrowsAreNotCaught",
			src: `
				fn f() {
					try {
						val g = fn () throws _ { throw "inner" }
						val y = 1
					} catch {
						e => 0
					}
				}
			`,
			want: "fn () -> void",
		},
		{
			// The nested sink is minted at the enclosing body's level, not at the deeper
			// level a `val` initializer is typed at. A sink minted deep would be extruded by
			// a later exceptional exit, and the resulting cycle would render as a μ-knot.
			name: "SinkInAValInitializerStaysAtTheBodyLevel",
			src: `
				fn a() throws string { throw "boom" }
				fn f() throws _ {
					val x = try { a() } catch { 5 => 0 }
					return x
				}
			`,
			want: "fn () -> 0 throws string | ...",
		},
	})
}

// A class-shaped catch arm covers the member whose class it names and leaves the rest.
// Coverage is decided by the same relation `match` exhaustiveness reads, so an instance
// pattern covers a nominal member exactly as it does in a `match`.
func TestInferTryCatchOverClassErrors(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "AnUnnamedClassMemberIsRethrown",
			src: `
				class FooError { msg: string, constructor(mut self, msg: string) { self.msg = msg } }
				class BarError { msg: string, constructor(mut self, msg: string) { self.msg = msg } }
				fn a() throws FooError | BarError { throw FooError("x") }
				fn f() throws _ { try { a() } catch { FooError{msg} => msg } }
			`,
			want: "fn () -> void throws BarError | ...",
		},
		{
			// The arm binds the narrowed member, so `msg` is the named class's field rather
			// than a property read against the whole caught union.
			name: "AnArmDestructuresTheMemberItNames",
			src: `
				class FooError { msg: string, constructor(mut self, msg: string) { self.msg = msg } }
				class BarError { code: number, constructor(mut self, code: number) { self.code = code } }
				fn a() throws FooError | BarError { throw FooError("x") }
				fn f() { try { a() } catch { FooError{msg} => { return msg }, e => { return 0 } } }
			`,
			want: "fn () -> string | 0",
		},
	})
}

// The form's value is the join of the try block's tail value and each non-diverging arm
// body, the same branch join `match` builds from its arms.
func TestInferTryCatchValue(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "ValueJoinsTheBlockAndTheArms",
			src:  `fn f() { return try { 1 } catch { e => 2 } }`,
			want: "fn () -> 1 | 2",
		},
		{
			// A diverging arm contributes nothing, so only the block's value survives.
			name: "ADivergingArmDropsOutOfTheJoin",
			src:  `fn f() throws _ { return try { 1 } catch { e => throw 5 } }`,
			want: "fn () -> 1 throws 5",
		},
		{
			// Every path leaves, so the form diverges and the body reaches no normal exit.
			name: "EveryPathDivergingMakesTheFormDiverge",
			src:  `fn f() -> never throws _ { try { throw "boom" } catch { e => throw 5 } }`,
			want: "fn () -> never throws 5",
		},
	})
}

// A fully handled `try` leaves the enclosing clause unreached, so the unused-clause warning
// PR10 added still fires. The nested sink is what makes that visible: the body's exceptional
// exits were all consumed inside the `try`.
func TestInferTryCatchLeavesAnUnusedClauseUnused(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() throws string { try { throw "boom" } catch { e => 0 } }`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"1:15-1:21: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
		msgWithSpan(errs[0]))
	require.Equal(t, "fn () -> void throws string", values["f"])
}
