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
fn f(mut p: Point) { p.x = 0 }`,
			want: []string{"5:22-5:25: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "ThroughModuleLevelVal",
			src: `val mut config: {readonly apiUrl: string} = {apiUrl: "a"}
fn f() { config.apiUrl = "b" }`,
			want: []string{"2:10-2:23: cannot assign to 'apiUrl' because it is a readonly property"},
		},
		{
			name: "ThroughObjectParam",
			src:  `fn f(mut obj: {readonly a: number}) { obj.a = 5 }`,
			want: []string{"1:39-1:44: cannot assign to 'a' because it is a readonly property"},
		},
		{
			name: "ThroughMappedReadonlyType",
			src: `type RO<T> = {readonly [P]: T[P] for P in keyof T}
fn f(mut o: RO<{a: number}>) { o.a = 5 }`,
			want: []string{"2:32-2:35: cannot assign to 'a' because it is a readonly property"},
		},
		{
			name: "ConstantStringIndex",
			src:  `fn f(mut obj: {readonly a: number}) { obj["a"] = 5 }`,
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
fn f(mut obj: {readonly a: number}) { sink(obj) }`,
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

// TestReadonlyFieldConstructorInit asserts that a constructor may write each of its class's
// own readonly fields once per path to initialize it, and that every other write to the
// field is still rejected.
func TestReadonlyFieldConstructorInit(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "InitFromParam",
			src: `class Foo {
    readonly x: number,
    constructor(&mut self, n: number) { self.x = n },
}`,
			want: []string{},
		},
		{
			name: "InitFromLiteral",
			src:  `class A { readonly x: number, constructor(&mut self) { self.x = 0 } }`,
			want: []string{},
		},
		{
			name: "InitOnEachBranch",
			src: `class A {
    readonly x: number,
    constructor(&mut self, b: boolean) {
        if b { self.x = 1 } else { self.x = 2 }
    },
}`,
			want: []string{},
		},
		{
			name: "InitThroughStringIndex",
			src: `class A {
    readonly x: number,
    constructor(&mut self) { self["x"] = 1 },
}`,
			want: []string{},
		},
		{
			name: "InitOptional",
			src: `class A {
    readonly x?: number,
    constructor(&mut self) { self.x = 1 },
}`,
			want: []string{},
		},
		{
			name: "InitFromBranchingExpr",
			src: `class A {
    readonly x: number,
    constructor(&mut self, b: boolean) {
        self.x = if b { 1 } else { 2 }
    },
}`,
			want: []string{},
		},
		{
			name: "InitFromMatch",
			src: `class A {
    readonly x: number,
    constructor(&mut self, n: number) {
        self.x = match n {
            1 => 10,
            _ => 20,
        }
    },
}`,
			want: []string{},
		},
		{
			name: "InitFromTryCatch",
			src: `class A {
    readonly x: number,
    constructor(&mut self, n: number) {
        self.x = try {
            n
        } catch {
            _ => 0,
        }
    },
}`,
			want: []string{},
		},
		{
			name: "InitFromDo",
			src: `class A {
    readonly x: number,
    constructor(&mut self, n: number) {
        self.x = do {
            n
        }
    },
}`,
			want: []string{},
		},
		{
			name: "InitFromIfVal",
			src: `class A {
    readonly x: number,
    constructor(&mut self, o: number | undefined) {
        self.x = if val y = o {
            y
        } else {
            0
        }
    },
}`,
			want: []string{},
		},
		{
			name: "WritableInitFromBranchingExpr",
			src: `class A {
    x: number,
    constructor(&mut self, b: boolean) {
        self.x = if b { 1 } else { 2 }
    },
}`,
			want: []string{},
		},
		{
			name: "InitAfterNestedThrow",
			src: `class A {
    readonly x: number,
    constructor(&mut self, b: boolean) throws string {
        val v = b || throw "no"
        self.x = 1
    },
}`,
			want: []string{},
		},
		{
			name: "SecondWriteInSameStatement",
			src: `class A {
    readonly x?: number,
    constructor(&mut self) {
        val t = [self.x = 1, self.x = 2]
    },
}`,
			want: []string{"4:30-4:36: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "WriteAfterBranchingInit",
			src: `class A {
    readonly x: number,
    constructor(&mut self, b: boolean) {
        self.x = if b { 1 } else { 2 }
        self.x = 3
    },
}`,
			want: []string{"5:9-5:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "InitWrongType",
			src: `class A {
    readonly x: number,
    constructor(&mut self) { self.x = "s" },
}`,
			want: []string{"3:39-3:42: cannot constrain \"s\" <: number"},
		},
		{
			name: "SecondWrite",
			src: `class A {
    readonly x: number,
    constructor(&mut self) {
        self.x = 1
        self.x = 2
    },
}`,
			want: []string{"5:9-5:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "WriteAfterConditionalInit",
			src: `class A {
    readonly x: number,
    constructor(&mut self, b: boolean) {
        if b { self.x = 1 }
        self.x = 2
    },
}`,
			want: []string{"5:9-5:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "WriteInLoop",
			src: `class A {
    readonly x?: number,
    constructor(&mut self, xs: Array<number>) {
        for v in xs { self.x = v }
    },
}`,
			want: []string{"4:23-4:29: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "WriteInClosure",
			src: `class A {
    readonly x?: number,
    constructor(&mut self) {
        val f = fn() { self.x = 1 }
    },
}`,
			want: []string{"4:24-4:30: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "InheritedReadonlyField",
			src: `class Base {
    readonly x: number,
    constructor(&mut self) { self.x = 1 },
}
class Derived extends Base {
    constructor(&mut self) {
        super()
        self.x = 2
    },
}`,
			want: []string{"8:9-8:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "MissingInit",
			src:  `class A { readonly x: number, constructor(&mut self) {} }`,
			want: []string{"1:54-1:56: Field 'x' is not initialized on every path through the constructor."},
		},
		{
			// Each readonly field is tracked on its own, so the two branches may assign them
			// in different orders.
			name: "InitTwoFieldsOnEachBranch",
			src: `class P {
    readonly x: number,
    readonly y: number,
    constructor(&mut self, b: boolean) {
        if b {
            self.x = 1
            self.y = 2
        } else {
            self.y = 3
            self.x = 4
        }
    },
}`,
			want: []string{},
		},
		{
			name: "RepeatedInitOfOneOfTwoFields",
			src: `class P {
    readonly x: number,
    readonly y: number,
    constructor(&mut self) {
        self.x = 1
        self.y = 2
        self.x = 3
    },
}`,
			want: []string{"7:9-7:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			name: "MissingInitOfOneOfTwoFields",
			src: `class P {
    readonly x: number,
    readonly y: number,
    constructor(&mut self) {
        self.x = 1
    },
}`,
			want: []string{"4:28-6:6: Field 'y' is not initialized on every path through the constructor."},
		},
		{
			name: "RepeatedInitAfterMatch",
			src: `class A {
    readonly x: number,
    constructor(&mut self, n: number) {
        self.x = match n {
            1 => 10,
            _ => 20,
        }
        self.x = 3
    },
}`,
			want: []string{"8:9-8:15: cannot assign to 'x' because it is a readonly property"},
		},
		{
			// The write in the arm runs first. The assignment writes at the join after the
			// arm, so it is the second write.
			name: "InitInMatchArmAndAssignment",
			src: `class A {
    readonly x: number,
    constructor(&mut self, n: number) {
        self.x = match n {
            1 => {
                self.x = 5
                10
            },
            _ => 20,
        }
    },
}`,
			want: []string{"4:9-4:15: cannot assign to 'x' because it is a readonly property"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			got := messagesWithSpan(t, errs)
			if got == nil {
				got = []string{}
			}
			require.Equal(t, tt.want, got)
		})
	}
}
