package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDeclarePrivateConstructor covers a `declare` class whose constructors are private. Its
// value carries only the public constructors, so a class with none cannot be constructed
// while its statics, its instance pattern and its type stay usable. Each row renders the
// binding named by value, or lists every diagnostic in order.
func TestDeclarePrivateConstructor(t *testing.T) {
	t.Parallel()

	const bitmap = `declare class Bitmap {
		w: number,
		private constructor(&mut self),
		static s: number,
	}
	`

	tests := []struct {
		name  string
		src   string
		value string
		want  string
		errs  []string
	}{
		{
			name:  "the value carries the statics and no constructor",
			src:   bitmap,
			value: "Bitmap",
			want:  "{s: number}",
		},
		{
			name: "constructing it is reported",
			src:  bitmap + `val b = Bitmap()`,
			errs: []string{"`Bitmap` cannot be constructed because it declares no public constructor"},
		},
		{
			name:  "a static is read through the value",
			src:   bitmap + `val n = Bitmap.s`,
			value: "n",
			want:  "number",
		},
		{
			name:  "an instance pattern narrows to it",
			src:   bitmap + `fn f(x: Bitmap | string) -> number { return match x { Bitmap {w} => w, _ => 0 } }`,
			value: "f",
			want:  "fn (x: Bitmap | string) -> number",
		},
		{
			name:  "a typed binding pattern narrows to it",
			src:   bitmap + `fn f(x: unknown) -> number { return match x { b: Bitmap => b.w, _ => 0 } }`,
			value: "f",
			want:  "fn (x: unknown) -> number",
		},
		{
			name: "a subclass calling super is reported",
			src:  bitmap + `class Mine extends Bitmap { constructor(&mut self) { super() } }`,
			errs: []string{"`Bitmap` cannot be constructed because it declares no public constructor"},
		},
		{
			name: "a declare subclass with a private constructor is not constructible either",
			src: bitmap + `declare class Sub extends Bitmap { private constructor(&mut self) }
				val s = Sub()`,
			errs: []string{"`Sub` cannot be constructed because it declares no public constructor"},
		},
		{
			name: "a public constructor beside a private one is the only one the value carries",
			src: `declare class P {
				private constructor(&mut self),
				constructor(&mut self, x: number),
			}
			val p = P(1)
			val q = P()`,
			errs: []string{"Not enough arguments: expected at least 1, but got 0"},
		},
		{
			name:  "a public constructor beside a private one renders alone",
			src:   `declare class P { private constructor(&mut self), constructor(&mut self, x: number) }`,
			value: "P",
			want:  "{new (x: number) -> P}",
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
			if tt.want != "" {
				require.Equal(t, tt.want, values[tt.value])
			}
		})
	}
}
