package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadonlyWriteDiagnostic asserts that every form of direct write to a readonly
// property reports the same message, blamed on the assignment target. A readonly field
// that flows into a writable requirement without being written reports the subtyping
// message instead.
func TestReadonlyWriteDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "ThroughSelf",
			src: `class Counter {
    readonly value: number,
    increment(&mut self) {
        self.value = self.value + 1
    },
}`,
			want: []string{"4:9-4:19: cannot assign to 'value' because it is a readonly property"},
		},
		{
			name: "ThroughMutClassParam",
			src: `class Point {
    readonly x: number,
    y: number,
}
fn f(p: mut Point) { p.x = 0 }`,
			want: []string{"5:22-5:25: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "ThroughModuleLevelVal",
			src: `val config: mut {readonly apiUrl: string} = {apiUrl: "a"}
fn f() { config.apiUrl = "b" }`,
			want: []string{"2:10-2:23: cannot assign to 'apiUrl' because it is a readonly property"},
		},
		{
			name: "ThroughObjectParam",
			src:  `fn f(obj: mut {readonly a: number}) { obj.a = 5 }`,
			want: []string{"1:39-1:44: cannot assign to 'a' because it is a readonly property"},
		},
		{
			name: "ThroughMappedReadonlyType",
			src: `type RO<T> = {readonly [P]: T[P] for P in keyof T}
fn f(o: mut RO<{a: number}>) { o.a = 5 }`,
			want: []string{"2:32-2:35: cannot assign to 'a' because it is a readonly property"},
		},
		{
			name: "ConstantStringIndex",
			src:  `fn f(obj: mut {readonly a: number}) { obj["a"] = 5 }`,
			want: []string{"1:39-1:47: cannot assign to 'a' because it is a readonly property"},
		},
		{
			name: "ReadonlyIndexSignature",
			src:  `fn f(d: &mut {readonly [K: string]?: number}, k: string) { d[k] = 1 }`,
			want: []string{"1:60-1:64: cannot assign to '[string]' because it is a readonly property"},
		},
		{
			name: "SubtypingIntoWritableParam",
			src: `fn sink(o: mut {a: number}) {}
fn f(obj: mut {readonly a: number}) { sink(obj) }`,
			want: []string{"2:44-2:47: readonly field 'a' cannot satisfy a writable field requirement"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
