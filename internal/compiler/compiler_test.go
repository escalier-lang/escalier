package compiler

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestGenerateSourceMap(t *testing.T) {
	source := parser.Source{
		Path:     "input.esc",
		Contents: "val foo = 5\nval bar = \"hello\"\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}

func TestGenerateSourceMapWithFuncDecls(t *testing.T) {
	source := parser.Source{
		Path:     "input.esc",
		Contents: "fn add(a, b) {\n  return a + b\n}\nfn sub(a, b) { return a - b }\n",
	}
	output := Compile(source)
	snaps.MatchSnapshot(t, output)
}
