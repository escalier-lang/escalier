package compiler

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

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
