package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestClassInstanceMoves pins that an owned class instance moves at every flow site an owned
// object moves at. A borrow destination leaves it usable, and a promise copies.
func TestClassInstanceMoves(t *testing.T) {
	const counter = `
		class C { v: number }
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an owned parameter moves the instance",
			src: counter + `
				fn take(c: C) -> number { return c.v }
				fn f() {
					val c = C(1)
					take(c)
					take(c)
				}
			`,
			want: []string{"8:11-8:12: use of moved value 'c'"},
		},
		{
			name: "a borrow parameter leaves the instance usable",
			src: counter + `
				fn look(c: &C) -> number { return c.v }
				fn f() {
					val c = C(1)
					look(&c)
					look(&c)
				}
			`,
		},
		{
			name: "a binding moves the instance",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					val d = c
					return c.v
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'c'"},
		},
		{
			name: "a borrow binding leaves the instance usable",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					val d = &c
					return c.v + d.v
				}
			`,
		},
		{
			name: "a reassignment moves the instance",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					var d = C(2)
					d = c
					return c.v
				}
			`,
			want: []string{"8:13-8:16: use of moved value 'c'"},
		},
		{
			name: "a tuple literal moves the instance",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					val ys = [c]
					return c.v
				}
			`,
			want: []string{"7:13-7:16: use of moved value 'c'"},
		},
		{
			name: "returning the instance twice moves it twice",
			src: counter + `
				fn f() -> [C, C] {
					val c = C(1)
					return [c, c]
				}
			`,
			want: []string{"6:17-6:18: use of moved value 'c'"},
		},
		{
			name: "a mutable owned parameter takes an immutable instance it moves",
			src: counter + `
				fn bump(mut c: mut C) { c.v = c.v + 1 }
				fn f() -> number {
					val c = C(1)
					bump(c)
					return c.v
				}
			`,
			want: []string{"8:13-8:16: use of moved value 'c'"},
		},
		{
			name: "a promise copies",
			src: `
				fn f(p: Promise<number>) {
					val q = p
					val r = p
				}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
