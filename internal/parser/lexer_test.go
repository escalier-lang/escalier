package parser

import (
	"os"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

func TestMain(m *testing.M) {
	v := m.Run()
	snaps.Clean(m) // remove unused snapshots
	os.Exit(v)
}

func TestLexingKeywords(t *testing.T) {
	source := Source{
		Path:     "input.esc",
		Contents: "fn var val",
	}

	lexer := NewLexer(source)

	snaps.MatchSnapshot(t, lexer.Lex())
}

func TestLexingOperators(t *testing.T) {
	source := Source{
		Path:     "input.esc",
		Contents: "+ - * / =",
	}

	lexer := NewLexer(source)

	snaps.MatchSnapshot(t, lexer.Lex())
}

func TestLexingIdentifiers(t *testing.T) {
	source := Source{
		Path:     "input.esc",
		Contents: "foo\nbar",
	}

	lexer := NewLexer(source)

	snaps.MatchSnapshot(t, lexer.Lex())
}

func TestLexingLiterals(t *testing.T) {
	source := Source{
		Path:     "input.esc",
		Contents: "\"hello\"",
	}

	lexer := NewLexer(source)

	snaps.MatchSnapshot(t, lexer.Lex())
}

func TestLexingParens(t *testing.T) {
	source := Source{
		Path:     "input.esc",
		Contents: "a * (b + c)",
	}

	lexer := NewLexer(source)

	snaps.MatchSnapshot(t, lexer.Lex())
}
