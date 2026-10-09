package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLowerBoundedBinder covers a type parameter written with a `>:` lower bound: how a call
// instantiates it, what a body may do with it, how it renders, and how its bound is checked
// against a type argument, a default, a constraint and a later interface declaration.
func TestLowerBoundedBinder(t *testing.T) {
	t.Parallel()

	const bag = `
		class Bag<T> {
			items: Array<T>,
			contains<B >: T>(&self, x: B) -> boolean { return false },
		}
	`
	tests := []struct {
		name string
		src  string
		// want maps a binding to its rendered type. errs is every diagnostic, in order.
		want map[string]string
		errs []string
	}{
		{
			// `B` starts at `number` and widens to admit the argument, so the call checks.
			name: "a call widens the binder to admit its argument",
			src: bag + `
				val b: Bag<number> = Bag([1])
				val r = b.contains("s")
				val m = b.contains
			`,
			want: map[string]string{"r": "boolean", "m": "fn <B >: number>(x: B) -> boolean"},
		},
		{
			name: "a return starts from the bound",
			src: `
				fn pick<B >: number>(x: B) -> B { return x }
				val r = pick("s")
				val n = pick(1)
			`,
			want: map[string]string{"r": `number | "s"`, "n": "number"},
		},
		{
			// Every instantiation of `B` is a supertype of `number`, so a number is a `B`.
			name: "a body returns a value below the bound",
			src:  `fn f<B >: number>() -> B { return 1 }`,
			want: map[string]string{"f": "fn <B >: number>() -> B"},
		},
		{
			name: "a body cannot flow a value above the bound into the binder",
			src: `fn f<B >: number>(x: B) -> B {
				val y: B = "s"
				return y
			}`,
			errs: []string{`cannot constrain "s" <: B`},
		},
		{
			// `U`'s own bound reaches `B`, which the lower bound neither helps nor hinders.
			name: "a sibling bounded above by the binder flows into it",
			src:  `fn f<B >: number, U <: B>(u: U) -> B { return u }`,
			want: map[string]string{"f": "fn <B >: number, U <: B>(u: U) -> B"},
		},
		{
			name: "a sibling flows into the binder it bounds",
			src:  `fn f<T >: U, U>(u: U) -> T { return u }`,
			want: map[string]string{"f": "fn <T >: U, U>(u: U) -> T"},
		},
		{
			// `1` reaches `U` through `T`'s bound, and `U` admits only what its caller chose.
			name: "a value below a sibling-bounded binder is rejected",
			src:  `fn f<T >: U, U>() -> T { return 1 }`,
			errs: []string{"cannot constrain 1 <: T"},
		},
		{
			name: "an intersection naming the binder flows into it",
			src:  `fn f<B >: number>(x: B & {tag: string}) -> B { return x }`,
			want: map[string]string{"f": "fn <B >: number>(x: B & {tag: string}) -> B"},
		},
		{
			// `h("s")` is a variable until the call resolves it, and the bound is checked then.
			name: "a value reaching the binder through a variable is checked when it resolves",
			src: `fn f<B >: number>(h: fn <V>(v: V) -> V) -> B {
				return h("s")
			}`,
			errs: []string{`cannot constrain "s" <: B`},
		},
		{
			// `b: Box<T>` records `number` on `T` while the signature resolves. That floor is the
			// signature's, so the body is not reported for forcing it.
			name: "a bound a type reference records is not a floor the body forced",
			src: `class Box<B >: number> { v: B }
				fn g<T>(b: Box<T>) -> T { return b.v }`,
			want: map[string]string{"g": "fn <T>(b: Box<T>) -> T"},
		},
		{
			// A lower bound says what flows into `B`, not what `B` is, so `x` is not a number.
			name: "a body cannot read the binder as its bound",
			src:  `fn f<B >: number>(x: B) -> boolean { return x > 1 }`,
			errs: []string{"cannot constrain B <: number"},
		},
		{
			name: "a lower bound above the constraint is rejected",
			src:  `fn f<B >: string <: number>(x: B) -> B { return x }`,
			errs: []string{"cannot constrain string <: number"},
		},
		{
			name: "a default below the lower bound is rejected",
			src:  `class Box<B >: number = string> { v: B }`,
			errs: []string{"cannot constrain number <: string"},
		},
		{
			name: "a type argument is checked against the lower bound",
			src: `
				type Widen<B >: string> = B
				val ok: Widen<number | string> = 1
				val bad: Widen<number> = 1
			`,
			errs: []string{"cannot constrain string <: number"},
		},
		{
			name: "both bounds and a default render in order",
			src:  `class Box<B >: number <: number | string = number> { v: B }`,
			want: map[string]string{"Box": "<B >: number <: number | string = number> {new (v: B) -> Box<B>}"},
		},
		{
			// `T` occurs only in the lower bound, which counts as a use.
			name: "a parameter named only in a lower bound is used",
			src: `
				class C<T> {
					m<B >: T>(&self, x: B) -> boolean { return false },
				}
			`,
		},
		{
			name: "a later interface declaration may not add a lower bound",
			src: `
				interface Box<T> { v: T }
				interface Box<T >: number> { w: T }
			`,
			errs: []string{"declarations of interface Box must agree on their type parameters: only the first declaration may write a bound"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, errorMessagesOf(errs))
			} else {
				require.Equal(t, tt.errs, errorMessagesOf(errs))
			}
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "binding %q", name)
			}
		})
	}
}

// TestInferClassLowerBoundedMethod ports the `NumBag` and `Keyed` cases of
// TestInferClassSelfMethodInputVariance to a base method whose own binder is bounded below by
// the class parameter. `T` then appears only in that bound, an output position, so `Bag` is
// covariant by the ordinary rule rather than through the reader exemption, and an override is
// held to the quantified signature rather than to the widest instance.
func TestInferClassLowerBoundedMethod(t *testing.T) {
	t.Parallel()

	const bag = `
		class Bag<T> {
			items: Array<T>,
			contains<B >: T>(&self, x: B) -> boolean { return false },
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an immutable instance widens",
			src: bag + `
				fn widen(b: &Bag<number>) -> &Bag<number | string> { return b }
			`,
		},
		{
			name: "an immutable instance does not narrow",
			src: bag + `
				fn narrow(b: &Bag<number | string>) -> &Bag<number> { return b }
			`,
			want: []string{"cannot constrain string <: number"},
		},
		{
			// `T` has no other occurrence, so the lower bound alone decides the variance.
			name: "a parameter only a lower bound names widens",
			src: `
				class Sink<T> {
					emit<B >: T>(&self, x: B) -> boolean { return false },
				}
				fn widen(s: &Sink<number>) -> &Sink<number | string> { return s }
			`,
		},
		{
			// Through `Bag<number | string>`, `contains("s")` would run this override with a
			// string. The override drops the binder, so it no longer accepts what the base
			// signature promises to accept, and the error names the bound it dropped.
			name: "an override that narrows the binder is rejected",
			src: bag + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: number) -> boolean { return x > 1 },
				}
			`,
			want: []string{
				"class `NumBag` redeclares inherited member `contains` with type " +
					"`fn (x: number) -> boolean`, which is not compatible with " +
					"`fn <B >: number>(x: B) -> boolean` declared by `Bag`",
			},
		},
		{
			name: "an override keeping the binder is allowed",
			src: bag + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains<B >: number>(&self, x: B) -> boolean { return false },
				}
			`,
		},
		{
			// The binder has no upper bound, so the body cannot read `x` as a number.
			name: "an override keeping the binder cannot read it as the bound",
			src: bag + `
				class NumBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains<B >: number>(&self, x: B) -> boolean { return x > 1 },
				}
			`,
			want: []string{"cannot constrain B <: number"},
		},
		{
			name: "an override accepting any input is allowed",
			src: bag + `
				class AnyBag extends Bag<number> {
					constructor(&mut self) { super([1]) },
					contains(&self, x: unknown) -> boolean { return false },
				}
			`,
		},
		{
			// `x` is a `B` and not a `U`, so it cannot be handed to `key`, a consumer of `U`.
			name: "an override passing the binder to a consumer of the parameter is rejected",
			src: bag + `
				class Keyed<U> extends Bag<U> {
					key: fn (x: U) -> number,
					constructor(&mut self, items: Array<U>, key: fn (x: U) -> number) {
						super(items)
						self.key = key
					},
					contains<B >: U>(&self, x: B) -> boolean { return (self.key)(x) > 0 },
				}
			`,
			want: []string{"cannot constrain B <: U"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if tt.want == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}
