package compiler

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

// TestCallableBindingDtsValidSyntax verifies that a top-level binding
// whose type is a callable (FuncType) referencing `Self` produces a
// valid TypeScript .d.ts declaration. The existing emission path
// produces `interface Name(args) => Ret`, which is invalid TS — a
// callable interface must be `interface Name { (args): Ret }`.
func TestCallableBindingDtsValidSyntax(t *testing.T) {
	src := &ast.Source{
		ID:   0,
		Path: "lib/index.esc",
		Contents: `export val mut obj1 = {
    value: 0,
    increment(mut self, amount: number) -> Self {
        self.value = self.value + amount
        return self
    },
}
val inc = obj1.increment
`,
	}
	output := CompilePackage([]*ast.Source{src})
	dts := output.CompUnits["lib/index"].DTS

	// The known bug: emitted as `interface __inc_self__(amount: number) => …`.
	// Walk every line that starts with `interface ` and assert there is no
	// `(` or `=>` before the opening `{`.
	for _, line := range strings.Split(dts, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "interface ") {
			continue
		}
		brace := strings.Index(trimmed, "{")
		if brace < 0 {
			t.Errorf("interface line missing opening brace: %q", trimmed)
			continue
		}
		header := trimmed[:brace]
		assert.NotContainsf(t, header, "(",
			"interface header must not contain `(` before `{`; got %q", trimmed)
		assert.NotContainsf(t, header, "=>",
			"interface header must not contain `=>` before `{`; got %q", trimmed)
	}
}

func TestVarDecls(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "val foo = 5\nvar bar = \"hello\"\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestFuncDecls(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "fn add(a: number, b: number) {\n  return a + b\n}\nfn sub(a: number, b: number) { return a - b }\nval sum = add(1, 2)\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestArrays(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "val nums = [1, 2, 3]\nval first = nums[0]\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestMemberAccess(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "console.log(\"hello, world\")\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestTypeCast(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "val x = 5 : number\nval y = \"hello\" : string\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestGeneratorFunction(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "fn count() {\n  yield 1\n  yield 2\n  yield 3\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestGeneratorWithReturn(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "fn myGen() {\n  yield 1\n  return 42\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestYieldFrom(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "fn delegating() {\n  declare val nums: Array<number>\n  yield from nums\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestForIn(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "declare val items: Array<number>\nfor x in items {\n  val y = x\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestForInIterator(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "declare val items: Iterator<number>\nfor x in items {\n  val y = x\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestForAwaitIn(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "async fn processItems() {\n  declare val items: Array<number>\n  for await x in items {\n    val y = x\n  }\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestForAwaitInAsyncIterator(t *testing.T) {
	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: "async fn processItems() {\n  declare val items: AsyncIterator<number>\n  for await x in items {\n    val y = x\n  }\n}\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}
