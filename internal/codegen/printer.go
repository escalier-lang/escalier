package codegen

import (
	"fmt"
	"strconv"
	"unicode"
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

func (p *Printer) PrintLit(lit Lit) {
	start := p.location

	switch l := lit.(type) {
	case *StrLit:
		p.print(fmt.Sprintf("%q", l.Value))
	case *NumLit:
		p.print(strconv.FormatFloat(l.Value, 'f', -1, 32))
	case *BoolLit:
		if l.Value {
			p.print("true")
		} else {
			p.print("false")
		}
	case *NullLit:
		p.print("null")
	case *UndefinedLit:
		p.print("undefined")
	default:
		panic(fmt.Sprintf("PrintLit: unknown literal type: %T", l))
	}

	end := p.location
	lit.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) PrintExpr(expr Expr) {
	start := p.location

	switch e := expr.(type) {
	case *BinaryExpr:
		p.PrintExpr(e.Left)
		p.print(" " + binaryOpMap[e.Op] + " ")
		p.PrintExpr(e.Right)
	case *LitExpr:
		p.PrintLit(e.Lit)
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
	case *ObjectExpr:
		p.print("{")
		for i, elem := range e.Elems {
			if i > 0 {
				p.print(", ")
			}
			switch elem := elem.(type) {
			case *MethodExpr:
				p.printMethod(elem.Name, elem.Params, elem.Body)
			case *GetterExpr:
				p.print("get ")
				p.printMethod(elem.Name, []*Param{}, elem.Body)
			case *SetterExpr:
				p.print("set ")
				p.printMethod(elem.Name, elem.Params, elem.Body)
			case *PropertyExpr:
				p.printObjKey(elem.Key)
				p.print(": ")
				p.PrintExpr(elem.Value)
			case *RestSpreadExpr:
				p.print("...")
				p.PrintExpr(elem.Arg)
			default:
				panic(fmt.Sprintf("PrintExpr: unknown object expression element type: %T", elem))
			}
		}
	default:
		panic(fmt.Sprintf("PrintExpr: unknown expression type: %T", expr))
	}

	end := p.location
	expr.SetSpan(&Span{Start: start, End: end})
}

// IsValidIdentifier checks if a string is a valid identifier.
// Valid identifiers start with a letter, '$', or '_', and can contain
// those same characters plus numbers. They cannot contain whitespace.
func IsValidIdentifier(name string) bool {
	if name == "" {
		return false
	}

	// Check first character
	firstChar := rune(name[0])
	if !(unicode.IsLetter(firstChar) || firstChar == '$' || firstChar == '_') {
		return false
	}

	// Check remaining characters
	for _, char := range name[1:] {
		if !(unicode.IsLetter(char) || unicode.IsDigit(char) || char == '$' || char == '_') {
			return false
		}
	}

	return true
}

func (p *Printer) printObjKey(key ObjKey) {
	start := p.location

	switch key := key.(type) {
	case *IdentExpr:
		p.print(key.Name)
	case *StrLit:
		// check if the string is a valid identifier
		if IsValidIdentifier(key.Value) {
			p.print(key.Value)
		} else {
			p.print(fmt.Sprintf("%q", key.Value))
		}
	case *NumLit:
		p.print(strconv.FormatFloat(key.Value, 'f', -1, 32))
	case *ComputedKey:
		p.print("[")
		p.PrintExpr(key.Expr)
		p.print("]")
	default:
		panic(fmt.Sprintf("printObjKey: unknown object key type: %T", key))
	}

	end := p.location
	key.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) printMethod(name ObjKey, params []*Param, body []Stmt) {
	p.printObjKey(name)
	p.print("(")
	for i, param := range params {
		if i > 0 {
			p.print(", ")
		}
		p.printPattern(param.Pattern)
	}
	p.print(") {")
	p.indent++
	for _, stmt := range body {
		p.NewLine()
		p.PrintStmt(stmt)
	}
	p.indent--
	p.NewLine()
	p.print("}")
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
		pat.Default.IfSome(func(e Expr) {
			p.print(" = ")
			p.PrintExpr(e)
		})
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
				elem.Default.IfSome(func(e Expr) {
					p.print(" = ")
					p.PrintExpr(e)
				})
			case *ObjShorthandPat:
				p.print(elem.Key)
				elem.Default.IfSome(func(e Expr) {
					p.print(" = ")
					p.PrintExpr(e)
				})
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
			p.printPattern(elem)
		}
		p.print("]")
	case *RestPat:
		p.print("...")
		p.printPattern(pat.Pattern)
	}
	end := p.location
	pat.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) printParam(param *Param) {
	p.printPattern(param.Pattern)
	param.TypeAnn.IfSome(func(ta TypeAnn) {
		p.print(": ")
		p.PrintTypeAnn(ta)
	})
}

func (p *Printer) PrintDecl(decl Decl) {
	start := p.location

	if decl.Export() {
		p.print("export ")
	}

	if decl.Declare() {
		p.print("declare ")
	}

	switch d := decl.(type) {
	case *VarDecl:
		switch d.Kind {
		case VarKind:
			p.print("let ")
		case ValKind:
			p.print("const ")
		}
		for i, decl := range d.Decls {
			if i > 0 {
				p.print(", ")
			}
			p.printPattern(decl.Pattern)
			decl.TypeAnn.IfSome(func(ta TypeAnn) {
				p.print(": ")
				p.PrintTypeAnn(ta)
			})
			if decl.Init != nil {
				p.print(" = ")
				p.PrintExpr(decl.Init)
			}
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
		p.print(")")

		d.TypeAnn.IfSome(func(ta TypeAnn) {
			p.print(": ")
			p.PrintTypeAnn(ta)
		})

		d.Body.IfSome(func(stmts []Stmt) {
			p.print(" {")

			p.indent++

			for _, stmt := range stmts {
				p.NewLine()
				p.PrintStmt(stmt)
			}

			p.indent--
			p.NewLine()

			p.print("}")
		})

		d.Body.IfNone(func() {
			p.print(";")
		})
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
		s.Expr.IfSome(func(e Expr) {
			p.print(" ")
			p.PrintExpr(e)
		})
		p.print(";")
	}

	end := p.location
	stmt.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) PrintTypeAnn(ta TypeAnn) {
	switch ta := ta.(type) {
	case *LitTypeAnn:
		p.PrintLit(ta.Lit)
	case *NumberTypeAnn:
		p.print("number")
	case *StringTypeAnn:
		p.print("string")
	case *BooleanTypeAnn:
		p.print("boolean")
	case *NullTypeAnn:
		p.print("null")
	case *UndefinedTypeAnn:
		p.print("undefined")
	case *UnknownTypeAnn:
		p.print("unknown")
	case *NeverTypeAnn:
		p.print("never")
	case *ObjectTypeAnn:
		p.print("{")
		for i, elem := range ta.Elems {
			if i > 0 {
				p.print(", ")
			}
			switch elem := elem.(type) {
			case *MethodTypeAnn:
				p.printObjKey(elem.Name)
				p.print("(")
				for i, param := range elem.Fn.Params {
					if i > 0 {
						p.print(", ")
					}
					p.printPattern(param.Pattern)
				}
				p.print(": ")
				p.PrintTypeAnn(elem.Fn.Return)
			case *GetterTypeAnn:
				p.print("get ")
				p.printObjKey(elem.Name)
				p.print("(")
				for i, param := range elem.Fn.Params {
					if i > 0 {
						p.print(", ")
					}
					p.printPattern(param.Pattern)
				}
				p.print(": ")
				p.PrintTypeAnn(elem.Fn.Return)
			case *SetterTypeAnn:
				p.print("set ")
				p.printObjKey(elem.Name)
				p.print("(")
				for i, param := range elem.Fn.Params {
					if i > 0 {
						p.print(", ")
					}
					p.printPattern(param.Pattern)
				}
				p.print(": ")
				p.PrintTypeAnn(elem.Fn.Return)
			case *PropertyTypeAnn:
				p.printObjKey(elem.Name)
				elem.Value.IfSome(func(value TypeAnn) {
					p.print(": ")
					p.PrintTypeAnn(value)
				})
			case *RestSpreadTypeAnn:
				p.print("...")
				p.PrintTypeAnn(elem.Value)
			default:
				panic(fmt.Sprintf("PrintTypeAnn: unknown object type annotation element type: %T", elem))
			}
		}
		p.print("}")
	case *TupleTypeAnn:
		p.print("[")
		for i, elem := range ta.Elems {
			if i > 0 {
				p.print(", ")
			}
			p.PrintTypeAnn(elem)
		}
		p.print("]")
	case *UnionTypeAnn:
		panic("PrintTypeAnn: UnionTypeAnn not implemented")
	case *IntersectionTypeAnn:
		panic("PrintTypeAnn: IntersectionTypeAnn not implemented")
	case *TypeRefTypeAnn:
		panic("PrintTypeAnn: TypeRefTypeAnn not implemented")
	case *FuncTypeAnn:
		panic("PrintTypeAnn: FuncTypeAnn not implemented")
	case *KeyOfTypeAnn:
		panic("PrintTypeAnn: KeyOfTypeAnn not implemented")
	case *TypeOfTypeAnn:
		panic("PrintTypeAnn: TypeOfTypeAnn not implemented")
	case *IndexTypeAnn:
		panic("PrintTypeAnn: IndexTypeAnn not implemented")
	case *CondTypeAnn:
		panic("PrintTypeAnn: CondTypeAnn not implemented")
	case *InferTypeAnn:
		panic("PrintTypeAnn: InferTypeAnn not implemented")
	case *AnyTypeAnn:
		panic("PrintTypeAnn: AnyTypeAnn not implemented")
	case *TemplateLitTypeAnn:
		panic("PrintTypeAnn: TemplateLitTypeAnn not implemented")
	case *IntrinsicTypeAnn:
		panic("PrintTypeAnn: IntrinsicTypeAnn not implemented")
	case *ImportType:
		panic("PrintTypeAnn: ImportType not implemented")
	}
}

func (p *Printer) PrintModule(mod *Module) string {
	for _, stmt := range mod.Stmts {
		p.PrintStmt(stmt)
		p.NewLine()
	}
	return p.Output
}
