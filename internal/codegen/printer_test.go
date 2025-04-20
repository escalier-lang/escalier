package codegen

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestPrintExpr(t *testing.T) {
	sum := &BinaryExpr{
		Left: &NumExpr{
			Value:  0.1,
			span:   nil,
			source: nil,
		},
		Op: Plus,
		Right: &NumExpr{
			Value:  0.2,
			span:   nil,
			source: nil,
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

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	m1, _ := p.ParseScript()
	builder := &Builder{
		tempId: 0,
	}
	m2 := builder.BuildScript(m1)

	printer := NewPrinter()
	printer.PrintModule(m2)

	snaps.MatchSnapshot(t, printer.Output)
	if printer.location.Line != 11 {
		t.Errorf("got %d, want 11", printer.location.Line)
	}
}
