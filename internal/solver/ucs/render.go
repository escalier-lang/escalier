package ucs

import (
	"fmt"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
)

// This file renders the AST fragments the IR points at: patterns, guard conditions, arm
// bodies, and the match target. The source printer produces every one of those forms, so
// the rendering here is a thin wrapper over it. Two things the printer does not promise
// on its own are added.
//
//  1. Compact output. A snapshot embeds a fragment inside one line of the IR's own
//     nesting, so a rendering that spans lines and carries its own indentation would
//     collide with it. printer.CompactOptions keeps each fragment on one line.
//  2. Totality. printer.Print returns an error rather than a string for a node it cannot
//     dispatch on. A nil fragment reaches it as a nil interface. A lowering bug should
//     surface as printable IR rather than a crash or an error return, so both cases fall
//     back to a placeholder in angle brackets.

// exprString renders an expression on one line.
func exprString(e ast.Expr) string { return renderNode(e) }

// patString renders a pattern on one line.
func patString(p ast.Pat) string { return renderNode(p) }

// litString renders a literal on one line. A literal is not an ast.Node, so it reaches the
// printer wrapped in the expression form that holds one.
func litString(lit ast.Lit) string {
	if lit == nil {
		return "<nil>"
	}
	return renderNode(ast.NewLitExpr(lit))
}

// bodyString renders an arm body on one line. An arm carries either a single expression or
// a block, and a lowering that produced neither renders as `<empty>`.
func bodyString(b ast.BlockOrExpr) string {
	if b.Expr != nil {
		return renderNode(b.Expr)
	}
	if b.Block == nil {
		return "<empty>"
	}
	out, err := printer.PrintBlock(b.Block, printer.CompactOptions())
	if err != nil {
		return nodeKind(b.Block)
	}
	return out
}

// renderNode prints a node on one line. A nil fragment renders `<nil>`, and a node the
// printer cannot dispatch on renders as its kind in angle brackets.
func renderNode(n ast.Node) string {
	if n == nil {
		return "<nil>"
	}
	out, err := printer.Print(n, printer.CompactOptions())
	if err != nil {
		return nodeKind(n)
	}
	return out
}

// nodeKind names a node by its Go type, with the pointer and package qualifier stripped,
// so a `*ast.FuncExpr` reads as `<FuncExpr>`. It stands in wherever a renderer has nothing
// better to print, and it names IR nodes as well as AST ones.
func nodeKind(n any) string {
	name := strings.TrimPrefix(fmt.Sprintf("%T", n), "*")
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	return "<" + name + ">"
}
