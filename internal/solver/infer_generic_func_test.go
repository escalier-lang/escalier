package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A standalone `fn f<T>(…)` declaration resolves its explicit type-parameter list into
// the function's own FuncType.TypeParams, so the declared quantifier renders with the
// written name and each call instantiates it independently, each argument keeping its
// own literal type rather than widening or unifying across calls.
func TestInferGenericFuncDecl(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// The declared `<T>` renders with its written name, and the two calls
			// instantiate it independently, so `a` and `b` keep their own literal types.
			name: "IdentityRendersAndInstantiates",
			src: `
				fn id<T>(x: T) -> T { return x }
				val a = id(5)
				val b = id("hi")
			`,
			want: map[string]string{
				"id": "fn <T>(x: T) -> T",
				"a":  "5",
				"b":  `"hi"`,
			},
		},
		{
			// A bodyless `declare fn` resolves its type-parameter list the same way,
			// adopting its declared return without a body to constrain.
			name: "DeclareFn",
			src:  `declare fn id<T>(x: T) -> T`,
			want: map[string]string{
				"id": "fn <T>(x: T) -> T",
			},
		},
		{
			// Two distinct type parameters bind independently, so a call passing a
			// number and a string returns a tuple of each argument's literal type.
			name: "TwoParams",
			src: `
				fn pair<T, U>(x: T, y: U) -> [T, U] { return [x, y] }
				val p = pair(1, "two")
			`,
			want: map[string]string{
				"pair": "fn <T, U>(x: T, y: U) -> [T, U]",
				"p":    `[1, "two"]`,
			},
		},
		{
			// A bounded parameter keeps its bound on the quantifier but not on the use
			// site, so `x` reads as `T` while `<T: number>` carries the constraint.
			name: "BoundedParam",
			src:  `fn first<T: number>(x: T) -> T { return x }`,
			want: map[string]string{
				"first": "fn <T: number>(x: T) -> T",
			},
		},
		{
			// A body that returns one of several parameters of the same type is genuinely
			// parametric: `T` gets no concrete lower bound, so it is not flagged.
			name: "ReturnsOneOfTwoSameTypeParams",
			src:  `fn first<T>(x: T, y: T) -> T { return x }`,
			want: map[string]string{
				"first": "fn <T>(x: T, y: T) -> T",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "type mismatch for %q", name)
			}
		})
	}
}

// A body-carrying generic function whose body forces a declared type parameter used in an
// output position to a concrete floor is rejected at the declaration. The caller chooses
// the parameter, so a signature that hands back an arbitrary `T` while the body only ever
// produces `number` over-promises.
func TestInferGenericFuncBodyOverPromises(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
	}{
		{
			// The body returns `x: number` as `T`, so `T` carries the concrete floor
			// `number` the caller never supplied.
			name: "ReturnParamForcedToConcrete",
			src:  `fn make<T>(x: number) -> T { return x }`,
			msg:  "1:1-1:40: the body forces type parameter `T` to `number`, so it cannot stand for an arbitrary type",
		},
		{
			// A literal in the return forces the floor even when the parameter also
			// appears in an input position, since the body still never produces an
			// arbitrary `T`.
			name: "ReturnLiteralWithParamInInput",
			src:  `fn weird<T>(x: T) -> T { return 5 }`,
			msg:  "1:1-1:36: the body forces type parameter `T` to `5`, so it cannot stand for an arbitrary type",
		},
		{
			// A `T | number` parameter returned as `T` is unsound: a caller doing
			// `leak<string>(5)` passes a valid `string | number` and receives `5` typed as
			// `string`. The union splits into `T <: T` and `number <: T`, so the `number`
			// branch is an independently concrete floor even though the sibling branch is
			// the caller's own `T`.
			name: "UnionParamWithConcreteBranch",
			src:  `fn leak<T>(x: T | number) -> T { return x }`,
			msg:  "1:1-1:44: the body forces type parameter `T` to `number`, so it cannot stand for an arbitrary type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, &TypeParamNotProducibleError{}, errs[0])
			require.Equal(t, tt.msg, msgWithSpan(errs[0]))
		})
	}
}

// The annotation form reuses the same bounded-inference-var core. The body's return value
// flows into the annotation's fresh instance through an intermediate var, so the floor is
// reached transitively and the annotation form is flagged the same as the standalone
// declaration. The binding still adopts its pristine annotation, so `f` renders with the
// declared quantifier alongside the error.
func TestInferGenericFuncAnnotationBodyOverPromises(t *testing.T) {
	tests := []struct {
		name string
		src  string
		msg  string
		want string
	}{
		{
			name: "ReturnParamForcedToConcrete",
			src:  `val f: fn<T>(x: number) -> T = fn (x) { return x }`,
			msg:  "1:32-1:51: the body forces type parameter `T` to `number`, so it cannot stand for an arbitrary type",
			want: "fn <T>(x: number) -> T",
		},
		{
			// The union member `T` still binds through the annotation's child scope and
			// reaches constrain's union-super two-pass rule, so the binding solves and
			// renders. The `number` branch is the independently concrete floor that the
			// producibility check rejects.
			name: "UnionParamWithConcreteBranch",
			src:  `val f: fn<T>(x: T | number) -> T = fn (x) { return x }`,
			msg:  "1:36-1:55: the body forces type parameter `T` to `number`, so it cannot stand for an arbitrary type",
			want: "fn <T>(x: T | number) -> T",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Len(t, errs, 1)
			require.IsType(t, &TypeParamNotProducibleError{}, errs[0])
			require.Equal(t, tt.msg, msgWithSpan(errs[0]))
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// A bodyless `declare fn` asserts its signature with no body to check, so the producibility
// check does not run and an over-promising shape such as `declare fn make<T>(x: number) -> T`
// stays accepted. It is the programmer's assertion that some external implementation
// produces `T`.
func TestInferGenericDeclareFuncNotProducibilityChecked(t *testing.T) {
	values, _, errs := inferSource(t, `declare fn make<T>(x: number) -> T`)
	require.Empty(t, errs)
	require.Equal(t, "fn <T>(x: number) -> T", values["make"])
}

// A method's own type parameters stay gated at the rank-1 boundary: per-instance
// projection is not yet applied by the class-body freeze, so resolving them would
// collapse two calls to one shared var. The method reports the type-param feature as
// unsupported and infers monomorphically rather than panicking in the body freeze.
func TestInferGenericMethodStillGated(t *testing.T) {
	_, _, errs := inferSource(t, `
		class Box {
			wrap<T>(self, x: T) -> T { return x }
		}
	`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	// The gate reports the type-param feature, then the two `T` references cascade to
	// unsupported because the parameter was never declared in scope.
	require.Equal(t, []string{
		"Unsupported: TypeParam",
		"Unsupported: TypeRefTypeAnn",
		"Unsupported: TypeRefTypeAnn",
	}, msgs)
}

// A parameter whose own type is polymorphic — a callback annotated `fn <V>(x: V) -> V` —
// stays rejected at the rank-1 boundary. The generic function-type annotation itself
// resolves, so its `<V>` binds and the two `V` references read that quantified var, but
// a generic function in parameter position is rank-2, so the declaration reports the
// parameter as unsupported and recovers to a fresh var rather than approximating it.
func TestInferGenericFuncHigherRankParamRejected(t *testing.T) {
	_, _, errs := inferSource(t, `fn apply<T>(g: fn <V>(x: V) -> V, y: T) -> T { return g(y) }`)
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Message()
	}
	// The `<V>` annotation resolves, so there is no `TypeRefTypeAnn` cascade; only the
	// rank-1 boundary rejects the generic function in parameter position.
	require.Equal(t, []string{
		"Unsupported: higher-rank function parameter",
	}, msgs)
}
