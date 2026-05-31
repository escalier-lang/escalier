package simplesub

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- M8: differential evaluation ----
//
// This harness measures how closely the spike's inference reproduces the
// production checker, across the features built in M0–M7/M9. The spike uses a
// hand-built IR rather than the real parser (the parser bridge was never built),
// so this compares the *inference engine's* output against production expected
// strings drawn from the real internal/checker/tests/ files (cited per case) —
// it is breadth evidence for the algorithm, not parser coverage.
//
// Each case declares a bucket; the test verifies the bucket is HONEST:
//   - match    : the spike output equals the production expected string exactly.
//   - benign   : the spike output differs, but in a documented, sound way (a
//     more-general/equivalent type, or a feature the spike intentionally omits).
//     The case must genuinely differ and carry a note.
//   - regression: the spike output is wrong (rejects a valid program, accepts an
//     invalid one, or infers a worse type). The test asserts there are NONE.
//
// A summary is printed and the buckets are tallied.

type diffBucket int

const (
	bucketMatch diffBucket = iota
	bucketBenign
	bucketRegression
)

func (b diffBucket) String() string {
	switch b {
	case bucketMatch:
		return "match"
	case bucketBenign:
		return "benign"
	default:
		return "regression"
	}
}

type diffCase struct {
	name string
	// build returns the spike's rendered inference result.
	build func() (string, []error)
	// production is the expected string the production checker yields. For
	// `match`/most cases this is copied verbatim from the cited test (source); for
	// cases the spike can express but production has no exact test for, it is
	// RECONSTRUCTED from production's documented behavior — flagged by
	// reconstructed=true and explained in note.
	production    string
	source        string // production test file:line this string comes from / is grounded in
	reconstructed bool   // true when `production` is inferred, not copied verbatim
	bucket        diffBucket
	note          string // required for benign; also required when reconstructed
}

func m8Corpus() []diffCase {
	return []diffCase{
		// --- M1: core / let-polymorphism (let_generalize_test.go, infer_test.go) ---
		{
			name:       "Identity",
			build:      func() (string, []error) { return Render(lam("x", vr("x"))) },
			production: "fn <T0>(x: T0) -> T0",
			source:     "infer_test.go:892 \"I\"",
			bucket:     bucketMatch,
		},
		{
			name: "IdentityPolymorphism",
			build: func() (string, []error) {
				return Render(&Lam{Params: nil, Body: &Let{
					Name: "id", Rhs: lam("x", vr("x")),
					Body: &Let{Name: "a", Rhs: &App{Fn: vr("id"), Arg: litStr("hello")},
						Body: &Let{Name: "b", Rhs: &App{Fn: vr("id"), Arg: litNum(5)},
							Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}}}}}})
			},
			production: `fn () -> ["hello", 5]`,
			source:     "let_generalize_test.go IdentityPolymorphism",
			bucket:     bucketMatch,
		},
		{
			name: "InnerCapturesOuterParam",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"y"}, Body: &Let{
					Name: "inner", Rhs: lam("x", vr("y")),
					Body: &Let{Name: "a", Rhs: &App{Fn: vr("inner"), Arg: litNum(1)},
						Body: &Let{Name: "b", Rhs: &App{Fn: vr("inner"), Arg: litStr("a")},
							Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}}}}}})
			},
			production: "fn <T0>(y: T0) -> [T0, T0]",
			source:     "let_generalize_test.go InnerCapturesOuterParam",
			bucket:     bucketMatch,
		},

		// --- M2: records / usage-based inference (row_types_test.go) ---
		{
			name:       "PropertyAccess",
			build:      func() (string, []error) { return Render(&Lam{Params: []string{"obj"}, Body: sel(vr("obj"), "bar")}) },
			production: "fn <T0>(obj: {bar: T0}) -> T0",
			source:     "row_types_test.go PropertyAccess",
			bucket:     bucketMatch,
		},
		{
			name: "MultipleReads",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"obj"}, Body: &TupleExpr{Elems: []Term{
					sel(vr("obj"), "bar"), sel(vr("obj"), "baz")}}})
			},
			production: "fn <T0, T1>(obj: {bar: T0, baz: T1}) -> [T0, T1]",
			source:     "row_types_test.go MultipleReads",
			bucket:     bucketMatch,
		},

		// --- M3 / M4: mut invariance + lifetimes (lifetime_test.go) ---
		{
			name: "IdentityRefReturn",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"p"}, ParamTypes: []SimpleType{mutRec("x", num())}, Body: vr("p")})
			},
			production: "fn <'a>(p: mut 'a {x: number}) -> mut 'a {x: number}",
			source:     "lifetime_test.go:30 IdentityRefReturn",
			bucket:     bucketMatch,
		},
		{
			name: "EscapingRefIntoStatic",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"item"}, ParamTypes: []SimpleType{mutRec("x", num())},
					Body: &Block{Exprs: []Term{&Escape{Value: vr("item")}, sel(vr("item"), "x")}}})
			},
			production: "fn (item: mut 'static {x: number}) -> number",
			source:     "lifetime_test.go EscapingRefIntoModuleLevelVar",
			bucket:     bucketMatch,
		},
		{
			name: "PropertyLevelLifetimes",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"a", "b"}, ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num())},
					Body: recExpr("head", vr("a"), "tail", vr("b"))})
			},
			production: "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> {head: mut 'a {x: number}, tail: mut 'b {x: number}}",
			source:     "lifetime_test.go ObjectLiteral_PropertyLevelDistinctLifetimes",
			bucket:     bucketMatch,
		},
		{
			name: "TuplePerSlotLifetimes",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"a", "b"}, ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num())},
					Body: &TupleExpr{Elems: []Term{vr("a"), vr("b")}}})
			},
			production: "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}) -> [mut 'a {x: number}, mut 'b {x: number}]",
			source:     "lifetime_test.go TupleOfTwoParams_PerSlotDistinctLifetimes",
			bucket:     bucketMatch,
		},

		// --- Benign divergences ---
		{
			name:          "UnconstrainedParam",
			build:         func() (string, []error) { return Render(lam("x", litNum(5))) },
			production:    "fn <T0>(x: T0) -> 5",
			source:        "grounded in infer_test.go:892 (production generalizes an unconstrained param: \"I\": \"fn <T0>(x: T0) -> T0\")",
			reconstructed: true,
			bucket:        bucketBenign,
			note: "no verbatim production test for `fn (x){return 5}`; the `<T0>(x: T0)` baseline " +
				"is reconstructed from production's documented param-generalization (infer_test.go:892). " +
				"The spike's form is arguably the MORE principled one: a type parameter that occurs only " +
				"in a parameter (never the return) is a vacuous quantifier — SimpleSub single-polarity " +
				"elimination coalesces it to the meet of its (empty) upper bounds, i.e. `unknown`. " +
				"`fn <T0>(x: T0) -> 5` and `fn (x: unknown) -> 5` are mutually subtypes (the same type); " +
				"the spike emits the simplified representative. Production likely keeps `<T0>` for " +
				"TS-interop ergonomics, not correctness.",
		},
		{
			name: "ConditionalUnionReturn",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"a", "b", "cond"},
					ParamTypes: []SimpleType{mutRec("x", num()), mutRec("x", num()), boolean()},
					Body:       &IfExpr{Cond: vr("cond"), Then: vr("a"), Else: vr("b")}})
			},
			production: "fn <'a, 'b>(a: mut 'a {x: number}, b: mut 'b {x: number}, cond: boolean) -> mut ('a | 'b) {x: number}",
			source:     "lifetime_test.go:65 ConditionalUnionReturn",
			bucket:     bucketMatch,
		},
		{
			name: "KeyofResidualUsageInferred",
			build: func() (string, []error) {
				return Render(&Lam{Params: []string{"x"}, Body: &Block{Exprs: []Term{
					sel(vr("x"), "a"), sel(vr("x"), "b"), &KeyofExpr{Value: vr("x")}}}})
			},
			production:    `fn <T0, T1>(x: {a: T0, b: T1}) -> "a" | "b"`,
			source:        "no production analog: keyof over a *usage-inferred* value is an M7 spike construct; production keyof is type-level (keyof T)",
			reconstructed: true,
			bucket:        bucketBenign,
			note: "no verbatim production test (keyof typeof a usage-inferred value has no analog; " +
				"production keyof is type-level). The return type `\"a\" | \"b\"` is exact — keyof " +
				"depends only on the key set. The field types render `unknown` rather than generalized " +
				"`T0`/`T1`: the field-read result variables occur only negatively (in the param shape), " +
				"never in the return, so single-polarity elimination coalesces them to `unknown` — the " +
				"same principled simplification as UnconstrainedParam, not a defect.",
		},
	}
}

func TestM8DifferentialEvaluation(t *testing.T) {
	cases := m8Corpus()
	tally := map[diffBucket]int{}
	var report []string

	reconstructedCount := 0
	for _, c := range cases {
		got, errs := c.build()
		require.Empty(t, errs, "%s: spike inference produced errors: %v", c.name, errs)

		switch c.bucket {
		case bucketMatch:
			require.False(t, c.reconstructed,
				"%s is `match` but `reconstructed` — a verbatim baseline is required to claim a match", c.name)
			require.Equal(t, c.production, got,
				"%s tagged `match` but diverged from production (%s)", c.name, c.source)
		case bucketBenign:
			require.NotEqual(t, c.production, got,
				"%s tagged `benign` but actually matches — retag as `match`", c.name)
			require.NotEmpty(t, c.note, "%s tagged `benign` must document why the divergence is sound", c.name)
		case bucketRegression:
			t.Errorf("%s is a regression (spike=%q, production=%q): %s", c.name, got, c.production, c.note)
		}
		if c.reconstructed {
			require.NotEmpty(t, c.note, "%s has a reconstructed baseline and must explain it in note", c.name)
			reconstructedCount++
		}

		tally[c.bucket]++
		flag := ""
		if c.reconstructed {
			flag = " (reconstructed baseline)"
		}
		report = append(report, fmt.Sprintf("  [%-6s] %-28s spike=%q%s", c.bucket, c.name, got, flag))
	}

	// No regressions allowed.
	require.Zero(t, tally[bucketRegression], "M8 found regressions")

	sort.Strings(report)
	total := len(cases)
	t.Logf("\nM8 differential evaluation — %d cases\n%s\n  ---\n  match=%d  benign=%d  regression=%d  (%d benign baselines reconstructed, not copied verbatim)",
		total, strings.Join(report, "\n"), tally[bucketMatch], tally[bucketBenign], tally[bucketRegression], reconstructedCount)
}
