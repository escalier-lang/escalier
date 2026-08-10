package solver

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// These cases pin what the walk over the normalized form is responsible for: first-match
// order, arm scoping, and typing each arm exactly once however many paths the normalized
// form reaches it by. The inferred types and messages a `match` produces are pinned by the
// pattern suites instead, in infer_pattern_test.go, infer_pattern_nominal_test.go,
// infer_pattern_mut_test.go, and the match cases in infer_expr_test.go.

// A `match` diagnostic names the `match` the user wrote, not a desugared form. Lowering
// erases the difference between `match`, `if val`, and `val … else`, so without the
// origin the IR carries a message about a failed pattern could name the wrong construct
// or none at all. This is the golden test for that.
func TestMatchDiagnosticsNameTheMatch(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// An inexact scrutinee carries an open tail of unknown values, so it takes a
		// catch-all arm. The wording asks for a branch, which is what a `match` is
		// written out of.
		"NonExhaustive": {
			src: `
				fn f(p: {x: number, ...}) {
					return match p {
						{x} => x
					}
				}
			`,
			want: "3:13-5:7: match is not exhaustive; add a catch-all branch",
		},
		// A guarded arm can always fail its guard, so it covers nothing and the same
		// wording applies.
		"GuardedArmDoesNotCover": {
			src: `
				fn f(p: {x: number, ...}, b: boolean) {
					return match p {
						{x} if b => x
					}
				}
			`,
			want: "3:13-5:7: match is not exhaustive; add a catch-all branch",
		},
		// An exact union takes an arm per member. The uncovered member leaves the match
		// non-exhaustive, and the message names the `match` as well as that member.
		"UnionMemberUncovered": {
			src: `
				fn f(p: {x: number} | {y: string}) {
					return match p {
						{x} => x
					}
				}
			`,
			want: "3:13-5:7: match is not exhaustive; add a branch for `{y: string}`",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A failed guard continues into the arms below it in source order, so an unguarded arm
// of the same shape stays reachable and joins the result. Normalization is what makes
// that continuation explicit, and the walk types the arm it reaches.
func TestInferMatchGuardFallsIntoTheNextArm(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}, b: boolean) {
			return match p {
				{x} if b => "guarded",
				{x} => "plain"
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (p: {x: number}, b: boolean) -> "guarded" | "plain"`, values["f"])
}

// An arm below a guarded arm is reached two ways: the guard falls into it, and the split
// falls into it when the pattern above did not match. Normalization emits the arm once per
// way, so the walk has to recognize the second as a copy of the first. Each case below
// puts a fault in the copied arm and asserts it is reported once.
func TestInferMatchTypesAFallthroughArmOnce(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// The copied arm is a catch-all, which normalization emits as the split's tail and
		// again as what the guard falls into. Its fault is in the body.
		"CatchAllBody": {
			src: `
				fn f(p: {x: number}, b: boolean) {
					return match p {
						{x} if b => 1,
						other => other.missing
					}
				}
			`,
			want: "5:22-5:29: object is missing property: missing",
		},
		// The copied arm makes a test of its own, so its fault is in the test rather than
		// below it. The test is asked about before the arm's continuation is walked, so
		// recognizing the copy at the body alone would report it twice.
		"CopiedTest": {
			src: `
				fn f(p: {x: number}, b: boolean) {
					return match p {
						{x} if b => 1,
						{y} => 2
					}
				}
			`,
			want: "2:13-2:24: object is missing property: y",
		},
		// A tuple test's fault is the whole-tuple requirement the arity mismatch fails,
		// which the copy would emit a second time.
		"CopiedTupleArity": {
			src: `
				fn f(t: [number, string], b: boolean) {
					return match t {
						[a, c] if b => 1,
						[a, c, d] => 2,
						_ => 3
					}
				}
			`,
			want: "2:13-2:29: cannot constrain tuple of length 2 <: tuple of length 3",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, msgWithSpan(errs[0]))
		})
	}
}

// A name an arm binds is scoped to that arm, including a name bound by the arm the
// split falls into. The walk puts every bind in a child scope, so `other` is gone by the
// statement after the `match`.
func TestInferMatchArmBindingDoesNotEscape(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number}, b: boolean) {
			val n = match p {
				{x} if b => 1,
				other => 2
			}
			return other
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "7:11-7:16: Unknown identifier: other", msgWithSpan(errs[0]))
}

// The target expression is inferred once, before the walk, and every arm binds against
// that one type. The ill-typed argument below is the probe: inferring `g(2)` emits one
// constraint failure, so a walk that re-inferred the target would report it once per arm.
//
// This says nothing about how many times the target runs. Evaluating it once is a
// property of the shared *ucs.Scrutinee node every projection hangs off, which
// TestPathBinderMaterializesEachScrutineeOnce pins on the solver side.
func TestInferMatchInfersTheTargetOnce(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn g(s: string) { return {x: 1} }
		fn f(b: boolean) {
			return match g(2) {
				{x} if b => x,
				{x} => x,
				_ => 0
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:19-4:20: cannot constrain 2 <: string", msgWithSpan(errs[0]))
}

// A diverging arm produces no value, so it joins nothing. A `match` whose arms all
// diverge coalesces to `never`.
func TestInferMatchAllArmsDiverge(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(n: number) throws string {
			val x = match n {
				1 => throw "one",
				_ => throw "other"
			}
			return x
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (n: number) -> never throws string", values["f"])
}

// A fallthrough runs with its branch's test known to have matched, so the walk keeps
// that test applied. Normalization drops the second arm's `Point` test, because a value
// reaching it already passed the first arm's. The extractor the first arm tested is what
// resolves `a` to the constructor's first parameter. Without it `a` would be an
// unconstrained variable, and the result would read `0` rather than `number`.
func TestInferMatchFallthroughKeepsTheMatchedTest(t *testing.T) {
	values, _, errs := inferSource(t, `
		class Point { x: number, y: number }
		fn f(p: Point, b: boolean) {
			return match p {
				Point(a, c) if b => 0,
				Point(a, c) => a
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: Point, b: boolean) -> number", values["f"])
}

// A term reached by one path keeps the matched test even when the arms below it are
// shared. Here the second arm is reached only through the first guard's failure, so its
// `x` narrows to the member the `{x}` test picked, while the `_ => 0` both guards fall
// into is the shared term and takes no test.
func TestInferMatchFallthroughKeepsTheTestPastAJoin(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}, b: boolean, c: boolean) {
			return match p {
				{x} if b => x,
				{x} if c => x,
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}, b: boolean, c: boolean) -> number", values["f"])
}

// A continuation both a failed guard and a failed test reach runs whether or not the
// shape matched, so it inherits no matched test. Here the `other` arm runs on a `p` that
// is not a `{x}` at all, and its body must read the whole union rather than the member
// the guarded arm tested.
func TestInferMatchJoinPointInheritsNoTest(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}, b: boolean) {
			return match p {
				{x} if b => 0,
				other => other
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}, b: boolean) -> 0 | {x: number} | {y: string}", values["f"])
}

// Union narrowing applies at every tag-level, not only the outermost one. Both arms here
// share the outer `{a}` shape, so the outer split narrows nothing, and each arm's inner
// split over `p.a` is what picks its member. Narrowing only at the top would bind each
// arm's leaf against both members and report the field the other member lacks.
func TestInferMatchNarrowsUnionAtEachLevel(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {a: {x: number}} | {a: {y: string}}) {
			return match p {
				{a: {x}} => x,
				{a: {y}} => y
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {a: {x: number}} | {a: {y: string}}) -> number | string", values["f"])
}

// An unguarded catch-all arm always runs, so normalization makes it the split's tail and
// leaves the arms below it out of the form the walk sees. Each one is reported unreachable
// and typed anyway, but its value stays out of the result, since the arm cannot run.
// Every arm below the catch-all is reported, and each blames the catch-all rather than the
// dead arm directly above it, so the second one still names the arm that covers it.
func TestInferMatchTypesArmsAfterACatchAll(t *testing.T) {
	src := `
		fn f(n: number) {
			return match n {
				all => all,
				1 => "one",
				2 => "two"
			}
		}
	`
	values, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		unreachableArm(5, 5, 15),
		unreachableArm(6, 5, 15),
	}, messagesWithSpan(errs))
	for _, e := range errs {
		require.Len(t, e.Related(), 1)
		require.Equal(t, "all => all", spanText(src, e.Related()[0]))
	}
	require.Equal(t, "fn (n: number) -> number", values["f"])
}

// A fault inside an unreachable arm is reported alongside the unreachable diagnostic,
// rather than going unchecked because the arm never reached the IR.
func TestInferMatchReportsFaultsAfterACatchAll(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(n: number) {
			return match n {
				all => all,
				1 => nope
			}
		}
	`)
	require.Equal(t, []string{
		unreachableArm(5, 5, 14),
		"5:10-5:14: Unknown identifier: nope",
	}, messagesWithSpan(errs))
}

// The unreachable diagnostic blames the dead arm and points at the arm that covers it, so
// both ends of the overlap are on the message.
func TestInferMatchUnreachableArmBlamesTheCoveringArm(t *testing.T) {
	src := "fn f(n: number) { return match n { all => all, 1 => 2 } }"
	_, _, errs := inferSource(t, src)
	requireBlame(t, src, errs, unreachableArm(1, 48, 54), "1 => 2", "all => all")
}

// Only a catch-all covers the arms below it. A bare `...rest` also ends the split, since
// the lowering reads no tag off it, but it is reported unsupported rather than treated as
// an arm that matches everything, so the arms below it draw no second message.
func TestInferMatchBareRestDoesNotCoverLaterArms(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(n: number) {
			return match n {
				...rest => 1,
				2 => 3
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "4:5-4:12: Unsupported: RestPat", msgWithSpan(errs[0]))
}

// A guarded catch-all can fail its condition, so it covers nothing and the arms below it
// stay reachable. Neither is reported.
func TestInferMatchGuardedCatchAllLeavesLaterArmsReachable(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(n: number, b: boolean) {
			return match n {
				all if b => all,
				1 => "one",
				_ => 0
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (n: number, b: boolean) -> number | "one"`, values["f"])
}

// An unreachable arm is typed outside the walk, so narrowArmScrutinee rather than the IR's
// tag test decides what its pattern binds against. `[a, b]` keeps only the member it can
// destructure, narrowing the exact union to `[number, number]`, so the dead arm draws the
// unreachable diagnostic alone. Binding it against the whole union would add a second message
// naming the `[string]` member the pattern was never meant to match.
func TestInferMatchUnreachableArmNarrowsItsScrutinee(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: [number, number] | [string]) {
			return match p {
				_ => 0,
				[a, b] => a
			}
		}
	`)
	require.Equal(t, []string{unreachableArm(5, 5, 16)}, messagesWithSpan(errs))
}

// unreachableArm renders the unreachable-arm message prefixed by the span of the arm it
// blames, which runs from startCol to endCol on line.
func unreachableArm(line, startCol, endCol int) string {
	return fmt.Sprintf(
		"%d:%d-%d:%d: this match arm is unreachable because an arm above it matches every value; drop it, or move it above that arm",
		line, startCol, line, endCol,
	)
}

// A nested pattern flattens into one split per tag-level, and each level's leaves bind
// off the projection the level above matched. The bound names read the nested field
// types, so the walk's projections agree with what one whole pattern would have bound.
func TestInferMatchNestedPatternBindsThroughProjections(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(l: {start: {x: number, y: string}}) {
			return match l {
				{start: {x, y}} => [x, y]
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (l: {start: {x: number, y: string}}) -> [number, string]", values["f"])
}
