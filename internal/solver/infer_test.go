package solver

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// parseModuleFiles parses several in-memory sources (path → contents) into ONE
// combined module via parser.ParseLibFiles. ParseLibFiles already unions multiple
// files into shared, path-derived namespaces — exactly the multi-file assembly a
// real build does — so there is no separate module-merge step. Sources are added
// in sorted path order with distinct SourceIDs so spans and any error ordering
// are deterministic across runs, and a single context spans the whole parse.
func parseModuleFiles(t *testing.T, srcs map[string]string) *ast.Module {
	t.Helper()
	paths := make([]string, 0, len(srcs))
	for path := range srcs {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	sources := make([]*ast.Source, len(paths))
	for id, path := range paths {
		sources[id] = &ast.Source{ID: id, Path: path, Contents: srcs[path]}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, sources)
	require.Empty(t, parseErrors, "expected no parse errors")
	return module
}

// parseModule parses a single in-memory .esc source and returns the module for
// inference and inspection (the Info side table, decl nodes). A thin wrapper over
// parseModuleFiles so all parsing flows through one helper.
func parseModule(t *testing.T, src string) *ast.Module {
	t.Helper()
	return parseModuleFiles(t, map[string]string{"input.esc": src})
}

// inferModule runs InferModule on an already-parsed module and renders the
// top-level value/type bindings straight off the module scope's own maps (not the
// prelude parent), so operators and the stdlib-type placeholders are excluded.
// Only inference errors flow back; parse errors fail the test in parseModuleFiles.
func inferModule(module *ast.Module) (values, types map[string]string, errs []SolverError) {
	scope, _, errs := InferModule(module)
	values = make(map[string]string, len(scope.values))
	for name, b := range scope.values {
		// PR1: every binding holds exactly one scheme; renderScheme adds the
		// <T0, …> quantifier prefix when generalization left type parameters behind.
		values[name] = renderScheme(b.Schemes[0])
	}
	types = renderBindings(scope.types, func(b TypeBinding) soltype.Type { return b.Type })
	return values, types, errs
}

// inferSource is the single-file table harness (§3.6) — fast, no on-disk
// fixtures. It is the one-file case of inferSources.
func inferSource(t *testing.T, src string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	return inferSources(t, map[string]string{"input.esc": src})
}

// inferSources is the multi-file table harness (§3.6): several in-memory sources
// keyed by file path, parsed into one combined module and inferred together. This
// exercises the exact dep-graph-spanning-files path an on-disk fixture would, with
// no package.json / file-discovery ceremony (the real fixture harness is M8).
func inferSources(t *testing.T, srcs map[string]string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	return inferModule(parseModuleFiles(t, srcs))
}

// renderBindings renders each binding in m to its soltype string, using typeOf
// to pull the soltype.Type out of the binding. One helper serves both the value
// and type sorts.
func renderBindings[B any](m map[string]B, typeOf func(B) soltype.Type) map[string]string {
	out := make(map[string]string, len(m))
	for name, b := range m {
		out[name] = soltype.Print(typeOf(b))
	}
	return out
}

func TestInferModuleValDecls(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "NumberLiteral",
			src:  `val x = 5`,
			want: map[string]string{"x": "5"},
		},
		{
			name: "StringLiteral",
			src:  `val s = "hi"`,
			want: map[string]string{"s": `"hi"`},
		},
		{
			name: "BoolLiteral",
			src:  `val b = true`,
			want: map[string]string{"b": "true"},
		},
		{
			name: "MultipleDecls",
			src: `
				val x = 5
				val s = "hi"
			`,
			want: map[string]string{"x": "5", "s": `"hi"`},
		},
		{
			name: "IdentifierInitializerReferencesEarlierDecl",
			src: `
				val x = 5
				val y = x
			`,
			want: map[string]string{"x": "5", "y": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// PR-4: object literals, tuple literals, and member access infer end-to-end
// through the real parser pipeline. Field reads resolve through a record-typed
// binding (constrain's record <: record arm lowers the result from the matching
// field), and a read of an absent field surfaces a MissingPropertyError.
func TestInferModuleObjectsAndTuples(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			name: "RecordLiteral",
			src:  `val o = {a: 5, b: "hi"}`,
			want: map[string]string{"o": `{a: 5, b: "hi"}`},
		},
		{
			name: "EmptyRecord",
			src:  `val o = {}`,
			want: map[string]string{"o": "{}"},
		},
		{
			name: "TupleLiteral",
			src:  `val t = [1, "hi"]`,
			want: map[string]string{"t": `[1, "hi"]`},
		},
		{
			name: "NestedRecordInTuple",
			src:  `val t = [{a: 1}, 2]`,
			want: map[string]string{"t": `[{a: 1}, 2]`},
		},
		{
			name: "FieldRead",
			src: `
				val o = {a: 5, b: "hi"}
				val x = o.a
			`,
			want: map[string]string{"o": `{a: 5, b: "hi"}`, "x": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// Reading a field the receiver lacks is a constraint failure (MissingProperty);
// the binding for the failed read resolves to the never placeholder.
func TestInferModuleFieldReadMissingProperty(t *testing.T) {
	src := `
		val o = {a: 5}
		val x = o.b
	`
	values, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "object is missing property: b", errs[0].Message())
	// M2.5: blame the member's prop, not the whole decl.
	require.Equal(t, "b", spanText(src, errs[0].Span()))
	require.Equal(t, map[string]string{"o": "{a: 5}", "x": "never"}, values)
}

// A forward reference — a decl that uses a name defined later in the source —
// failed in PR-2 (source-order walk). PR-5 orders declarations by the dep graph,
// so x's component is inferred before y's and the reference now resolves.
func TestInferModuleForwardReferenceResolves(t *testing.T) {
	values, _, errs := inferSource(t, `
		val y = x
		val x = 5
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{"x": "5", "y": "5"}, values)
}

// A top-level declaration outside the M2 subset reports a clean
// UnsupportedNodeError rather than panicking. A type alias is such a decl — type
// bindings are M3+ — so it registers a type-sort dep_graph key the SCC driver
// reports as unsupported. (FuncDecl, unsupported at the module level through
// PR-2, is now wired in by PR-5; see the func/recursion tests.)
func TestInferModuleUnsupportedDecl(t *testing.T) {
	src := `type Foo = number`
	_, types, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TypeDecl", errs[0].Message())
	// M2.5: the error self-blames from the decl node.
	require.Equal(t, src, spanText(src, errs[0].Span()))
	// The unsupported decl must not leak a type binding.
	require.NotContains(t, types, "Foo")
}

// A `val` with no initializer can't be inferred in M2 (annotation-driven binding
// needs TypeAnn support that lands later); it reports MissingInitializerError and
// binds NOTHING, so a later reference still fails as an unknown identifier rather
// than silently resolving to a placeholder.
func TestInferModuleVarDeclWithoutInitializer(t *testing.T) {
	src := `declare val x: number`
	values, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "Variable declaration requires an initializer: x", errs[0].Message())
	// M2.5: the error self-blames from the decl node (whose span, per the parser,
	// covers the binder but not the trailing annotation).
	require.Equal(t, "declare val x", spanText(src, errs[0].Span()))
	require.Empty(t, values)
}

// A no-initializer decl must not leak a binding: a later use of the name is a
// genuine unknown-identifier error, not a silent resolution to a placeholder.
func TestInferModuleNoInitializerDoesNotLeakBinding(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare val x: number
		val y = x
	`)
	require.Len(t, errs, 2)
	require.Equal(t, "Variable declaration requires an initializer: x", errs[0].Message())
	require.Equal(t, "Unknown identifier: x", errs[1].Message())
	// PR8 (Fix A): a binding whose definition is wholly the ErrorType recovery
	// sentinel (`val y = <unknown>`) recovers AS `error` rather than freezing to
	// `never`, so downstream uses of y absorb instead of cascading `<: never`.
	require.Equal(t, map[string]string{"y": "error"}, values)
}

// A destructuring pattern is IdentPat-only-gated in M2 (M4 adds tuple/record
// binding); the binding reports UnsupportedNodeError and introduces no value.
// The initializer `[1, 2]` is a tuple expression, which PR-4 now infers, so the
// only remaining error is the destructuring pattern on the binding side.
func TestInferModuleDestructuringPatternUnsupported(t *testing.T) {
	src := `val [a, b] = [1, 2]`
	values, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: TuplePat", errs[0].Message())
	// M2.5: blame the offending pattern, not the whole decl.
	require.Equal(t, "[a, b]", spanText(src, errs[0].Span()))
	require.Empty(t, values)
}

// A duplicate top-level `val` is a redeclaration error (unlike FuncDecl
// overloads); the first binding is kept and the second reports cleanly.
func TestInferModuleDuplicateTopLevelValIsError(t *testing.T) {
	src := `
		val x = 5
		val x = "hi"
	`
	values, _, errs := inferSource(t, src)
	require.Len(t, errs, 1)
	require.Equal(t, "Duplicate declaration: x", errs[0].Message())
	// M2.5: blame the second decl; relate the first ("previously declared here").
	require.Equal(t, `val x = "hi"`, spanText(src, errs[0].Span()))
	require.Len(t, errs[0].Related(), 1)
	require.Equal(t, `val x = 5`, spanText(src, errs[0].Related()[0]))
	require.Equal(t, map[string]string{"x": "5"}, values)
}

// PR-5: dep_graph SCC ordering wires top-level FuncDecls into the module walk and
// makes inference order-independent. Each case asserts the rendered MONOMORPHIC
// binding types end-to-end — recursion resolves through the group var, but M1
// ships no schemes so nothing generalizes (no <T0>); that is M3.
func TestInferModuleSCCOrdering(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]string
	}{
		{
			// A function declared before the one it calls still resolves: callee's
			// component is inferred first, so the call sees its concrete type.
			name: "OutOfOrderFuncReference",
			src: `
				fn caller(n: number) { callee(n) }
				fn callee(n: number) -> number { n }
			`,
			want: map[string]string{
				"caller": "fn (n: number) -> number",
				"callee": "fn (n: number) -> number",
			},
		},
		{
			// A self-recursive function with no base case (M2 has no conditionals)
			// never returns, so its return type coalesces to never. It resolves
			// because the SCC driver pre-binds foo to a var before its body.
			name: "SelfRecursive",
			src:  `fn foo(x: number) { foo(x) }`,
			want: map[string]string{"foo": "fn (x: number) -> never"},
		},
		{
			// A mutually-recursive pair: each body calls the other. Return
			// annotations ground the cycle in a concrete type, so both resolve to
			// the annotated function type.
			name: "MutuallyRecursiveGrounded",
			src: `
				fn ping(n: number) -> number { pong(n) }
				fn pong(n: number) -> number { ping(n) }
			`,
			want: map[string]string{
				"ping": "fn (n: number) -> number",
				"pong": "fn (n: number) -> number",
			},
		},
		{
			// A chain of forward references across val and fn declarations,
			// declared in reverse dependency order.
			name: "ValChainForwardReference",
			src: `
				val z = y
				val y = x
				val x = 5
			`,
			want: map[string]string{"x": "5", "y": "5", "z": "5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// An ungrounded mutually-recursive group — each body calls the other with no
// return annotation and no base case — builds a cyclic var↔var bound graph.
// M1's coalesce had no recursion guard, so a guard-free inline walk would loop
// here forever; PR-5 pulls the M3 path-scoped guard forward (coalesce.go,
// m2-implementation-plan §7), collapsing the ungrounded recursive return to
// never (⊥). The assertion is really a termination test: it must complete rather
// than hang.
func TestInferModuleUngroundedMutualRecursionTerminates(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn a(n: number) { b(n) }
		fn b(n: number) { a(n) }
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{
		"a": "fn (n: number) -> never",
		"b": "fn (n: number) -> never",
	}, values)
}

// A `val` that participates in a recursive group must be constrained against its
// peer's group var RAW and coalesced only once the group is complete. `a` (a
// `val`) references `z`, and `z` calls `a` back, so they form one SCC; `a` sorts
// first and is inferred before `z`. Coalescing `a`'s initializer at definition
// time (as inferVarDecl does for body-level binds) would read `z`'s still-empty
// var and freeze `a` to `never`; inferDeclDef instead returns the raw type, so
// `a` correctly resolves to `z`'s function type once `z`'s body grounds the
// return via its `-> number` annotation.
func TestInferModuleValInRecursiveGroupUsesRawType(t *testing.T) {
	values, _, errs := inferSource(t, `
		val a = z
		fn z() -> number { a() }
	`)
	require.Empty(t, errs)
	require.Equal(t, map[string]string{
		"a": "fn () -> number",
		"z": "fn () -> number",
	}, values)
}

// Repeated top-level functions of the same name are overloads, which M2 does not
// represent (overload-intersection is M3). Rather than merge the arms into one
// var — yielding an uncallable union-of-functions binding — M2 keeps the first
// arm (so the binding stays callable with that signature) and reports each extra
// arm with a clear diagnostic.
func TestInferModuleFunctionOverloadNotSupported(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		fn f(x: string) -> string { x }
		val r = f(5)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Function overloads are not supported in M2: f", errs[0].Message())
	// The first arm is kept, so f stays callable and the call to f(5) resolves.
	require.Equal(t, map[string]string{
		"f": "fn (x: number) -> number",
		"r": "number",
	}, values)
}

// A declaration kind the dep graph does not model — here a `namespace` block,
// which BuildDepGraph produces no binding key for — must still report a clean
// UnsupportedNodeError rather than vanishing silently. The reconciliation pass in
// inferDepGraph walks the module's own declarations and flags any the SCC walk
// never visited.
func TestInferModuleNamespaceDeclUnsupported(t *testing.T) {
	values, types, errs := inferSource(t, `
		namespace Foo {
			val x = 5
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unsupported in M2: NamespaceDecl", errs[0].Message())
	require.Empty(t, values)
	// The unsupported decl must not leak a type binding for the namespace.
	require.NotContains(t, types, "Foo")
}

// PR-6: multi-file resolution. Several in-memory files are parsed into one
// combined module (parser.ParseLibFiles unions files that share a path-derived
// namespace) and inferred together; a top-level `val`/`fn` in one file resolves a
// binding from another by root-namespace short name. This is the M2 exit
// criterion — a multi-file module resolving via the dep graph — exercised
// end-to-end through the real parser + InferModule + soltype printer. All
// inference is MONOMORPHIC (M1 ships no schemes; <T0> generalization is M3).
func TestInferMultiFile(t *testing.T) {
	tests := []struct {
		name string
		srcs map[string]string
		want map[string]string
	}{
		{
			// File b reads a `val` defined in file a by short name. The dep graph
			// spans both files, so a's component is inferred before b's.
			name: "ValReferencesOtherFile",
			srcs: map[string]string{
				"a.esc": `val x = 5`,
				"b.esc": `val y = x`,
			},
			want: map[string]string{"x": "5", "y": "5"},
		},
		{
			// The defining file can come later in sorted order than the using
			// file — SCC ordering, not file order, drives inference.
			name: "ForwardReferenceAcrossFiles",
			srcs: map[string]string{
				"a.esc": `val y = x`,
				"b.esc": `val x = 5`,
			},
			want: map[string]string{"x": "5", "y": "5"},
		},
		{
			// A function defined in one file is applied in another; the call
			// resolves the callee's cross-file signature.
			name: "CallFunctionFromOtherFile",
			srcs: map[string]string{
				"a.esc": `fn id(n: number) -> number { n }`,
				"b.esc": `val r = id(5)`,
			},
			want: map[string]string{
				"id": "fn (n: number) -> number",
				"r":  "number",
			},
		},
		{
			// Mutually-recursive functions split across two files resolve through
			// the shared group var, grounded by their return annotations.
			name: "MutualRecursionAcrossFiles",
			srcs: map[string]string{
				"a.esc": `fn ping(n: number) -> number { pong(n) }`,
				"b.esc": `fn pong(n: number) -> number { ping(n) }`,
			},
			want: map[string]string{
				"ping": "fn (n: number) -> number",
				"pong": "fn (n: number) -> number",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSources(t, tt.srcs)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values)
		})
	}
}

// An unknown identifier that no file defines stays an unbound-name error even in
// the multi-file path: the combined dep graph has no binding for it, so the
// reference resolves to the never placeholder and reports cleanly.
func TestInferMultiFileUnknownIdentifier(t *testing.T) {
	srcA := `val y = missing`
	values, _, errs := inferSources(t, map[string]string{
		"a.esc": srcA,
		"b.esc": `val z = 5`,
	})
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: missing", errs[0].Message())
	// M2.5: the error self-blames from the ident node.
	require.Equal(t, "missing", spanText(srcA, errs[0].Span()))
	// PR8 (Fix A): a binding whose definition is wholly the ErrorType recovery
	// sentinel recovers AS `error` rather than freezing to `never`.
	require.Equal(t, map[string]string{"y": "error", "z": "5"}, values)
}

// Error recovery for a NAMED callee: a too-many-args call still yields the
// callee's declared return type (not `never`), matching the inline-callee recovery
// asserted by TestInferCallTooManyArgs. M3's inferIdent returns an instantiated
// var rather than a concrete FuncType, so inferCall recovers the return through the
// var's FuncType lower bound (resolveFunc); without that, `r` regressed to `never`.
// PR4: too-many is the extra-arg lint (TooManyArgsError), not a FuncArityMismatch.
func TestInferModuleNamedCalleeArityMismatchRecoversReturn(t *testing.T) {
	values, _, errs := inferSource(t, `
		fn f(x: number) -> number { x }
		val r = f(1, 2)
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Too many arguments: expected at most 1, but got 2", errs[0].Message())
	require.Equal(t, "number", values["r"], "the result recovers to the declared return, not never")
}
