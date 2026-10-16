package solver

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// parseScript parses a single in-memory .esc source as a script and returns the AST.
// A script is top-level statements that run in source order with function-body
// semantics. parseScript is the script counterpart to parseModule, parsing through
// ParseScript rather than the library-module assembly ParseLibFiles drives.
func parseScript(t *testing.T, src string) *ast.Script {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx := context.Background()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "expected no parse errors")
	registerTestSources(t, map[int]*ast.Source{source.ID: source})
	return script
}

// inferScriptSource parses src as a script, runs InferScript, and renders the
// top-level value and type bindings off the script scope's own maps rather than the
// prelude parent. It is the script counterpart to inferSource. A script's top-level
// bindings are linear locals, so this is how their generalized types are inspected.
func inferScriptSource(t *testing.T, src string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	scope, _, errs := InferScript(parseScript(t, src), testStdlibSource())
	values = make(map[string]string, len(scope.values))
	for name, b := range scope.values {
		values[name] = renderScheme(b.Schemes[0])
	}
	types = renderBindings(scope.types, func(b TypeBinding) soltype.Type { return b.Type })
	return values, types, errs
}

// TestInferScriptSourceOrder checks the core of the script entry point: top-level
// statements infer in source order, each binding seeing only the ones before it,
// exactly as inside a function body and unlike a module's dependency-ordered decls.
func TestInferScriptSourceOrder(t *testing.T) {
	values, _, errs := inferScriptSource(t, `
		val x = 5
		val y = x
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.Equal(t, "5", values["y"])
}

// TestInferScriptEnum checks that an enum can be declared and used in a script (bin/).
// A script's top-level statements run in source order, so an enum declared before its
// use binds its type and variant constructors just as in a module: a constructor call
// yields the enum's alias type, rendered under its own name.
func TestInferScriptEnum(t *testing.T) {
	values, types, errs := inferScriptSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		val c = Color.Hex("#fff")
		val mk = Color.RGB
	`)
	require.Empty(t, errs)
	require.Equal(t, "Color", types["Color"])
	require.Equal(t, "Color", values["c"])
	require.Equal(t, "{new (r: number, g: number, b: number) -> Color, [Symbol.customMatcher](subject: Color.RGB) -> [number, number, number]}", values["mk"])
}

// TestInferScriptEnumOutOfOrder checks that a script does NOT resolve an enum used before
// its declaration. Unlike a module, whose top-level declarations are dependency-ordered,
// a script's statements are linear, so a forward reference is an unknown identifier.
func TestInferScriptEnumOutOfOrder(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val c = Color.Hex("#fff")
		enum Color {
			RGB(r: number),
			Hex(code: string),
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: Color", errs[0].Message())
}

// TestInferScriptClass checks that a class can be declared and used in a script (bin/):
// the constructor value and instance type bind, and field and method access resolve
// through the projected class body, just as in a module.
func TestInferScriptClass(t *testing.T) {
	values, types, errs := inferScriptSource(t, `
		class Point {
			x: number,
			y: number,
			getX(&self) -> number { return self.x },
		}
		val p = Point(1, 2)
		val px = p.x
		val d = p.getX()
	`)
	require.Empty(t, errs)
	require.Equal(t, "Point", types["Point"])
	require.Equal(t, "{new (x: number, y: number) -> Point}", values["Point"])
	require.Equal(t, "Point", values["p"])
	require.Equal(t, "number", values["px"])
	require.Equal(t, "number", values["d"])
}

// TestInferScriptClassOutOfOrder checks that a script does NOT resolve a class used
// before its declaration — its statements are linear, so a forward reference is an
// unknown identifier, unlike a module's dependency-ordered declarations.
func TestInferScriptClassOutOfOrder(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val p = Point(1, 2)
		class Point { x: number, y: number }
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: Point", errs[0].Message())
}

// TestInferScriptTypeAndFuncDecls covers a `type` alias, a `declare fn`, and a `fn` with a
// body written at a script's top level. Each binds where it appears, the way a class or
// enum does, so a reference before the declaration is unbound. A function declaration
// nested in a block or a function body is rejected.
func TestInferScriptTypeAndFuncDecls(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantValues map[string]string
		wantTypes  map[string]string
		wantErrs   []string
	}{
		{
			name: "Alias",
			src: `
				type Point = {x: number, y: number}
				val p: Point = {x: 5, y: 10}
			`,
			wantValues: map[string]string{"p": "Point"},
			wantTypes:  map[string]string{"Point": "Point"},
		},
		{
			name: "GenericRecursiveAlias",
			src: `
				type List<T> = {head: T, tail: List<T> | null}
				val l: List<number> = {head: 1, tail: null}
			`,
			wantValues: map[string]string{"l": "List<number>"},
		},
		{
			name: "AliasBeforeDeclaration",
			src: `
				val p: Point = {x: 5, y: 10}
				type Point = {x: number, y: number}
			`,
			wantErrs: []string{"cannot find type `Point`"},
		},
		{
			name: "DeclareFn",
			// A tagged template literal needs backticks, which a raw string cannot hold.
			src: "declare fn foo(strings: Array<string>, ...values: Array<number>) -> string\n" +
				"val output = foo`${1} ${2}`",
			wantValues: map[string]string{
				"foo":    "fn (strings: Array<string>, ...values: Array<number>) -> string",
				"output": "string",
			},
		},
		{
			name: "GenericDeclareFn",
			src: `
				declare fn id<T>(x: T) -> T
				val n = id(5)
				val s = id("a")
			`,
			wantValues: map[string]string{"n": "5", "s": "\"a\""},
		},
		{
			name: "OverloadedDeclareFn",
			src: `
				declare fn parse(x: string) -> number
				declare fn parse(x: number) -> string
				val a = parse("1")
				val b = parse(1)
			`,
			wantValues: map[string]string{"a": "number", "b": "string"},
		},
		{
			name: "DuplicateOverloadArm",
			src: `
				declare fn g(x: number) -> number
				declare fn g(x: number) -> string
			`,
			wantErrs: []string{"Overload arms must have distinguishable parameter types: g"},
		},
		{
			name: "DeclareFnBeforeDeclaration",
			src: `
				val n = double(2)
				declare fn double(x: number) -> number
			`,
			wantErrs: []string{"Unknown identifier: double"},
		},
		{
			name: "DuplicateAlias",
			src: `
				type T = number
				val x: T = 5
				type T = string
				val y: number = x
			`,
			wantErrs: []string{"Duplicate declaration: T"},
		},
		{
			// A nested block is no top level, so the alias there is rejected and the one at the
			// top level binds.
			name: "AliasInANestedBlock",
			src: `
				if true { type T = string }
				type T = number
				val x: T = 5
			`,
			wantValues: map[string]string{"x": "T"},
			wantErrs:   []string{"Declaration not allowed in function body: TypeDecl"},
		},
		{
			name: "DeclareFnInANestedBlock",
			src: `
				if true { declare fn g() -> number }
			`,
			wantErrs: []string{"Declaration not allowed in function body: FuncDecl"},
		},
		{
			name: "AliasReusingAClassName",
			src: `
				class T { x: number }
				type T = number
			`,
			wantErrs: []string{"Duplicate declaration: T"},
		},
		{
			name: "FnWithBody",
			src: `
				fn add(a: number, b: number) {
					return a + b
				}
				val sum = add(1, 2)
			`,
			wantValues: map[string]string{
				"add": "fn (a: number, b: number) -> number",
				"sum": "number",
			},
		},
		{
			name: "RecursiveFn",
			src: `
				fn countdown(n: number) {
					if n == 0 {
						return "done"
					}
					return countdown(n - 1)
				}
				val r = countdown(3)
			`,
			wantValues: map[string]string{
				"countdown": "fn (n: number) -> \"done\"",
				"r":         "\"done\"",
			},
		},
		{
			name: "RecursiveFnWritingACapture",
			src: `
				val mut total = {v: 0}
				fn sum(n: number) {
					if n > 0 {
						total.v = total.v + n
						sum(n - 1)
					}
				}
				sum(3)
				val r = total.v
			`,
			wantValues: map[string]string{
				"sum": "fn (n: number) -> undefined",
				"r":   "number",
			},
		},
		{
			// The recursive call writes x through the capture while `a` still borrows x.
			name: "RecursiveCallWritesACaptureBesideLiveBorrow",
			src: captureDecls + `
				val mut x = {v: 1}
				fn f(n: number) {
					if n > 0 {
						val a = &x
						f(n - 1)
						readRead(a, a)
					}
					x.v = n
				}
			`,
			wantErrs: []string{"this call may run 'f', which writes 'x' while it is borrowed as immutable"},
		},
		{
			// The parameter f shadows the function's own name, so `&f` borrows the parameter.
			name: "ParamShadowsTheFnName",
			src: `
				fn f(f: {v: number}) {
					val r = &f
					return r
				}
			`,
			wantValues: map[string]string{"f": "fn (f: {v: number}) -> &{v: number}"},
		},
		{
			name: "RecursiveFnThatCannotReturn",
			src: `
				fn f() {
					return {next: f()}
				}
			`,
			wantErrs: []string{"`f` has type `fn () -> {next: μX0.{next: X0}}`, and no finite value inhabits its return type, so a call to it never returns; give the recursion a base case, make the recursive property optional, or defer the recursive call behind a function or a Promise"},
		},
		{
			name: "FnCapturesAnEarlierVal",
			src: `
				val base = 10
				fn addBase(x: number) {
					return x + base
				}
				val r = addBase(1)
			`,
			wantValues: map[string]string{
				"addBase": "fn (x: number) -> number",
				"r":       "number",
			},
		},
		{
			name: "FnCapturesAnEarlierVar",
			src: `
				var count = 0
				fn bump() {
					count = count + 1
					return count
				}
				val r = bump()
			`,
			wantValues: map[string]string{
				"bump": "fn () -> number",
				"r":    "number",
			},
		},
		{
			name: "GenericFn",
			src: `
				fn id<T>(x: T) -> T {
					return x
				}
				val n = id(5)
				val s = id("a")
			`,
			wantValues: map[string]string{
				"id": "fn <T>(x: T) -> T",
				"n":  "5",
				"s":  "\"a\"",
			},
		},
		{
			name: "GeneratorFn",
			src: `
				gen fn nums() {
					yield 1
					yield 2
				}
				val g = nums()
			`,
			wantValues: map[string]string{
				"nums": "fn () -> Generator<1 | 2, undefined, unknown>",
				"g":    "Generator<1 | 2, undefined, unknown>",
			},
		},
		{
			name: "AsyncFn",
			src: `
				async fn fetchNum() {
					return 5
				}
				val p = fetchNum()
			`,
			wantValues: map[string]string{
				"fetchNum": "fn () -> Promise<5>",
				"p":        "Promise<5>",
			},
		},
		{
			name: "FnBeforeDeclaration",
			src: `
				val n = double(2)
				fn double(x: number) {
					return x * 2
				}
			`,
			wantErrs: []string{"Unknown identifier: double"},
		},
		{
			name: "FnInANestedBlock",
			src: `
				if true {
					fn g() {
						return 1
					}
				}
			`,
			wantErrs: []string{"Declaration not allowed in function body: FuncDecl"},
		},
		{
			name: "FnInAFunctionBody",
			src: `
				fn outer() {
					fn inner() {
						return 1
					}
				}
			`,
			wantErrs: []string{"Declaration not allowed in function body: FuncDecl"},
		},
		{
			name: "OverloadedFnWithBody",
			src: `
				fn parse(x: string) {
					return 1
				}
				fn parse(x: number) {
					return "1"
				}
				val b = parse(1)
			`,
			wantValues: map[string]string{"b": "\"1\""},
			wantErrs:   []string{"A script cannot overload a function declared with a body: parse"},
		},
		{
			name: "DeclareFnAfterFnWithBody",
			src: `
				fn parse(x: string) {
					return 1
				}
				declare fn parse(x: number) -> string
			`,
			wantErrs: []string{"A script cannot overload a function declared with a body: parse"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferScriptSource(t, tt.src)
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Message()
			}
			if tt.wantErrs == nil {
				require.Empty(t, msgs)
			} else {
				require.Equal(t, tt.wantErrs, msgs)
			}
			for name, want := range tt.wantValues {
				require.Equal(t, want, values[name], name)
			}
			for name, want := range tt.wantTypes {
				require.Equal(t, want, types[name], name)
			}
		})
	}
}

// TestScriptTransitionParity is the milestone's central claim: a `mut`→immutable
// transition at script top level is checked identically to the same statements
// wrapped in a function body. Each case runs through InferScript directly, then again
// through InferModule after being indented into `fn test() { … }`. The two error lists
// must match each other and the expected verdict. That match proves a script's
// top-level body runs the same liveness, alias, and transition machinery a function
// body does.
func TestScriptTransitionParity(t *testing.T) {
	tests := map[string]struct {
		// stmts are valid both at script top level and inside a function body, so the
		// same text drives both entry points.
		stmts string
		want  []string
	}{
		// Rule 1, the mut→immutable case, errors when the mutable source is live after an
		// immutable borrow of it. A top-level `val items: mut {…}` borrowed immutably and
		// then used mutably reports the Rule 1 transition error.
		"Rule1_SourceLive_Error": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				val snapshot: &{x: number} = items
				items.x = 2
				snapshot
			`,
			want: []string{
				"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
			},
		},
		// Rule 1: safe when the mutable source is dead after the borrow.
		"Rule1_SourceDead_OK": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				items.x = 2
				val snapshot: &{x: number} = items
				snapshot
			`,
		},
		// Rule 3: two mutable borrows of the same value are always allowed.
		"Rule3_MultipleMutableAliases_OK": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: &mut {x: number} = a
				b.x = 2
				a.x
			`,
		},
		// Chain aliasing through a mutable borrow: the conflict names the live mutable
		// alias, not the source itself.
		"ChainAlias_TargetLive_Error": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: &mut {x: number} = a
				val c: &{x: number} = b
				a.x = 2
				c
			`,
			want: []string{
				"cannot assign 'b' to immutable 'c': 'a' still has mutable access to 'b' after this point",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, scriptErrs := inferScriptSource(t, tt.stmts)
			scriptMsgs := transitionMessages(t, scriptErrs)

			_, _, fnErrs := inferSource(t, "fn test() {"+tt.stmts+"\n}")
			fnMsgs := transitionMessages(t, fnErrs)

			if len(tt.want) == 0 {
				require.Empty(t, scriptMsgs)
				require.Empty(t, fnMsgs)
				return
			}
			require.ElementsMatch(t, tt.want, scriptMsgs)
			require.ElementsMatch(t, tt.want, fnMsgs, "script and function-wrapped forms must report the same transition errors")
		})
	}
}

// TestScriptFnCaptureParity asserts that a script-level `fn f() { … }` checks its captures
// of the script's earlier bindings the way `val f = fn () { … }` does. Each case writes the
// function on one line marked FN, then runs once with `fn f()` there and once with
// `val f = fn ()`. Both runs must report exactly the expected messages.
func TestScriptFnCaptureParity(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// Calling f writes x through the capture while `a` still borrows x immutably.
		"WriteThroughCaptureBesideLiveBorrow": {
			src: captureDecls + `
				val mut x = {v: 1}
				val a = &x
				FN { x.v = 5 }
				f()
				readRead(a, a)
			`,
			want: []string{"10:5-10:6: this call may run 'f', which writes 'x' while it is borrowed as immutable"},
		},
		// Calling f reads x, so a call after x moves is a use after the move.
		"CallAfterMovingTheCapture": {
			src: captureDecls + `
				val x = {v: 1}
				FN { readRead(&x, &x) }
				take(x)
				f()
			`,
			want: []string{"10:5-10:6: use of moved value 'x'"},
		},
		// f reads x only while it runs, so x can move once the last call returns.
		"MoveAfterTheLastCall": {
			src: captureDecls + `
				val x = {v: 1}
				FN { readRead(&x, &x) }
				f()
				take(x)
			`,
		},
		// f writes the captured `var` count, which the script reads after the call.
		"WriteThroughCapturedVar": {
			src: `
				var count = 0
				FN { count = count + 1 }
				f()
				val n = count
			`,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			for _, form := range []string{"fn f()", "val f = fn ()"} {
				_, _, errs := inferScriptSource(t, strings.ReplaceAll(tt.src, "FN", form))
				require.Equal(t, tt.want, messagesWithSpan(t, errs), form)
			}
		})
	}
}

// TestScriptLinearScoping asserts the defining difference between a script and a
// module: a script's top-level statements are a linear body, so a binding sees only
// the ones before it. The same source that forward-references a later binding is an
// "Unknown identifier" error as a script but type-checks as a module, where
// BuildDepGraph orders declarations by dependency rather than source position.
func TestScriptLinearScoping(t *testing.T) {
	src := `
		val y = x
		val x = 5
	`
	_, _, scriptErrs := inferScriptSource(t, src)
	require.Len(t, scriptErrs, 1)
	require.Equal(t, "2:11-2:12: Unknown identifier: x", msgWithSpan(t, scriptErrs[0]))

	// The identical source is well-formed as a module. The dep graph types `val x`
	// before the `val y` that refers to it.
	_, _, moduleErrs := inferSource(t, src)
	require.Empty(t, moduleErrs)
}

// TestScriptEmpty checks that a script with no statements infers cleanly. The
// liveness pre-pass over an empty block and the empty statement walk must not panic
// or invent diagnostics.
func TestScriptEmpty(t *testing.T) {
	values, _, errs := inferScriptSource(t, "")
	require.Empty(t, errs)
	require.Empty(t, values)
}

// TestScriptRedeclaration checks that a script allows a name to be redeclared, the
// function-body rule rather than the module rule. inferStmt overwrites the name's binding
// on a body-level `val`/`var`, so a second declaration rebinds without constraining
// the old and new types together. A later reference sees the new type. The identical
// source as a module is rejected, because the dep-graph driver reports a duplicate
// top-level declaration.
func TestScriptRedeclaration(t *testing.T) {
	src := `
		val x = 5
		val x = "hi"
		val y = x
	`
	values, _, scriptErrs := inferScriptSource(t, src)
	require.Empty(t, scriptErrs)
	require.Equal(t, `"hi"`, values["x"])
	require.Equal(t, `"hi"`, values["y"])

	// The same source is a duplicate-declaration error as a module.
	_, _, moduleErrs := inferSource(t, src)
	require.Len(t, moduleErrs, 1)
	require.Equal(t, "3:3-3:15: Duplicate declaration: x", msgWithSpan(t, moduleErrs[0]))
}

// TestScriptReassignTransition exercises the reassignment transition path
// (inferAssign), distinct from the declaration-aliasing path the other parity cases
// drive. inferAssign reads the enclosing statement from c.fn.currentStmt to find its
// CFG StmtRef, which only exists because InferScript installs a funcCtx and runs the
// liveness pre-pass over the script body. Reassigning an owned value into an owned destination
// moves it, so the later use of the source is a use-after-move. Mutating it before the
// reassignment makes it dead, and the move stays silent.
func TestScriptReassignTransition(t *testing.T) {
	t.Run("source_live_error", func(t *testing.T) {
		_, _, errs := inferScriptSource(t, `
			var snap: {x: number} = {x: 0}
			val items: mut {x: number} = {x: 1}
			snap = items
			items.x = 2
			snap
		`)
		require.Equal(t, []string{
			"5:4-5:9: use of moved value 'items'",
		}, transitionMessagesWithSpan(t, errs))
	})

	t.Run("source_dead_ok", func(t *testing.T) {
		_, _, errs := inferScriptSource(t, `
			var snap: {x: number} = {x: 0}
			val items: mut {x: number} = {x: 1}
			items.x = 2
			snap = items
			snap
		`)
		require.Empty(t, transitionMessages(t, errs))
	})
}

// TestScriptBorrowLifetimeParity checks that the lifetime origination/escape
// machinery runs over a script body too. A function expression with a `&mut`
// parameter, bound to a top-level `val`, carries a fresh lifetime on its parameter
// and threads it through the return. The `'a` in the rendered type is the evidence
// the borrow lifetime was inferred, not skipped. The identical source as a module
// binds the same `id`, and the two rendered types must match. The script entry
// point and the module entry point thread the borrow lifetime the same way.
func TestScriptBorrowLifetimeParity(t *testing.T) {
	const src = `val id = fn (p: &mut {x: number}) { return p }`

	scriptValues, _, scriptErrs := inferScriptSource(t, src)
	require.Empty(t, scriptErrs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", scriptValues["id"])

	moduleValues, _, moduleErrs := inferSource(t, src)
	require.Empty(t, moduleErrs)
	require.Equal(t, scriptValues["id"], moduleValues["id"])
}

// TestScriptAwaitOutsideAsync asserts the top-level `await` diagnostic. A script has no
// enclosing function to mark `async`, so the error carries no related span. That
// matches a module top-level await, rather than pointing Related() at the whole
// script. This guards InferScript passing a nil funcCtx node.
func TestScriptAwaitOutsideAsync(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val x = 5
		val y = await x
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-3:18: await can only be used inside an async function", msgWithSpan(t, errs[0]))
	require.Empty(t, errs[0].Related())
}

// inferScriptInLib parses libSrc as a library module and scriptSrc as a script,
// infers the module, then infers the script against the module's scope through
// InferScriptInLib. It returns the script scope's own value bindings and the
// script's diagnostics. The module is required to infer cleanly, so an error the
// caller sees belongs to the script.
func inferScriptInLib(t *testing.T, libSrc, scriptSrc string) (values map[string]string, errs []SolverError) {
	t.Helper()
	ctx := context.Background()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: libSrc}
	module, libParseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, libParseErrors, "expected no parse errors in the library")

	scriptSource := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: scriptSrc}
	script, scriptParseErrors := parser.NewParser(ctx, scriptSource).ParseScript()
	require.Empty(t, scriptParseErrors, "expected no parse errors in the script")

	registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, scriptSource.ID: scriptSource})

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors, "expected no errors from the library module")

	scope, _, errs := InferScriptInLib(script, lib)
	values = make(map[string]string, len(scope.values))
	for name, b := range scope.values {
		values[name] = renderScheme(b.Schemes[0])
	}
	return values, errs
}

// TestInferScriptInLib checks the bin/ to lib/ seam: a script infers against the
// scope a library module's run produced, so a name the script does not declare
// resolves to the library's binding without an import.
//
// The class and alias cases are what a second inference run could not do. Both
// resolve to a handle carrying a name whose definition lives on the run's Context,
// so reading `p.x` off a library class or annotating against a library alias only
// works because the script carries the library's run on.
func TestInferScriptInLib(t *testing.T) {
	tests := []struct {
		name       string
		lib        string
		script     string
		wantValues map[string]string
	}{
		{
			name:       "Value",
			lib:        `export val greeting = "hello"`,
			script:     `val g = greeting`,
			wantValues: map[string]string{"g": `"hello"`},
		},
		{
			name: "ClassMember",
			lib: `
				export class Point {
					x: number,
					y: number,
					getX(&self) -> number { return self.x },
				}
			`,
			script: `
				val p = Point(1, 2)
				val px = p.x
				val gx = p.getX()
			`,
			wantValues: map[string]string{"p": "Point", "px": "number", "gx": "number"},
		},
		{
			name:       "TypeAliasAnnotation",
			lib:        `export type Pair = {a: number, b: number}`,
			script:     `val pr: Pair = {a: 1, b: 2}`,
			wantValues: map[string]string{"pr": "Pair"},
		},
		{
			name:       "ScriptBindingShadowsLibrary",
			lib:        `export val greeting = "hello"`,
			script:     `val greeting = "goodbye"`,
			wantValues: map[string]string{"greeting": `"goodbye"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferScriptInLib(t, test.lib, test.script)
			require.Empty(t, errs)
			for name, want := range test.wantValues {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestInferScriptInLibUnknownName checks that the library scope adds the library's
// bindings and nothing else: a name neither the script nor the library declares is
// still unknown.
func TestInferScriptInLibUnknownName(t *testing.T) {
	_, errs := inferScriptInLib(t, `export val greeting = "hello"`, `val f = farewell`)
	require.Len(t, errs, 1)
	require.Equal(t, "1:9-1:17: Unknown identifier: farewell", msgWithSpan(t, errs[0]))
}

// TestInferScriptInLibReadsUnexportedNames checks that a bin/ script reads a lib/
// declaration the library does not export. The two are one package, and `export`
// gates what a *different* package sees rather than what this package's own scripts
// do. The module scope holds every top-level declaration, so the script resolves a
// private value, function, class, and type alias the same way it resolves an
// exported one.
func TestInferScriptInLibReadsUnexportedNames(t *testing.T) {
	values, errs := inferScriptInLib(t, `
		val secret = 42
		fn echo(n: number) -> number { return n }
		class Hidden {
			tag: string,
		}
		type Alias = string
		export val public = "seen"
	`, `
		val s = secret
		val d = echo(secret)
		val h = Hidden("inner")
		val t = h.tag
		val a: Alias = "annotated"
		val p = public
	`)
	require.Empty(t, errs)
	require.Equal(t, "42", values["s"])
	require.Equal(t, "number", values["d"])
	require.Equal(t, "Hidden", values["h"])
	require.Equal(t, "string", values["t"])
	// Renders under the alias's own name, which is what shows the script resolved the
	// library's private `Alias` rather than widening to its underlying type.
	require.Equal(t, "Alias", values["a"])
	require.Equal(t, `"seen"`, values["p"])
}

// TestInferScriptInLibClassShadowsLibrary checks that a class a script declares is
// its own class rather than an addition to the library's class of the same name.
// Both scripts and the library register their nominal definitions in the run's one
// Context, so each needs a key prefix of its own for two same-named classes to hold
// two definitions.
func TestInferScriptInLibClassShadowsLibrary(t *testing.T) {
	values, errs := inferScriptInLib(t, `
		export class Point {
			x: number,
		}
		export fn origin() -> Point { return Point(0) }
	`, `
		class Point {
			label: string,
		}
		val p = Point("here")
		val l = p.label
		val o = origin()
		val ox = o.x
	`)
	require.Empty(t, errs)
	require.Equal(t, `{new (label: string) -> Point}`, values["Point"])
	require.Equal(t, "Point", values["p"])
	require.Equal(t, "string", values["l"])
	require.Equal(t, "Point", values["o"])
	require.Equal(t, "number", values["ox"])
}

// TestInferScriptInLibClassPerScript checks that two scripts checked against one
// library each keep their own definition for a class name they both declare. The
// registries live on the run all three share, so without a per-script key the second
// script's definition would replace the first's while the first's scope still holds a
// handle naming it.
func TestInferScriptInLibClassPerScript(t *testing.T) {
	ctx := context.Background()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	scripts := []*ast.Source{
		{ID: 1, Path: "bin/first.esc", Contents: "class Shape {\n\tsides: number,\n}\nval s = Shape(3)\nval n = s.sides"},
		{ID: 2, Path: "bin/second.esc", Contents: "class Shape {\n\tname: string,\n}\nval s = Shape(\"square\")\nval n = s.name"},
	}
	sources := map[int]*ast.Source{libSource.ID: libSource}
	for _, src := range scripts {
		sources[src.ID] = src
	}
	registerTestSources(t, sources)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)

	wantMember := []string{"number", "string"}
	qnames := make([]string, len(scripts))
	for i, src := range scripts {
		script, scriptParseErrors := parser.NewParser(ctx, src).ParseScript()
		require.Empty(t, scriptParseErrors)
		scope, _, errs := InferScriptInLib(script, lib)
		require.Empty(t, errs, "script %s", src.Path)

		b, found := scope.GetValue("n")
		require.True(t, found)
		require.Equal(t, wantMember[i], renderScheme(b.Schemes[0]), "script %s", src.Path)

		shape, found := scope.GetType("Shape")
		require.True(t, found)
		cls, isClass := shape.Type.(*soltype.ClassType)
		require.True(t, isClass)
		qnames[i] = cls.Name
	}

	// Each script's handle names a key of its own, and both definitions still hold
	// their own members once the second script has run.
	require.NotEqual(t, qnames[0], qnames[1])
	wantBody := []string{"{sides: number}", "{name: string}"}
	for i, qname := range qnames {
		def, registered := lib.checker.ctx.classDef(qname)
		require.True(t, registered, "script %s", scripts[i].Path)
		require.Equal(t, wantBody[i], soltype.Print(def.Body), "script %s", scripts[i].Path)
	}
}

// TestInferScriptInLibLeavesTheLibraryTableAlone checks that re-checking a script
// against one library does not grow the library run's Prov table. The table maps a
// type to the source it came from, and a script's own entries belong to the script:
// an editor re-checks one bin/ file on every keystroke against a cached library, so
// a table shared with the library would grow for as long as the session lasts.
func TestInferScriptInLibLeavesTheLibraryTableAlone(t *testing.T) {
	ctx := context.Background()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val greeting = "hello"`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	scriptSource := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: "val g = greeting\nval h = g"}
	registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, scriptSource.ID: scriptSource})

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	before := len(lib.checker.prov)

	for range 5 {
		script, scriptParseErrors := parser.NewParser(ctx, scriptSource).ParseScript()
		require.Empty(t, scriptParseErrors)
		_, _, errs := InferScriptInLib(script, lib)
		require.Empty(t, errs)
	}

	require.Equal(t, before, len(lib.checker.prov))
}

// TestInferScriptInLibRechecksAScript checks what a second check of one script
// against one library sees. An editor re-checks a bin/ file on every keystroke, so
// both checks share the run holding the class and alias registries. Each check must
// see the declarations it was given rather than the union of every check so far. The
// second version below drops a class the first declared, which is what a registry
// that only overwrites would keep.
func TestInferScriptInLibRechecksAScript(t *testing.T) {
	ctx := context.Background()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	classesBefore := len(lib.checker.ctx.classes)

	// Every version is the same file, so they carry the same source id and key their
	// declarations under the same names.
	versions := []struct {
		name     string
		contents string
		member   string
		classes  int
	}{
		{
			name:     "TwoClasses",
			contents: "class Point {\n\tx: number,\n}\nclass Shape {\n\tsides: number,\n}\nval p = Point(1)\nval m = p.x",
			member:   "number",
			classes:  2,
		},
		{
			name:     "OneClassWithAChangedMember",
			contents: "class Point {\n\tlabel: string,\n}\nval p = Point(\"here\")\nval m = p.label",
			member:   "string",
			classes:  1,
		},
		{
			name:     "BackToTheFirstMember",
			contents: "class Point {\n\tx: number,\n}\nval p = Point(2)\nval m = p.x",
			member:   "number",
			classes:  1,
		},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			source := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: version.contents}
			registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, source.ID: source})
			script, scriptParseErrors := parser.NewParser(ctx, source).ParseScript()
			require.Empty(t, scriptParseErrors)

			scope, _, errs := InferScriptInLib(script, lib)
			require.Empty(t, errs)
			b, found := scope.GetValue("m")
			require.True(t, found)
			require.Equal(t, version.member, renderScheme(b.Schemes[0]))

			require.Equal(t, classesBefore+version.classes, len(lib.checker.ctx.classes))
		})
	}
}

// TestInferScriptInLibRechecksAnEnum is TestInferScriptInLibRechecksAScript for an
// enum, which registers a class per variant and one alias for the enum itself. An
// enum is how a script reaches the alias registry at all, since the walk rejects a
// TypeDecl in a function body and a script body is one. This is what checks that the
// alias side is cleared too.
func TestInferScriptInLibRechecksAnEnum(t *testing.T) {
	ctx := context.Background()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	classesBefore := len(lib.checker.ctx.classes)
	aliasesBefore := len(lib.checker.ctx.aliases)

	versions := []struct {
		name     string
		contents string
		payload  string
		variants int
	}{
		{
			name:     "TwoVariants",
			contents: "enum Color {\n\tHex(code: string),\n\tRgb(r: number),\n}\nval c = Color.Hex(\"#fff\")\nval p = c",
			payload:  "Color",
			variants: 2,
		},
		{
			name:     "OneVariantWithAChangedPayload",
			contents: "enum Color {\n\tHex(code: number),\n}\nval c = Color.Hex(1)\nval p = c",
			payload:  "Color",
			variants: 1,
		},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			source := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: version.contents}
			registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, source.ID: source})
			script, scriptParseErrors := parser.NewParser(ctx, source).ParseScript()
			require.Empty(t, scriptParseErrors)

			scope, _, errs := InferScriptInLib(script, lib)
			require.Empty(t, errs)
			b, found := scope.GetValue("p")
			require.True(t, found)
			require.Equal(t, version.payload, renderScheme(b.Schemes[0]))

			// One class per variant this version declares, and one alias for the
			// enum, whichever check this is.
			require.Equal(t, classesBefore+version.variants, len(lib.checker.ctx.classes))
			require.Equal(t, aliasesBefore+1, len(lib.checker.ctx.aliases))
		})
	}
}
