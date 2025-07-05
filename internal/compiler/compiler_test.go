package compiler

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestVarDecls(t *testing.T) {
	source := &ast.Source{
		Path:     "input.esc",
		Contents: "val foo = 5\nvar bar = \"hello\"\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestFuncDecls(t *testing.T) {
	source := &ast.Source{
		Path:     "input.esc",
		Contents: "fn add(a: number, b: number) {\n  return a + b\n}\nfn sub(a: number, b: number) { return a - b }\nval sum = add(1, 2)\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestArrays(t *testing.T) {
	source := &ast.Source{
		Path:     "input.esc",
		Contents: "val nums = [1, 2, 3]\nval first = nums[0]\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestMemberAccess(t *testing.T) {
	source := &ast.Source{
		Path:     "input.esc",
		Contents: "console.log(\"hello, world\")\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}
