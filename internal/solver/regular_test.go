package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// probeKnot infers src, reads the alias reference bound to `Probe`, and returns the μ-knot the
// regular-tree check proves for it, or the empty string when it proves none. Every case below
// declares the aliases under test and names the one reference it asks about as `Probe`.
func probeKnot(t *testing.T, src string) string {
	t.Helper()
	nodes, ctx, errs := inferTypeNodes(t, src)
	require.Empty(t, Messages(errs))
	ref, ok := nodes["Probe"].(*soltype.AliasType)
	require.True(t, ok, "Probe must bind an alias reference, got %T", nodes["Probe"])
	knot := ctx.muKnotFor(ref, newSeenPairs())
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
		name string
		src  string
		want string
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, probeKnot(t, tt.src))
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
	require.Equal(t, []string{notProductiveMsg("2:8-2:12", "Grow")}, messagesWithSpan(errs))
	ref, ok := nodes["Probe"].(*soltype.AliasType)
	require.True(t, ok, "Probe must bind an alias reference, got %T", nodes["Probe"])
	require.Nil(t, ctx.muKnotFor(ref, newSeenPairs()))
}

// A value whose own type is a μ-knot checks against a regular alias whose instantiations never
// repeat. Both sides reach constrain as knots, so its seen-set closes on the second lap. Without the
// normalization the alias side would grow its argument forever and the comparison would be cut off
// with an ExpansionLimitError.
//
// `node` returns a knot because its recursive call makes its own return variable's lower bound
// mention it, which is the shape coalesce renders as a μ form. The one unrolled level in front of the
// knot is the monomorphic-recursion artifact TestInferRecursiveRendersMuKnot describes.
func TestConstrainRegularAliasClosesOnItsKnot(t *testing.T) {
	values, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		fn node() { return {a: "c", b: node()} }
		fn use() -> H<{c: number}> { return node() }
	`)
	require.Empty(t, Messages(errs))
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
	require.Equal(t, []string{`4:3-4:42: cannot constrain "c" <: never`}, messagesWithSpan(errs))
}

// Normalizing does not make the comparison blind: a value that disagrees with the knot's body is
// still rejected, and once rather than once per level.
func TestConstrainRegularAliasStillReportsAMismatch(t *testing.T) {
	_, _, errs := inferSource(t, `
		type H<T> = {a: keyof T, b: H<{c: T}>}
		fn node() { return {a: "wrong", b: node()} }
		fn use() -> H<{c: number}> { return node() }
	`)
	require.Equal(t, []string{`4:3-4:47: cannot constrain "wrong" <: "c"`}, messagesWithSpan(errs))
}

// Two instantiations of a regular alias whose argument stops reaching its emitted body denote one
// tree, so the comparison between them succeeds. `keyof {c: X}` is `"c"` for both, and neither the
// `number` nor the `string` appears anywhere in the tree either side denotes. Both normalize to the
// memoized knot, so the second lap of the comparison asks the pair the first lap already assumed and
// the seen-set closes on it. Without the normalization neither side would ever repeat an
// instantiation and the comparison would be cut off at maxUnwrapDepth.
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
	}, messagesWithSpan(errs))
}
