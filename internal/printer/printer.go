package printer

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// Options contains configuration for the printer
type Options struct {
	Indent        string // e.g., "  " or "\t"
	MaxLineLength int    // Maximum line length before breaking
}

// DefaultOptions returns default printer options
func DefaultOptions() Options {
	return Options{
		Indent:        "    ", // 4 spaces
		MaxLineLength: 80,
	}
}

// Printer handles pretty-printing of AST nodes
type Printer struct {
	writer      io.Writer
	opts        Options
	indentLevel int
	needIndent  bool
	lastChar    byte
}

// NewPrinter creates a new printer with the given options
func NewPrinter(writer io.Writer, opts Options) *Printer {
	return &Printer{
		writer:      writer,
		opts:        opts,
		indentLevel: 0,
		needIndent:  true,
		lastChar:    0,
	}
}

// Helper methods for output management

func (p *Printer) writeString(s string) {
	if p.needIndent && len(s) > 0 && s[0] != '\n' {
		indent := strings.Repeat(p.opts.Indent, p.indentLevel)
		io.WriteString(p.writer, indent)
		p.needIndent = false
	}
	io.WriteString(p.writer, s)
	if len(s) > 0 {
		p.lastChar = s[len(s)-1]
	}
}

func (p *Printer) newline() {
	p.writeString("\n")
	p.needIndent = true
}

func (p *Printer) space() {
	if p.lastChar != ' ' && p.lastChar != '\n' && p.lastChar != 0 {
		p.writeString(" ")
	}
}

func (p *Printer) indent() {
	p.indentLevel++
}

func (p *Printer) dedent() {
	if p.indentLevel > 0 {
		p.indentLevel--
	}
}

// Print methods for different AST node types

// PrintScript prints a Script node
func (p *Printer) PrintScript(script *ast.Script) error {
	for i, stmt := range script.Stmts {
		p.printStmt(stmt)
		if i < len(script.Stmts)-1 {
			p.newline()
		}
	}
	return nil
}

// PrintModule prints a Module node
func (p *Printer) PrintModule(module *ast.Module) error {
	// Iterate over namespaces
	module.Namespaces.Scan(func(key string, ns *ast.Namespace) bool {
		if key != "" {
			p.writeString("namespace ")
			p.writeString(key)
			p.writeString(" {")
			p.newline()
			p.indent()
		}

		for i, decl := range ns.Decls {
			p.printDecl(decl)
			if i < len(ns.Decls)-1 {
				p.newline()
			}
		}

		if key != "" {
			p.dedent()
			p.writeString("}")
			p.newline()
		}
		return true
	})
	return nil
}

// Statement printing

func (p *Printer) printStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		p.printExpr(s.Expr)
	case *ast.DeclStmt:
		p.printDecl(s.Decl)
	case *ast.ReturnStmt:
		p.writeString("return")
		if s.Expr != nil {
			p.space()
			p.printExpr(s.Expr)
		}
	default:
		p.writeString("/* unknown statement */")
	}
}

// Declaration printing

func (p *Printer) printDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		p.printVarDecl(d)
	case *ast.FuncDecl:
		p.printFuncDecl(d)
	case *ast.TypeDecl:
		p.printTypeDecl(d)
	case *ast.InterfaceDecl:
		p.printInterfaceDecl(d)
	case *ast.EnumDecl:
		p.printEnumDecl(d)
	default:
		p.writeString("/* unknown declaration */")
	}
}

func (p *Printer) printVarDecl(decl *ast.VarDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	if decl.Kind == ast.ValKind {
		p.writeString("val ")
	} else {
		p.writeString("var ")
	}

	p.printPattern(decl.Pattern)

	if decl.TypeAnn != nil {
		p.writeString(": ")
		p.printTypeAnn(decl.TypeAnn)
	}

	if decl.Init != nil {
		p.writeString(" = ")
		p.printExpr(decl.Init)
	}
}

func (p *Printer) printFuncDecl(decl *ast.FuncDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	if decl.Async {
		p.writeString("async ")
	}
	p.writeString("fn ")
	p.writeString(decl.Name.Name)

	p.printFuncSig(&decl.FuncSig)

	if decl.Body != nil {
		p.space()
		p.printBlock(decl.Body)
	}
}

func (p *Printer) printTypeDecl(decl *ast.TypeDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("type ")
	p.writeString(decl.Name.Name)

	if len(decl.TypeParams) > 0 {
		p.printTypeParams(decl.TypeParams)
	}

	p.writeString(" = ")
	p.printTypeAnn(decl.TypeAnn)
}

func (p *Printer) printInterfaceDecl(decl *ast.InterfaceDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("interface ")
	p.writeString(decl.Name.Name)

	if len(decl.TypeParams) > 0 {
		p.printTypeParams(decl.TypeParams)
	}

	p.space()
	p.printTypeAnn(decl.TypeAnn)
}

func (p *Printer) printEnumDecl(decl *ast.EnumDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("enum ")
	p.writeString(decl.Name.Name)

	if len(decl.TypeParams) > 0 {
		p.printTypeParams(decl.TypeParams)
	}

	p.writeString(" {")
	p.newline()
	p.indent()

	for i, elem := range decl.Elems {
		switch e := elem.(type) {
		case *ast.EnumVariant:
			p.writeString(e.Name.Name)
			if len(e.Params) > 0 {
				p.writeString("(")
				for j, param := range e.Params {
					p.printPattern(param.Pattern)
					if param.TypeAnn != nil {
						p.writeString(": ")
						p.printTypeAnn(param.TypeAnn)
					}
					if j < len(e.Params)-1 {
						p.writeString(", ")
					}
				}
				p.writeString(")")
			}
		case *ast.EnumSpread:
			p.writeString("...")
			p.writeString(e.Arg.Name)
		}

		if i < len(decl.Elems)-1 {
			p.writeString(",")
			p.newline()
		}
	}

	p.newline()
	p.dedent()
	p.writeString("}")
}

// Expression printing

func (p *Printer) printExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.IgnoreExpr:
		p.writeString("_")
	case *ast.EmptyExpr:
		// Empty expression - don't print anything
	case *ast.BinaryExpr:
		p.printBinaryExpr(e)
	case *ast.UnaryExpr:
		p.printUnaryExpr(e)
	case *ast.LiteralExpr:
		p.printLiteral(e.Lit)
	case *ast.IdentExpr:
		p.writeString(e.Name)
	case *ast.FuncExpr:
		p.printFuncExpr(e)
	case *ast.CallExpr:
		p.printCallExpr(e)
	case *ast.IndexExpr:
		p.printIndexExpr(e)
	case *ast.MemberExpr:
		p.printMemberExpr(e)
	case *ast.TupleExpr:
		p.printTupleExpr(e)
	case *ast.ObjectExpr:
		p.printObjectExpr(e)
	case *ast.IfElseExpr:
		p.printIfElseExpr(e)
	case *ast.IfLetExpr:
		p.printIfLetExpr(e)
	case *ast.MatchExpr:
		p.printMatchExpr(e)
	case *ast.AssignExpr:
		p.printAssignExpr(e)
	case *ast.TryCatchExpr:
		p.printTryCatchExpr(e)
	case *ast.DoExpr:
		p.printDoExpr(e)
	case *ast.AwaitExpr:
		p.printAwaitExpr(e)
	case *ast.ThrowExpr:
		p.printThrowExpr(e)
	case *ast.TemplateLitExpr:
		p.printTemplateLitExpr(e)
	case *ast.TaggedTemplateLitExpr:
		p.printTaggedTemplateLitExpr(e)
	case *ast.JSXElementExpr:
		p.writeString("/* JSX element */")
	case *ast.JSXFragmentExpr:
		p.writeString("/* JSX fragment */")
	case *ast.TypeCastExpr:
		p.printTypeCastExpr(e)
	default:
		p.writeString("/* unknown expression */")
	}
}

func (p *Printer) printBinaryExpr(expr *ast.BinaryExpr) {
	needsParens := p.needsParens(expr.Left)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Left)
	if needsParens {
		p.writeString(")")
	}

	p.space()
	p.writeString(string(expr.Op))
	p.space()

	needsParens = p.needsParens(expr.Right)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Right)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printUnaryExpr(expr *ast.UnaryExpr) {
	switch expr.Op {
	case ast.UnaryPlus:
		p.writeString("+")
	case ast.UnaryMinus:
		p.writeString("-")
	case ast.LogicalNot:
		p.writeString("!")
	}

	needsParens := p.needsParens(expr.Arg)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Arg)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printCallExpr(expr *ast.CallExpr) {
	if expr.OptChain {
		p.printExpr(expr.Callee)
		p.writeString("?(")
	} else {
		p.printExpr(expr.Callee)
		p.writeString("(")
	}

	for i, arg := range expr.Args {
		p.printExpr(arg)
		if i < len(expr.Args)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")
}

func (p *Printer) printIndexExpr(expr *ast.IndexExpr) {
	p.printExpr(expr.Object)
	if expr.OptChain {
		p.writeString("?[")
	} else {
		p.writeString("[")
	}
	p.printExpr(expr.Index)
	p.writeString("]")
}

func (p *Printer) printMemberExpr(expr *ast.MemberExpr) {
	p.printExpr(expr.Object)
	if expr.OptChain {
		p.writeString("?.")
	} else {
		p.writeString(".")
	}
	p.writeString(expr.Prop.Name)
}

func (p *Printer) printTupleExpr(expr *ast.TupleExpr) {
	p.writeString("[")
	for i, elem := range expr.Elems {
		p.printExpr(elem)
		if i < len(expr.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("]")
}

func (p *Printer) printObjectExpr(expr *ast.ObjectExpr) {
	if len(expr.Elems) == 0 {
		p.writeString("{}")
		return
	}

	p.writeString("{")
	p.newline()
	p.indent()

	for i, elem := range expr.Elems {
		p.printObjExprElem(elem)
		if i < len(expr.Elems)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printObjExprElem(elem ast.ObjExprElem) {
	switch e := elem.(type) {
	case *ast.CallableExpr:
		p.printFuncExpr(&e.Fn)
	case *ast.ConstructorExpr:
		p.writeString("new")
		p.printFuncExpr(&e.Fn)
	case *ast.MethodExpr:
		p.printObjKey(e.Name)
		if e.MutSelf != nil {
			p.writeString("(")
			if *e.MutSelf {
				p.writeString("mut self")
			} else {
				p.writeString("self")
			}
			if len(e.Fn.Params) > 0 {
				p.writeString(", ")
			}
		} else {
			p.writeString("(")
		}
		for i, param := range e.Fn.Params {
			p.printPattern(param.Pattern)
			if param.TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(param.TypeAnn)
			}
			if i < len(e.Fn.Params)-1 {
				p.writeString(", ")
			}
		}
		p.writeString(")")
		if e.Fn.Return != nil {
			p.writeString(": ")
			p.printTypeAnn(e.Fn.Return)
		}
		p.space()
		p.printBlock(e.Fn.Body)
	case *ast.GetterExpr:
		p.writeString("get ")
		p.printObjKey(e.Name)
		p.writeString("()")
		if e.Fn.Return != nil {
			p.writeString(": ")
			p.printTypeAnn(e.Fn.Return)
		}
		p.space()
		p.printBlock(e.Fn.Body)
	case *ast.SetterExpr:
		p.writeString("set ")
		p.printObjKey(e.Name)
		p.writeString("(")
		if len(e.Fn.Params) > 0 {
			p.printPattern(e.Fn.Params[0].Pattern)
			if e.Fn.Params[0].TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(e.Fn.Params[0].TypeAnn)
			}
		}
		p.writeString(")")
		p.space()
		p.printBlock(e.Fn.Body)
	case *ast.PropertyExpr:
		if e.Readonly {
			p.writeString("readonly ")
		}
		p.printObjKey(e.Name)
		if e.Optional {
			p.writeString("?")
		}
		if e.Value != nil {
			p.writeString(": ")
			p.printExpr(e.Value)
		}
	case *ast.RestSpreadExpr:
		p.writeString("...")
		p.printExpr(e.Value)
	}
}

func (p *Printer) printIfElseExpr(expr *ast.IfElseExpr) {
	p.writeString("if ")
	p.printExpr(expr.Cond)
	p.space()
	p.printBlock(&expr.Cons)

	if expr.Alt != nil {
		p.space()
		p.writeString("else ")
		if expr.Alt.Block != nil {
			p.printBlock(expr.Alt.Block)
		} else if expr.Alt.Expr != nil {
			// Check if it's another if expression for "else if"
			if _, ok := expr.Alt.Expr.(*ast.IfElseExpr); ok {
				p.printExpr(expr.Alt.Expr)
			} else {
				p.printExpr(expr.Alt.Expr)
			}
		}
	}
}

func (p *Printer) printIfLetExpr(expr *ast.IfLetExpr) {
	p.writeString("if let ")
	p.printPattern(expr.Pattern)
	p.writeString(" = ")
	p.printExpr(expr.Target)
	p.space()
	p.printBlock(&expr.Cons)

	if expr.Alt != nil {
		p.space()
		p.writeString("else ")
		if expr.Alt.Block != nil {
			p.printBlock(expr.Alt.Block)
		} else if expr.Alt.Expr != nil {
			p.printExpr(expr.Alt.Expr)
		}
	}
}

func (p *Printer) printMatchExpr(expr *ast.MatchExpr) {
	p.writeString("match ")
	p.printExpr(expr.Target)
	p.writeString(" {")
	p.newline()
	p.indent()

	for i, c := range expr.Cases {
		p.printPattern(c.Pattern)
		if c.Guard != nil {
			p.writeString(" if ")
			p.printExpr(c.Guard)
		}
		p.writeString(" => ")
		if c.Body.Block != nil {
			p.printBlock(c.Body.Block)
		} else if c.Body.Expr != nil {
			p.printExpr(c.Body.Expr)
		}
		if i < len(expr.Cases)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printAssignExpr(expr *ast.AssignExpr) {
	p.printExpr(expr.Left)
	p.writeString(" = ")
	p.printExpr(expr.Right)
}

func (p *Printer) printTryCatchExpr(expr *ast.TryCatchExpr) {
	p.writeString("try ")
	p.printBlock(&expr.Try)

	if len(expr.Catch) > 0 {
		p.space()
		p.writeString("catch {")
		p.newline()
		p.indent()

		for i, c := range expr.Catch {
			p.printPattern(c.Pattern)
			if c.Guard != nil {
				p.writeString(" if ")
				p.printExpr(c.Guard)
			}
			p.writeString(" => ")
			if c.Body.Block != nil {
				p.printBlock(c.Body.Block)
			} else if c.Body.Expr != nil {
				p.printExpr(c.Body.Expr)
			}
			if i < len(expr.Catch)-1 {
				p.writeString(",")
			}
			p.newline()
		}

		p.dedent()
		p.writeString("}")
	}
}

func (p *Printer) printDoExpr(expr *ast.DoExpr) {
	p.writeString("do ")
	p.printBlock(&expr.Body)
}

func (p *Printer) printAwaitExpr(expr *ast.AwaitExpr) {
	p.writeString("await ")
	needsParens := p.needsParens(expr.Arg)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Arg)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printThrowExpr(expr *ast.ThrowExpr) {
	p.writeString("throw ")
	p.printExpr(expr.Arg)
}

func (p *Printer) printTemplateLitExpr(expr *ast.TemplateLitExpr) {
	p.writeString("`")
	for i, quasi := range expr.Quasis {
		p.writeString(quasi.Value)
		if i < len(expr.Exprs) {
			p.writeString("${")
			p.printExpr(expr.Exprs[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printTaggedTemplateLitExpr(expr *ast.TaggedTemplateLitExpr) {
	p.printExpr(expr.Tag)
	p.writeString("`")
	for i, quasi := range expr.Quasis {
		p.writeString(quasi.Value)
		if i < len(expr.Exprs) {
			p.writeString("${")
			p.printExpr(expr.Exprs[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printTypeCastExpr(expr *ast.TypeCastExpr) {
	p.printExpr(expr.Expr)
	p.writeString(":")
	// Wrap union/intersection types in parentheses for clarity
	needsParens := false
	switch expr.TypeAnn.(type) {
	case *ast.UnionTypeAnn, *ast.IntersectionTypeAnn:
		needsParens = true
	}
	if needsParens {
		p.writeString("(")
	}
	p.printTypeAnn(expr.TypeAnn)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printFuncExpr(expr *ast.FuncExpr) {
	if expr.Async {
		p.writeString("async ")
	}
	p.writeString("fn ")
	p.printFuncSig(&expr.FuncSig)
	p.space()
	p.printBlock(expr.Body)
}

func (p *Printer) printFuncSig(sig *ast.FuncSig) {
	if len(sig.TypeParams) > 0 {
		p.printTypeParams(sig.TypeParams)
	}

	p.writeString("(")
	for i, param := range sig.Params {
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
		if i < len(sig.Params)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")

	if sig.Return != nil {
		p.writeString(" -> ")
		p.printTypeAnn(sig.Return)
	}

	if sig.Throws != nil {
		p.writeString(" throws ")
		p.printTypeAnn(sig.Throws)
	}
}

func (p *Printer) printBlock(block *ast.Block) {
	p.writeString("{")
	if len(block.Stmts) > 0 {
		p.newline()
		p.indent()

		for _, stmt := range block.Stmts {
			p.printStmt(stmt)
			p.newline()
		}

		p.dedent()
	}
	p.writeString("}")
}

// Pattern printing

func (p *Printer) printPattern(pat ast.Pat) {
	switch pt := pat.(type) {
	case *ast.IdentPat:
		p.writeString(pt.Name)
		if pt.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(pt.TypeAnn)
		}
		if pt.Default != nil {
			p.writeString(" = ")
			p.printExpr(pt.Default)
		}
	case *ast.ObjectPat:
		p.printObjectPattern(pt)
	case *ast.TuplePat:
		p.printTuplePattern(pt)
	case *ast.ExtractorPat:
		p.printExtractorPattern(pt)
	case *ast.InstancePat:
		p.printInstancePattern(pt)
	case *ast.RestPat:
		p.writeString("...")
		p.printPattern(pt.Pattern)
	case *ast.LitPat:
		p.printLiteral(pt.Lit)
	case *ast.WildcardPat:
		p.writeString("_")
	default:
		p.writeString("/* unknown pattern */")
	}
}

func (p *Printer) printObjectPattern(pat *ast.ObjectPat) {
	p.writeString("{")
	for i, elem := range pat.Elems {
		switch e := elem.(type) {
		case *ast.ObjKeyValuePat:
			p.writeString(e.Key.Name)
			p.writeString(": ")
			p.printPattern(e.Value)
		case *ast.ObjShorthandPat:
			p.writeString(e.Key.Name)
			if e.TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(e.TypeAnn)
			}
			if e.Default != nil {
				p.writeString(" = ")
				p.printExpr(e.Default)
			}
		case *ast.ObjRestPat:
			p.writeString("...")
			p.printPattern(e.Pattern)
		}
		if i < len(pat.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("}")
}

func (p *Printer) printTuplePattern(pat *ast.TuplePat) {
	p.writeString("[")
	for i, elem := range pat.Elems {
		p.printPattern(elem)
		if i < len(pat.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("]")
}

func (p *Printer) printExtractorPattern(pat *ast.ExtractorPat) {
	p.printQualIdent(pat.Name)
	p.writeString("(")
	for i, arg := range pat.Args {
		p.printPattern(arg)
		if i < len(pat.Args)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")
}

func (p *Printer) printInstancePattern(pat *ast.InstancePat) {
	p.printQualIdent(pat.ClassName)
	p.space()
	p.printPattern(pat.Object)
}

// Type annotation printing

func (p *Printer) printTypeAnn(typ ast.TypeAnn) {
	switch t := typ.(type) {
	case *ast.LitTypeAnn:
		p.printLiteral(t.Lit)
	case *ast.NumberTypeAnn:
		p.writeString("number")
	case *ast.StringTypeAnn:
		p.writeString("string")
	case *ast.BooleanTypeAnn:
		p.writeString("boolean")
	case *ast.SymbolTypeAnn:
		p.writeString("symbol")
	case *ast.UniqueSymbolTypeAnn:
		p.writeString("unique symbol")
	case *ast.BigintTypeAnn:
		p.writeString("bigint")
	case *ast.AnyTypeAnn:
		p.writeString("any")
	case *ast.UnknownTypeAnn:
		p.writeString("unknown")
	case *ast.NeverTypeAnn:
		p.writeString("never")
	case *ast.ObjectTypeAnn:
		p.printObjectTypeAnn(t)
	case *ast.TupleTypeAnn:
		p.printTupleTypeAnn(t)
	case *ast.UnionTypeAnn:
		p.printUnionTypeAnn(t)
	case *ast.IntersectionTypeAnn:
		p.printIntersectionTypeAnn(t)
	case *ast.TypeRefTypeAnn:
		p.printTypeRefTypeAnn(t)
	case *ast.FuncTypeAnn:
		p.printFuncTypeAnn(t)
	case *ast.KeyOfTypeAnn:
		p.writeString("keyof ")
		p.printTypeAnn(t.Type)
	case *ast.TypeOfTypeAnn:
		p.writeString("typeof ")
		p.printQualIdent(t.Value)
	case *ast.IndexTypeAnn:
		p.printTypeAnn(t.Target)
		p.writeString("[")
		p.printTypeAnn(t.Index)
		p.writeString("]")
	case *ast.CondTypeAnn:
		p.writeString("if ")
		p.printTypeAnn(t.Check)
		p.writeString(" : ")
		p.printTypeAnn(t.Extends)
		p.writeString(" { ")
		p.printTypeAnn(t.Then)
		p.writeString(" } else { ")
		p.printTypeAnn(t.Else)
		p.writeString(" }")
	case *ast.InferTypeAnn:
		p.writeString("infer ")
		p.writeString(t.Name)
	case *ast.WildcardTypeAnn:
		p.writeString("_")
	case *ast.TemplateLitTypeAnn:
		p.printTemplateLitTypeAnn(t)
	case *ast.IntrinsicTypeAnn:
		p.writeString("intrinsic")
	case *ast.ImportTypeAnn:
		p.writeString("import(\"")
		p.writeString(t.Source)
		p.writeString("\")")
		if t.Qualifier != nil {
			p.writeString(".")
			p.printQualIdent(t.Qualifier)
		}
		if len(t.TypeArgs) > 0 {
			p.writeString("<")
			for i, arg := range t.TypeArgs {
				p.printTypeAnn(arg)
				if i < len(t.TypeArgs)-1 {
					p.writeString(", ")
				}
			}
			p.writeString(">")
		}
	case *ast.MatchTypeAnn:
		p.printMatchTypeAnn(t)
	case *ast.MutableTypeAnn:
		p.writeString("mut ")
		p.printTypeAnn(t.Target)
	case *ast.EmptyTypeAnn:
		// Empty type annotation
	case *ast.RestSpreadTypeAnn:
		p.writeString("...")
		p.printTypeAnn(t.Value)
	default:
		p.writeString("/* unknown type */")
	}
}

func (p *Printer) printObjectTypeAnn(typ *ast.ObjectTypeAnn) {
	if len(typ.Elems) == 0 {
		p.writeString("{}")
		return
	}

	p.writeString("{")
	p.newline()
	p.indent()

	for i, elem := range typ.Elems {
		p.printObjTypeAnnElem(elem)
		if i < len(typ.Elems)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printObjTypeAnnElem(elem ast.ObjTypeAnnElem) {
	switch e := elem.(type) {
	case *ast.CallableTypeAnn:
		p.printFuncTypeAnn(e.Fn)
	case *ast.ConstructorTypeAnn:
		p.writeString("new")
		p.printFuncTypeAnn(e.Fn)
	case *ast.MethodTypeAnn:
		p.printObjKey(e.Name)
		if len(e.Fn.TypeParams) > 0 {
			p.printTypeParams(e.Fn.TypeParams)
		}
		p.writeString("(")
		for i, param := range e.Fn.Params {
			p.printPattern(param.Pattern)
			if param.Optional {
				p.writeString("?")
			}
			if param.TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(param.TypeAnn)
			}
			if i < len(e.Fn.Params)-1 {
				p.writeString(", ")
			}
		}
		p.writeString(")")
		p.writeString(" -> ")
		p.printTypeAnn(e.Fn.Return)
		if e.Fn.Throws != nil {
			p.writeString(" throws ")
			p.printTypeAnn(e.Fn.Throws)
		}
	case *ast.GetterTypeAnn:
		p.writeString("get ")
		p.printObjKey(e.Name)
		p.writeString("(): ")
		p.printTypeAnn(e.Fn.Return)
	case *ast.SetterTypeAnn:
		p.writeString("set ")
		p.printObjKey(e.Name)
		p.writeString("(")
		if len(e.Fn.Params) > 0 {
			p.printPattern(e.Fn.Params[0].Pattern)
			if e.Fn.Params[0].TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(e.Fn.Params[0].TypeAnn)
			}
		}
		p.writeString(")")
		// Setters require -> void in Escalier syntax
		p.writeString(" -> ")
		p.printTypeAnn(e.Fn.Return)
	case *ast.PropertyTypeAnn:
		if e.Readonly {
			p.writeString("readonly ")
		}
		p.printObjKey(e.Name)
		if e.Optional {
			p.writeString("?")
		}
		p.writeString(": ")
		p.printTypeAnn(e.Value)
	case *ast.MappedTypeAnn:
		// Print readonly modifier if present
		if e.ReadOnly != nil {
			if *e.ReadOnly == ast.MMAdd {
				p.writeString("readonly ")
			} else if *e.ReadOnly == ast.MMRemove {
				p.writeString("-readonly ")
			}
		}
		// Print [name]
		p.writeString("[")
		if e.Name != nil {
			p.printTypeAnn(e.Name)
		} else {
			p.writeString(e.TypeParam.Name)
		}
		p.writeString("]")
		// Print optional modifier
		if e.Optional != nil {
			if *e.Optional == ast.MMAdd {
				p.writeString("+?")
			} else if *e.Optional == ast.MMRemove {
				p.writeString("-?")
			}
		}
		// Print : value
		p.writeString(": ")
		p.printTypeAnn(e.Value)
		// Print for K in constraint
		p.writeString(" for ")
		p.writeString(e.TypeParam.Name)
		p.writeString(" in ")
		p.printTypeAnn(e.TypeParam.Constraint)
		// Print if clause if present
		if e.Check != nil && e.Extends != nil {
			p.writeString(" if ")
			p.printTypeAnn(e.Check)
			p.writeString(" : ")
			p.printTypeAnn(e.Extends)
		}
	case *ast.RestSpreadTypeAnn:
		p.writeString("...")
		p.printTypeAnn(e.Value)
	}
}

func (p *Printer) printTupleTypeAnn(typ *ast.TupleTypeAnn) {
	p.writeString("[")
	for i, elem := range typ.Elems {
		p.printTypeAnn(elem)
		if i < len(typ.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("]")
}

func (p *Printer) printUnionTypeAnn(typ *ast.UnionTypeAnn) {
	for i, t := range typ.Types {
		if i > 0 {
			p.writeString(" | ")
		}
		p.printTypeAnn(t)
	}
}

func (p *Printer) printIntersectionTypeAnn(typ *ast.IntersectionTypeAnn) {
	for i, t := range typ.Types {
		if i > 0 {
			p.writeString(" & ")
		}
		p.printTypeAnn(t)
	}
}

func (p *Printer) printTypeRefTypeAnn(typ *ast.TypeRefTypeAnn) {
	p.printQualIdent(typ.Name)
	if len(typ.TypeArgs) > 0 {
		p.writeString("<")
		for i, arg := range typ.TypeArgs {
			p.printTypeAnn(arg)
			if i < len(typ.TypeArgs)-1 {
				p.writeString(", ")
			}
		}
		p.writeString(">")
	}
}

func (p *Printer) printFuncTypeAnn(typ *ast.FuncTypeAnn) {
	if len(typ.TypeParams) > 0 {
		p.printTypeParams(typ.TypeParams)
	}

	p.writeString("fn (")
	for i, param := range typ.Params {
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
		if i < len(typ.Params)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")

	p.writeString(" -> ")
	p.printTypeAnn(typ.Return)

	if typ.Throws != nil {
		p.writeString(" throws ")
		p.printTypeAnn(typ.Throws)
	}
}

func (p *Printer) printTemplateLitTypeAnn(typ *ast.TemplateLitTypeAnn) {
	p.writeString("`")
	for i, quasi := range typ.Quasis {
		p.writeString(quasi.Value)
		if i < len(typ.TypeAnns) {
			p.writeString("${")
			p.printTypeAnn(typ.TypeAnns[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printMatchTypeAnn(typ *ast.MatchTypeAnn) {
	p.writeString("match ")
	p.printTypeAnn(typ.Target)
	p.writeString(" {")
	p.newline()
	p.indent()

	for i, c := range typ.Cases {
		p.printTypeAnn(c.Extends)
		p.writeString(" => ")
		p.printTypeAnn(c.Cons)
		if i < len(typ.Cases)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

// Literal printing

func (p *Printer) printLiteral(lit ast.Lit) {
	switch l := lit.(type) {
	case *ast.BoolLit:
		if l.Value {
			p.writeString("true")
		} else {
			p.writeString("false")
		}
	case *ast.NumLit:
		p.writeString(formatNumber(l.Value))
	case *ast.StrLit:
		p.writeString(strconv.Quote(l.Value))
	case *ast.RegexLit:
		p.writeString(l.Value)
	case *ast.BigIntLit:
		p.writeString(l.Value.String())
		p.writeString("n")
	case *ast.NullLit:
		p.writeString("null")
	case *ast.UndefinedLit:
		p.writeString("undefined")
	default:
		p.writeString("/* unknown literal */")
	}
}

// Helper methods

func (p *Printer) printTypeParams(params []*ast.TypeParam) {
	p.writeString("<")
	for i, param := range params {
		p.writeString(param.Name)
		if param.Constraint != nil {
			p.writeString(" extends ")
			p.printTypeAnn(param.Constraint)
		}
		if param.Default != nil {
			p.writeString(" = ")
			p.printTypeAnn(param.Default)
		}
		if i < len(params)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(">")
}

func (p *Printer) printQualIdent(qi ast.QualIdent) {
	switch q := qi.(type) {
	case *ast.Ident:
		p.writeString(q.Name)
	case *ast.Member:
		p.printQualIdent(q.Left)
		p.writeString(".")
		p.writeString(q.Right.Name)
	}
}

func (p *Printer) printObjKey(key ast.ObjKey) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		p.writeString(k.Name)
	case *ast.StrLit:
		p.writeString(strconv.Quote(k.Value))
	case *ast.NumLit:
		p.writeString(formatNumber(k.Value))
	case *ast.ComputedKey:
		p.writeString("[")
		p.printExpr(k.Expr)
		p.writeString("]")
	}
}

func (p *Printer) needsParens(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.IfElseExpr, *ast.MatchExpr, *ast.AssignExpr:
		return true
	default:
		return false
	}
}

func formatNumber(f float64) string {
	// Format the number, removing unnecessary trailing zeros
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return s
}

// Public API

// Print prints an AST node to a string
func Print(node ast.Node, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)

	switch n := node.(type) {
	case *ast.Script:
		if err := printer.PrintScript(n); err != nil {
			return "", err
		}
	case *ast.Module:
		if err := printer.PrintModule(n); err != nil {
			return "", err
		}
	case ast.Expr:
		printer.printExpr(n)
	case ast.Stmt:
		printer.printStmt(n)
	case ast.Decl:
		printer.printDecl(n)
	case ast.Pat:
		printer.printPattern(n)
	case ast.TypeAnn:
		printer.printTypeAnn(n)
	default:
		return "", fmt.Errorf("unsupported node type: %T", node)
	}

	return builder.String(), nil
}

// PrintToWriter prints an AST node to an io.Writer
func PrintToWriter(node ast.Node, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)

	switch n := node.(type) {
	case *ast.Script:
		return printer.PrintScript(n)
	case *ast.Module:
		return printer.PrintModule(n)
	case ast.Expr:
		printer.printExpr(n)
		return nil
	case ast.Stmt:
		printer.printStmt(n)
		return nil
	case ast.Decl:
		printer.printDecl(n)
		return nil
	case ast.Pat:
		printer.printPattern(n)
		return nil
	case ast.TypeAnn:
		printer.printTypeAnn(n)
		return nil
	default:
		return fmt.Errorf("unsupported node type: %T", node)
	}
}

// PrintScript prints a Script to a string
func PrintScript(script *ast.Script, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)
	if err := printer.PrintScript(script); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// PrintScriptToWriter prints a Script to an io.Writer
func PrintScriptToWriter(script *ast.Script, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)
	return printer.PrintScript(script)
}

// PrintModule prints a Module to a string
func PrintModule(module *ast.Module, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)
	if err := printer.PrintModule(module); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// PrintModuleToWriter prints a Module to an io.Writer
func PrintModuleToWriter(module *ast.Module, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)
	return printer.PrintModule(module)
}
