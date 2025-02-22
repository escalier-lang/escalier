package parser

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
)

func TestParsingStringLiteral(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "\"hello\"",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

func TestParsingNumberLiteral(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "5",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
}

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
	assert.Len(t, parser.errors, 0)
}

func TestParsingIncompleteBinaryExpr(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a - b +",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 1)
	snaps.MatchSnapshot(t, parser.errors[0])
}

func TestParsingExtraOperatorsInBinaryExpr(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + * b",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 1)
	snaps.MatchSnapshot(t, parser.errors[0])
}

func TestParsingMulAdd(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a * b + c * d",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingMulDiv(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a / b * c",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingUnaryOps(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "+a - -b",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingParens(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a * (b + c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "foo(a, b, c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingIncompleteCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "foo(a,",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 1)
	snaps.MatchSnapshot(t, parser.errors[0])
}

func TestParsingCallPrecedence(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + foo(b)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingCurriedCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "foo(a)(b)(c)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingOptChainCall(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "foo?(bar)",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingArrayLiteral(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "[1, 2, 3]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingMember(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a.b?.c",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingMemberPrecedence(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + b.c",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingIncompleteMember(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + b.",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 1)
	snaps.MatchSnapshot(t, parser.errors[0])
}

func TestParsingIncompleteMemberOptChain(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + b?.",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 1)
	snaps.MatchSnapshot(t, parser.errors[0])
}

func TestParsingIndex(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a[base + offset]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingIndexPrecedence(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a + b[c]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingMultipleIndexes(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a[i][j]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}

func TestParsingOptChainIndex(t *testing.T) {
	source := Source{
		path:     "input.esc",
		Contents: "a?[base + offset]",
	}

	parser := NewParser(source)
	expr, _ := parser.parseExpr()

	snaps.MatchSnapshot(t, expr)
	assert.Len(t, parser.errors, 0)
}
