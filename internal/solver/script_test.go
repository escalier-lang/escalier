package solver

import (
	"context"
	"testing"
	"time"

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
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "expected no parse errors")
	return script
}

// inferScriptSource parses src as a script, runs InferScript, and renders the
// top-level value and type bindings off the script scope's own maps rather than the
// prelude parent. It is the script counterpart to inferSource. A script's top-level
// bindings are linear locals, so this is how their generalized types are inspected.
func inferScriptSource(t *testing.T, src string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	scope, _, errs := InferScript(parseScript(t, src))
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
		// Rule 1, the mut→immutable case, errors when the mutable source is live after
		// the alias. This is the Accept example. A top-level `val items: mut {…}` aliased
		// to an immutable binding and then used mutably reports the Rule 1 transition
		// error.
		"Rule1_SourceLive_Error": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				val snapshot: {x: number} = items
				items.x = 2
				snapshot
			`,
			want: []string{
				"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
			},
		},
		// Rule 1: safe when the mutable source is dead after the alias.
		"Rule1_SourceDead_OK": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				items.x = 2
				val snapshot: {x: number} = items
				snapshot
			`,
		},
		// Rule 3: two mutable aliases of the same value are always allowed.
		"Rule3_MultipleMutableAliases_OK": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: mut {x: number} = a
				b.x = 2
				a.x
			`,
		},
		// Chain aliasing through a mutable intermediate: the conflict names the live
		// mutable alias, not the source itself.
		"ChainAlias_TargetLive_Error": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: mut {x: number} = a
				val c: {x: number} = b
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

// TestScriptLinearScoping pins the defining difference between a script and a
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
	require.Equal(t, "Unknown identifier: x", scriptErrs[0].Message())

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
// function-body rule rather than the module rule. inferStmt overwrites the name's slot
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
	require.Equal(t, "Duplicate declaration: x", moduleErrs[0].Message())
}

// TestScriptReassignTransition exercises the reassignment transition path
// (inferAssign), distinct from the declaration-aliasing path the other parity cases
// drive. inferAssign reads the enclosing statement from c.fn.currentStmt to find its
// CFG StmtRef, which only exists because InferScript installs a funcCtx and runs the
// liveness pre-pass over the script body. Reassigning a live mutable owned value into
// an immutable binding is a Rule 1 transition. Mutating it before the reassignment
// makes it dead, and the transition stays silent.
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
			"cannot assign 'items' to immutable 'snap': 'items' is still used mutably after this point",
		}, transitionMessages(t, errs))
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

// TestScriptAwaitOutsideAsync pins the top-level `await` diagnostic. A script has no
// enclosing function to mark `async`, so the error carries no related span. That
// matches a module top-level await, rather than pointing Related() at the whole
// script. This guards InferScript passing a nil funcCtx node.
func TestScriptAwaitOutsideAsync(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val x = 5
		val y = await x
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "await can only be used inside an async function", errs[0].Message())
	require.Empty(t, errs[0].Related())
}
