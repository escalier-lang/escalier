package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A `try` block collects what it raises into a sink of its own, so an exception the catch
// arms cover never reaches the enclosing function's clause. A catch-all covers every member
// at once, so it always leaves the clause untouched, whatever the block raises. Naming each
// member individually does the same job where the block's throws are known, which
// TestInferTryCatchRethrowsWhatTheArmsLeave covers.
func TestInferTryCatchHandlesWithACatchAll(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			name: "CatchAllLetsAClauselessFunctionThrow",
			src:  `fn f() { try { throw "boom" } catch { e => 0 } }`,
			want: "fn () -> undefined",
		},
		{
			name: "CatchAllLetsAClauselessFunctionCallAThrowingCallee",
			src: `
				fn a() throws string { throw "boom" }
				fn f() { try { a() } catch { e => 0 } }
			`,
			want: "fn () -> undefined",
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
//
// This is the one place the tail is rendered. A rethrow drops it, since a throws clause is
// open already and would gain nothing from carrying it, but a catch arm matches an actual
// value and has to reckon with one the block did not predict.
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
// clause is the members no arm covers.
//
// The caught union's open tail is NOT among them, even though no arm short of a catch-all
// covers it. Every throws type is already open, since any call can raise something its
// signature did not name, so carrying the tail into the clause would re-state that once per
// `try` and, since only `unknown` can hold it, erase every named type the clause had. The
// tail stays where it is observable, on the caught binding, which
// TestInferTryCatchBindsAnInexactUnion covers.
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
			want: `fn (c: boolean) -> undefined throws "boom"`,
		},
		{
			// Nothing known escapes, so the clause is untouched and a caller with no clause
			// of its own is legal. This is the point of covering the members a callee
			// declares: the handling is what removes the obligation.
			name: "CoveringEveryKnownMemberLeavesNoClause",
			src:  `fn f() { try { throw "boom" } catch { "boom" => 0 } }`,
			want: "fn () -> undefined",
		},
		{
			// A guarded arm can always fail its guard, so it covers nothing and its member
			// is rethrown.
			name: "AGuardedArmCoversNothing",
			src:  `fn f() throws _ { try { throw "boom" } catch { "boom" if true => 0 } }`,
			want: `fn () -> undefined throws "boom"`,
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// A clause-less function raises `never`, so an uncovered member has nowhere to
			// land. The blame is the try/catch, not the `throw "boom"` inside the block:
			// that `throw` IS caught, and what fails is the re-raise past the arms.
			name: "RethrowNeedsAClause",
			src:  `fn f() { try { throw "boom" } catch { 5 => 0 } }`,
			wantErrs: []string{
				"1:10-1:45: the catch arms leave \"boom\" uncovered, so it is rethrown, and the enclosing `throws never` does not admit it. Cover it with a catch arm, or widen the enclosing clause",
			},
		},
		{
			// A clause narrower than what escapes fails the same way. Only the uncovered
			// member is named, so the diagnostic points at the one type to account for.
			name: "RethrowMustSatisfyANarrowClause",
			src:  `fn f(c: boolean) throws string { try { if c { throw "a" } else { throw 5 } } catch { "a" => 0 } }`,
			wantErrs: []string{
				"1:34-1:94: the catch arms leave 5 uncovered, so it is rethrown, and the enclosing `throws string` does not admit it. Cover it with a catch arm, or widen the enclosing clause",
			},
		},
	})
}

// noArmsMsg is the MissingCatchArmError message, shared by every case below so the span is
// the only thing each one spells out.
const noArmsMsg = "a `try` with no catch arms catches nothing; drop the `try` and keep its block, or add at least one catch arm"

// The catch arms are what catch an exception, so a `try` without them catches nothing and
// is rejected. The block recovers as if written on its own, which keeps the missing arms
// to a single diagnostic: its throws reach the enclosing sink unchanged, with no caught
// binding and no open tail added on top.
//
// The blame span is where the two spellings differ. An omitted `catch` has nothing past the
// try block to point at, so the node ends there. A written `catch` ends at its closing
// brace, so the empty braces fall inside the blame and it lands where the arm goes.
func TestInferTryWithoutCatchArmsIsRejected(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name:     "OmittedCatchIsRejected",
			src:      `fn f() throws string { try { throw "boom" } }`,
			wantErrs: []string{"1:24-1:44: " + noArmsMsg},
		},
		{
			// A written `catch { }` reaches the same diagnostic, since `ast.TryCatchExpr`
			// records only the arms and both spellings leave that slice empty. The message
			// names both ways out, so it stays true whichever was written.
			name:     "WrittenButEmptyCatchIsRejected",
			src:      `fn f() throws string { try { throw "boom" } catch { } }`,
			wantErrs: []string{"1:24-1:54: " + noArmsMsg},
		},
		{
			// The parser skips comments while looking for cases, so a comment between the
			// braces still leaves no arms, and the closing brace on line 4 still ends the
			// node.
			name: "ACommentIsNotAnArm",
			src: `fn f() throws string {
	try { throw "boom" } catch {
		// an arm goes here
	}
}`,
			wantErrs: []string{"2:2-4:3: " + noArmsMsg},
		},
		{
			// The block's `throw` is checked against the enclosing clause exactly as it
			// would be with no `try` around it, so the missing arms are the only extra
			// diagnostic.
			name: "TheBlockStillReachesTheEnclosingClause",
			src:  `fn f() { try { throw "boom" } }`,
			wantErrs: []string{
				"1:10-1:30: " + noArmsMsg,
				`1:22-1:28: cannot constrain "boom" <: never`,
			},
		},
	})
}

// A `try` at module top level has no funcCtx to hold its nested sink, so it installs the
// checker's module-level one instead. Without that the block collects nothing and every
// caught binding reads `unknown`.
func TestInferTryCatchAtModuleTopLevel(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// want maps a top-level binding's name to its rendered type. A case naming two
		// bindings asserts a relationship between them that one binding cannot show.
		want map[string]string
	}{
		{
			name: "CaughtBindingCollectsTheBlocksThrows",
			src:  `val caught = try { throw "fail" } catch { msg => msg }`,
			want: map[string]string{"caught": `"fail" | ...`},
		},
		{
			// The install is undone when the block finishes, so a later `try` over a quiet
			// block collects nothing rather than inheriting the earlier block's throws.
			name: "TheSinkDoesNotLeakBetweenDecls",
			src: `
				val a = try { throw "one" } catch { e => e }
				val b = try { val q = 1 } catch { e => e }
			`,
			want: map[string]string{"a": `"one" | ...`, "b": "unknown"},
		},
		{
			// throwsSink reads the module-level sink only when there is no funcCtx, so a
			// function declared inside the block collects into its own signature. The block
			// diverges, so the binding is the caught type alone, and `"inner"` is absent
			// from it while the block's own `"outer"` is there.
			name: "ANestedFunctionKeepsItsOwnSink",
			src: `
				val caught = try {
					val g = fn () throws _ { throw "inner" }
					throw "outer"
				} catch { e => e }
			`,
			want: map[string]string{"caught": `"outer" | ...`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, _, errs := inferSource(t, test.src)
			require.Empty(t, errs)
			for name, want := range test.want {
				require.Equal(t, want, values[name])
			}
		})
	}
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
			want: "fn () -> undefined",
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
			want: "fn () -> undefined",
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
			want: "fn () -> 0 throws string",
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
			want: "fn () -> undefined throws BarError",
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

// What escapes a `try` is the set difference `caught & ¬handled`, where handled is the type
// the arms catch. Taking a difference rather than matching each member against each pattern
// by name is what lets an arm subtract more than the one type it spells: an arm naming a base
// class catches every value of a subclass, so a subclass member is subtracted too.
func TestInferTryCatchSubtractsThroughSubtyping(t *testing.T) {
	// base declares a root error class, a subclass of it, and an unrelated class, so a case
	// can vary which of the three an arm names.
	const base = `
		class AppError { code: number, constructor(mut self) { self.code = 0 } }
		class ParseError extends AppError { constructor(mut self) { super() } }
		class OtherError { tag: string, constructor(mut self) { self.tag = "" } }
	`
	runThrowsCases(t, []throwsCase{
		{
			// The block raises the subclass, and the arm names the base class. Every
			// ParseError is an AppError, so nothing is left over and the enclosing function
			// needs no clause.
			name: "ABaseClassArmSubtractsASubclassMember",
			src: base + `
				fn a() throws ParseError { throw ParseError() }
				fn f() { try { a() } catch { AppError{code} => code } }
			`,
			want: "fn () -> undefined",
		},
		{
			// The reverse does not subtract. An AppError need not be a ParseError, so the
			// member survives the difference and reaches the clause.
			name: "ASubclassArmLeavesTheBaseClassMember",
			src: base + `
				fn a() throws AppError { throw AppError() }
				fn f() throws _ { try { a() } catch { ParseError{code} => code } }
			`,
			want: "fn () -> undefined throws AppError",
		},
		{
			// One arm subtracts both members it covers, and the member outside the class
			// hierarchy is rethrown on its own.
			name: "ABaseClassArmLeavesAnUnrelatedMember",
			src: base + `
				fn a(c: boolean) throws ParseError | OtherError {
					if c { throw ParseError() } else { throw OtherError() }
				}
				fn f(c: boolean) throws _ { try { a(c) } catch { AppError{code} => code } }
			`,
			want: "fn (c: boolean) -> undefined throws OtherError",
		},
	})
}

// A generic class arm behaves exactly like a non-generic one. `catch { Failure{payload} => … }`
// tests the class alone, so it catches every Failure whatever type argument the value carries,
// including a Timeout that inherits from it.
//
// The difference is what reads this. Deciding `Timeout<number> <: Failure<T>`, for T the base
// class's own parameter, binds T, and memberSubtracted permits that because T belongs to the
// arm rather than to the member. Watching every variable instead would decline the subtraction
// and rethrow a member the arm demonstrably catches.
func TestInferTryCatchSubtractsThroughGenericClasses(t *testing.T) {
	// base declares a generic root error class and a generic subclass of it, so a case can
	// vary whether the arm names the member's own class or its base.
	const base = `
		class Failure<T> {
			payload: T,
			constructor(mut self, payload: T) { self.payload = payload }
		}
		class Timeout<T> extends Failure<T> {
			constructor(mut self, payload: T) { super(payload) }
		}
	`
	runThrowsCases(t, []throwsCase{
		{
			// The arm names the member's own class, so the type argument is all that has to
			// be decided and nothing escapes.
			name: "AnArmNamingTheMembersOwnGenericClassCatchesIt",
			src: base + `
				fn a() throws Failure<number> { throw Failure(0) }
				fn f() { try { a() } catch { Failure{payload} => 0 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// The arm names the member's base class. Every Timeout<number> is a
			// Failure<number>, so the member is subtracted and the function needs no clause,
			// the same verdict the non-generic pair of
			// TestInferTryCatchSubtractsThroughSubtyping reaches.
			name: "ABaseClassArmSubtractsAGenericSubclassMember",
			src: base + `
				fn a() throws Timeout<number> { throw Timeout(0) }
				fn f() { try { a() } catch { Failure{payload} => 0 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// The reverse still does not subtract. A Failure<number> need not be a Timeout, so
			// the member survives the difference and reaches the clause.
			name: "ASubclassArmLeavesTheGenericBaseClassMember",
			src: base + `
				fn a() throws Failure<number> { throw Failure(0) }
				fn f() throws _ { try { a() } catch { Timeout{payload} => 0 } }
			`,
			want: "fn () -> undefined throws Failure<number>",
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
// still fires. The nested sink is what makes that visible: every exceptional exit the body
// has was consumed inside the `try`.
func TestInferTryCatchLeavesAnUnusedClauseUnused(t *testing.T) {
	values, _, errs := inferSource(t, `fn f() throws string { try { throw "boom" } catch { e => 0 } }`)
	require.Len(t, errs, 1)
	require.Equal(t,
		"1:15-1:21: the body raises nothing, so the declared `throws string` is unreachable; drop the clause",
		msgWithSpan(errs[0]))
	require.Equal(t, "fn () -> undefined throws string", values["f"])
}

// Coverage is decided against the type an alias stands for, not against the alias handle. A
// transparent alias, an enum handle or a user `type` reference, carries the alias rather than
// its underlying union, and no arm's type is a supertype of the handle. Without expanding
// first, an aliased error type would read as uncovered however many arms name its members, so
// naming a union would behave unlike spelling it inline.
func TestInferTryCatchExpandsAliasedErrorTypes(t *testing.T) {
	runThrowsCases(t, []throwsCase{
		{
			// The control: the same union written inline, which never needed expanding.
			name: "InlineUnionIsCovered",
			src: `
				fn a() throws "a" | "b" { throw "a" }
				fn f() { try { a() } catch { "a" => 0, "b" => 1 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// The same union behind a name has to reach the same verdict.
			name: "AliasedUnionIsCovered",
			src: `
				type Err = "a" | "b"
				fn a() throws Err { throw "a" }
				fn f() { try { a() } catch { "a" => 0, "b" => 1 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// An enum registers as a transparent alias over its variant union, so its
			// variants are covered by extractor patterns the same way.
			name: "EnumVariantsAreCovered",
			src: `
				enum Color { Red, Green }
				fn a() throws Color { throw Color.Red() }
				fn f() { try { a() } catch { Color.Red => 0, Color.Green => 1 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// Expanding repeats through a union whose own members are aliases, so an alias
			// nested inside another reaches the arms as its underlying members.
			name: "ANestedAliasIsCovered",
			src: `
				type Inner = "a" | "b"
				type Outer = Inner | "c"
				fn a() throws Outer { throw "a" }
				fn f() { try { a() } catch { "a" => 0, "b" => 1, "c" => 2 } }
			`,
			want: "fn () -> undefined",
		},
		{
			// Partial coverage of a nested alias rethrows only the members left over. Without
			// the repeated expansion the whole of `Inner` would be weighed as one member,
			// which no arm covers, so the clause would carry `Inner` and name the `"a"` the
			// arm did catch.
			name: "OnlyTheUncoveredMemberOfANestedAliasIsRethrown",
			src: `
				type Inner = "a" | "b"
				type Outer = Inner | "c"
				fn a() throws Outer { throw "a" }
				fn f() throws _ { try { a() } catch { "a" => 0, "c" => 2 } }
			`,
			want: `fn () -> undefined throws "b"`,
		},
	})
	runThrowsErrCases(t, []throwsErrCase{
		{
			// Expanding does not make coverage lax. A member left unnamed is still rethrown,
			// and it is named by the type it expands to rather than by the alias.
			name: "AnUnnamedMemberOfAnAliasIsStillRethrown",
			src: `
				type Err = "a" | "b"
				fn a() throws Err { throw "a" }
				fn f() { try { a() } catch { "a" => 0 } }
			`,
			wantErrs: []string{
				"4:14-4:42: the catch arms leave \"b\" uncovered, so it is rethrown, and the enclosing `throws never` does not admit it. Cover it with a catch arm, or widen the enclosing clause",
			},
		},
	})
}

// A catch arm is a refutable context, so a structural pattern over a union of caught types
// binds against only the members it can destructure. `[a, b]` keeps `[number, number]` and
// drops `[string]`, which is what stops the arm from reporting a length mismatch against a
// member it was never meant to match. One diagnostic remains. It comes from the open `...`
// tail caughtType puts on every caught type, not from the dropped member. A fixed-arity tuple
// pattern cannot destructure that tail, so `[a, b]` still fails against
// `[number, number] | ...`.
func TestInferTryCatchArmNarrowsItsScrutinee(t *testing.T) {
	runThrowsErrCases(t, []throwsErrCase{
		{
			name: "TuplePatternKeepsOnlyTheMembersItDestructures",
			src: `
				fn f(b: boolean) {
					return try {
						if b { throw [1, 2] } else { throw ["a"] }
					} catch {
						[a, b] => a,
						e => 0
					}
				}
			`,
			wantErrs: []string{
				"6:7-6:13: cannot constrain tuple | ... <: tuple",
			},
		},
	})
}
