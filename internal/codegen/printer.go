package codegen

import (
	"fmt"
	"strconv"
)

type Printer struct {
	indent   int
	location Location
	Output   string
}

func NewPrinter() *Printer {
	return &Printer{
		indent:   0,
		location: Location{Line: 1, Column: 1},
		Output:   "",
	}
}

func (p *Printer) NewLine() {
	p.Output += "\n"
	p.location.Line++
	p.location.Column = 1
	for range p.indent {
		p.print("  ")
	}
}

var binaryOpMap = map[BinaryOp]string{
	Plus:              "+",
	Minus:             "-",
	Times:             "*",
	Divide:            "/",
	Modulo:            "%",
	LessThan:          "<",
	LessThanEqual:     "<=",
	GreaterThan:       ">",
	GreaterThanEqual:  ">=",
	EqualEqual:        "==",
	NotEqual:          "!=",
	LogicalAnd:        "&&",
	LogicalOr:         "||",
	NullishCoalescing: "??",
}

var unaryOpMap = map[UnaryOp]string{
	UnaryPlus:  "+",
	UnaryMinus: "-",
	LogicalNot: "!",
}

func (p *Printer) print(s string) {
	p.Output += s
	p.location.Column += len(s)
}

func (p *Printer) PrintExpr(expr Expr) {
	start := p.location

	switch e := expr.(type) {
	case *BinaryExpr:
		p.PrintExpr(e.Left)
		p.print(" " + binaryOpMap[e.Op] + " ")
		p.PrintExpr(e.Right)
	case *NumExpr:
		value := strconv.FormatFloat(e.Value, 'f', -1, 32)
		p.print(value)
	case *StrExpr:
		value := fmt.Sprintf("%q", e.Value)
		p.print(value)
	case *BoolExpr:
		if e.Value {
			p.print("true")
		} else {
			p.print("false")
		}
	case *IdentExpr:
		p.print(e.Name)
	case *UnaryExpr:
		p.print(unaryOpMap[e.Op])
		p.PrintExpr(e.Arg)
	case *CallExpr:
		p.PrintExpr(e.Callee)
		if e.OptChain {
			p.print("?")
		}
		p.print("(")
		for i, arg := range e.Args {
			if i > 0 {
				p.print(", ")
			}
			p.PrintExpr(arg)
		}
		p.print(")")
	case *FuncExpr:
		p.print("function (")
		for i, param := range e.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printPattern(param.Pattern)
		}
		p.print(") {")
		p.indent++
		for _, stmt := range e.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}
		p.indent--
		p.NewLine()
		p.print("}")
	case *IndexExpr:
		p.PrintExpr(e.Object)
		if e.OptChain {
			p.print("?")
		}
		p.print("[")
		p.PrintExpr(e.Index)
		p.print("]")
	case *MemberExpr:
		p.PrintExpr(e.Object)
		if e.OptChain {
			p.print("?")
		}
		p.print(".")
		p.printIdent(e.Prop)
	case *ArrayExpr:
		p.print("[")
		for i, elem := range e.Elems {
			if i > 0 {
				p.print(", ")
			}
			p.PrintExpr(elem)
		}
		p.print("]")
	}

	end := p.location
	expr.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) printIdent(id *Identifier) {
	start := p.location
	p.print(id.Name)
	end := p.location
	id.span = &Span{Start: start, End: end}
}

func (p *Printer) printPattern(pat Pat) {
	start := p.location
	switch pat := pat.(type) {
	case *IdentPat:
		p.print(pat.Name)
	case *ObjectPat:
		p.print("{")
		for i, elem := range pat.Elems {
			if i > 0 {
				p.print(", ")
			}
			switch elem := elem.(type) {
			case *ObjKeyValuePat:
				p.print(elem.Key)
				p.print(": ")
				p.printPattern(elem.Value)
				if elem.Default != nil {
					p.print(" = ")
					p.PrintExpr(elem.Default)
				}
			case *ObjShorthandPat:
				p.print(elem.Key)
				if elem.Default != nil {
					p.print(" = ")
					p.PrintExpr(elem.Default)
				}
			case *ObjRestPat:
				p.print("...")
				p.printPattern(elem.Pattern)
			}
		}
		p.print("}")
	case *TuplePat:
		p.print("[")
		for i, elem := range pat.Elems {
			if i > 0 {
				p.print(", ")
			}
			switch elem := elem.(type) {
			case *TupleElemPat:
				p.printPattern(elem.Pattern)
				if elem.Default != nil {
					p.print(" = ")
					p.PrintExpr(elem.Default)
				}
			case *TupleRestPat:
				p.print("...")
				p.printPattern(elem.Pattern)
			}
		}
		p.print("]")
	}
	end := p.location
	pat.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) printParam(param *Param) {
	p.printPattern(param.Pattern)
}

func (p *Printer) PrintDecl(decl Decl) {
	start := p.location

	if decl.Declare() {
		p.print("declare ")
	}
	if decl.Export() {
		p.print("export ")
	}

	switch d := decl.(type) {
	case *VarDecl:
		switch d.Kind {
		case VarKind:
			p.print("let ")
		case ValKind:
			p.print("const ")
		}
		p.printPattern(d.Pattern)
		if d.Init != nil {
			p.print(" = ")
			p.PrintExpr(d.Init)
		}
		p.print(";")
	case *FuncDecl:
		p.print("function ")
		p.print(d.Name.Name)

		p.print("(")
		for i, param := range d.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printParam(param)
		}
		p.print(") {")

		p.indent++

		for _, stmt := range d.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}

		p.indent--
		p.NewLine()

		p.print("}")
	}

	end := p.location
	decl.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) PrintStmt(stmt Stmt) {
	start := p.location

	switch s := stmt.(type) {
	case *ExprStmt:
		p.PrintExpr(s.Expr)
		p.print(";")
	case *DeclStmt:
		p.PrintDecl(s.Decl)
	case *ReturnStmt:
		p.print("return")
		if s.Expr != nil {
			p.print(" ")
			p.PrintExpr(s.Expr)
		}
		p.print(";")
	}

	end := p.location
	stmt.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) PrintModule(mod *Module) {
	for _, stmt := range mod.Stmts {
		p.PrintStmt(stmt)
		p.NewLine()
	}
}
