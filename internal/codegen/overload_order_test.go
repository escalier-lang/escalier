package codegen

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// The dispatcher's if-else chain tests arms in the order DispatchOrder computes, so
// the emitted chain is where that order is visible. Each case below writes an overload
// set whose arms return different strings, so the order the guards appear in — and the
// body each guard carries — names the order the arms are tested in.
//
// The checker ranks the same sets the same way; internal/solver's
// TestOverloadDispatchAgreesWithResolution measures the two against each other.
func TestBuildOverloadedFuncDispatchOrder(t *testing.T) {
	tests := map[string]struct {
		src      string
		expected string
	}{
		// A literal admits one value out of its primitive's set, so its guard has to run
		// before the primitive's. Testing `typeof param0 === "number"` first would swallow
		// f(5) into the primitive arm, which is the arm the checker did NOT resolve it to.
		"LiteralBeforeItsPrimitive": {
			src: `fn f(x: number) -> string { return "n" }
fn f(x: 5) -> string { return "five" }`,
			expected: `export function f(param0) {
  if (param0 === 5) {
    const x = param0;
    return "five";
  } else if (typeof param0 === "number") {
    const x = param0;
    return "n";
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
		// An object with more required properties accepts fewer arguments, so its guard
		// runs first. Every `{x, y}` is also an `{x}`.
		"MoreRequiredPropertiesFirst": {
			src: `fn f(p: {x: number}) -> string { return "x" }
fn f(p: {x: number, y: number}) -> string { return "xy" }`,
			expected: `export function f(param0) {
  if (param0 !== null && typeof param0 === "object" && "x" in param0 && typeof param0.x === "number" && "y" in param0 && typeof param0.y === "number") {
    const p = param0;
    return "xy";
  } else if (param0 !== null && typeof param0 === "object" && "x" in param0 && typeof param0.x === "number") {
    const p = param0;
    return "x";
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
		// A type parameter cannot be tested at runtime, so its guard is a bare `true`.
		// Placed first it would answer every call, so it has to run last.
		"UntestableArmLast": {
			src: `fn f<T>(x: T) -> string { return "any" }
fn f(x: string) -> string { return "s" }`,
			expected: `export function f(param0) {
  if (typeof param0 === "string") {
    const x = param0;
    return "s";
  } else if (true) {
    const x = param0;
    return "any";
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
		// Two primitives of different families accept disjoint arguments, so neither
		// outranks the other and the chain keeps declaration order.
		"DisjointPrimitivesKeepDeclarationOrder": {
			src: `fn f(x: number) -> string { return "n" }
fn f(x: string) -> string { return "s" }`,
			expected: `export function f(param0) {
  if (typeof param0 === "number") {
    const x = param0;
    return "n";
  } else if (typeof param0 === "string") {
    const x = param0;
    return "s";
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
		// A declare-only arm gets no branch of its own, but it still takes part in the
		// ranking, because the checker ranks it too. Here `{y, w, ...}` outranks
		// `{y, ...}`, which is what leaves the later `{x, ...}` arm ahead of it. Ranking
		// only the arms that get a branch would drop that and put `{y, ...}` first, so
		// f({x: 1, y: 2}) would return true where the checker typed it as a number.
		"DeclareOnlyArmParticipatesInRanking": {
			src: `fn f(p: {y: number, ...}) -> boolean { return true }
declare fn f(p: {y: number, w: number, ...}) -> string
fn f(p: {x: number, ...}) -> number { return 1 }`,
			expected: `export function f(param0) {
  if (param0 !== null && typeof param0 === "object" && "x" in param0 && typeof param0.x === "number") {
    const p = param0;
    return 1;
  } else if (param0 !== null && typeof param0 === "object" && "y" in param0 && typeof param0.y === "number") {
    const p = param0;
    return true;
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
		// A guard tests only the parameters its own arm declares, so the arm with more
		// parameters runs first. Otherwise `typeof param0 === "string"` alone would answer
		// a two-argument call.
		"MoreParametersFirst": {
			src: `fn f(x: string) -> string { return "one" }
fn f(x: string, y: string) -> string { return "two" }`,
			expected: `export function f(param0, param1) {
  if (typeof param0 === "string" && typeof param1 === "string") {
    const x = param0;
    const y = param1;
    return "two";
  } else if (typeof param0 === "string") {
    const x = param0;
    return "one";
  } else throw new TypeError("No overload matches the provided arguments for function 'f'");
}`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			module, parseErrs := parser.ParseLibFiles(ctx, []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: test.src},
			})
			require.Empty(t, parseErrs, "expected no parse errors")

			depGraph := dep_graph.BuildDepGraph(module)
			builder := &Builder{tempId: 0, depGraph: depGraph}
			out := builder.BuildTopLevelDecls(depGraph)

			printer := NewPrinter()
			for i, stmt := range out.Stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}
			require.Equal(t, test.expected, printer.Output)
		})
	}
}
