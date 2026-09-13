package dts_to_esc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A type parameter occurring once, among the parameters, is dropped and its one
// occurrence becomes what it was constrained to.
func TestElideVacuousTypeParams(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// std:boolean's call signature, the case #1579 names first.
			name: "AnUnconstrainedParameterBecomesUnknown",
			src:  `export declare class Boolean { <T>(value?: T) -> boolean }`,
			want: "(value?: unknown) -> boolean",
		},
		{
			name: "AConstrainedParameterBecomesItsConstraint",
			src:  `export declare class C { m<T: string>(self, v: T) -> boolean }`,
			want: "m(self, v: string) -> boolean",
		},
		{
			name: "AConstrainedParameterKeepsAWiderConstraint",
			src:  `export declare class C { m<T: string | number>(self, v: T) -> boolean }`,
			want: "m(self, v: string | number) -> boolean",
		},
		{
			name: "AnObjectTypeMemberIsRewrittenToo",
			src:  `export type F = { m<T>(v: T) -> boolean }`,
			want: "m(v: unknown) -> boolean",
		},

		// Left alone.
		{
			name: "AParameterUsedTwiceStays",
			src:  `export declare class C { m<T>(self, v: T) -> T }`,
			want: "m<T>(self, v: T) -> T",
		},
		{
			name: "AParameterUsedTwiceAmongTheParametersStays",
			src:  `export declare class C { m<T>(self, a: T, b: T) -> boolean }`,
			want: "m<T>(self, a: T, b: T) -> boolean",
		},
		{
			// Nothing infers it, but rewriting it changes what a call yields.
			name: "AReturnOnlyParameterStays",
			src:  `export declare class C { m<T>(self) -> T }`,
			want: "m<T>(self) -> T",
		},
		{
			// Object.fromEntries. Its parameter is vacuous only because the
			// conversion dropped the return that used it.
			name: "ADefaultedParameterStays",
			src:  `export declare class C { static m<T = any>(entries: Iterable<T>) -> {} }`,
			want: "static m<T = any>(entries: Iterable<T>) -> {}",
		},
		{
			// std:set's difference. #1579 asks for `ReadonlySetLike<unknown>` here,
			// which is not sound: `ReadonlySetLike<U>` declares `has(value: U)`, so
			// `ReadonlySetLike<unknown>` is not a supertype of the
			// `ReadonlySetLike<string>` a caller passes today.
			name: "AnOccurrenceInsideATypeArgumentStays",
			src:  `export declare class C<T> { difference<U>(self, other: SetLike<U>) -> C<T> }`,
			want: "difference<U>(self, other: SetLike<U>) -> C<T>",
		},
		{
			// A parameter of a callback parameter is contravariant, so widening it
			// rejects callbacks the generic form accepted.
			name: "AnOccurrenceInsideACallbackParameterStays",
			src:  `export declare fn each<T>(cb: fn (x: T) -> undefined) -> undefined`,
			want: "fn each<T>(cb: fn (x: T) -> undefined) -> undefined",
		},
		{
			// An inner signature rebinding the name contributes an occurrence, so
			// the outer parameter is left alone rather than having the inner one
			// rewritten under it.
			name: "AShadowedNameStays",
			src:  `export declare fn outer<T>(cb: fn <T>(x: T) -> boolean) -> undefined`,
			want: "fn outer<T>(cb: fn<T> (x: T) -> boolean) -> undefined",
		},
		{
			name: "AParameterAnotherParameterConstrainsStays",
			src:  `export declare class C { m<T, U: T>(self, v: T) -> U }`,
			want: "m<T, U: T>(self, v: T) -> U",
		},
		{
			name: "AParameterUsedInAThrowsStays",
			src:  `export declare fn f<T>(v: T) -> boolean throws T`,
			want: "fn f<T>(v: T) -> boolean throws T",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod := &StandaloneModule{Module: parseSource(t, tt.src)}
			elideVacuousTypeParams(mod)

			out, err := RenderStandaloneModule(mod)
			require.NoError(t, err)
			require.Contains(t, out, tt.want, "rendered:\n%s", out)
		})
	}
}
