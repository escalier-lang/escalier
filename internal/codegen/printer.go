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
	Assign:            "=",
	Plus:              "+",
	Minus:             "-",
	Times:             "*",
	Divide:            "/",
	Modulo:            "%",
	Concatenation:     "+",
	LessThan:          "<",
	LessThanEqual:     "<=",
	GreaterThan:       ">",
	GreaterThanEqual:  ">=",
	EqualEqual:        "==",
	NotEqual:          "!=",
	StrictEqual:       "===",
	StrictNotEqual:    "!==",
	LogicalAnd:        "&&",
	LogicalOr:         "||",
	NullishCoalescing: "??",
	In:                "in",
	InstanceOf:        "instanceof",
}

var unaryOpMap = map[UnaryOp]string{
	UnaryPlus:  "+",
	UnaryMinus: "-",
	LogicalNot: "!",
	TypeOf:     "typeof ",
}

// Binding power of a JavaScript operator. A larger number binds tighter, so `a + b * c`
// groups as `a + (b * c)` because precMultiplicative exceeds precAdditive. The codegen AST
// records grouping in its tree shape rather than in parenthesis nodes, so the printer
// consults these to decide when an operand needs parentheses to survive being reparsed.
const (
	precAssign         = 2
	precCond           = 3
	precNullish        = 4
	precOr             = 4
	precAnd            = 5
	precEquality       = 8
	precRelational     = 9
	precAdditive       = 11
	precMultiplicative = 12
	precUnary          = 14
	// precPrimary is for an expression that carries no operator of its own, such as a
	// call, a member access, or a literal. Nothing can regroup it, so it never needs
	// parentheses.
	precPrimary = 20
)

// Binding power of a TypeScript type operator. This is a separate scale from the
// expression one above, and the two never mix. A larger number binds tighter, so
// `A | B & C` groups as `A | (B & C)`.
const (
	// precTypeOpenEnded covers the forms that have no closing delimiter of their own: the
	// function type `(a: A) => B`, the conditional type `A extends B ? C : D`, and the
	// rest spread `...A`. Each reaches as far right as the syntax allows, so any of them
	// inside a union or an intersection has to be wrapped. TypeScript refuses to guess a
	// grouping for the function type and rejects an unwrapped one with TS1385. It reads
	// an unwrapped conditional type differently instead, taking
	// `number | A extends B ? C : D` as `(number | A) extends B ? C : D`.
	precTypeOpenEnded    = 1
	precTypeUnion        = 2
	precTypeIntersection = 3
	// precTypePrefix covers `keyof T` and `infer T`. Both bind tighter than `|` and `&`,
	// which is why `keyof A | B` reads as `(keyof A) | B`.
	precTypePrefix = 4
	// precTypePrimary is for a type annotation that carries no operator of its own, such
	// as a type reference, an object type, a tuple, or an indexed access. Nothing can
	// regroup it, so it never needs parentheses.
	precTypePrimary = 5
)

// typeAnnPrecedence returns the binding power of a type annotation's top-level operator.
func typeAnnPrecedence(ta TypeAnn) int {
	switch ta.(type) {
	case *FuncTypeAnn, *CondTypeAnn, *RestSpreadTypeAnn:
		return precTypeOpenEnded
	case *UnionTypeAnn:
		return precTypeUnion
	case *IntersectionTypeAnn:
		return precTypeIntersection
	case *KeyOfTypeAnn, *InferTypeAnn:
		return precTypePrefix
	default:
		return precTypePrimary
	}
}

func binaryPrecedence(op BinaryOp) int {
	switch op {
	case Assign:
		return precAssign
	case NullishCoalescing:
		return precNullish
	case LogicalOr:
		return precOr
	case LogicalAnd:
		return precAnd
	case EqualEqual, NotEqual, StrictEqual, StrictNotEqual:
		return precEquality
	case LessThan, LessThanEqual, GreaterThan, GreaterThanEqual, In, InstanceOf:
		return precRelational
	case Plus, Minus, Concatenation:
		return precAdditive
	case Times, Divide, Modulo:
		return precMultiplicative
	default:
		return precPrimary
	}
}

// exprPrecedence returns the binding power of an expression's top-level operator.
func exprPrecedence(expr Expr) int {
	switch e := expr.(type) {
	case *BinaryExpr:
		return binaryPrecedence(e.Op)
	case *UnaryExpr, *AwaitExpr:
		return precUnary
	case *CondExpr:
		return precCond
	case *YieldExpr:
		return precAssign
	default:
		return precPrimary
	}
}

// mixesNullishAndLogical reports whether writing expr as an operand of parentOp would place
// `??` next to `||` or `&&`. JavaScript rejects that pairing outright instead of choosing a
// grouping for it, so it needs parentheses whichever side each operator is on.
func mixesNullishAndLogical(parentOp BinaryOp, expr Expr) bool {
	binExpr, isBin := expr.(*BinaryExpr)
	if !isBin {
		return false
	}
	isLogical := func(op BinaryOp) bool { return op == LogicalAnd || op == LogicalOr }
	if parentOp == NullishCoalescing {
		return isLogical(binExpr.Op)
	}
	if isLogical(parentOp) {
		return binExpr.Op == NullishCoalescing
	}
	return false
}

// isAssociative reports whether regrouping an operator's own operands leaves the result
// unchanged, so `a && (b && c)` can drop its parentheses. The short-circuiting operators
// qualify. Arithmetic does not: `+` over strings and floats depends on its grouping, as
// `(a + b) + c` and `a + (b + c)` show for a string `c` or for values that round.
func isAssociative(op BinaryOp) bool {
	switch op {
	case LogicalAnd, LogicalOr, NullishCoalescing:
		return true
	default:
		return false
	}
}

// printBinaryOperand prints one side of a binary operator, parenthesized when JavaScript
// would otherwise regroup it. An operand that binds looser than its parent always needs
// them. One that binds equally needs them on the side the operator does not associate
// with, which is why `a - (b - c)` keeps its parentheses and `(a - b) - c` does not.
func (p *Printer) printBinaryOperand(expr Expr, parentOp BinaryOp, isRight bool) {
	// Assign is the one right-associative operator here, so it is the left operand that
	// needs parentheses at equal binding power.
	equalNeedsParens := isRight
	if parentOp == Assign {
		equalNeedsParens = !isRight
	}
	if isAssociative(parentOp) {
		equalNeedsParens = false
	}
	prec := exprPrecedence(expr)
	parentPrec := binaryPrecedence(parentOp)
	needsParens := prec < parentPrec || (prec == parentPrec && equalNeedsParens)
	p.printMaybeParens(expr, needsParens || mixesNullishAndLogical(parentOp, expr))
}

// printReceiver prints a receiver: the callee of a call or a `new`, the object of an index
// or a member access, and the tag of a tagged template. A receiver binds tighter than every
// operator, so an operator expression in that position needs parentheses to survive being
// reparsed. Without them `(a + b).toFixed(2)` emits `a + b.toFixed(2)`, which calls the
// method on `b` and adds `a` to the string it returns.
//
// A number literal needs them for a different reason. It takes the following `.` as its
// own decimal point, so `(5).toFixed(2)` emitted bare would be a JavaScript syntax error.
func (p *Printer) printReceiver(expr Expr) {
	p.printMaybeParens(expr, exprPrecedence(expr) < precPrimary || isNumLitExpr(expr))
}

// startsAmbiguously reports whether an expression statement would emit a token JavaScript
// reads as the start of something other than an expression. A statement beginning with
// `function` starts a function declaration, and one beginning with `{` starts a block, so
// `function () {}();` and `{}.x;` are both syntax errors. Wrapping the whole expression in
// parentheses forces the expression reading.
//
// Only the leftmost token matters, so this walks the positions that emit first. Everything
// else emits an operator, a keyword, or a bracket of its own, which already disambiguates.
// `a + function () {}();` is legal exactly because the statement opens on `a`.
func startsAmbiguously(expr Expr) bool {
	for {
		switch e := expr.(type) {
		case *FuncExpr, *ObjectExpr:
			return true
		case *BinaryExpr:
			expr = e.Left
		case *CallExpr:
			expr = e.Callee
		case *IndexExpr:
			expr = e.Object
		case *MemberExpr:
			expr = e.Object
		case *CondExpr:
			expr = e.Cond
		case *TaggedTemplateLitExpr:
			expr = e.Tag
		case *TypeCastExpr:
			expr = e.Expr
		default:
			return false
		}
	}
}

// isNumLitExpr reports whether an expression is a number literal.
func isNumLitExpr(expr Expr) bool {
	litExpr, isLit := expr.(*LitExpr)
	if !isLit {
		return false
	}
	_, isNum := litExpr.Lit.(*NumLit)
	return isNum
}

func (p *Printer) printMaybeParens(expr Expr, needsParens bool) {
	if !needsParens {
		p.PrintExpr(expr)
		return
	}
	p.print("(")
	p.PrintExpr(expr)
	p.print(")")
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
	case *RegexLit:
		p.print(l.Value)
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
		p.printBinaryOperand(e.Left, e.Op, false)
		p.print(" " + binaryOpMap[e.Op] + " ")
		p.printBinaryOperand(e.Right, e.Op, true)
	case *LitExpr:
		p.PrintLit(e.Lit)
	case *IdentExpr:
		// Convert 'self' to 'this' inside methods
		if e.Name == "self" {
			p.print("this")
		} else {
			p.print(fullyQualifyName(e.Name, e.Namespace))
		}
	case *SpreadExpr:
		p.print("...")
		p.PrintExpr(e.Arg)
	case *EmptyExpr:
		// EmptyExpr generates no output - used as a placeholder
		// for terminal expressions that shouldn't be used
	case *UnaryExpr:
		p.print(unaryOpMap[e.Op])
		// A prefix operator takes only an operand that binds at least as tightly as it
		// does, so `!(a || b)` keeps its parentheses.
		p.printMaybeParens(e.Arg, exprPrecedence(e.Arg) < precUnary)
	case *CondExpr:
		// `?` and `:` delimit the branches, so only the test can be regrouped.
		p.printMaybeParens(e.Cond, exprPrecedence(e.Cond) <= precCond)
		p.print(" ? ")
		p.PrintExpr(e.Cons)
		p.print(" : ")
		p.PrintExpr(e.Alt)
	case *CallExpr:
		p.printReceiver(e.Callee)
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
	case *NewExpr:
		p.print("new ")
		p.printReceiver(e.Callee)
		p.print("(")
		for i, arg := range e.Args {
			if i > 0 {
				p.print(", ")
			}
			p.PrintExpr(arg)
		}
		p.print(")")
	case *FuncExpr:
		if e.async {
			p.print("async ")
		}
		if e.generator {
			p.print("function* (")
		} else {
			p.print("function (")
		}
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
		p.printReceiver(e.Object)
		if e.OptChain {
			p.print("?")
		}
		p.print("[")
		p.PrintExpr(e.Index)
		p.print("]")
	case *MemberExpr:
		p.printReceiver(e.Object)
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
			case *PropertyExpr:
				p.printObjKey(elem.Key)
				if elem.Value != nil {
					p.print(": ")
					p.PrintExpr(elem.Value)
				}
			case *RestSpreadExpr:
				p.print("...")
				p.PrintExpr(elem.Arg)
			default:
				panic(fmt.Sprintf("PrintExpr: unknown object expression element type: %T", elem))
			}
		}
		p.print("}")
	case *MatchExpr:
		// MatchExpr should not appear in the final codegen AST as it should be
		// converted to if-else statements during the build phase
		panic("MatchExpr should not appear in codegen AST")
	case *AwaitExpr:
		p.print("await ")
		p.PrintExpr(e.Arg)
	case *YieldExpr:
		p.print("yield")
		if e.IsDelegate {
			p.print("*")
		}
		if e.Value != nil {
			p.print(" ")
			p.PrintExpr(e.Value)
		}
	case *TypeCastExpr:
		// TypeCastExpr should not appear in the final codegen AST as it should be
		// converted to the inner expression during the build phase, but if it does
		// appear, just print the inner expression
		p.PrintExpr(e.Expr)
	case *TemplateLitExpr:
		p.print("`")
		for i, quasi := range e.Quasis {
			p.print(quasi)
			if i < len(e.Exprs) {
				p.print("${")
				p.PrintExpr(e.Exprs[i])
				p.print("}")
			}
		}
		p.print("`")
	case *TaggedTemplateLitExpr:
		p.printReceiver(e.Tag)
		p.print("`")
		for i, quasi := range e.Quasis {
			p.print(quasi)
			if i < len(e.Exprs) {
				p.print("${")
				p.PrintExpr(e.Exprs[i])
				p.print("}")
			}
		}
		p.print("`")
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

func (p *Printer) printObjTypeAnnElem(elem ObjTypeAnnElem) {
	switch elem := elem.(type) {
	case *CallableTypeAnn:
		p.print("(")
		for i, param := range elem.Fn.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printObjTypeAnnParam(param)
		}
		p.print(")")
		p.print(": ")
		p.PrintTypeAnn(elem.Fn.Return)
	case *ConstructorTypeAnn:
		p.print("new ")
		// Print type parameters if present
		if len(elem.Fn.TypeParams) > 0 {
			p.print("<")
			for i, tp := range elem.Fn.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(tp.Name)
				if tp.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(tp.Constraint)
				}
				if tp.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(tp.Default)
				}
			}
			p.print(">")
		}
		p.print("(")
		for i, param := range elem.Fn.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printObjTypeAnnParam(param)
		}
		p.print(")")
		p.print(": ")
		p.PrintTypeAnn(elem.Fn.Return)
	case *MethodTypeAnn:
		p.printObjKey(elem.Name)
		// Print type parameters if present
		if len(elem.Fn.TypeParams) > 0 {
			p.print("<")
			for i, tp := range elem.Fn.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(tp.Name)
				if tp.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(tp.Constraint)
				}
				if tp.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(tp.Default)
				}
			}
			p.print(">")
		}
		p.print("(")
		for i, param := range elem.Fn.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printObjTypeAnnParam(param)
		}
		p.print(")")
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
			p.printObjTypeAnnParam(param)
		}
		p.print(")")
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
			p.printObjTypeAnnParam(param)
		}
		p.print(")")
		// TypeScript doesn't allow setters to have a return type
	case *PropertyTypeAnn:
		if elem.Readonly {
			p.print("readonly ")
		}
		p.printObjKey(elem.Name)
		if elem.Optional {
			p.print("?")
		}
		p.print(": ")
		p.PrintTypeAnn(elem.Value)
	case *IndexSignatureTypeAnn:
		if elem.Readonly {
			p.print("readonly ")
		}
		p.print("[")
		p.print(elem.KeyName)
		p.print(": ")
		p.PrintTypeAnn(elem.KeyType)
		p.print("]: ")
		p.PrintTypeAnn(elem.Value)
	case *RestSpreadTypeAnn:
		p.print("...")
		p.PrintTypeAnn(elem.Value)
	case *MappedTypeAnn:
		// Print readonly modifier if present
		if elem.ReadOnly != nil {
			if *elem.ReadOnly == MMAdd {
				p.print("readonly ")
			} else if *elem.ReadOnly == MMRemove {
				p.print("-readonly ")
			}
		}
		p.print("[")
		p.print(elem.TypeParam.Name)
		p.print(" in ")
		p.PrintTypeAnn(elem.TypeParam.Constraint)
		// If a Name is provided, use it for key remapping
		// TypeScript syntax: [K in Keys as NewKey]
		if elem.Name != nil {
			p.print(" as ")
			p.PrintTypeAnn(elem.Name)
		}
		p.print("]")
		// Print optional modifier if present
		if elem.Optional != nil {
			if *elem.Optional == MMAdd {
				p.print("?")
			} else if *elem.Optional == MMRemove {
				p.print("-?")
			}
		}
		p.print(": ")
		p.PrintTypeAnn(elem.Value)
	default:
		panic(fmt.Sprintf("printObjTypeAnnElem: unknown object type annotation element type: %T", elem))
	}
}

func (p *Printer) printIdent(id *Identifier) {
	start := p.location
	p.print(id.Name)
	end := p.location
	id.span = &Span{Start: start, End: end}
}

func (p *Printer) printQualIdent(qi QualIdent) {
	switch q := qi.(type) {
	case *Ident:
		p.print(q.Name)
	case *Member:
		p.printQualIdent(q.Left)
		p.print(".")
		p.print(q.Right.Name)
	default:
		panic(fmt.Sprintf("printQualIdent: unknown QualIdent type: %T", qi))
	}
}

func (p *Printer) printPattern(pat Pat) {
	start := p.location
	switch pat := pat.(type) {
	case *IdentPat:
		p.print(pat.Name)
		if pat.Default != nil {
			p.print(" = ")
			p.PrintExpr(pat.Default)
		}
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
	if param.TypeAnn != nil {
		p.print(": ")
		p.PrintTypeAnn(param.TypeAnn)
	}
}

// printObjTypeAnnParam prints a parameter inside an object type
// annotation (callable, constructor, method, getter, setter signatures).
// Unlike printParam, it must not emit a JS default expression — type
// positions are purely structural — and it must surface the optional
// marker. A parameter is treated as optional when either the explicit
// `Optional` flag is set or the underlying identifier pattern carries a
// default value in source.
func (p *Printer) printObjTypeAnnParam(param *Param) {
	optional := param.Optional
	if ident, ok := param.Pattern.(*IdentPat); ok {
		p.print(ident.Name)
		if ident.Default != nil {
			optional = true
		}
	} else {
		p.printPattern(param.Pattern)
	}
	if optional {
		p.print("?")
	}
	if param.TypeAnn != nil {
		p.print(": ")
		p.PrintTypeAnn(param.TypeAnn)
	}
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
			if decl.TypeAnn != nil {
				p.print(": ")
				p.PrintTypeAnn(decl.TypeAnn)
			}
			if decl.Init != nil {
				p.print(" = ")
				p.PrintExpr(decl.Init)
			}
		}
		p.print(";")
	case *FuncDecl:
		if d.async {
			p.print("async ")
		}
		if d.generator {
			p.print("function* ")
		} else {
			p.print("function ")
		}
		p.print(d.Name.Name)

		// Print type parameters if present
		if len(d.TypeParams) > 0 {
			p.print("<")
			for i, param := range d.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(param.Name)
				if param.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(param.Constraint)
				}
				if param.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(param.Default)
				}
			}
			p.print(">")
		}

		p.print("(")
		for i, param := range d.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printParam(param)
		}
		p.print(")")

		if d.TypeAnn != nil {
			p.print(": ")
			p.PrintTypeAnn(d.TypeAnn)
		}

		if d.Body != nil {
			p.print(" {")

			p.indent++

			for _, stmt := range d.Body {
				p.NewLine()
				p.PrintStmt(stmt)
			}

			p.indent--
			p.NewLine()

			p.print("}")
		}

		if d.Body == nil {
			p.print(";")
		}
	case *TypeDecl:
		if d.Interface {
			p.print("interface ")
		} else {
			p.print("type ")
		}
		p.print(d.Name.Name)
		if len(d.TypeParams) > 0 {
			p.print("<")
			for i, param := range d.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(param.Name)
				if param.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(param.Constraint)
				}
				if param.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(param.Default)
				}
			}
			p.print(">")
		}
		if d.TypeAnn != nil {
			if !d.Interface {
				p.print(" = ")
			}
			p.PrintTypeAnn(d.TypeAnn)
		}
		if !d.Interface {
			p.print(";")
		}
	case *InterfaceDecl:
		p.print("interface ")
		p.print(d.Name.Name)
		if len(d.TypeParams) > 0 {
			p.print("<")
			for i, param := range d.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(param.Name)
				if param.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(param.Constraint)
				}
				if param.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(param.Default)
				}
			}
			p.print(">")
		}
		if len(d.Extends) > 0 {
			p.print(" extends ")
			for i, ext := range d.Extends {
				if i > 0 {
					p.print(", ")
				}
				p.PrintTypeAnn(ext)
			}
		}
		p.print(" {")
		if len(d.Members) > 0 {
			p.indent++
			for _, member := range d.Members {
				p.NewLine()
				p.printObjTypeAnnElem(member)
				p.print(";")
			}
			p.indent--
			p.NewLine()
		}
		p.print("}")
	case *NamespaceDecl:
		p.print("namespace ")
		p.print(d.Name.Name)
		p.print(" {")

		p.indent++
		for _, stmt := range d.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}
		p.indent--

		p.NewLine()
		p.print("}")
	case *ClassDecl:
		p.print("class ")
		p.print(d.Name.Name)
		if d.SuperClass != nil {
			p.print(" extends ")
			p.PrintExpr(d.SuperClass)
		}
		p.print(" {")
		p.indent++

		// Print class body elements (including constructor if present)
		for _, elem := range d.Body {
			p.NewLine()
			p.printClassElem(elem)
		}

		p.indent--
		p.NewLine()
		p.print("}")
	case *ImportDecl:
		p.print("import { ")
		for i, spec := range d.Specifiers {
			if i > 0 {
				p.print(", ")
			}
			p.print(spec)
		}
		p.print(" } from \"")
		p.print(d.Path)
		p.print("\";")
	}

	end := p.location
	decl.SetSpan(&Span{Start: start, End: end})
}

func (p *Printer) PrintStmt(stmt Stmt) {
	start := p.location

	switch s := stmt.(type) {
	case *ExprStmt:
		p.printMaybeParens(s.Expr, startsAmbiguously(s.Expr))
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
	case *BlockStmt:
		p.print("{")
		p.indent++
		for _, stmt := range s.Stmts {
			p.NewLine()
			p.PrintStmt(stmt)
		}
		p.indent--
		p.NewLine()
		p.print("}")
	case *IfStmt:
		p.print("if (")
		p.PrintExpr(s.Test)
		p.print(") ")
		p.PrintStmt(s.Cons)
		if s.Alt != nil {
			p.print(" else ")
			p.PrintStmt(s.Alt)
		}
	case *ThrowStmt:
		p.print("throw ")
		p.PrintExpr(s.Expr)
		p.print(";")
	case *TryStmt:
		p.print("try ")
		p.PrintStmt(s.Try)
		if s.Catch != nil {
			p.print(" catch ")
			if s.Catch.Param != nil {
				p.print("(")
				p.printPattern(s.Catch.Param)
				p.print(") ")
			}
			p.PrintStmt(s.Catch.Body)
		}
		if s.Finally != nil {
			p.print(" finally ")
			p.PrintStmt(s.Finally)
		}
	case *ForOfStmt:
		p.print("for ")
		if s.IsAwait {
			p.print("await ")
		}
		p.print("(const ")
		p.printPattern(s.Pattern)
		p.print(" of ")
		p.PrintExpr(s.Iterable)
		p.print(") {")
		p.indent++
		for _, stmt := range s.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}
		p.indent--
		p.NewLine()
		p.print("}")
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
	case *SymbolTypeAnn:
		p.print("symbol")
	case *UniqueSymbolTypeAnn:
		p.print("unique symbol")
	case *BigIntTypeAnn:
		p.print("bigint")
	case *NullTypeAnn:
		p.print("null")
	case *UndefinedTypeAnn:
		p.print("undefined")
	case *VoidTypeAnn:
		p.print("void")
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
			p.printObjTypeAnnElem(elem)
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
		for i, elem := range ta.Types {
			if i > 0 {
				p.print(" | ")
			}
			p.printTypeAnnAt(elem, precTypeUnion)
		}
	case *IntersectionTypeAnn:
		for i, elem := range ta.Types {
			if i > 0 {
				p.print(" & ")
			}
			p.printTypeAnnAt(elem, precTypeIntersection)
		}
	case *TypeRefTypeAnn:
		p.print(ta.Name)
		if len(ta.TypeArgs) > 0 {
			p.print("<")
			for i, arg := range ta.TypeArgs {
				if i > 0 {
					p.print(", ")
				}
				p.PrintTypeAnn(arg)
			}
			p.print(">")
		}
	case *FuncTypeAnn:
		// Print type parameters if present
		if len(ta.TypeParams) > 0 {
			p.print("<")
			for i, tp := range ta.TypeParams {
				if i > 0 {
					p.print(", ")
				}
				p.print(tp.Name)
				if tp.Constraint != nil {
					p.print(" extends ")
					p.PrintTypeAnn(tp.Constraint)
				}
				if tp.Default != nil {
					p.print(" = ")
					p.PrintTypeAnn(tp.Default)
				}
			}
			p.print(">")
		}
		p.print("(")
		for i, param := range ta.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printPattern(param.Pattern)
			if param.TypeAnn != nil {
				p.print(": ")
				p.PrintTypeAnn(param.TypeAnn)
			}
		}
		p.print(")")
		p.print(" => ")
		p.PrintTypeAnn(ta.Return)
	case *KeyOfTypeAnn:
		p.print("keyof ")
		p.printTypeAnnAt(ta.Type, precTypePrefix)
	case *TypeOfTypeAnn:
		p.print("typeof ")
		p.printQualIdent(ta.Value)
	case *IndexTypeAnn:
		p.printTypeAnnAt(ta.Target, precTypePrimary)
		p.print("[")
		p.PrintTypeAnn(ta.Index)
		p.print("]")
	case *CondTypeAnn:
		// A function type or a nested conditional on either side of `extends` would run
		// past the `?`, so both sides demand at least union binding power. The branches
		// are delimited by `?` and `:`, so they stay bare.
		p.printTypeAnnAt(ta.Check, precTypeUnion)
		p.print(" extends ")
		p.printTypeAnnAt(ta.Extends, precTypeUnion)
		p.print(" ? ")
		p.PrintTypeAnn(ta.Cons)
		p.print(" : ")
		p.PrintTypeAnn(ta.Alt)
	case *InferTypeAnn:
		panic("PrintTypeAnn: InferTypeAnn not implemented")
	case *AnyTypeAnn:
		p.print("any")
	case *TemplateLitTypeAnn:
		p.print("`")
		for i, quasi := range ta.Quasis {
			p.print(quasi.Value)
			if i < len(ta.TypeAnns) {
				p.print("${")
				p.PrintTypeAnn(ta.TypeAnns[i])
				p.print("}")
			}
		}
		p.print("`")
	case *IntrinsicTypeAnn:
		panic("PrintTypeAnn: IntrinsicTypeAnn not implemented")
	case *ImportType:
		panic("PrintTypeAnn: ImportType not implemented")
	case *RestSpreadTypeAnn:
		p.print("...")
		p.PrintTypeAnn(ta.Value)
	}
}

// printTypeAnnAt prints a type annotation, parenthesizing it when its own operator binds
// looser than minPrec. Callers pass the binding power the surrounding position demands.
// A looser annotation left bare would be pulled apart by the operator around it, naming a
// different type than the tree holds. An intersection member demands precTypeIntersection,
// so `(number | string) & boolean` keeps its parentheses rather than reparsing as
// `number | (string & boolean)`.
func (p *Printer) printTypeAnnAt(ta TypeAnn, minPrec int) {
	if typeAnnPrecedence(ta) >= minPrec {
		p.PrintTypeAnn(ta)
		return
	}
	p.print("(")
	p.PrintTypeAnn(ta)
	p.print(")")
}

func (p *Printer) PrintModule(mod *Module) string {
	for _, stmt := range mod.Stmts {
		p.PrintStmt(stmt)
		p.NewLine()
	}
	return p.Output
}

func (p *Printer) printClassElem(elem ClassElem) {
	switch e := elem.(type) {
	case *MethodElem:
		if e.Static {
			p.print("static ")
		}
		if e.Async {
			p.print("async ")
		}
		if e.Generator {
			p.print("*")
		}
		p.printObjKey(e.Name)
		p.print("(")
		for i, param := range e.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printParam(param)
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
	case *GetterElem:
		if e.Static {
			p.print("static ")
		}
		p.print("get ")
		p.printObjKey(e.Name)
		p.print("() {")

		p.indent++
		for _, stmt := range e.Body {
			p.NewLine()
			p.PrintStmt(stmt)
		}
		p.indent--

		p.NewLine()
		p.print("}")
	case *SetterElem:
		if e.Static {
			p.print("static ")
		}
		p.print("set ")
		p.printObjKey(e.Name)
		p.print("(")
		for i, param := range e.Params {
			if i > 0 {
				p.print(", ")
			}
			p.printParam(param)
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
	case *FieldElem:
		// Instance fields are handled by the constructor.
		if e.Static {
			p.print("static ")
			p.printObjKey(e.Name)
			if e.Value != nil {
				p.print(" = ")
				p.PrintExpr(e.Value)
			}
			p.print(";")
		}
	}
}
