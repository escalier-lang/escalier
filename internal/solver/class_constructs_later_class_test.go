package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMemberBodyConstructsALaterClass covers a member body that constructs a class declared
// later in the same component, as `static mk() { return B(1) }` does when `B` follows `A`
// and constructs `A` in turn. Each row renders a member read off the finished classes.
func TestAMemberBodyConstructsALaterClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "a static constructs a later class",
			src: `class A {
				v: number,
				static mk() { return B(1) },
			}
			class B {
				n: number,
				toA(&self) { return A(self.n) },
			}
			val mk = A.mk
			val toA = B(1).toA`,
			want: map[string]string{"mk": "fn () -> B", "toA": "fn () -> A"},
		},
		{
			name: "a method calls a function that constructs a later class",
			src: `class A {
				v: number,
				toB(&self) { return makeB(self.v) },
			}
			fn makeB(n: number) { return B(n) }
			class B {
				n: number,
				toA(&self) { return A(self.n) },
			}
			val toB = A(1).toB`,
			want: map[string]string{"toB": "fn () -> B"},
		},
		{
			name: "a static returns a later class whose own static constructs a class after it",
			src: `class A {
				v: number,
				static getB() { return B },
			}
			class B {
				n: number,
				static mk() { return C(1) },
			}
			class C {
				m: number,
				toA(&self) { return A(self.m) },
			}
			val c = A.getB().mk()`,
			want: map[string]string{"c": "C"},
		},
		{
			name: "a generic class read at two instantiations later in its component",
			src: `class Box<T> {
				v: T,
				get(&self) { return self.v },
				toUser(&self) { return User(1) },
			}
			class User {
				id: number,
				static pair() { return [Box(1).get(), Box("s").get()] },
			}
			val toUser = Box(true).toUser
			val pair = User.pair`,
			want: map[string]string{"toUser": "fn () -> User", "pair": "fn () -> [1, \"s\"]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errorMessagesOf(errs))
			for name, want := range tt.want {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}
