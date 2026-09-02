package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// probeKnot infers src, reads the alias reference bound to `Probe`, and returns the μ-knot the
// regular-tree check proves for it, or the empty string when it proves none. Every case below
// declares the aliases under test and names the one reference it asks about as `Probe`.
//
// wantDiags is what the declarations themselves report, which is empty for most cases. An alias
// whose recursion carries its parameter only inside its own reference has a phantom parameter, and
// the unused-type-parameter check warns about it at the declaration.
func probeKnot(t *testing.T, src string, wantDiags []string) string {
	t.Helper()
	nodes, ctx, errs := inferTypeNodes(t, src)
	if len(wantDiags) == 0 {
		require.Empty(t, Messages(errs))
	} else {
		require.Equal(t, wantDiags, messagesWithSpan(t, errs))
	}
	ref, ok := nodes["Probe"].(*soltype.AliasType)
	require.True(t, ok, "Probe must bind an alias reference, got %T", nodes["Probe"])
	knot := ctx.muKnotFor(ref)
	if knot == nil {
		return ""
	}
	return soltype.Print(knot)
}

// The regular-tree check settles an alias reference whose unfolding emits one shape from that
// reference down, and declines every other. Each case names its reference `Probe` and states the
// μ-knot the check proves, or the empty string for a reference the check leaves to the plain
// expansion.
func TestMuKnotForSettlesRegularAlias(t *testing.T) {
	tests := []struct {
		name      string
		src       string
		want      string
		wantDiags []string
	}{
		{
			// The headline shape. `keyof {c: X}` is `"c"` whatever X is, so the argument stops
			// reaching the emitted body one level in and every level below the first is `{a: "c", …}`.
			name: "argument stops reaching the body one level in",
			src: `
				type H<T> = {a: keyof T, b: H<{c: T}>}
				type Probe = H<{c: number}>
			`,
			want: `μX0.{a: "c", b: X0}`,
		},
		{
			// The same alias one lap higher. `keyof number` is `never` rather than `"c"`, so this
			// reference emits a body the levels below it do not, and no knot stands for it. Expanding
			// it normally walks one lap closer, and the reference that lap emits is settled above.
			name: "the lap above the knot is not the knot",
			src: `
				type H<T> = {a: keyof T, b: H<{c: T}>}
				type Probe = H<number>
			`,
			want: "",
		},
		{
			// The parameter never reaches the emitted body at all, so the knot is already tied at the
			// reference the source wrote. markPhantomParams reaches the same conclusion about this
			// alias from its declaration alone, and settles `Deep<number> <: Deep<string>` by making
			// the two references intern to one identity.
			name: "parameter never reaches the body",
			src: `
				type Deep<T> = {a: Deep<{b: T}>}
				type Probe = Deep<number>
			`,
			want: "μX0.{a: X0}",
			wantDiags: []string{
				"2:15-2:16: no argument passed to type parameter T can appear in the type, so " +
					"Deep<number> and Deep<string> are the same type",
			},
		},
		{
			// Two recursive references growing the argument the same way emit one body, so one binder
			// stands for both positions.
			name: "two references growing the argument alike",
			src: `
				type Fork<T> = {a: keyof T, l: Fork<{c: T}>, r: Fork<{c: T}>}
				type Probe = Fork<{c: number}>
			`,
			want: `μX0.{a: "c", l: X0, r: X0}`,
		},
		{
			// Two recursive references growing the argument differently emit different bodies, so
			// which reference a level was reached through decides the tree below it and one knot
			// cannot stand for both.
			name: "two references growing the argument differently",
			src: `
				type Split<T> = {a: keyof T, l: Split<{c: T}>, r: Split<{d: T}>}
				type Probe = Split<{c: number}>
			`,
			want: "",
		},
		{
			// Each parameter grows independently, and neither reaches the emitted body past the first
			// level, so the pair of them settles the same way one does.
			name: "two parameters, both stopping",
			src: `
				type Pair<T, U> = {a: keyof T, b: keyof U, rest: Pair<{x: T}, {y: U}>}
				type Probe = Pair<{x: number}, {y: string}>
			`,
			want: `μX0.{a: "x", b: "y", rest: X0}`,
		},
		{
			// The parameter reaches the emitted body at every level, so the instantiations denote
			// different trees and none of them is regular. maxUnwrapDepth stays the backstop for this
			// shape — see TestConstrainNonRegularAliasStillReachesTheBudget.
			name: "parameter keeps reaching the body",
			src: `
				type Nest<T> = {here: T, deeper: Nest<{b: T}>}
				type Probe = Nest<number>
			`,
			want: "",
		},
		{
			// The recursion passes its argument through unchanged, so the instantiation repeats and
			// constrain's seen-set closes on it with no knot. Declining here is what keeps the
			// ordinary recursive alias comparing and rendering the way it does without this check.
			name: "recursion repeats its instantiation",
			src: `
				type List<T> = {head: T, tail?: List<T>}
				type Probe = List<number>
			`,
			want: "",
		},
		{
			// A non-generic alias has no argument to grow, so its instantiation repeats for the same
			// reason.
			name: "no type parameter to grow",
			src: `
				type Cycle = {next: Cycle}
				type Probe = Cycle
			`,
			want: "",
		},
		{
			// The recursive reference is a spread's operand, so abstracting it leaves `{...X0}`. This
			// alias does have a knot, `μX0.{a: "c", b: X0}`, since spreading one operand into an
			// otherwise empty object yields that operand. The check declines it anyway, because
			// reduction has no rule for grounding a spread whose operand is a μ-variable, and the
			// residual spread would reach constrain and fail a comparison against a real object that
			// should hold. The comparison falls back to the budget, which is where it sat before this
			// check existed.
			name: "recursive reference under an object spread",
			src: `
				type Sp<T> = {a: keyof T, b: {...Sp<{c: T}>}}
				type Probe = Sp<{c: number}>
			`,
			want: "",
		},
		{
			// The positional twin, declined for the same reason and with the same knot going begging.
			// `[...X]` over a tuple is X, so `Tp<{c: number}>` is `μX0.["c", X0]`. Proving that knot
			// would buy nothing on its own: `[...Tp<…>]` fails to check against a real tuple whether or
			// not a knot exists, since the first unfolding runs on the plain expansion and reduction
			// cannot ground that spread either.
			name: "recursive reference under a tuple spread",
			src: `
				type Tp<T> = [keyof T, [...Tp<{c: T}>]]
				type Probe = Tp<{c: number}>
			`,
			want: "",
		},
		{
			// A spread of a NON-recursive operand does merge, so the level emits the tuple the splice
			// yields and the knot is proven over that. This is the positional twin of the object shape
			// above, and it reads its elements through the same widening in unreducedOp.
			name: "tuple spreads a non-recursive operand",
			src: `
				type Pair = [number, string]
				type WT<T> = [keyof T, ...Pair, WT<{c: T}>]
				type Probe = WT<{c: number}>
			`,
			want: `μX0.["c", number, string, X0]`,
		},
		{
			// The object twin of the case above, for the pair to be read together.
			name: "object spreads a non-recursive operand",
			src: `
				type Base = {tag: string}
				type WO<T> = {a: keyof T, ...Base, b: WO<{c: T}>}
				type Probe = WO<{c: number}>
			`,
			want: `μX0.{a: "c", tag: string, b: X0}`,
		},
		{
			// Nothing stands between the knot and its own binder, so `μX0.X0` would unfold to itself
			// and close against any super at all. coalesce's tie carries the same guard.
			name: "knot body would be the bare binder",
			src: `
				type Bare<T> = [Bare<{a: T}>][0]
				type Probe = Bare<number>
			`,
			want: "",
			wantDiags: []string{
				"2:15-2:16: no argument passed to type parameter T can appear in the type, so " +
					"Bare<number> and Bare<string> are the same type",
			},
		},
		{
			// The same degeneracy behind a transparent alias, which unfolds to whatever it was handed.
			name: "knot body would be a bare alias reference",
			src: `
				type Id<X> = X
				type Wrapped<T> = Id<Wrapped<{a: T}>>
				type Probe = Wrapped<number>
			`,
			want: "",
			wantDiags: []string{
				"3:18-3:19: no argument passed to type parameter T can appear in the type, so " +
					"Wrapped<number> and Wrapped<string> are the same type",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, probeKnot(t, tt.src, tt.wantDiags))
		})
	}
}

// A lap of this alias's recursion emits no structure, so checkProductive rejects it at its
// declaration and it names no type to normalize. It sits outside the table above because that
// rejection is a diagnostic, and every case in the table infers cleanly.
func TestMuKnotForDeclinesUnproductiveAlias(t *testing.T) {
	nodes, ctx, errs := inferTypeNodes(t, `
		type Grow<T> = Grow<{a: T}>
		type Probe = Grow<number>
	`)
	require.Equal(t, []string{notProductiveMsg("2:8-2:12", "Grow")}, messagesWithSpan(t, errs))
	ref, ok := nodes["Probe"].(*soltype.AliasType)
	require.True(t, ok, "Probe must bind an alias reference, got %T", nodes["Probe"])
	require.Nil(t, ctx.muKnotFor(ref))
}

// A value whose own type is a μ-knot checks against a regular alias whose instantiations never
// repeat. The first unfolding runs on the alias's expansion, and the second hands constrain the
// knot, so both sides are knots from there down and the seen-set closes on them. Without the
// normalization the alias side would grow its argument forever and the comparison would be cut off
// with an ExpansionLimitError.
//
// `node` returns a knot because its recursive call makes its own return variable's lower bound
// mention it, which is the shape coalesce renders as a μ form. The one unrolled level in front of the
// knot is the monomorphic-recursion artifact TestInferRecursiveRendersMuKnot describes.
//
// The recursion is unguarded, so calling `node` never returns. That is forced rather than careless:
// `H` has no base case, so no finite value inhabits `H<{c: number}>` and only an infinite value can
// be checked against it. TestInferGuardedRecursionRendersMuKnot carries the terminating shapes.
func TestConstrainRegularAliasClosesOnItsKnot(t *testing.T) {
	values, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		fn node() { return {a: "c", b: node()} }
		fn use() -> H<{c: number}> { return node() }
	`)
	require.Equal(t,
		[]string{nonReturningMsg("3:6-3:10", "node", `fn () -> {a: "c", b: μX0.{a: "c", b: X0}}`)},
		messagesWithSpan(t, errs))
	require.Equal(t, `fn () -> {a: "c", b: μX0.{a: "c", b: X0}}`, values["node"])
	require.Equal(t, "fn () -> H<{c: number}>", values["use"])
}

// The reference one lap above the knot checks too. Its own level emits `keyof number`, which is
// `never`, so the value has to supply a `never` there; the knot settles every level below it. The
// mismatch on the first field is what proves the level was compared rather than skipped.
func TestConstrainRegularAliasChecksTheLapAboveTheKnot(t *testing.T) {
	_, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		fn node() { return {a: "c", b: node()} }
		fn use() -> H<number> { return node() }
	`)
	require.Equal(t, []string{
		nonReturningMsg("3:6-3:10", "node", `fn () -> {a: "c", b: μX0.{a: "c", b: X0}}`),
		`4:3-4:42: cannot constrain "c" <: never`,
	}, messagesWithSpan(t, errs))
}

// Normalizing does not make the comparison blind: a value that disagrees with the knot's body is
// still rejected. The mismatch is reported twice, once for each unfolding the comparison makes. The
// first unfolding runs on the alias's expansion and the second on the knot, since evalTypeOperator
// waits for the second before substituting one. Without the normalization the same source reports
// the mismatch once per lap until maxUnwrapDepth cuts the walk off, roughly two hundred times, and
// then adds two ExpansionLimitErrors on top.
func TestConstrainRegularAliasStillReportsAMismatch(t *testing.T) {
	_, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		fn node() { return {a: "wrong", b: node()} }
		fn use() -> H<{c: number}> { return node() }
	`)
	require.Equal(t, []string{
		nonReturningMsg("3:6-3:10", "node", `fn () -> {a: "wrong", b: μX0.{a: "wrong", b: X0}}`),
		`4:3-4:47: cannot constrain "wrong" <: "c"`,
		`4:3-4:47: cannot constrain "wrong" <: "c"`,
	}, messagesWithSpan(t, errs))
}

// Two instantiations of a regular alias whose argument stops reaching its emitted body denote one
// tree, so the comparison between them succeeds. `keyof {c: X}` is `"c"` for both, and neither the
// `number` nor the `string` appears anywhere in the tree either side denotes. Both sides normalize
// to the one memoized knot on their second unfolding, so the lap after that asks a pair an earlier
// lap already assumed and the seen-set closes on it. Without the normalization neither side would
// ever repeat an instantiation and the comparison would be cut off at maxUnwrapDepth.
func TestConstrainRegularAliasInstantiationsAgree(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Chain<T> = {a: keyof T, b: Chain<{c: T}>}
		declare fn make() -> Chain<{c: number}>
		fn use() -> Chain<{c: string}> { return make() }
	`)
	require.Empty(t, Messages(errs))
}

// An alias whose parameter reaches its emitted body at every level denotes a genuinely non-regular
// tree, which no finite knot represents. The check proves nothing for it and maxUnwrapDepth cuts the
// comparison off, the same outcome as before the check existed.
func TestConstrainNonRegularAliasStillReachesTheBudget(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Nest<T> = {here: T, deeper: Nest<{b: T}>}
		declare fn make() -> Nest<number>
		fn use() -> Nest<string> { return make() }
	`)
	require.Equal(t, []string{
		"4:3-4:45: cannot constrain number <: string",
		"4:3-4:45: comparing two instantiations of `Nest` reached the limit of 200 type-operator " +
			"expansions and was cut off; either the two sides recurse without ever repeating a pair " +
			"the check can close on, or their alias chains run deeper than the limit unfolds",
	}, messagesWithSpan(t, errs))
}

// A malformed member inside an alias body reports once and does not stop the alias being normalized.
// Reduction substitutes the ErrorType sentinel for `{x: number}["z"]`, so the level the alias emits
// is a real node carrying `error` at that position and the knot is proven over it. The diagnostic
// comes from the plain expansion, which runs before any knot is substituted, and `error` absorbs
// everywhere below, so nothing derived from it cascades.
func TestConstrainRegularAliasKeepsAReductionDiagnostic(t *testing.T) {
	nodes, ctx, _ := inferTypeNodes(t, `
		type H<T> = {a: keyof T, e: {x: number}["z"], b: H<{c: T}>}
		type Probe = H<{c: number}>
	`)
	ref, ok := nodes["Probe"].(*soltype.AliasType)
	require.True(t, ok, "Probe must bind an alias reference, got %T", nodes["Probe"])
	knot := ctx.muKnotFor(ref)
	require.NotNil(t, knot)
	require.Equal(t, `μX0.{a: "c", e: error, b: X0}`, soltype.Print(knot))

	_, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, e: {x: number}["z"], b: H<{c: T}>}
		fn node() { return {a: "c", e: 1, b: node()} }
		fn use() -> H<{c: number}> { return node() }
	`)
	require.Equal(t, []string{
		nonReturningMsg("3:6-3:10", "node", `fn () -> {a: "c", e: 1, b: μX0.{a: "c", e: 1, b: X0}}`),
		`4:3-4:47: object {x: number} has no property "z"`,
	}, messagesWithSpan(t, errs))
}

// A member read off a regular alias keeps the alias name on the type it yields. evalTypeOperator
// waits for the second unfolding before substituting a knot, and a member read makes only the first,
// so the value it pulls out is recorded as the alias reference the expansion emitted.
func TestMemberReadOnRegularAliasKeepsTheAliasName(t *testing.T) {
	values, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		declare fn mk() -> H<{c: number}>
		fn field() { return mk().b }
	`)
	require.Empty(t, Messages(errs))
	require.Equal(t, "fn () -> H<{c: {c: number}}>", values["field"])
}

// A tuple alias that splices a non-recursive operand every lap checks against another of its own
// instantiations. Both denote one tree, since `keyof {c: X}` is `"c"` whatever X is and the spliced
// elements are fixed. Reducing the splice is what makes the level a plain tuple the knot can be read
// off; without it the level keeps a `...Pair` element, no knot is proven, and the comparison is cut
// off at maxUnwrapDepth.
func TestConstrainRegularTupleAliasSplicesItsSpread(t *testing.T) {
	_, _, errs := inferSource(t, `
		type Pair = [number, string]
		type WT<T> = [keyof T, ...Pair, WT<{c: T}>]
		declare fn mk() -> WT<{c: number}>
		fn use() -> WT<{c: {c: number}}> { return mk() }
	`)
	require.Empty(t, Messages(errs))
}
