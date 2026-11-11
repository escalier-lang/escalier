package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMissingSignature covers the functions whose signatures have to be written in full,
// checked as a module and as a script with Options.RequireSignatures set. expected lists
// every diagnostic the source reports.
func TestMissingSignature(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		script   bool
		expected []string
	}{
		{
			name: "UnannotatedParam",
			src:  `fn read(p) { return p.x }`,
			expected: []string{
				"1:4-1:8: 'read' is declared at module level, so parameter 'p' needs a type and 'read' needs a return type",
			},
		},
		{
			name: "UnannotatedParams",
			src:  `fn add(a, b, c: number) -> number { return a + b + c }`,
			expected: []string{
				"1:4-1:7: 'add' is declared at module level, so parameters 'a' and 'b' need types",
			},
		},
		{
			name: "MissingReturnType",
			src:  `fn count(xs: &Array<number>) { return xs.length }`,
			expected: []string{
				"1:4-1:9: 'count' is declared at module level, so it needs a return type; write `-> number`",
			},
		},
		{
			name: "ExportedReturnsNothing",
			src:  `export fn log(x: number) { x }`,
			expected: []string{
				"1:11-1:14: 'log' is declared at module level, so it needs a return type; write `-> undefined`",
			},
		},
		{
			name: "ModuleLevelClosure",
			src: `
				declare fn tick() -> Promise<undefined>
				export val load = async fn (pick: 'static fn () -> string) {
					await tick()
					return pick().length
				}
			`,
			expected: []string{
				"3:16-3:20: 'load' is a module-level closure, so it needs a return type; " +
					"write `-> Promise<number>` on the closure, or annotate 'load'",
			},
		},
		{
			name: "AnnotatedModuleLevelClosure",
			src:  `val double: fn (x: number) -> number = fn (x) { return x * 2 }`,
		},
		{
			name: "ClosureInsideBody",
			src: `
				fn twice(x: number) -> number {
					val double = fn (y) { return y * 2 }
					return double(double(x))
				}
			`,
		},
		{
			name: "Method",
			src: `
				class Counter {
					count: number,
					get(&self) { return self.count },
					set(&mut self, n: number) -> undefined { self.count = n },
				}
			`,
			expected: []string{
				"4:6-4:9: 'get' is a method, so it needs a return type; write `-> number`",
			},
		},
		{
			name: "ReturnNamesLifetime",
			src: `
				type Flat = {x: number}
				fn pick(a: &Flat, b: &Flat) { return a }
			`,
			expected: []string{
				"3:8-3:12: 'pick' is declared at module level, so it needs a return type; " +
					"write `fn pick<'a>(a: &'a Flat, b: &Flat) -> &'a Flat`",
			},
		},
		{
			name: "ClosureMissingParamType",
			src:  `val double = fn (x) { return x * 2 }`,
			expected: []string{
				"1:5-1:11: 'double' is a module-level closure, so parameter 'x' needs a type and 'double' " +
					"needs a return type; write them on the closure, or annotate 'double'",
			},
		},
		{
			name: "GetterAndSetter",
			src: `
				class Cell {
					value: number,
					get v(&self) { return self.value },
					set v(&mut self, n) { self.value = n },
				}
			`,
			expected: []string{
				"4:10-4:11: 'v' is a method, so it needs a return type; write `-> number`",
				"5:10-5:11: 'v' is a method, so parameter 'n' needs a type",
			},
		},
		{
			name: "ComputedKeyMethod",
			src: `
				val k = "k"
				class C {
					[k](&self) { return 1 },
				}
			`,
			expected: []string{
				"4:7-4:8: '[k]' is a method, so it needs a return type",
			},
		},
		{
			name:   "ScriptTopLevelFunc",
			src:    `fn count(xs: &Array<number>) { return xs.length }`,
			script: true,
			expected: []string{
				"1:4-1:9: 'count' is declared at a script's top level, so it needs a return type; write `-> number`",
			},
		},
		{
			name:   "ScriptMethod",
			src:    "class Box {\n\tx: number,\n\tget(&self) { return self.x },\n}",
			script: true,
			expected: []string{
				"3:2-3:5: 'get' is a method, so it needs a return type; write `-> number`",
			},
		},
		{
			name:   "ScriptClosure",
			src:    `val count = fn (xs: &Array<number>) { return xs.length }`,
			script: true,
			expected: []string{
				"1:5-1:10: 'count' is a closure at a script's top level, so it needs a return type; " +
					"write `-> number` on the closure, or annotate 'count'",
			},
		},
		{
			name: "FullSignature",
			src: `
				fn count(xs: &Array<number>) -> number { return xs.length }
				declare fn ext(x: number) -> number
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := Options{RequireSignatures: true}
			var errs []SolverError
			if tt.script {
				_, _, errs = InferScriptAgainstStdlib(parseScript(t, tt.src), committedTree, opts)
			} else {
				errs = InferModuleAgainstStdlib(parseModule(t, tt.src), committedTree, opts).Errors
			}
			var got []string
			for _, e := range errs {
				got = append(got, msgWithSpan(t, e))
			}
			require.Equal(t, tt.expected, got)
			for _, e := range errs {
				require.True(t, isWarning(e))
			}
		})
	}
}

// TestMissingSignatureOff asserts that a run without Options.RequireSignatures reports no
// MissingSignatureError.
func TestMissingSignatureOff(t *testing.T) {
	res := InferModuleAgainstStdlib(parseModule(t, `fn read(p) { return p.x }`), committedTree, Options{})
	require.Empty(t, res.Errors)
}
