package ucs

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// This file renders the AST fragments the IR points at into the compact one-line
// forms the IR printer embeds. Those fragments are patterns, guard conditions, arm
// bodies, and the match target. The renderer is deliberately small and lossy rather
// than a second copy of internal/printer. Importing internal/printer would pull the
// parser into the solver's dependency graph, and what matters in an IR snapshot is
// the shape of the splits. A form this renderer does not spell out prints as its
// node kind in angle brackets, for example `<FuncExpr>`.

// exprString renders an expression on one line.
func exprString(e ast.Expr) string {
	switch e := e.(type) {
	case nil:
		return "<nil>"
	case *ast.IdentExpr:
		return e.Name
	case *ast.LiteralExpr:
		return litString(e.Lit)
	case *ast.MemberExpr:
		return exprString(e.Object) + "." + e.Prop.Name
	case *ast.IndexExpr:
		return exprString(e.Object) + "[" + exprString(e.Index) + "]"
	case *ast.CallExpr:
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = exprString(arg)
		}
		return exprString(e.Callee) + "(" + strings.Join(args, ", ") + ")"
	case *ast.TupleExpr:
		elems := make([]string, len(e.Elems))
		for i, elem := range e.Elems {
			elems[i] = exprString(elem)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *ast.BinaryExpr:
		return exprString(e.Left) + " " + string(e.Op) + " " + exprString(e.Right)
	case *ast.UnaryExpr:
		return unaryOpString(e.Op) + exprString(e.Arg)
	default:
		return nodeKind(e)
	}
}

// unaryOpString renders a unary operator's source spelling.
func unaryOpString(op ast.UnaryOp) string {
	switch op {
	case ast.UnaryPlus:
		return "+"
	case ast.UnaryMinus:
		return "-"
	case ast.LogicalNot:
		return "!"
	default:
		return "?"
	}
}

// litString renders a literal's source spelling.
func litString(lit ast.Lit) string {
	switch l := lit.(type) {
	case nil:
		return "<nil>"
	case *ast.BoolLit:
		return strconv.FormatBool(l.Value)
	case *ast.NumLit:
		return strconv.FormatFloat(l.Value, 'g', -1, 64)
	case *ast.StrLit:
		return strconv.Quote(l.Value)
	case *ast.RegexLit:
		return l.Value
	case *ast.BigIntLit:
		return l.Value.String() + "n"
	case *ast.NullLit:
		return "null"
	case *ast.UndefinedLit:
		return "undefined"
	default:
		return nodeKind(lit)
	}
}

// patString renders a pattern on one line, matching the source spelling closely
// enough that a core split's `pat …` reads like the arm the user wrote.
func patString(p ast.Pat) string {
	switch p := p.(type) {
	case nil:
		return "<nil>"
	case *ast.IdentPat:
		if p.Mutable {
			return "mut " + p.Name
		}
		return p.Name
	case *ast.WildcardPat:
		return "_"
	case *ast.LitPat:
		return litString(p.Lit)
	case *ast.TuplePat:
		elems := make([]string, len(p.Elems))
		for i, elem := range p.Elems {
			elems[i] = patString(elem)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *ast.ObjectPat:
		return objPatString(p)
	case *ast.ExtractorPat:
		args := make([]string, len(p.Args))
		for i, arg := range p.Args {
			args[i] = patString(arg)
		}
		return ast.QualIdentToString(p.Name) + "(" + strings.Join(args, ", ") + ")"
	case *ast.InstancePat:
		return ast.QualIdentToString(p.ClassName) + " " + objPatString(p.Object)
	case *ast.RestPat:
		return "..." + patString(p.Pattern)
	default:
		return nodeKind(p)
	}
}

// objPatString renders an object pattern's braces and elements.
func objPatString(p *ast.ObjectPat) string {
	if p == nil {
		return "{}"
	}
	elems := make([]string, 0, len(p.Elems))
	for _, elem := range p.Elems {
		switch e := elem.(type) {
		case *ast.ObjKeyValuePat:
			elems = append(elems, e.Key.Name+": "+patString(e.Value))
		case *ast.ObjShorthandPat:
			if e.Mutable {
				elems = append(elems, "mut "+e.Key.Name)
			} else {
				elems = append(elems, e.Key.Name)
			}
		case *ast.ObjRestPat:
			elems = append(elems, "..."+patString(e.Pattern))
		default:
			elems = append(elems, nodeKind(elem))
		}
	}
	return "{" + strings.Join(elems, ", ") + "}"
}

// bodyString renders an arm body on one line. A body written as a bare expression
// renders as that expression; a block renders its statements inside braces, so two
// blocks that differ stay distinguishable in a snapshot.
func bodyString(b ast.BlockOrExpr) string {
	if b.Expr != nil {
		return exprString(b.Expr)
	}
	if b.Block != nil {
		stmts := make([]string, len(b.Block.Stmts))
		for i, stmt := range b.Block.Stmts {
			stmts[i] = stmtString(stmt)
		}
		return "{ " + strings.Join(stmts, "; ") + " }"
	}
	return "<empty>"
}

// stmtString renders a statement on one line.
func stmtString(s ast.Stmt) string {
	switch s := s.(type) {
	case nil:
		return "<nil>"
	case *ast.ExprStmt:
		return exprString(s.Expr)
	case *ast.ReturnStmt:
		if s.Expr == nil {
			return "return"
		}
		return "return " + exprString(s.Expr)
	default:
		return nodeKind(s)
	}
}

// nodeKind renders a value's concrete type as `<TypeName>`, the placeholder every
// renderer here falls back to for a form it does not spell out. A `*ast.FuncExpr`
// renders as `<FuncExpr>`. The pointer marker and the package qualifier are dropped,
// so a ucs type renders the same way an ast type does.
func nodeKind(n any) string {
	name := strings.TrimPrefix(fmt.Sprintf("%T", n), "*")
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	return "<" + name + ">"
}
