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
		return groupedExprString(e.Object) + "." + e.Prop.Name
	case *ast.IndexExpr:
		return groupedExprString(e.Object) + "[" + exprString(e.Index) + "]"
	case *ast.CallExpr:
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = exprString(arg)
		}
		return groupedExprString(e.Callee) + "(" + strings.Join(args, ", ") + ")"
	case *ast.TupleExpr:
		elems := make([]string, len(e.Elems))
		for i, elem := range e.Elems {
			elems[i] = exprString(elem)
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *ast.BinaryExpr:
		return groupedExprString(e.Left) + " " + string(e.Op) + " " + groupedExprString(e.Right)
	case *ast.UnaryExpr:
		return unaryOpString(e.Op) + groupedExprString(e.Arg)
	default:
		return nodeKind(e)
	}
}

// groupedExprString renders a subexpression whose grouping the surrounding syntax
// does not already make clear, parenthesizing it when it is an operator expression.
// The renderer prints no precedence-aware parentheses of its own, so without this a
// nested operator reads as though it sat at the outer level.
//
// Two guards that test different things would otherwise share one rendering, since
// `!(x > y)` and `(!x) > y` both flatten to `!x > y`. A receiver is worse than
// ambiguous: `(a + b).c` flattens to `a + b.c`, which names a different expression
// than the one the IR holds.
//
// Callers pass only the positions that need it. An argument list, an index, and a
// tuple element are already delimited by their brackets, so those stay ungrouped.
func groupedExprString(e ast.Expr) string {
	switch e.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr:
		return "(" + exprString(e) + ")"
	default:
		return exprString(e)
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
		return leafPatString(p.Mutable, p.Name, p.TypeAnn, p.Default)
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
			elems = append(elems, leafPatString(e.Mutable, e.Key.Name, e.TypeAnn, e.Default))
		case *ast.ObjRestPat:
			elems = append(elems, "..."+patString(e.Pattern))
		default:
			elems = append(elems, nodeKind(elem))
		}
	}
	return "{" + strings.Join(elems, ", ") + "}"
}

// leafPatString renders a binding leaf, which is either an `IdentPat` or the
// shorthand element of an object pattern. Both can carry a `mut` prefix, a type
// annotation, and a default, and all three are rendered. The default is the one that
// changes matching. It makes the field optional, so `{x = 0}` and `{x}` match
// different values and must not share a snapshot.
func leafPatString(mutable bool, name string, typeAnn ast.TypeAnn, dflt ast.Expr) string {
	var b strings.Builder
	if mutable {
		b.WriteString("mut ")
	}
	b.WriteString(name)
	if typeAnn != nil {
		b.WriteString(": " + typeAnnString(typeAnn))
	}
	if dflt != nil {
		b.WriteString(" = " + exprString(dflt))
	}
	return b.String()
}

// typeAnnString renders a type annotation on one line. It spells out the forms that
// show up on a pattern leaf and falls back to the node kind for the rest, which is
// enough to keep two arms that differ only in their annotation distinguishable.
func typeAnnString(t ast.TypeAnn) string {
	switch t := t.(type) {
	case nil:
		return "<nil>"
	case *ast.NumberTypeAnn:
		return "number"
	case *ast.StringTypeAnn:
		return "string"
	case *ast.BooleanTypeAnn:
		return "boolean"
	case *ast.BigintTypeAnn:
		return "bigint"
	case *ast.SymbolTypeAnn:
		return "symbol"
	case *ast.AnyTypeAnn:
		return "any"
	case *ast.UnknownTypeAnn:
		return "unknown"
	case *ast.NeverTypeAnn:
		return "never"
	case *ast.WildcardTypeAnn:
		return "_"
	case *ast.LitTypeAnn:
		return litString(t.Lit)
	case *ast.TypeRefTypeAnn:
		name := ast.QualIdentToString(t.Name)
		if len(t.TypeArgs) == 0 {
			return name
		}
		args := make([]string, len(t.TypeArgs))
		for i, arg := range t.TypeArgs {
			args[i] = typeAnnString(arg)
		}
		return name + "<" + strings.Join(args, ", ") + ">"
	case *ast.TupleTypeAnn:
		elems := make([]string, 0, len(t.Elems)+1)
		for _, elem := range t.Elems {
			elems = append(elems, typeAnnString(elem))
		}
		if t.Inexact {
			elems = append(elems, "...")
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *ast.UnionTypeAnn:
		if t.Inexact {
			return joinTypeAnns(t.Types, " | ") + " | ..."
		}
		return joinTypeAnns(t.Types, " | ")
	case *ast.IntersectionTypeAnn:
		parts := make([]string, len(t.Types))
		for i, member := range t.Types {
			parts[i] = groupedTypeAnnString(member)
		}
		return strings.Join(parts, " & ")
	default:
		return nodeKind(t)
	}
}

// groupedTypeAnnString renders a member of an intersection, parenthesizing a union.
// `&` binds tighter than `|` in the surface syntax, so a bare union member would
// reassociate: `(number | string) & boolean` and `number | (string & boolean)` both
// flatten to `number | string & boolean`, which names the second of the two. An
// intersection nested in a union needs no parentheses, because the tighter operator
// already groups the way the tree does.
func groupedTypeAnnString(t ast.TypeAnn) string {
	if _, ok := t.(*ast.UnionTypeAnn); ok {
		return "(" + typeAnnString(t) + ")"
	}
	return typeAnnString(t)
}

func joinTypeAnns(anns []ast.TypeAnn, sep string) string {
	parts := make([]string, len(anns))
	for i, ann := range anns {
		parts[i] = typeAnnString(ann)
	}
	return strings.Join(parts, sep)
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
