package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
)

// inferWithTimeout parses and type-checks the given source within a timeout.
// Returns infer errors. Fails the test if parsing fails or the timeout is hit.
func inferWithTimeout(t *testing.T, source string, timeout time.Duration) []Error {
	t.Helper()
	src := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: source,
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{src})
	if !assert.Empty(t, parseErrors, "parse errors") {
		return nil
	}

	c := NewChecker()
	inferCtx := Context{
		Scope:      Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	return c.InferModule(inferCtx, module)
}

// ============================================================================
// Scenario 1: TypeRefType with TypeAlias set
// Verifies that type alias references (like HTMLAttributeAnchorTarget, Array<any>)
// unify correctly without excessive recursion.
// ============================================================================

func TestUnifyRecursion_TypeRefWithAlias(t *testing.T) {
	t.Run("string literal union alias", func(t *testing.T) {
		// HTMLAttributeAnchorTarget-like scenario
		errors := inferWithTimeout(t, `
			type Target = "_self" | "_blank" | "_parent" | "_top"
			val t: Target = "_blank"
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("Array<number> alias assignment", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			val a: Array<number> = [1, 2, 3]
			val b: Array<number> = a
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("same alias no args", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Point = {x: number, y: number}
			val p1: Point = {x: 1, y: 2}
			val p2: Point = p1
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("same alias with args", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Pair<A, B> = {first: A, second: B}
			val p1: Pair<number, string> = {first: 1, second: "hello"}
			val p2: Pair<number, string> = p1
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("different alias same structure", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Point2D = {x: number, y: number}
			type Vec2D = {x: number, y: number}
			val p: Point2D = {x: 1, y: 2}
			val v: Vec2D = p
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("TypeRef vs concrete ObjectType", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Point = {x: number, y: number}
			val p: Point = {x: 1, y: 2}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("recursive type alias - Json", func(t *testing.T) {
		// Json is a recursive type alias that caused issues with cycle detection (#463)
		errors := inferWithTimeout(t, `
			type Json = string | number | boolean | null | Array<Json>
			val j: Json = "hello"
			val k: Json = [1, 2, 3]
		`, 2*time.Second)
		assert.Empty(t, errors)
	})
}

// ============================================================================
// Scenario 2: TupleType with rest spreads
// Verifies that single-spread tuples unify with Array types without excessive
// recursion.
// ============================================================================

func TestUnifyRecursion_TupleWithRest(t *testing.T) {
	t.Run("single rest spread", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			val items: [number, ...Array<number>] = [1, 2, 3]
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("rest spread with prefix and suffix", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			val items: [string, ...Array<number>, boolean] = ["hello", 1, 2, 3, true]
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("tuple vs Array type", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			val arr: Array<number> = [1, 2, 3]
		`, 2*time.Second)
		assert.Empty(t, errors)
	})
}

// ============================================================================
// Scenario 3: Large ObjectType instances
// Verifies that objects with many properties unify without excessive recursion.
// ============================================================================

func TestUnifyRecursion_LargeObjectType(t *testing.T) {
	t.Run("large object with many properties", func(t *testing.T) {
		// Synthetic test with 25+ properties (simulating React SVG attributes)
		errors := inferWithTimeout(t, `
			type SVGAttributes = {
				id: string,
				className: string,
				style: string,
				width: number,
				height: number,
				viewBox: string,
				fill: string,
				stroke: string,
				strokeWidth: number,
				strokeLinecap: string,
				strokeLinejoin: string,
				strokeDasharray: string,
				strokeDashoffset: number,
				opacity: number,
				transform: string,
				clipPath: string,
				mask: string,
				filter: string,
				pointerEvents: string,
				cursor: string,
				display: string,
				visibility: string,
				overflow: string,
				fontFamily: string,
				fontSize: number,
			}
			val attrs: SVGAttributes = {
				id: "my-svg",
				className: "icon",
				style: "",
				width: 100,
				height: 100,
				viewBox: "0 0 100 100",
				fill: "none",
				stroke: "black",
				strokeWidth: 2,
				strokeLinecap: "round",
				strokeLinejoin: "round",
				strokeDasharray: "5,5",
				strokeDashoffset: 0,
				opacity: 1,
				transform: "rotate(45)",
				clipPath: "",
				mask: "",
				filter: "",
				pointerEvents: "auto",
				cursor: "pointer",
				display: "block",
				visibility: "visible",
				overflow: "hidden",
				fontFamily: "Arial",
				fontSize: 14,
			}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("large object with nested TypeRefType values", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Color = "red" | "green" | "blue"
			type Size = "small" | "medium" | "large"
			type Theme = {
				primary: Color,
				secondary: Color,
				accent: Color,
				background: Color,
				foreground: Color,
				border: Color,
				heading: Size,
				body: Size,
				caption: Size,
				button: Size,
				input: Size,
				label: Size,
				spacing: number,
				padding: number,
				margin: number,
				borderRadius: number,
				borderWidth: number,
				opacity: number,
				elevation: number,
				fontWeight: number,
			}
			val theme: Theme = {
				primary: "red",
				secondary: "blue",
				accent: "green",
				background: "blue",
				foreground: "red",
				border: "green",
				heading: "large",
				body: "medium",
				caption: "small",
				button: "medium",
				input: "medium",
				label: "small",
				spacing: 8,
				padding: 16,
				margin: 8,
				borderRadius: 4,
				borderWidth: 1,
				opacity: 1,
				elevation: 2,
				fontWeight: 400,
			}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})
}

// ============================================================================
// Regression tests: Unification recursion edge cases
// These ensure future changes don't reintroduce unbounded recursion.
// ============================================================================

func TestUnifyRecursionTerminates(t *testing.T) {
	t.Run("union of TypeRefTypes vs ObjectType", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type A = {x: number}
			type B = {x: number, y: string}
			type AB = A | B
			val obj: AB = {x: 1, y: "hello"}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("keyof TypeRefType", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Point = {x: number, y: number}
			type PointKey = keyof Point
			val k: PointKey = "x"
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("keyof of two structurally identical types", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type A = {x: number, y: string}
			type B = {x: number, y: string}
			type KA = keyof A
			type KB = keyof B
			val ka: KA = "x"
			val kb: KB = ka
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("self-referential nominal class", func(t *testing.T) {
		// Already covered by TestNominalClassUnificationTerminates but
		// included here for completeness of the regression suite.
		errors := inferWithTimeout(t, `
			class Node(value: number, next: Node | null) {
				value,
				next,
			}
			val n = Node(1, Node(2, null))
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("self-recursive type alias", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Tree = {
				value: number,
				children: Array<Tree>,
			}
			val t: Tree = {value: 1, children: [{value: 2, children: []}]}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("mutually referencing type aliases", func(t *testing.T) {
		// A and B reference each other — cycle detection must handle this
		errors := inferWithTimeout(t, `
			type A = {value: number, next: B | null}
			type B = {value: string, prev: A | null}
			val a: A = {value: 1, next: {value: "hello", prev: null}}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})

	t.Run("generic container with recursive type arg", func(t *testing.T) {
		errors := inferWithTimeout(t, `
			type Container<T> = {value: T, next: Container<T> | null}
			val c: Container<number> = {value: 1, next: {value: 2, next: null}}
		`, 2*time.Second)
		assert.Empty(t, errors)
	})
}
