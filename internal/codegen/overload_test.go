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
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const d = param0;
      const e = param1;
      return d + e;
    } else if (arguments.length === 1 && typeof param0 === "number") {
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
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const x = param0;
      const y = param1;
      this.x = x + y;
    } else if (arguments.length === 1 && typeof param0 === "number") {
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
    if (arguments.length === 2 && typeof param0 === "string" && typeof param1 === "string") {
      super();
      const label = param0;
      const extra = param1;
      this.label = label + extra;
    } else if (arguments.length === 1 && typeof param0 === "string") {
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
	require.Contains(t, got, `    } else if (arguments.length === 1 && typeof param0 === "string") {
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
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {`)
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

// A rest parameter gathers every argument from its own position onward. The dispatch
// member declares one positional parameter per slot the widest arm uses, so the
// arguments past the last of them are reachable only through `arguments`. Binding the
// rest pattern to its own slot would take `grow(1, 2, 3)` as `ds = 1` and drop the
// rest, and binding the pattern with its `...` still attached would emit
// `const ...ds = param0`, a JS SyntaxError.
func TestBuildOverloadArmRestParameter(t *testing.T) {
	got := buildSource(t, `
		class Box {
			grow(mut self, ...ds: Array<number>) -> number { return 1 },
			grow(mut self, d: number, e: number) -> number { return d + e },
		}`)
	require.Equal(t, `export class Box {
  grow(param0, param1) {
    if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const d = param0;
      const e = param1;
      return d + e;
    } else if (Array.prototype.every.call(arguments, function (elem) {
      return typeof elem === "number";
    })) {
      const ds = Array.prototype.slice.call(arguments);
      return 1;
    } else throw new TypeError("No overload matches the provided arguments for method 'grow'");
  }
}`, got)
}

// A rest parameter that follows fixed parameters gathers the tail past them, and its
// element test skips the positions their own guards already cover.
func TestBuildOverloadArmRestParameterAfterAFixedOne(t *testing.T) {
	got := buildSource(t, `
		class Box {
			grow(mut self, label: string, ...ds: Array<number>) -> number { return 1 },
			grow(mut self, d: number, e: number) -> number { return d + e },
		}`)
	require.Equal(t, `export class Box {
  grow(param0, param1) {
    if (arguments.length >= 1 && typeof param0 === "string" && Array.prototype.every.call(arguments, function (elem, index) {
      return index < 1 || typeof elem === "number";
    })) {
      const label = param0;
      const ds = Array.prototype.slice.call(arguments, 1);
      return 1;
    } else if (arguments.length === 2 && typeof param0 === "number" && typeof param1 === "number") {
      const d = param0;
      const e = param1;
      return d + e;
    } else throw new TypeError("No overload matches the provided arguments for method 'grow'");
  }
}`, got)
}

// An arm whose count no call can meet is never reached, so each arm tests how many
// arguments it takes. Without that test the two-parameter arm here takes
// `grow(1, 2, 3)` and drops the third argument, and the rest arm below it never runs.
func TestBuildOverloadArmsTestArgumentCount(t *testing.T) {
	got := buildSource(t, `
		class Box {
			grow(mut self) -> number { return 0 },
			grow(mut self, d: number) -> number { return d },
			grow(mut self, d?: number) -> number { return 1 },
		}`)
	require.Equal(t, `export class Box {
  grow(param0) {
    if (arguments.length === 1 && typeof param0 === "number") {
      const d = param0;
      return d;
    } else if (arguments.length <= 1 && (typeof param0 === "undefined" || typeof param0 === "number")) {
      const d = param0;
      return 1;
    } else if (arguments.length === 0) {
      return 0;
    } else throw new TypeError("No overload matches the provided arguments for method 'grow'");
  }
}`, got)
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

// A parameter the caller may leave out accepts an absent slot. The slot holds
// `undefined`, which no type guard admits, so an arm written `fn f(x: number = 5)`
// would otherwise be passed over for the call `f()` that is exactly its own and the
// dispatch would fall through to the TypeError.
func TestBuildOverloadGuardAcceptsAnOmittedSlot(t *testing.T) {
	tests := []struct {
		name string
		arm  string
	}{
		{name: "a defaulted parameter", arm: "fn f(x: number = 5) -> number { return x }"},
		{name: "an optional parameter", arm: "fn f(x?: number) -> number { return 1 }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSource(t,
				tt.arm+"\nfn f(a: string, b: string) -> string { return a }")
			require.Contains(t, got,
				`} else if (arguments.length <= 1 && (typeof param0 === "undefined" || typeof param0 === "number")) {`)
		})
	}
	t.Run("a required parameter still tests its type alone", func(t *testing.T) {
		got := buildSource(t, `
			fn f(x: number) -> number { return x }
			fn f(a: string, b: string) -> string { return a }`)
		require.Contains(t, got, `} else if (arguments.length === 1 && typeof param0 === "number") {`)
		require.NotContains(t, got, `typeof param0 === "undefined"`)
	})
}

// Arms the comparison calls equal keep the order they were written in. The chain is
// first-match and two unannotated arms both build the guard `true`, so an unstable
// sort could swap which body a call runs.
func TestBuildOverloadTiedArmsKeepSourceOrder(t *testing.T) {
	got := buildSource(t, `
		class Box {
			pick(mut self, a) { return 1 },
			pick(mut self, b) { return 2 },
		}`)
	require.Contains(t, got, `    if (arguments.length === 1) {
      const a = param0;
      return 1;
    } else if (arguments.length === 1) {
      const b = param0;
      return 2;
    } else`)
}
