package codegen

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// buildSource lowers one module of Escalier source to the JS it emits. It runs the
// parser and codegen without the checker in between, which is what lets a class
// declaring two constructors be exercised here — internal/checker still reports that
// as unsupported, so such a class never reaches codegen through the build path.
func buildSource(t *testing.T, src string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	module, errs := parser.ParseLibFiles(ctx, []*ast.Source{
		{ID: 0, Path: "main.esc", Contents: src},
	})
	require.Empty(t, errs, "parse errors: %v", errs)

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
	return printer.Output
}

// An overloaded method emits one JS method carrying a dispatch chain. Emitting one
// member per arm would leave a class with two `grow` definitions, which JS accepts
// with the last one winning, so every arm but the last would vanish in silence.
func TestBuildOverloadedMethod(t *testing.T) {
	got := buildSource(t, `
		class Box {
			grow(mut self, d: number) -> number { return d },
			grow(mut self, d: number, e: number) -> number { return d + e },
		}`)
	require.Equal(t, `export class Box {
  grow(param0, param1) {
    if (typeof param0 === "number" && typeof param1 === "number") {
      const d = param0;
      const e = param1;
      return d + e;
    } else if (typeof param0 === "number") {
      const d = param0;
      return d;
    } else throw new TypeError("No overload matches the provided arguments for method 'grow'");
  }
}`, got)
}

// An overloaded constructor emits one JS constructor. Two `constructor` members in
// one class is a SyntaxError, so the alternative is a module that does not load.
func TestBuildOverloadedConstructor(t *testing.T) {
	got := buildSource(t, `
		class Box {
			x: number,
			constructor(mut self, x: number) { self.x = x },
			constructor(mut self, x: number, y: number) { self.x = x + y },
		}`)
	require.Equal(t, `export class Box {
  constructor(param0, param1) {
    if (typeof param0 === "number" && typeof param1 === "number") {
      const x = param0;
      const y = param1;
      this.x = x + y;
    } else if (typeof param0 === "number") {
      const x = param0;
      this.x = x;
    } else throw new TypeError("No overload matches the provided arguments for constructor");
  }
}`, got)
}

// A derived class's synthesized `super()` leads each arm's own branch, so whichever
// arm runs calls the superclass constructor exactly once before touching `this`.
func TestBuildOverloadedConstructorInADerivedClass(t *testing.T) {
	got := buildSource(t, `
		class Base { }
		class Crate extends Base {
			label: string,
			constructor(mut self, label: string) { self.label = label },
			constructor(mut self, label: string, extra: string) { self.label = label + extra },
		}`)
	require.Contains(t, got, `  constructor(param0, param1) {
    if (typeof param0 === "string" && typeof param1 === "string") {
      super();
      const label = param0;
      const extra = param1;
      this.label = label + extra;
    } else if (typeof param0 === "string") {
      super();
      const label = param0;
      this.label = label;
    } else throw new TypeError("No overload matches the provided arguments for constructor");
  }`)
}

// An arm that writes its own `super(…)` keeps it where it was written rather than
// getting a second synthesized call ahead of it.
func TestBuildOverloadedConstructorKeepsAWrittenSuperCall(t *testing.T) {
	got := buildSource(t, `
		class Base { }
		class Crate extends Base {
			label: string,
			constructor(mut self, label: string) { super()  self.label = label },
			constructor(mut self, label: string, extra: string) { self.label = label + extra },
		}`)
	require.Contains(t, got, `    } else if (typeof param0 === "string") {
      const label = param0;
      super();
      this.label = label;
    } else`)
}

// A member that is not overloaded keeps its ordinary lowering, so grouping costs a
// class nothing where it declares each name once.
func TestBuildUnoverloadedMembersAreUnchanged(t *testing.T) {
	got := buildSource(t, `
		class Box {
			x: number,
			constructor(mut self, x: number) { self.x = x },
			area(self) -> number { return self.x },
			get width(self) -> number { return self.x },
			set width(mut self, w: number) { self.x = w },
		}`)
	require.Equal(t, `export class Box {
  constructor(temp1) {
    const x = temp1;
    this.x = x;
  }
  area() {
    return this.x;
  }
  get width() {
    return this.x;
  }
  set width(temp2) {
    const w = temp2;
    this.x = w;
  }
}`, got)
}

// A static method overloads apart from an instance method of the same name, so a
// class declaring both emits two members rather than fusing them into one.
func TestBuildOverloadedStaticAndInstanceMethodsStayApart(t *testing.T) {
	got := buildSource(t, `
		class Box {
			of(self, x: number) -> number { return x },
			static of(x: number) -> number { return x },
			static of(x: number, y: number) -> number { return x + y },
		}`)
	require.Contains(t, got, `  of(temp1) {
    const x = temp1;
    return x;
  }`)
	require.Contains(t, got, `  static of(param0, param1) {
    if (typeof param0 === "number" && typeof param1 === "number") {`)
}

// A parameter default binds through the same conditional buildParams emits for an
// ordinary parameter. Binding the pattern with its default still attached would put
// the default where an assignment target belongs — `const d = 5 = param0` — which is
// a JS `SyntaxError`, so the module would not parse.
func TestBuildOverloadArmParameterDefault(t *testing.T) {
	t.Run("a method", func(t *testing.T) {
		got := buildSource(t, `
			class Box {
				grow(mut self, d: number = 5) -> number { return d },
				grow(mut self, d: number, e: number) -> number { return d + e },
			}`)
		require.Contains(t, got, `      const d = typeof param0 !== "undefined" ? param0 : 5;`)
	})
	t.Run("a top-level function", func(t *testing.T) {
		got := buildSource(t, `
			fn f(d: number = 5) -> number { return d }
			fn f(d: number, e: number) -> number { return d + e }`)
		require.Contains(t, got, `    const d = typeof param0 !== "undefined" ? param0 : 5;`)
	})
}

// A rest parameter binds the slot it sits at rather than gathering the slots after
// it, which is what the guard beside it already tests. Binding the rest pattern
// itself would emit `const ...ds = param0`, a JS SyntaxError. Dispatching such an arm
// on argument count is #1545.
func TestBuildOverloadArmRestParameter(t *testing.T) {
	got := buildSource(t, `
		class Box {
			grow(mut self, ...ds: Array<number>) -> number { return 1 },
			grow(mut self, d: number, e: number) -> number { return d + e },
		}`)
	require.Contains(t, got, `    } else if (Array.isArray(param0)) {
      const ds = param0;
      return 1;
    } else`)
	require.NotContains(t, got, "const ...ds")
}

// A numeric member key groups the same way a named one does. Routing it to the
// per-declaration key reserved for computed keys would leave the arms ungrouped, and
// two members named `1` is the silent-overwrite this change exists to stop.
func TestBuildOverloadedMethodWithANumericKey(t *testing.T) {
	got := buildSource(t, `
		class Box {
			1(mut self, d: number) -> number { return d },
			1(mut self, d: number, e: number) -> number { return d + e },
		}`)
	require.Contains(t, got, `  1(param0, param1) {`)
	require.Contains(t, got,
		`No overload matches the provided arguments for method '1'`)
	require.Equal(t, 1, strings.Count(got, "1(param0, param1)"))
}

// A body-less arm contributes a signature and no code, so it gets no branch. Giving
// it one would guard a body returning undefined ahead of an arm that has code to run.
func TestBuildOverloadSetDropsABodylessArm(t *testing.T) {
	got := buildSource(t, `
		declare class Box {
			grow(mut self, d: number) -> number,
			grow(mut self, d: number, e: number) -> number,
		}`)
	require.Empty(t, got)
}
