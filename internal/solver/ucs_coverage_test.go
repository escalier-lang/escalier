package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
	"github.com/stretchr/testify/require"
)

// These cases pin the coverage check against the shapes normalization produces, which do
// not line up one-to-one with the arms the user wrote. An arm can lose its own branch to a
// copy inside an earlier branch's fallthrough, a guarded arm can truncate the top-level
// split, and a nested pattern becomes a split of its own. The verdict has to come out the
// same as reading the arms would give.
//
// The messages a non-exhaustive form reports are pinned in full by the pattern suites, by
// TestMatchDiagnosticsNameTheMatch in ucs_walk_test.go, and by
// TestNonExhaustiveMessageNamesWhatEscapes below.

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
			want: "3:13-6:7: match is not exhaustive; add a branch for `3`",
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
		require.Equal(t, "3:12-7:6: match is not exhaustive; `{b: string}` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it", msgWithSpan(errs[0]))
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
		require.Equal(t, "7:12-11:6: match is not exhaustive; `Color.RGB` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably", msgWithSpan(errs[0]))
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

// An annotation test covers whichever values of the scrutinee its type admits. That is a
// fact about the annotation rather than about a pattern's shape, so it is asked of every
// union member kind rather than dispatched on the member the way a shape tag is.
func TestMatchCoverageCreditsAnnotationTests(t *testing.T) {
	tests := map[string]struct {
		src string
		// wantErrs is the full set of expected messages, and empty when the arms cover the
		// scrutinee.
		wantErrs []string
	}{
		// Primitive members, each named by the arm annotated with it.
		"PrimitiveMembers": {
			src: `
				fn f(u: number | string) {
					return match u {
						x: number => x,
						y: string => y
					}
				}
			`,
		},
		// Literal members. A literal type annotation admits the one value its member holds.
		"LiteralMembers": {
			src: `
				fn f(u: 1 | 2) {
					return match u {
						x: 1 => x,
						y: 2 => y
					}
				}
			`,
		},
		// An annotation test and a shape tag credit different members of one union, so the
		// two kinds of tag add up.
		"AnnotationAndShapeTag": {
			src: `
				fn f(u: number | {x: number}) {
					return match u {
						n: number => n,
						{x} => x
					}
				}
			`,
		},
		// A guarded arm covers nothing, since a false condition falls past it. The annotation
		// it tests does not change that, though it does name the member the message reports.
		"GuardedAnnotatedArm": {
			src: `
				fn f(u: number | string, b: boolean) {
					return match u {
						x: number if b => x,
						y: string => y
					}
				}
			`,
			wantErrs: []string{"3:13-6:7: match is not exhaustive; `number` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it"},
		},
		// An annotation naming no type resolves to nothing, so the arm credits no member. The
		// walk reports the missing type once, and this check resolves the annotation again
		// without repeating that diagnostic.
		"UnresolvableAnnotation": {
			src: `
				fn f(u: number | string) {
					return match u {
						x: Nope => 0
					}
				}
			`,
			wantErrs: []string{
				"4:10-4:14: cannot find type `Nope`",
				"3:13-5:7: match is not exhaustive; add branches for `number`, `string`",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantErrs, messagesWithSpan(errs))
		})
	}
}

// The message names what escapes and the edit that would cover it, rather than always asking
// for a catch-all. Each row leaves a value uncovered for a different reason, and the readings
// the coverage walk takes of a branch are what tell those reasons apart.
func TestNonExhaustiveMessageNamesWhatEscapes(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// No branch names the second member, so the message asks for one matching its shape.
		"UnionMemberNoArmNames": {
			src: `
				fn f(p: {x: number} | {y: string}) {
					return match p {
						{x} => x
					}
				}
			`,
			want: "match is not exhaustive; add a branch for `{y: string}`",
		},
		// Two members escape at once, so both are named in source order.
		"SeveralUnionMembers": {
			src: `
				fn f(p: {x: number} | {y: string} | {z: boolean}) {
					return match p {
						{x} => x
					}
				}
			`,
			want: "match is not exhaustive; add branches for `{y: string}`, `{z: boolean}`",
		},
		// The arm's shape covers the whole exact scrutinee, and only its guard lets a value
		// fall through, so the fix is an unguarded branch rather than a catch-all.
		"GuardedArmOverExactObject": {
			src: `
				fn f(p: {x: number}, b: boolean) {
					return match p {
						{x} if b => x
					}
				}
			`,
			want: "match is not exhaustive; `{x: number}` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it",
		},
		// One member is named by a guarded arm and the other by nothing, so the message
		// carries a clause for each.
		"GuardedMemberAlongsideAnUnnamedOne": {
			src: `
				fn f(p: {x: number} | {y: string}, b: boolean) {
					return match p {
						{x} if b => 0
					}
				}
			`,
			want: "match is not exhaustive; add a branch for `{y: string}`; `{x: number}` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it",
		},
		// A catch-all arm names every value, so a guard on it is the only reason any member
		// falls through and every member takes the guarded wording.
		"GuardedCatchAllOverUnion": {
			src: `
				fn f(p: {x: number} | {y: string}, b: boolean) {
					return match p {
						q if b => 0
					}
				}
			`,
			want: "match is not exhaustive; `{x: number}`, `{y: string}` are matched only by guarded branches, whose guards can fail, so add unguarded branches for them",
		},
		// `{x: 1}` names the scrutinee's shape but matches only when the field is 1, so the
		// fix is a branch binding that shape irrefutably.
		"RefutableSubPattern": {
			src: `
				fn f(p: {x: number}) {
					return match p {
						{x: 1} => 10
					}
				}
			`,
			want: "match is not exhaustive; `{x: number}` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably",
		},
		// An enum's witness is the variant an arm has to name.
		"EnumVariant": {
			src: `
				enum Color {
					RGB(r: number, g: number, b: number),
					Hex(code: string),
				}
				fn f(c: Color) {
					return match c {
						Color.RGB(r, g, b) => r
					}
				}
			`,
			want: "match is not exhaustive; add a branch for `Color.Hex`",
		},
		// A generic enum's variant renders without the type arguments the instance carries,
		// since `MyOption.None` is what the missing arm writes.
		"GenericEnumVariant": {
			src: `
				enum MyOption<T> {
					Some(value: T),
					None,
				}
				fn f(o: MyOption<number>) {
					return match o {
						MyOption.Some(value) => value
					}
				}
			`,
			want: "match is not exhaustive; add a branch for `MyOption.None`",
		},
		// A member no shape tag can name is still named, since an annotated arm such as
		// `n: number => n` covers whichever member its type admits.
		"PrimitiveMember": {
			src: `
				fn f(x: number | string) {
					return match x {
						1 => 1
					}
				}
			`,
			want: "match is not exhaustive; add branches for `number`, `string`",
		},
		// A transparent alias member is uncovered whatever shape the arms name, since the
		// coverage rules read the member rather than the type it stands for. An annotated arm
		// `c: C => 2` is what covers it, so the alias name is the witness to report.
		"AliasUnionMember": {
			src: `
				type C = {y: string}
				fn f(p: {x: number} | C) {
					return match p {
						{x} => 1
					}
				}
			`,
			want: "match is not exhaustive; add a branch for `C`",
		},
		// A structural scrutinee's witness is the shape behind its annotation, so a covering
		// pattern's fields are visible where an alias name would hide them.
		"AliasStructuralScrutinee": {
			src: `
				type P = {x: number}
				fn f(p: P) {
					return match p {
						{x: 1} => 10
					}
				}
			`,
			want: "match is not exhaustive; `{x: number}` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably",
		},
		// An inexact scrutinee's open tail holds values no pattern names, so there is no
		// witness to report and a catch-all is the only edit that covers it.
		"InexactObjectHasNoWitness": {
			src: `
				fn f(p: {x: number, ...}) {
					return match p {
						{x} => x
					}
				}
			`,
			want: "match is not exhaustive; add a catch-all branch",
		},
		// An inexact union's tail takes a catch-all whatever its members, and its uncovered
		// members each still take a branch. The message asks for both. A literal arm covers
		// only its own value, so neither `number` nor `string` is covered here.
		"InexactUnionNamesMembersAndCatchAll": {
			src: `
				fn f(b: boolean) {
					val x: number | string | ... = if b { 1 } else { "b" }
					return match x {
						1 => 1,
						"b" => 2
					}
				}
			`,
			want: "match is not exhaustive; add branches for `number`, `string`, and a catch-all branch",
		},
		// The same inexact union with every member covered. Only the tail is left, so the
		// catch-all is the whole of the advice.
		"InexactUnionWithEveryMemberCovered": {
			src: `
				fn f(b: boolean) {
					val x: number | string | ... = if b { 1 } else { "b" }
					return match x {
						n: number => 1,
						s: string => 2
					}
				}
			`,
			want: "match is not exhaustive; add a catch-all branch",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.Equal(t, tt.want, errs[0].Message())
		})
	}
}

// Every clause of the message has a singular and a plural form, and the catch-all clause
// combines with each of them. The sources above reach only some of those pairings, so these
// rows build the error directly and pin the rest.
func TestNonExhaustiveMessagePluralForms(t *testing.T) {
	tests := map[string]struct {
		err  *NonExhaustiveMatchError
		want string
	}{
		// One unmatched witness alongside an open tail. The sources above reach this pairing
		// only with two or more witnesses.
		"OneUnmatchedWithCatchAll": {
			err:  &NonExhaustiveMatchError{Unmatched: []soltype.Type{numLit(1)}, NeedsCatchAll: true},
			want: "match is not exhaustive; add a branch for `1`, and a catch-all branch",
		},
		// An open tail with no unmatched witness leaves the catch-all to stand as its own
		// clause, ahead of whatever the other groups ask for.
		"GuardedWithCatchAll": {
			err:  &NonExhaustiveMatchError{Guarded: []soltype.Type{numLit(1)}, NeedsCatchAll: true},
			want: "match is not exhaustive; add a catch-all branch; `1` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it",
		},
		"TwoRefutable": {
			err:  &NonExhaustiveMatchError{Refutable: []soltype.Type{numLit(1), numLit(2)}},
			want: "match is not exhaustive; `1`, `2` are matched only by branches whose own patterns can fail, so add branches that match them irrefutably",
		},
		"OneOfEach": {
			err: &NonExhaustiveMatchError{
				Unmatched: []soltype.Type{numLit(1)},
				Guarded:   []soltype.Type{numLit(2)},
				Refutable: []soltype.Type{numLit(3)},
			},
			want: "match is not exhaustive; add a branch for `1`; `2` is matched only by a guarded branch, whose guard can fail, so add an unguarded branch for it; `3` is matched only by a branch whose own pattern can fail, so add a branch that matches it irrefutably",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.err.Message())
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
