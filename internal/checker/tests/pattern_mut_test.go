package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatternLevelMut_BindingTypes confirms that `val mut x = …` and the
// per-leaf shorthand/key-value forms wrap the binding's stored type in
// `mut`, while plain bindings stay unwrapped.
func TestPatternLevelMut_BindingTypes(t *testing.T) {
	tests := map[string]struct {
		input        string
		bindingName  string
		expectedType string
	}{
		"ValMutWrapsType": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val mut p = Point(0, 0)
			`,
			bindingName:  "p",
			expectedType: "mut Point",
		},
		"VarMutWrapsType": {
			input: `
				class Point(x: number, y: number) { x, y, }
				var mut p = Point(0, 0)
			`,
			bindingName:  "p",
			expectedType: "mut Point",
		},
		"ValMutWithAnnotationIsIdempotent": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val mut p: Point = Point(0, 0)
			`,
			bindingName:  "p",
			expectedType: "mut Point",
		},
		"PlainValStaysUnwrapped": {
			input: `
				class Point(x: number, y: number) { x, y, }
				val p = Point(0, 0)
			`,
			bindingName:  "p",
			expectedType: "Point",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedType, got,
				"unexpected type for %q", test.bindingName)
		})
	}
}

// TestPatternLevelMut_MutationBehavior exercises the runtime-relevant
// consequences of pattern-level `mut`: a `mut`-bound place is mutable
// through that place; a plain place is not. Includes per-leaf
// destructuring cases for both shorthand and key-value forms.
func TestPatternLevelMut_MutationBehavior(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectErrors bool
	}{
		"PlainBindingCannotMutateField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn test() {
					val p = Point(0, 0)
					p.x = 1
				}
			`,
			expectErrors: true,
		},
		"ValMutBindingCanMutateField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn test() {
					val mut p = Point(0, 0)
					p.x = 1
				}
			`,
			expectErrors: false,
		},
		"VarMutBindingCanMutateField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn test() {
					var mut p = Point(0, 0)
					p.x = 1
				}
			`,
			expectErrors: false,
		},
		"ValMutBindingCanCallMutSelfMethod": {
			input: `
				class Counter(count: number) {
					count,
					tick(mut self) -> number { self.count = self.count + 1 return self.count }
				}
				fn test() {
					val mut c = Counter(0)
					c.tick()
				}
			`,
			expectErrors: false,
		},
		"FuncParamMutCanMutateField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn move(mut p: Point) { p.x = 1 }
			`,
			expectErrors: false,
		},
		"FuncParamPlainCannotMutateField": {
			input: `
				class Point(x: number, y: number) { x, y, }
				fn move(p: Point) { p.x = 1 }
			`,
			expectErrors: true,
		},
		"DestructureShorthand_MutLeafCanMutate": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { mut a, b } = o
					a.v = 1
				}
			`,
			expectErrors: false,
		},
		"DestructureShorthand_PlainLeafCannotMutate": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { mut a, b } = o
					b.v = 1
				}
			`,
			expectErrors: true,
		},
		"DestructureKeyValue_MutLeafCanMutate": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { a: mut x, b: y } = o
					x.v = 1
				}
			`,
			expectErrors: false,
		},
		"DestructureKeyValue_PlainLeafCannotMutate": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { a: mut x, b: y } = o
					y.v = 1
				}
			`,
			expectErrors: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			inferErrors := c.InferModule(inferCtx, module)

			if test.expectErrors {
				assert.NotEmpty(t, inferErrors, "expected inference errors for %s", name)
				for i, err := range inferErrors {
					t.Logf("Error[%d]: %s", i, err.Message())
				}
			} else {
				if len(inferErrors) > 0 {
					for i, err := range inferErrors {
						t.Logf("Unexpected Error[%d]: %s", i, err.Message())
					}
				}
				assert.Empty(t, inferErrors, "expected no inference errors for %s", name)
			}
		})
	}
}

// TestPatternLevelMut_ValVarMatrix locks in the (val/var) × (mut/non-mut)
// matrix from the implementation plan. Each binding's `Assignable`
// (rebind axis) and `Mutable` (value-mutation axis) flags are independent
// and derived from the pattern + decl kind.
func TestPatternLevelMut_ValVarMatrix(t *testing.T) {
	tests := map[string]struct {
		input              string
		bindingName        string
		expectedAssignable bool
		expectedMutable    bool
	}{
		"Val": {
			input:              `class Point(x: number, y: number) { x, y, } val p = Point(0, 0)`,
			bindingName:        "p",
			expectedAssignable: false,
			expectedMutable:    false,
		},
		"Var": {
			input:              `class Point(x: number, y: number) { x, y, } var p = Point(0, 0)`,
			bindingName:        "p",
			expectedAssignable: true,
			expectedMutable:    false,
		},
		"ValMut": {
			input:              `class Point(x: number, y: number) { x, y, } val mut p = Point(0, 0)`,
			bindingName:        "p",
			expectedAssignable: false,
			expectedMutable:    true,
		},
		"VarMut": {
			input:              `class Point(x: number, y: number) { x, y, } var mut p = Point(0, 0)`,
			bindingName:        "p",
			expectedAssignable: true,
			expectedMutable:    true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			binding, ok := ns.Values[test.bindingName]
			require.Truef(t, ok, "binding %q not found", test.bindingName)
			assert.Equalf(t, test.expectedAssignable, binding.Assignable,
				"unexpected Assignable for %q", test.bindingName)
			assert.Equalf(t, test.expectedMutable, binding.Mutable,
				"unexpected Mutable for %q", test.bindingName)
		})
	}
}

// TestPatternLevelMut_BindMutSelfMethod replays the motivating fixture
// shape: with the binding marked `mut`, the mut-self method becomes
// visible at the use site (Phase 1 receiver-mutability filter).
func TestPatternLevelMut_BindMutSelfMethod(t *testing.T) {
	t.Parallel()
	input := `
		val value: number = 0
		val mut obj1 = {
			value,
			increment(mut self) -> number {
				self.value = self.value + 1
				return self.value
			},
		}
		val inc = obj1.increment
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	_, inferErrors := c.InferScript(inferCtx, script)
	if len(inferErrors) > 0 {
		for i, err := range inferErrors {
			t.Logf("Unexpected Error[%d]: %s", i, err.Message())
		}
	}
	assert.Empty(t, inferErrors, "expected no inference errors")
}

// TestPatternLevelMut_ForInPreservesMut covers the `for mut x in xs`
// loop pattern: Assignable is force-cleared (loop vars don't rebind)
// while Mutable flows through from the pattern.
func TestPatternLevelMut_ForInPreservesMut(t *testing.T) {
	t.Parallel()
	input := `
		class Point(x: number, y: number) { x, y, }
		fn test(pts: Array<Point>) {
			for mut p in pts {
				p.x = 1
			}
		}
	`
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: input}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
	require.Empty(t, parseErrors, "expected no parse errors")

	c := NewChecker(ctx)
	inferCtx := Context{Scope: Prelude(c)}
	inferErrors := c.InferModule(inferCtx, module)
	if len(inferErrors) > 0 {
		for i, err := range inferErrors {
			t.Logf("Unexpected Error[%d]: %s", i, err.Message())
		}
	}
	assert.Empty(t, inferErrors, "expected no inference errors")
}

// TestPatternLevelMut_GenericParam confirms that `fn f<T>(mut p: T)`
// produces a parameter binding whose type is `MutType(TypeVar(T))` —
// the wrap composes structurally with type-variable parameters.
func TestPatternLevelMut_GenericParam(t *testing.T) {
	t.Parallel()
	input := `
		class Point(x: number, y: number) { x, y, }
		fn f<T>(mut p: T) -> T { return p }
	`
	ns := mustInferAsModule(t, input)
	binding, ok := ns.Values["f"]
	require.True(t, ok, "f binding not found")
	fn, ok := type_system.Prune(binding.Type).(*type_system.FuncType)
	require.True(t, ok, "expected FuncType for f, got %T", binding.Type)
	require.Len(t, fn.Params, 1, "expected one parameter")
	pruned := type_system.Prune(fn.Params[0].Type)
	mut, isMut := pruned.(*type_system.MutType)
	require.Truef(t, isMut, "expected param type wrapped in MutType, got %T", pruned)
	// The wrapped inner type should be a TypeRef to T (the type parameter).
	inner := type_system.Prune(mut.Type)
	_, isRef := inner.(*type_system.TypeRefType)
	assert.Truef(t, isRef, "expected MutType wrapping a TypeRefType, got %T", inner)
}

// confirms the binding for a `mut` parameter carries a MutType-wrapped
// type (so the receiver-mutability filter and transition checks pick
// it up).
func TestPatternLevelMut_ParamBindingTypeIsWrapped(t *testing.T) {
	t.Parallel()
	input := `
		class Point(x: number, y: number) { x, y, }
		fn move(mut p: Point) { p.x = 1 }
	`
	ns := mustInferAsModule(t, input)
	moveBinding, ok := ns.Values["move"]
	require.True(t, ok, "move binding not found")
	fn, ok := type_system.Prune(moveBinding.Type).(*type_system.FuncType)
	require.True(t, ok, "expected FuncType for move, got %T", moveBinding.Type)
	require.Len(t, fn.Params, 1, "expected one parameter")
	pruned := type_system.Prune(fn.Params[0].Type)
	_, isMut := pruned.(*type_system.MutType)
	assert.Truef(t, isMut, "expected param type wrapped in MutType, got %T", pruned)
}

// TestPatternLevelMut_NoLeakIntoParentContainer verifies that wrapping
// a leaf binding in `mut` does NOT leak the MutType wrapper into the
// surrounding ObjectPat / TuplePat's structural type. Per-leaf `mut`
// is a property of the *binding*, not of the destructured value-shape;
// leaking it would make the parent pattern's inferred ObjectType /
// TupleType structurally inconsistent with the value being destructured.
func TestPatternLevelMut_NoLeakIntoParentContainer(t *testing.T) {
	tests := map[string]struct {
		input       string
		patternKind string // "object" or "tuple"
	}{
		"ObjKeyValueMutLeaf": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { a: mut x, b: y } = o
				}
			`,
			patternKind: "object",
		},
		"ObjShorthandMutLeaf": {
			input: `
				class Inner(v: number) { v, }
				class Outer(a: Inner, b: Inner) { a, b, }
				fn test() {
					val o = Outer(Inner(0), Inner(0))
					val { mut a, b } = o
				}
			`,
			patternKind: "object",
		},
		"TupleMutLeaf": {
			input: `
				class Inner(v: number) { v, }
				fn test(t: [Inner, Inner]) {
					val [mut x, y] = t
				}
			`,
			patternKind: "tuple",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			require.Empty(t, parseErrors, "expected no parse errors")
			c := NewChecker(ctx)
			inferCtx := Context{Scope: Prelude(c)}
			_ = c.InferModule(inferCtx, module)

			pat := findDestructurePattern(module, test.patternKind)
			require.NotNil(t, pat, "expected to find destructure pattern in module")

			switch p := pat.(type) {
			case *ast.ObjectPat:
				ot, ok := type_system.Prune(p.InferredType()).(*type_system.ObjectType)
				require.Truef(t, ok, "expected ObjectType, got %T", p.InferredType())
				for _, elem := range ot.Elems {
					prop, ok := elem.(*type_system.PropertyElem)
					if !ok {
						continue
					}
					_, isMut := type_system.Prune(prop.Value).(*type_system.MutType)
					assert.Falsef(t, isMut,
						"property %v should not be wrapped in MutType (leak from leaf binding); got %v",
						prop.Name, prop.Value)
				}
			case *ast.TuplePat:
				tt, ok := type_system.Prune(p.InferredType()).(*type_system.TupleType)
				require.Truef(t, ok, "expected TupleType, got %T", p.InferredType())
				for i, elem := range tt.Elems {
					_, isMut := type_system.Prune(elem).(*type_system.MutType)
					assert.Falsef(t, isMut,
						"element %d should not be wrapped in MutType (leak from leaf binding); got %v",
						i, elem)
				}
			}
		})
	}
}

// findDestructurePattern walks the module looking for a var-decl whose
// pattern is the requested kind ("object" or "tuple"). Helper for the
// no-leak test above.
func findDestructurePattern(module *ast.Module, kind string) ast.Pat {
	var found ast.Pat
	module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Body == nil {
				continue
			}
			for _, stmt := range fn.Body.Stmts {
				ds, ok := stmt.(*ast.DeclStmt)
				if !ok {
					continue
				}
				vd, ok := ds.Decl.(*ast.VarDecl)
				if !ok {
					continue
				}
				switch kind {
				case "object":
					if op, ok := vd.Pattern.(*ast.ObjectPat); ok {
						found = op
						return false
					}
				case "tuple":
					if tp, ok := vd.Pattern.(*ast.TuplePat); ok {
						found = tp
						return false
					}
				}
			}
		}
		return true
	})
	return found
}

// TestPatternLevelMut_SpanIncludesMutKeyword verifies that the span of
// a `mut`-prefixed identifier pattern covers the `mut` keyword as well
// as the bound name. Diagnostics like CannotMutateImmutableError should
// underline the binding-side `mut`, not just the identifier.
func TestPatternLevelMut_SpanIncludesMutKeyword(t *testing.T) {
	tests := map[string]struct {
		input    string
		findName string // identifier name to find within the pattern
	}{
		"PlainMutIdent": {
			input:    `val mut x = 1`,
			findName: "x",
		},
		"ObjShorthandMut": {
			input:    `val { mut x } = obj`,
			findName: "x",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			p := parser.NewParser(ctx, source)
			script, _ := p.ParseScript()
			require.NotNil(t, script, "expected a parsed script")

			leaf := findMutLeafSpan(script, test.findName)
			require.NotNil(t, leaf, "expected to find mut leaf for %s", test.findName)

			startCol := leaf.Span().Start.Column
			endCol := leaf.Span().End.Column
			contents := test.input
			require.True(t, startCol >= 1 && int(startCol)-1 < len(contents),
				"start column out of range")
			snippet := contents[startCol-1 : endCol-1]
			assert.Containsf(t, snippet, "mut",
				"expected pattern span to include the `mut` keyword, got %q",
				snippet)
		})
	}
}

// findMutLeafSpan locates the IdentPat or ObjShorthandPat with the given
// name and Mutable=true within a script.
func findMutLeafSpan(script *ast.Script, name string) ast.Node {
	var found ast.Node
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		switch v := n.(type) {
		case *ast.IdentPat:
			if v.Mutable && v.Name == name {
				found = v
				return
			}
		case *ast.ObjShorthandPat:
			if v.Mutable && v.Key.Name == name {
				found = v
				return
			}
		}
	}
	for _, stmt := range script.Stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		vd, ok := ds.Decl.(*ast.VarDecl)
		if !ok {
			continue
		}
		walkPattern(vd.Pattern, walk)
		if found != nil {
			return found
		}
	}
	return nil
}

func walkPattern(pat ast.Pat, fn func(ast.Node)) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		fn(p)
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjShorthandPat:
				fn(e)
			case *ast.ObjKeyValuePat:
				walkPattern(e.Value, fn)
			}
		}
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			walkPattern(elem, fn)
		}
	}
}
