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

// Each form below leaves a value matching no arm, and each reports one error naming the
// `match`. The span runs from `match` to the closing brace of its arms.
func TestMatchCoverageNonExhaustive(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// A guarded arm covers nothing on its own, and a guarded catch-all does not save an
		// inexact scrutinee. The open tail still holds values no structural arm can see.
		"GuardedCatchAllOverInexact": {
			src: `
				fn f(p: {x: number, ...}, b: boolean) {
					return match p {
						_ if b => 0,
						{x} => x
					}
				}
			`,
			want: "3:13-6:7: match is not exhaustive; add a catch-all branch",
		},
		// A literal below a field is refutable and the plain arm below it reaches only the
		// values the scrutinee's fields describe, so the open tail stays uncovered.
		"LiteralFieldArmsOverInexact": {
			src: `
				fn f(p: {x: number, ...}) {
					return match p {
						{x: 1} => "one",
						{x} => "other"
					}
				}
			`,
			want: "3:13-6:7: match is not exhaustive; add a catch-all branch",
		},
		// A literal arm covers only its own value, so a union member no literal names is
		// uncovered however many arms the form has.
		"UncoveredLiteralMember": {
			src: `
				fn f(n: 1 | 2 | 3) {
					return match n {
						1 => "one",
						2 => "two"
					}
				}
			`,
			want: "3:13-6:7: match is not exhaustive; add a catch-all branch",
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

// An arm below a fallible branch is copied into that branch's fallthrough, specialized
// against the tag the branch tested. A branch inside such a copy covers only values that
// already passed that tag, so its test is no coverage claim about the scrutinee's other
// values. Each form below leaves a member uncovered behind a copy that reads as covering.
func TestMatchCoverageIgnoresBranchesUnderAnotherTag(t *testing.T) {
	// A `{b: string}` value takes the second arm, whose guard can fail into a tail no arm
	// covers. The `{b}` branch that does cover sits inside the first arm's fallthrough, which
	// only a `{a: number}` value reaches.
	t.Run("ObjectUnion", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			fn f(v: {a: number} | {b: string}, g: boolean) {
				return match v {
					{a} if g => 0,
					{b} if g => 1,
					{a} => 2
				}
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "3:12-7:6: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
	})

	// The same shape on the nominal path. `Color.RGB(0, g, b)` matches only when the first
	// field is 0, so it covers no variant, and the covering `Color.Hex` branch sits inside the
	// first arm's fallthrough. `Color.RGB(1, 2, 3)` matches no arm.
	t.Run("EnumVariants", func(t *testing.T) {
		_, _, errs := inferSource(t, `
			enum Color {
				RGB(r: number, g: number, b: number),
				Hex(code: string),
			}
			fn f(c: Color) {
				return match c {
					Color.Hex("x") => 0,
					Color.RGB(0, g, b) => 1,
					Color.Hex(code) => 2
				}
			}
		`)
		require.Len(t, errs, 1)
		require.Equal(t, "7:12-11:6: match is not exhaustive; add a catch-all branch", msgWithSpan(errs[0]))
	})
}

// A bare `...rest` arm is reported unsupported by the pass that binds it, and the coverage
// check adds nothing on top. The arm binds every value, so asking for a catch-all would name
// a branch the user already wrote, and the arms below it are out of the split entirely.
func TestMatchCoverageBareRestArmReportsOnlyTheUnsupportedPattern(t *testing.T) {
	tests := map[string]string{
		"Alone": `
			fn f(p: {x: number}) {
				return match p {
					...rest => 0
				}
			}
		`,
		"AboveACatchAll": `
			fn f(n: 1 | 2) {
				return match n {
					...rest => 0,
					_ => 1
				}
			}
		`,
	}

	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, src)
			require.Len(t, errs, 1)
			require.Equal(t, "4:6-4:13: Unsupported: RestPat", msgWithSpan(errs[0]))
		})
	}
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
