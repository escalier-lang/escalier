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
	for i := 0; i < p.indent; i++ {
		p.Output += "  "
		p.location.Column += 2
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
	Equal:             "==",
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

func (p *Printer) PrintExpr(expr *Expr) {
	start := p.location

	switch e := expr.Kind.(type) {
	case *EBinary:
		p.PrintExpr(e.Left)
		op := binaryOpMap[e.Op]
		p.Output += " " + op + " "
		p.location.Column += 2 + len(op)
		p.PrintExpr(e.Right)
	case *ENumber:
		value := strconv.FormatFloat(e.Value, 'f', -1, 32)
		p.Output += value
		p.location.Column += len(value)
	case *EString:
		value := fmt.Sprintf("%q", e.Value)
		p.Output += value
		p.location.Column += len(value)
	case *EIdentifier:
		p.Output += e.Name
		p.location.Column += len(e.Name)
	case *EUnary:
		op := unaryOpMap[e.Op]
		p.Output += op
		p.location.Column += len(op)
		p.PrintExpr(e.Arg)
	case *ECall:
		p.PrintExpr(e.Callee)
		if e.OptChain {
			p.Output += "?"
			p.location.Column++
		}
		p.Output += "("
		p.location.Column++
		for i, arg := range e.Args {
			if i > 0 {
				p.Output += ", "
				p.location.Column += 2
			}
			p.PrintExpr(arg)
		}
		p.Output += ")"
		p.location.Column++
	case *EIndex:
		p.PrintExpr(e.Object)
		if e.OptChain {
			p.Output += "?"
			p.location.Column++
		}
		p.Output += "["
		p.location.Column++
		p.PrintExpr(e.Index)
		p.Output += "]"
		p.location.Column++
	case *EMember:
		p.PrintExpr(e.Object)
		if e.OptChain {
			p.Output += "?"
			p.location.Column++
		}
		p.Output += "."
		p.location.Column++
		p.Output += e.Prop.Name
		p.location.Column += len(e.Prop.Name)
	case *EArray:
		p.Output += "["
		p.location.Column++
		for i, elem := range e.Elems {
			if i > 0 {
				p.Output += ", "
				p.location.Column += 2
			}
			p.PrintExpr(elem)
		}
		p.Output += "]"
		p.location.Column++
	}

	end := p.location
	expr.span = &Span{Start: start, End: end}
}

func (p *Printer) printPattern(pat Pat) {
	switch pat := pat.(type) {
	case *IdentPat:
		p.Output += pat.Name
		p.location.Column += len(pat.Name)
	case *ObjectPat:
		panic("TODO: print object pat")
	case *TuplePat:
		panic("TODO: print tuple pat")
	}
}

func (p *Printer) printParam(param *Param) {
	p.printPattern(param.Pattern)
}

func (p *Printer) PrintDecl(decl *Decl) {
	start := p.location

	if decl.Declare {
		p.Output += "declare "
		p.location.Column += 8
	}
	if decl.Export {
		p.Output += "export "
		p.location.Column += 7
	}

	switch d := decl.Kind.(type) {
	case *DVariable:
		switch d.Kind {
		case VarKind:
			p.Output += "let "
			p.location.Column += 4
		case ValKind:
			p.Output += "const "
			p.location.Column += 6
		}
		p.printPattern(d.Pattern)
		if d.Init != nil {
			p.Output += " = "
			p.location.Column += 3
			p.PrintExpr(d.Init)
		}
		p.Output += ";"
		p.location.Column++
	case *DFunction:
		p.Output += "function "
		p.location.Column += 9
		p.Output += d.Name.Name
		p.location.Column += len(d.Name.Name)

		p.Output += "("
		p.location.Column++
		for i, param := range d.Params {
			if i > 0 {
				p.Output += ", "
				p.location.Column += 2
			}
			p.printParam(param)
		}
		p.Output += ") {"
		p.location.Column += 3

		p.indent++

		for _, stmt := range d.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}

		p.indent--
		p.NewLine()

		p.Output += "}"
		p.location.Column++
	}

	end := p.location
	decl.span = &Span{Start: start, End: end}
}

func (p *Printer) PrintStmt(stmt *Stmt) {
	start := p.location

	switch s := stmt.Kind.(type) {
	case *SExpr:
		p.PrintExpr(s.Expr)
		p.Output += ";"
		p.location.Column++
	case *SDecl:
		p.PrintDecl(s.Decl)
	case *SReturn:
		p.Output += "return"
		p.location.Column += 6
		if s.Expr != nil {
			p.Output += " "
			p.location.Column++
			p.PrintExpr(s.Expr)
		}
		p.Output += ";"
		p.location.Column++
	}

	end := p.location
	stmt.span = &Span{Start: start, End: end}
}

func (p *Printer) PrintModule(mod *Module) {
	for _, stmt := range mod.Stmts {
		p.PrintStmt(stmt)
		p.NewLine()
	}
}
