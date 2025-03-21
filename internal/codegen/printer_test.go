package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestPrintExpr(t *testing.T) {
	sum := &Expr{
		Kind: &EBinary{
			Left: &Expr{
				Kind:   &ENumber{Value: 0.1},
				span:   nil,
				source: nil,
			},
			Op: Plus,
			Right: &Expr{
				Kind:   &ENumber{Value: 0.2},
				span:   nil,
				source: nil,
			},
		},
		span:   nil,
		source: nil,
	}

	printer := NewPrinter()
	printer.PrintExpr(sum)

	want := "0.1 + 0.2"
	if got := printer.Output; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	snaps.MatchSnapshot(t, sum)
}

func TestPrintModule(t *testing.T) {
	source := parser.Source{
		Path: "input.esc",
		Contents: `fn add(a, b) { return a + b }
fn sub(a, b) { return a - b }`,
	}
	p := parser.NewParser(source)
	m1 := p.ParseModule()
	m2 := TransformModule(m1)

	printer := NewPrinter()
	printer.PrintModule(m2)

	snaps.MatchSnapshot(t, printer.Output)
	if printer.location.Line != 7 {
		t.Errorf("got %d, want 7", printer.location.Line)
	}
}
