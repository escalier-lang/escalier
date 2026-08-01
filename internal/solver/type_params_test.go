package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeParamDefaultForwardRef covers the ordering rule on a type parameter's default. It
// may name only a parameter declared before it. A reference that omits a trailing argument
// fills it from that parameter's default, substituting the arguments before it, so a default
// naming a later parameter or itself has nothing to substitute and would carry the
// declaration's own var into the instance.
//
// The rule reaches every `<…>` list, since the alias, class, enum, and function-annotation
// paths all resolve their parameters through resolveTypeParams. It covers the default
// position alone, so a bound naming a later sibling still resolves.
func TestTypeParamDefaultForwardRef(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "AliasLaterParam",
			src: `
				type Pair<T = U, U = number> = {a: T, b: U}
				val p: Pair<string, number> = {a: "s", b: 1}
			`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		{
			name: "AliasSelf",
			src: `
				type Loop<T = T> = {v: T}
				val l: Loop<number> = {v: 1}
			`,
			want: []string{"the default for type parameter `T` cannot reference `T` itself"},
		},
		// The reference is nested inside a type argument rather than written bare, so the
		// scan has to reach it through the annotation rather than read the default's head.
		{
			name: "AliasNestedInTypeArgument",
			src: `
				type Box<X> = {v: X}
				type Wrap<T = Box<U>, U = number> = {a: T, b: U}
				val w: Wrap<Box<number>, number> = {a: {v: 1}, b: 2}
			`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		{
			name: "ClassLaterParam",
			src:  `class C<T = U, U = number> { value: T }`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		{
			name: "EnumLaterParam",
			src:  `enum Opt<T = U, U = number> { Some(T), None }`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		{
			name: "FuncAnnotationLaterParam",
			src:  `val f: fn<T = U, U = number>(x: T) -> T = fn (x) { return x }`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		// A default naming an earlier parameter is what the rule allows, since that
		// parameter's argument is already resolved when the default fills the later slot.
		{
			name: "AliasEarlierParamAccepted",
			src: `
				type Pair<T = number, U = T> = {a: T, b: U}
				val p: Pair = {a: 1, b: 2}
			`,
		},
		// A bound is not substituted positionally, so a mutual F-bound keeps resolving.
		{
			name: "MutualBoundAccepted",
			src: `
				type Pair<T: U, U: T> = {a: T, b: U}
				val p: Pair<number, number> = {a: 1, b: 2}
			`,
		},
		// The nested `fn <U>(…)` quantifier declares its own `U`, so the `U` inside it reads
		// that binder rather than the later sibling parameter.
		{
			name: "NestedQuantifierShadowsAccepted",
			src: `
				type Holder<T = fn<U>(x: U) -> U, U = number> = {a: T, b: U}
				val h: Holder<fn<X>(x: X) -> X, number> = {a: fn (x) { return x }, b: 1}
			`,
		},
		// A mapped type's `[U: "a"]` key is its own binder, so the `U` the value position
		// reads is that key rather than the later sibling parameter.
		{
			name: "MappedKeyShadowsAccepted",
			src: `
				type Named<T = {[U: "a"]: U}, U = number> = {a: T, b: U}
			`,
		},
		// An `infer U` clause binds `U` for the conditional's Then branch, so the `U` that
		// branch reads is the capture rather than the later sibling parameter.
		{
			name: "InferClauseShadowsAccepted",
			src: `
				type Pick<T = if [number] : [infer U] { U } else { never }, U = string> = {a: T, b: U}
			`,
		},
		// A default that reaches two later parameters names both, so one pass over the reported
		// errors is enough to fix it.
		{
			name: "TwoLaterParamsBothReported",
			src: `
				type Bad<T = {a: U, b: V}, U = number, V = string> = {t: T, u: U, v: V}
			`,
			want: []string{
				"the default for type parameter `T` cannot reference `U`, which is declared after it",
				"the default for type parameter `T` cannot reference `V`, which is declared after it",
			},
		},
		// One name written twice needs one fix, so it reports once, blaming its leftmost
		// reference.
		{
			name: "RepeatedLaterParamReportedOnce",
			src: `
				type Bad<T = {a: U, b: U}, U = number> = {t: T, u: U}
			`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		// A binder covers its own region and no more. The mapped key `U` is nested inside the
		// `m` property, so the `b: U` beside it reads the later sibling parameter and is
		// reported even though the same name is bound elsewhere in the default.
		{
			name: "MappedKeyDoesNotShadowSibling",
			src: `
				type Bad<T = {m: {[U: "a"]: number}, b: U}, U = unknown> = {t: T, u: U}
			`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
		// An `infer U` capture does not reach the Else branch, matching where
		// resolveCondTypeAnn declares the name, so the `U` there reads the later sibling.
		{
			name: "InferClauseDoesNotShadowElseBranch",
			src: `
				type Bad<T = if [number] : [infer U] { number } else { U }, U = unknown> = {t: T, u: U}
			`,
			want: []string{"the default for type parameter `T` cannot reference `U`, which is declared after it"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, len(tt.want))
			for i, want := range tt.want {
				require.Equal(t, want, errs[i].Message())
			}
		})
	}
}

// TestTypeParamDefaultForwardRefLeavesParamRequired checks the recovery. A rejected default is
// dropped rather than replaced, which leaves that parameter required, so a reference that omits
// it reports an arity mismatch alongside the ordering error. `T` is the first parameter, so no
// defaulted parameter sits before it and buildAliasInstance's count catches the omission. `U`
// keeps its own `= number` default, so the range the arity message states starts at one rather
// than two.
func TestTypeParamDefaultForwardRefLeavesParamRequired(t *testing.T) {
	src := `
		type Pair<T = U, U = number> = {a: T, b: U}
		val p: Pair = {a: 1, b: 2}
	`
	_, _, errs := inferSource(t, src)
	require.Len(t, errs, 2)
	require.Equal(t, "the default for type parameter `T` cannot reference `U`, which is declared after it", errs[0].Message())
	require.Equal(t, "type alias `Pair` expects between 1 and 2 type arguments but got 0", errs[1].Message())
}

// TestTypeParamDefaultIsPerReference checks that a default filled at one reference does not
// reach another. `U = T` substitutes the argument that reference wrote for `T`, so `p` and `q`
// each carry their own second argument instead of sharing one var declared on the alias.
func TestTypeParamDefaultIsPerReference(t *testing.T) {
	src := `
		type Pair<T, U = T> = {a: T, b: U}
		val p: Pair<number> = {a: 1, b: 2}
		val q: Pair<string> = {a: "s", b: "t"}
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, errs)
	require.Equal(t, "Pair<number, number>", values["p"])
	require.Equal(t, "Pair<string, string>", values["q"])
}

// TestTypeParamDefaultAgainstBound covers the declaration-site check that a type parameter's
// default satisfies its own bound. The default fills the argument at every use site that omits
// it, so a default outside the bound would supply an argument the bound forbids. The check is
// shared by every `<…>` clause, so a class, an alias, and a function each report it, and a bound
// naming a sibling parameter is compared the same way.
func TestTypeParamDefaultAgainstBound(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ClassDefaultOutsideBound",
			src:  `class Box<T: string = number> { value: T }`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "ClassDefaultInsideBound",
			src:  `class Box<T: string = "hi"> { value: T }`,
			want: nil,
		},
		{
			name: "AliasDefaultOutsideBound",
			src:  `type Box<T: string = number> = {value: T}`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "FuncDefaultOutsideBound",
			src:  `fn id<T: string = number>(x: T) -> T { return x }`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "DefaultNamesSiblingWithBound",
			src:  `class Box<U: string, T: U = number> { value: T }`,
			want: []string{"cannot constrain number <: string"},
		},
		{
			name: "DefaultNamesLaterSiblingBound",
			src:  `class Box<T: U = number, U: string> { value: T }`,
			want: []string{"cannot constrain number <: string"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, errs := inferSource(t, test.src)
			var msgs []string
			for _, e := range errs {
				msgs = append(msgs, e.Message())
			}
			require.Equal(t, test.want, msgs)
		})
	}
}
