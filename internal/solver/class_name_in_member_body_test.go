package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAMemberBodyReadsItsOwnClassValue covers a member body that names its own class, as
// `clone(&self) { return Widget(self.name) }` does. The member is coalesced before the
// class's binding var is solved, so the body has to read the class value itself for the
// member's return type to be the class rather than `never`. Each row renders a member read
// off the finished class.
func TestAMemberBodyReadsItsOwnClassValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "an instance method constructs its own class",
			src: `class Widget {
				name: string,
				clone(&self) { return Widget(self.name) },
			}
			val m = Widget("a").clone`,
			want: map[string]string{"m": "fn () -> Widget"},
		},
		{
			name: "a static factory constructs its own class",
			src: `class Widget {
				name: string,
				static make(n: string) { return Widget(n) },
			}
			val m = Widget.make`,
			want: map[string]string{"m": "fn (n: string) -> Widget"},
		},
		{
			name: "a method of a class with an explicit constructor constructs its own class",
			src: `class Widget {
				name: string,
				constructor(&mut self, n: string) { self.name = n },
				clone(&self) { return Widget(self.name) },
			}
			val m = Widget("a").clone`,
			want: map[string]string{"m": "fn () -> Widget"},
		},
		{
			// The method's binder renders each variable the constraint graph records as an
			// upper bound of `U`, so the row reads the return through a call instead.
			name: "a generic method constructs its own generic class",
			src: `class Box<T> {
				v: T,
				map<U>(&self, f: fn (x: T) -> U) { return Box(f(self.v)) },
			}
			val b: Box<number> = Box(1)
			val r = b.map(fn (x: number) -> string { return "a" })`,
			want: map[string]string{"r": "Box<string>"},
		},
		{
			name: "a method returns its own class value",
			src: `class Widget {
				name: string,
				cls(&self) { return Widget },
			}
			val m = Widget("a").cls`,
			want: map[string]string{"m": "fn () -> {new (name: string) -> Widget}"},
		},
		{
			name: "a static returns its own class value",
			src: `class Widget {
				name: string,
				static me() { return Widget },
			}
			val m = Widget.me`,
			want: map[string]string{"m": "fn () -> μX0.{new (name: string) -> Widget, me() -> X0}"},
		},
		{
			name: "a method constructs its own class through its namespace",
			src: `namespace geo {
				class P {
					x: number,
					clone(&self) { return geo.P(self.x) },
				}
			}
			val m = geo.P(1).clone`,
			want: map[string]string{"m": "fn () -> P"},
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
