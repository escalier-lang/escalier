package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAmbiguousUnionCommitInGenericCode covers when inferring a whole program reports an
// AmbiguousUnionCommitWarning. A union arm that is a type parameter's variable, or a copy
// of one, is never named as the alternative. A union arm that is an inference variable a
// call instantiated still is, and a rigid parameter in that union renders under its
// declared name.
func TestAmbiguousUnionCommitInGenericCode(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantMsgs   []string
	}{
		{
			// `Box(5:number)` carries `number` through the links `return self.value` recorded
			// to a copy of `T | U`. The U copy there is no type the call chose.
			name: "ClassParamReturnedIntoUnionWithMethodParam",
			src: `
				class Box<T> {
					value: T,
					getValue<U>(&self, defaultValue: U) -> T | U {
						if self.value != 0 {
							return self.value
						} else {
							return defaultValue
						}
					}
				}
				val box = Box(5:number)
				val a = box.getValue("default":string)
				val b = box.getValue(10)
			`,
			wantValues: map[string]string{
				"box": "Box<number>",
				"a":   "number | string",
				"b":   "number",
			},
		},
		{
			// `return self.value` against `-> number | T` commits `number`. T is rigid in the
			// body, so it is not an alternative.
			name: "FieldReturnedIntoUnionWithMethodParam",
			src: `
				class Foo {
					value: number,
					getValue<T>(&self, defaultValue: T) -> number | T {
						if self.value != 0 {
							return self.value
						} else {
							return defaultValue
						}
					},
				}
				val foo = Foo(5)
				val a = foo.getValue("default":string)
				val b = foo.getValue(10)
			`,
			wantValues: map[string]string{
				"foo": "Foo",
				"a":   "number | string",
				"b":   "number",
			},
		},
		{
			name: "LiteralReturnedIntoUnionWithFuncParam",
			src: `
				fn g<A>(a: A) -> A | number { return 5 }
			`,
			wantValues: map[string]string{"g": "fn <A>(a: A) -> A | number"},
		},
		{
			// The call instantiates A to a fresh variable, which `5` could bind in place of
			// `number`. The decision commits `number`, so A's variable gets no lower bound and
			// `r` is `never`.
			name: "CallArgumentMatchesConcreteArmAndInstantiatedParam",
			src: `
				declare fn f<A>(x: A | number) -> A
				val r = f(5)
			`,
			wantValues: map[string]string{"r": "never"},
			wantMsgs: []string{
				"ambiguous match against t4 | number: committed number, but t4 would also match; annotate to disambiguate",
			},
		},
		{
			// `k(5)` instantiates k's A while h's T is rigid, so the union names T. T is not
			// the alternative, and A's variable is. As in the case above, the commit leaves A's
			// variable without a lower bound, so h returns `never`.
			name: "CallArgumentInsideGenericBodyNamesRigidParam",
			src: `
				fn h<T>(t: T, k: fn <A>(x: A | T | number) -> A) { return k(5) }
			`,
			wantValues: map[string]string{"h": "fn <T>(t: T, k: fn <A>(x: A | T | number) -> A) -> never"},
			wantMsgs: []string{
				"ambiguous match against t5 | T | number: committed number, but t5 would also match; annotate to disambiguate",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantMsgs, Messages(errs))
			for name, want := range tt.wantValues {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}
