package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/solver/ucs"
	"github.com/stretchr/testify/require"
)

// These cases pin the coverage check against the shapes normalization produces, which do
// not line up one-to-one with the arms the user wrote. An arm can lose its own branch to a
// copy inside an earlier branch's fallthrough, a guarded arm can truncate the top-level
// split, and a nested pattern becomes a split of its own. The verdict has to come out the
// same as reading the arms would give.
//
// The messages a non-exhaustive form reports are pinned in full by the pattern suites and by
// TestMatchDiagnosticsNameTheMatch in ucs_walk_test.go.

// A guarded arm whose pattern makes no test becomes the split's default tail and takes every
// branch below it with it, leaving the top-level split with no branch of its own. The arms
// live on inside the tail, so the members they cover are still covered.
func TestMatchCoverageReadsArmsBelowAGuardedCatchAll(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number} | {y: string}, b: boolean) {
			return match p {
				_ if b => 0,
				{x} => x,
				{y} => 1
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number} | {y: string}, b: boolean) -> number", values["f"])
}

// A guarded arm covers nothing on its own, and a guarded catch-all does not save an inexact
// scrutinee. The open tail still holds values no structural arm can see.
func TestMatchCoverageGuardedCatchAllLeavesInexactUncovered(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number, ...}, b: boolean) {
			return match p {
				_ if b => 0,
				{x} => x
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-6:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// A failed guard falls into the arms below it, so an unguarded catch-all below a guarded one
// still covers every value. Normalization writes that continuation as the guard's own
// default rather than as another branch.
func TestMatchCoverageCatchAllBelowAGuard(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number, ...}, b: boolean) {
			return match p {
				_ if b => 0,
				_ => 1
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {x: number, ...}, b: boolean) -> 0 | 1", values["f"])
}

// An arm repeating the tag of a guarded arm above it is the continuation that guard falls
// into, so normalization drops its own branch as a duplicate. The tag it covers is read off
// the guarded branch, whose every path now reaches a body.
func TestMatchCoverageArmDroppedAsADuplicateStillCovers(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(n: 1 | 2, b: boolean) {
			return match n {
				1 if b => "guarded",
				1 => "one",
				2 => "two"
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (n: 1 | 2, b: boolean) -> "guarded" | "one" | "two"`, values["f"])
}

// A nested structural pattern becomes a split over the projection it matches. Such a split
// always matches under the interim semantics, the same reading that made `{a: {b}}` an
// irrefutable pattern, so the arm covers an exact object scrutinee.
func TestMatchCoverageNestedObjectPatternCovers(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {a: {b: number}}) {
			return match p {
				{a: {b}} => b
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, "fn (p: {a: {b: number}}) -> number", values["f"])
}

// A literal below a field is refutable, so the arm holding it covers nothing and the arm
// below it is what covers the scrutinee. Normalization clears the second arm's tag as one
// the first already proved, leaving it as what the projected literal split falls into.
func TestMatchCoverageLiteralFieldArmFallsIntoThePlainArm(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(p: {x: number}) {
			return match p {
				{x: 1} => "one",
				{x} => "other"
			}
		}
	`)
	require.Empty(t, errs)
	require.Equal(t, `fn (p: {x: number}) -> "one" | "other"`, values["f"])
}

// The same two arms over an inexact scrutinee stay non-exhaustive. Neither reaches the open
// tail's values, so the form still takes a catch-all.
func TestMatchCoverageLiteralFieldArmsLeaveInexactUncovered(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(p: {x: number, ...}) {
			return match p {
				{x: 1} => "one",
				{x} => "other"
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-6:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}

// The wording is a function of the split's origin rather than of the node the error holds.
// Lowering erases the difference between `match`, `if val`, and `val … else`, so a message
// keyed off anything else would name the wrong construct once a second form reports this.
// Only a `match` reaches the check today, which is the first row.
func TestNonExhaustiveMessageNamesTheConstruct(t *testing.T) {
	tests := map[string]struct {
		kind ucs.OriginKind
		want string
	}{
		"MatchArm": {kind: ucs.OriginMatchArm, want: "match is not exhaustive; add a catch-all branch"},
		"IfVal":    {kind: ucs.OriginIfVal, want: "if val is not exhaustive; add a catch-all branch"},
		"ValElse":  {kind: ucs.OriginValElse, want: "val ... else is not exhaustive; add a catch-all branch"},
		"Guard":    {kind: ucs.OriginGuard, want: "guard is not exhaustive; add a catch-all branch"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := &NonExhaustiveMatchError{Origin: ucs.Invented(tt.kind)}
			require.Equal(t, tt.want, err.Message())
		})
	}
}

// A literal arm covers only its own value, so a union member no literal names is uncovered
// however many arms the form has.
func TestMatchCoverageUncoveredLiteralMember(t *testing.T) {
	_, _, errs := inferSource(t, `
		fn f(n: 1 | 2 | 3) {
			return match n {
				1 => "one",
				2 => "two"
			}
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-6:5: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
}
