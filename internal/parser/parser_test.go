package parser

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

func TestParsingAddition(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + b",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingAddSub(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a - b + c",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingMulAdd(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a * b + c * d",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingUnaryOps(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "+a - -b",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingParens(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a * (b + c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "-foo(a, b, c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingCurriedCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "foo(a)(b)(c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingArrayLiteral(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "[1, 2, 3]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingMember(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a.b?.c",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingIndex(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a[base + offset]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingMultipleIndexes(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a[i][j]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingOptChainIndex(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a?[base + offset]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}
