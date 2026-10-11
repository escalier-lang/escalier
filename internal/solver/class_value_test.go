package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClassValueBindsItsParametersOnEachSignature covers how a class value carries the
// class's type parameters: as the binder of every constructor and call signature, with a
// static member standing outside them. Each row renders the class value, or reports what a
// static that names a parameter gets.
func TestClassValueBindsItsParametersOnEachSignature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		// class is the class whose value want renders. errs is every diagnostic, in order.
		class string
		want  string
		errs  []string
	}{
		{
			name: "two constructors each carry the binder",
			src: `declare class Pair<A, B> {
				a: A,
				b: B,
				constructor(&mut self, a: A, b: B),
				constructor(&mut self, both: A & B),
			}`,
			class: "Pair",
			want:  "{new <A, B>(a: A, b: B) -> Pair<A, B>; new <A, B>(both: A & B) -> Pair<A, B>}",
		},
		{
			// A class declaring a call signature and no constructor is callable and not
			// constructible, so the value carries the call signature alone.
			name: "a call signature carries the binder",
			src: `declare class Tag<T> {
				v: T,
				(x: T) -> T,
			}`,
			class: "Tag",
			want:  "{<T>(x: T) -> T}",
		},
		{
			name: "a static with a binder of its own stands outside the class parameters",
			src: `class Holder<T> {
				v: T,
				static pick<U: string>(x: U) -> U { return x },
			}`,
			class: "Holder",
			want:  "{new <T>(v: T) -> Holder<T>, pick<U: string>(x: U) -> U}",
		},
		{
			name: "a static method naming a class parameter is reported",
			src: `class Holder<T> {
				v: T,
				static s(x: T) -> number { return 1 },
			}`,
			errs: []string{"static member `s` names type parameter `T`, which belongs to instances of `Holder`"},
		},
		{
			name: "a static field naming a class parameter is reported",
			src: `class Holder<T> {
				v: T,
				static x: Array<T> = [],
			}`,
			errs: []string{"static member `x` names type parameter `T`, which belongs to instances of `Holder`"},
		},
		{
			// The static's own binder is bounded by the class parameter, which it cannot read
			// any more than a parameter type can.
			name: "a static binder bounded by a class parameter is reported",
			src: `class Holder<T> {
				v: T,
				static f<U: T>(u: U) -> number { return 1 },
			}`,
			errs: []string{"static member `f` names type parameter `T`, which belongs to instances of `Holder`"},
		},
		{
			// A lifetime parameter is bound on the constructor the way a type parameter is,
			// and a static stands outside it the same way.
			name: "a lifetime parameter is bound on the constructor",
			src: `class Holder<'a> {
				peer: &'a mut {value: number},
				static count(n: number) -> number { return n },
			}`,
			class: "Holder",
			want:  "{new <'a>(peer: &'a mut {value: number}) -> Holder<'a>, count(n: number) -> number}",
		},
		{
			name: "a static method naming a class lifetime is reported",
			src: `class Holder<'a> {
				peer: &'a mut {value: number},
				static s(p: &'a mut {value: number}) -> number { return 1 },
			}`,
			errs: []string{"static member `s` names lifetime parameter `'a`, which belongs to instances of `Holder`"},
		},
		// DISABLED until #1927, which records a method's lifetime parameters as its own binder.
		// Until then inferFunc leaves the static's `'a` free in the value and the printer hoists
		// it to the prefix under a generated name, rendering `<'b> {new …, s(p: &'b mut …)}`.
		/*
			{
				// The static's own lifetime is a different variable from the class's, whatever it
				// is called, so nothing is reported, and it is the static's own binder, so each
				// signature renders its `'a` under its own `<…>`.
				name: "a static declaring its own lifetime is not reported",
				src: `class Holder<'a> {
					peer: &'a mut {value: number},
					static s<'a>(p: &'a mut {value: number}) -> &'a mut {value: number} { return p },
				}`,
				class: "Holder",
				want:  "{new <'a>(peer: &'a mut {value: number}) -> Holder<'a>, s<'a>(p: &'a mut {value: number}) -> &'a mut {value: number}}",
			},
		*/
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
			if tt.want != "" {
				require.Equal(t, tt.want, values[tt.class])
			}
		})
	}
}

// TestConstructionInstantiatesTheClassBinder covers a construction binding the class's
// parameters afresh: each construction's instance takes its own argument's type, a bound
// is checked against the argument at the call, and the instance reaches its methods.
func TestConstructionInstantiatesTheClassBinder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
		errs []string
	}{
		{
			name: "the instance takes the argument's type",
			src: `class Box<T> { v: T }
				val b = Box(1)
				val s = Box("s")`,
			want: map[string]string{"b": "Box<1>", "s": `Box<"s">`},
		},
		{
			name: "an upper bound is checked at the call",
			src: `class Box<B: number> { v: B }
				val b = Box("s")`,
			errs: []string{`2:17-2:20: cannot constrain "s" <: number`},
		},
		{
			// The written `Box<string>` fails the bound at the annotation, and the
			// construction fails it again where the instance flows into that annotation.
			name: "a lower bound is checked where the instance flows",
			src: `class Box<B> where number: B { v: B }
				val ok: Box<number | string> = Box("s")
				val bad: Box<string> = Box("s")`,
			errs: []string{
				"3:18-3:24: cannot constrain number <: string",
				"3:28-3:36: cannot constrain number <: string",
			},
		},
		{
			name: "a constructed instance reaches its methods",
			src: `class Box<T> {
				v: T,
				read(&self) -> T { return self.v },
			}
			val x = Box(5).read()`,
			want: map[string]string{"x": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			if tt.errs == nil {
				require.Empty(t, errorMessagesOf(errs))
			} else {
				require.Equal(t, tt.errs, messagesWithSpan(t, errs))
			}
			for k, want := range tt.want {
				require.Equal(t, want, values[k], "binding %q", k)
			}
		})
	}
}
