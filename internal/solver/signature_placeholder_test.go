package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"

	"github.com/stretchr/testify/require"
)

// TestSignaturePlaceholder covers where a signature checked with Options.RequireSignatures
// may write `_`. expected lists every diagnostic the source reports, each led by its severity.
func TestSignaturePlaceholder(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		script   bool
		expected []string
	}{
		{
			name: "ThrowsPlaceholder",
			src: `
				declare fn fail() -> number throws string
				fn f() -> number throws _ { return fail() }
			`,
		},
		{
			name: "PromiseErrorPlaceholder",
			src: `
				declare fn fail() -> Promise<number, string>
				async fn g() -> Promise<number, _> { return await fail() }
			`,
		},
		{
			name: "BorrowedErrorPlaceholder",
			src: `
				declare fn fail<'a>(e: &'a Array<string>) -> number throws &'a Array<string>
				fn f<'a>(e: &'a Array<string>) -> number throws &'a _ { return fail(e) }
			`,
		},
		{
			name: "PlaceholderInsidePromiseError",
			src: `
				declare fn fail() -> Promise<number, Array<string>>
				async fn f() -> Promise<number, Array<_>> { return await fail() }
			`,
		},
		{
			name: "GeneratorErrorPlaceholder",
			src: `
				declare fn fail() -> number throws string
				gen fn f() -> Generator<number, undefined, unknown, _> { yield fail() }
			`,
		},
		{
			name: "TwoPlaceholdersInOneParam",
			src:  `fn f(x: [_, _]) -> number { return x[0] + x[1] }`,
			expected: []string{
				"error 1:10-1:11: 'f' is declared at module level, so parameter 'x' needs a type in place of `_`; write `x: [number, number]`",
			},
		},
		{
			name: "ErrorPlaceholderInfersClassWithLifetime",
			src: `
				class Ref<'a> { v: &'a Array<string> }
				declare fn fail<'a>(e: &'a Array<string>) -> number throws Ref<'a>
				fn f<'a>(e: &'a Array<string>) -> number throws _ { return fail(e) }
			`,
			expected: []string{
				"error 4:53-4:54: `_` in the error type infers `Ref<'a>`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "ErrorPlaceholderInfersAliasWithLifetime",
			src: `
				type Ref<'a> = {v: &'a Array<string>}
				declare fn fail<'a>(e: &'a Array<string>) -> number throws Ref<'a>
				fn f<'a>(e: &'a Array<string>) -> number throws _ { return fail(e) }
			`,
			expected: []string{
				"error 4:53-4:54: `_` in the error type infers `Ref<'a>`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "ErrorPlaceholderInfersFuncWithLifetime",
			src: `
				declare fn fail<'a>(e: &'a Array<string>) -> number throws 'a fn () -> number
				fn f<'a>(e: &'a Array<string>) -> number throws _ { return fail(e) }
			`,
			expected: []string{
				"error 3:53-3:54: `_` in the error type infers `fn () -> number`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "ParamPlaceholder",
			src:  `fn f(x: _) -> number { return x + 1 }`,
			expected: []string{
				"error 1:9-1:10: 'f' is declared at module level, so parameter 'x' needs a type in place of `_`; write `x: number`",
			},
		},
		{
			name: "ReturnPlaceholder",
			src:  `fn f(x: number) -> _ { return x + 1 }`,
			expected: []string{
				"error 1:20-1:21: 'f' is declared at module level, so its return type may write `_` only for its error type; write `-> number`",
			},
		},
		{
			name: "PromiseValuePlaceholder",
			src:  `async fn f(x: number) -> Promise<_> { return x + 1 }`,
			expected: []string{
				"error 1:34-1:35: 'f' is declared at module level, so its return type may write `_` only for its error type; write `-> Promise<number>`",
			},
		},
		{
			name: "TypeParamPlaceholder",
			src:  `fn f<T: _>(x: T) -> T { return x }`,
			expected: []string{
				"error 1:9-1:10: 'f' is declared at module level, so its signature may write `_` only for its error type",
			},
		},
		{
			name: "PlaceholderInsideErrorType",
			src: `
				declare fn fail() -> number throws Array<string>
				fn f() -> number throws Array<_> { return fail() }
			`,
		},
		{
			name: "ErrorPlaceholderInfersBorrow",
			src: `
				declare fn fail<'a>(e: &'a Array<string>) -> number throws &'a Array<string>
				fn f<'a>(e: &'a Array<string>) -> number throws _ { return fail(e) }
			`,
			expected: []string{
				"error 3:53-3:54: `_` in the error type infers `&Array<string>`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "ClosureParamPlaceholder",
			src:  `val f = fn (x: _) -> number { return x + 1 }`,
			expected: []string{
				"error 1:16-1:17: 'f' is a module-level closure, so parameter 'x' needs a type in place of `_`; write `x: number` on the closure, or annotate 'f'",
			},
		},
		{
			name: "MethodParamPlaceholder",
			src: `
				class C {
					n: number,
					add(self, x: _) -> number { return self.n + x },
				}
			`,
			expected: []string{
				"error 4:19-4:20: 'add' is a method, so parameter 'x' needs a type in place of `_`; write `x: number`",
			},
		},
		{
			name: "MethodErrorPlaceholderInfersBorrow",
			src: `
				declare fn fail<'a>(e: &'a Array<string>) -> number throws &'a Array<string>
				class C {
					n: number,
					get<'a>(self, e: &'a Array<string>) -> number throws _ { return fail(e) },
				}
			`,
			expected: []string{
				"error 5:59-5:60: `_` in the error type infers `&Array<string>`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "ClosureErrorPlaceholderInfersBorrow",
			src: `
				declare fn fail<'a>(e: &'a Array<string>) -> number throws &'a Array<string>
				val f = fn <'a>(e: &'a Array<string>) -> number throws _ { return fail(e) }
			`,
			expected: []string{
				"error 3:60-3:61: `_` in the error type infers `&Array<string>`, which holds a lifetime; write the type, or write its borrow and lifetime around `_`, as in `&'a _`",
			},
		},
		{
			name: "GetterReturnPlaceholder",
			src: `
				class C {
					n: number,
					get v(&self) -> _ { return self.n },
				}
			`,
			expected: []string{
				"error 4:22-4:23: 'v' is a method, so its return type may write `_` only for its error type; write `-> number`",
			},
		},
		{
			name: "SetterParamPlaceholder",
			src: `
				class C {
					n: number,
					set v(&mut self, x: _) { self.n = x },
				}
			`,
			expected: []string{
				"error 4:26-4:27: 'v' is a method, so parameter 'x' needs a type in place of `_`; write `x: number`",
			},
		},
		{
			name: "MethodReturnPlaceholder",
			src: `
				class C {
					n: number,
					get(self) -> _ { return self.n },
				}
			`,
			expected: []string{
				"error 4:19-4:20: 'get' is a method, so its return type may write `_` only for its error type; write `-> number`",
			},
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
				severity := "error"
				if isWarning(e) {
					severity = "warning"
				}
				got = append(got, severity+" "+msgWithSpan(t, e))
			}
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestSignaturePlaceholderInfersErrorType asserts the signature recorded for a function whose
// error type writes `_`, with the `_` replaced by the error type its body throws.
func TestSignaturePlaceholderInfersErrorType(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected string
	}{
		{
			name:     "Throws",
			src:      "declare fn fail() -> number throws string\nfn f() -> number throws _ { return fail() }",
			expected: "() -> number throws string",
		},
		{
			name:     "PromiseError",
			src:      "declare fn fail() -> Promise<number, string>\nasync fn f() -> Promise<number, _> { return await fail() }",
			expected: "() -> Promise<number, string>",
		},
		{
			name:     "GeneratorError",
			src:      "declare fn fail() -> number throws string\ngen fn f() -> Generator<number, undefined, unknown, _> { yield fail() }",
			expected: "() -> Generator<number, undefined, unknown, string>",
		},
		{
			name:     "NestedInThrows",
			src:      "declare fn fail() -> number throws Array<string>\nfn f() -> number throws Array<_> { return fail() }",
			expected: "() -> number throws Array<string>",
		},
		{
			name:     "NestedInPromiseError",
			src:      "declare fn fail() -> Promise<number, Array<string>>\nasync fn f() -> Promise<number, Array<_>> { return await fail() }",
			expected: "() -> Promise<number, Array<string>>",
		},
		{
			name:     "NestedInFuncParam",
			src:      "declare fn fail() -> number throws fn (x: string) -> number\nfn f() -> number throws fn (x: _) -> number { return fail() }",
			expected: "() -> number throws fn (x: string) -> number",
		},
		{
			name:     "BorrowedError",
			src:      "declare fn fail<'a>(e: &'a Array<string>) -> number throws &'a Array<string>\nfn f<'a>(e: &'a Array<string>) -> number throws &'a _ { return fail(e) }",
			expected: "(&Array<string>) -> number throws &Array<string>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, decls := inferForSignatures(t, tt.src)
			var fn *ast.FuncDecl
			for _, d := range decls {
				if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "f" {
					fn = fd
				}
			}
			require.NotNil(t, fn)
			sig, ok := info.FuncDeclSignature(fn)
			require.True(t, ok)
			require.Equal(t, tt.expected, renderWritten(sig))
		})
	}
}
