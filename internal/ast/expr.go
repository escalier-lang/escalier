//go:generate go run ../../tools/gen_ast/gen_ast.go -p ./expr.go

package ast

import (
	"math/big"

	"github.com/escalier-lang/escalier/internal/provenance"
)

// NamespaceID represents a unique identifier for a namespace
type NamespaceID int

const RootNamespaceID NamespaceID = 0

//sumtype:decl
type Expr interface {
	isExpr()
	Node
	Inferrable
}

func (*IgnoreExpr) isExpr()            {}
func (*EmptyExpr) isExpr()             {}
func (*BinaryExpr) isExpr()            {}
func (*UnaryExpr) isExpr()             {}
func (*LiteralExpr) isExpr()           {}
func (*IdentExpr) isExpr()             {}
func (*FuncExpr) isExpr()              {}
func (*CallExpr) isExpr()              {}
func (*IndexExpr) isExpr()             {}
func (*MemberExpr) isExpr()            {}
func (*TupleExpr) isExpr()             {}
func (*ObjectExpr) isExpr()            {}
func (*IfElseExpr) isExpr()            {}
func (*IfLetExpr) isExpr()             {}
func (*MatchExpr) isExpr()             {}
func (*AssignExpr) isExpr()            {}
func (*TryCatchExpr) isExpr()          {}
func (*DoExpr) isExpr()                {}
func (*AwaitExpr) isExpr()             {}
func (*ThrowExpr) isExpr()             {}
func (*TemplateLitExpr) isExpr()       {}
func (*TaggedTemplateLitExpr) isExpr() {}
func (*JSXElementExpr) isExpr()        {}
func (*JSXFragmentExpr) isExpr()       {}
func (*TypeCastExpr) isExpr()          {}

type IgnoreExpr struct {
	span         Span
	inferredType Type
}

func (e *IgnoreExpr) Accept(v Visitor) {
	v.EnterExpr(e)
	v.ExitExpr(e)
}

type EmptyExpr struct {
	span         Span
	inferredType Type
}

func NewEmpty(span Span) *EmptyExpr {
	return &EmptyExpr{span: span, inferredType: nil}
}
func (e *EmptyExpr) Accept(v Visitor) {
	v.EnterExpr(e)
	v.ExitExpr(e)
}

type BinaryOp string

const (
	Plus              BinaryOp = "+"
	Minus             BinaryOp = "-"
	Times             BinaryOp = "*"
	Divide            BinaryOp = "/"
	Modulo            BinaryOp = "%"
	Concatenation     BinaryOp = "++"
	LessThan          BinaryOp = "<"
	LessThanEqual     BinaryOp = "<="
	GreaterThan       BinaryOp = ">"
	GreaterThanEqual  BinaryOp = ">="
	Equal             BinaryOp = "=="
	NotEqual          BinaryOp = "!="
	LogicalAnd        BinaryOp = "&&"
	LogicalOr         BinaryOp = "||"
	NullishCoalescing BinaryOp = "??"
	Assign            BinaryOp = "="
)

type BinaryExpr struct {
	Left         Expr
	Op           BinaryOp
	Right        Expr
	span         Span
	inferredType Type
}

func NewBinary(left, right Expr, op BinaryOp, span Span) *BinaryExpr {
	return &BinaryExpr{Left: left, Right: right, Op: op, span: span, inferredType: nil}
}
func (e *BinaryExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Left.Accept(v)
		e.Right.Accept(v)
	}
	v.ExitExpr(e)
}

type UnaryOp int

const (
	UnaryPlus  UnaryOp = iota // +
	UnaryMinus                // -
	LogicalNot                // !
)

type UnaryExpr struct {
	Op           UnaryOp
	Arg          Expr
	span         Span
	inferredType Type
}

func NewUnary(op UnaryOp, arg Expr, span Span) *UnaryExpr {
	return &UnaryExpr{Op: op, Arg: arg, span: span, inferredType: nil}
}
func (e *UnaryExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Arg.Accept(v)
	}
	v.ExitExpr(e)
}

//sumtype:decl
type Lit interface {
	isLiteral()
	Node
	Equal(Lit) bool
}

func (*BoolLit) isLiteral()      {}
func (*NumLit) isLiteral()       {}
func (*StrLit) isLiteral()       {}
func (*RegexLit) isLiteral()     {}
func (*BigIntLit) isLiteral()    {}
func (*NullLit) isLiteral()      {}
func (*UndefinedLit) isLiteral() {}

type BoolLit struct {
	Value bool
	span  Span
}
type NumLit struct {
	Value float64
	span  Span
}
type StrLit struct {
	Value string
	span  Span
}
type RegexLit struct {
	Value string
	span  Span
}
type BigIntLit struct {
	Value big.Int
	span  Span
}
type NullLit struct{ span Span }
type UndefinedLit struct{ span Span }

type LiteralExpr struct {
	Lit          Lit
	span         Span
	inferredType Type
}

func NewNumber(value float64, span Span) *NumLit {
	return &NumLit{Value: value, span: span}
}
func (l *NumLit) Span() Span { return l.span }
func (l *NumLit) Equal(other Lit) bool {
	if other, ok := other.(*NumLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *NumLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewString(value string, span Span) *StrLit {
	return &StrLit{Value: value, span: span}
}
func (l *StrLit) Span() Span { return l.span }
func (l *StrLit) Equal(other Lit) bool {
	if other, ok := other.(*StrLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *StrLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewRegex(value string, span Span) *RegexLit {
	return &RegexLit{Value: value, span: span}
}
func (l *RegexLit) Span() Span { return l.span }
func (l *RegexLit) Equal(other Lit) bool {
	if other, ok := other.(*RegexLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *RegexLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewBoolean(value bool, span Span) *BoolLit {
	return &BoolLit{Value: value, span: span}
}
func (l *BoolLit) Span() Span { return l.span }
func (l *BoolLit) Equal(other Lit) bool {
	if other, ok := other.(*BoolLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *BoolLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewBigInt(value big.Int, span Span) *BigIntLit {
	return &BigIntLit{Value: value, span: span}
}
func (l *BigIntLit) Span() Span { return l.span }
func (l *BigIntLit) Equal(other Lit) bool {
	if other, ok := other.(*BigIntLit); ok {
		return l.Value.Cmp(&other.Value) == 0
	}
	return false
}
func (l *BigIntLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewNull(span Span) *NullLit {
	return &NullLit{span: span}
}
func (l *NullLit) Span() Span { return l.span }
func (l *NullLit) Equal(other Lit) bool {
	if _, ok := other.(*NullLit); ok {
		return true
	}
	return false
}
func (l *NullLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func NewUndefined(span Span) *UndefinedLit {
	return &UndefinedLit{span: span}
}
func (l *UndefinedLit) Span() Span { return l.span }
func (l *UndefinedLit) Equal(other Lit) bool {
	if _, ok := other.(*UndefinedLit); ok {
		return true
	}
	return false
}
func (l *UndefinedLit) Accept(v Visitor) {
	v.EnterLit(l)
	v.ExitLit(l)
}

func (e *LiteralExpr) Accept(v Visitor) {
	v.EnterExpr(e)
	v.ExitExpr(e)
}

func NewLitExpr(lit Lit) *LiteralExpr {
	return &LiteralExpr{Lit: lit, span: lit.Span(), inferredType: nil}
}

type IdentExpr struct {
	Name         string
	Namespace    NamespaceID
	Source       provenance.Provenance
	span         Span
	inferredType Type
}

func NewIdent(name string, span Span) *IdentExpr {
	return &IdentExpr{Name: name, Namespace: RootNamespaceID, Source: nil, span: span, inferredType: nil}
}

func NewIdentWithNamespace(name string, namespace NamespaceID, span Span) *IdentExpr {
	return &IdentExpr{Name: name, Namespace: namespace, Source: nil, span: span, inferredType: nil}
}
func (e *IdentExpr) Accept(v Visitor) {
	v.EnterExpr(e)
	v.ExitExpr(e)
}

type TypeParam struct {
	Name       string
	Constraint TypeAnn
	Default    TypeAnn
}

func NewTypeParam(name string, constraint, defaultType TypeAnn) TypeParam {
	return TypeParam{Name: name, Constraint: constraint, Default: defaultType}
}

type FuncSig struct {
	TypeParams []*TypeParam
	Params     []*Param
	Return     TypeAnn // optional
	Throws     TypeAnn // optional
	Async      bool    // whether this is an async function
}

type FuncExpr struct {
	FuncSig
	Body         *Block
	span         Span
	inferredType Type
}

func NewFuncExpr(
	typeParams []*TypeParam,
	params []*Param,
	ret TypeAnn, // optional
	throws TypeAnn, // optional
	async bool,
	body *Block,
	span Span,
) *FuncExpr {
	return &FuncExpr{
		FuncSig: FuncSig{
			TypeParams: typeParams,
			Params:     params,
			Return:     ret,
			Throws:     throws,
			Async:      async,
		},
		Body:         body,
		span:         span,
		inferredType: nil,
	}
}
func (e *FuncExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		for _, param := range e.Params {
			param.Pattern.Accept(v)
		}
		for _, tp := range e.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(v)
			}
			if tp.Default != nil {
				tp.Default.Accept(v)
			}
		}
		if e.Return != nil {
			e.Return.Accept(v)
		}
		if e.Throws != nil {
			e.Throws.Accept(v)
		}
		e.Body.Accept(v)
	}
	v.ExitExpr(e)
}

type CallExpr struct {
	Callee       Expr
	Args         []Expr
	OptChain     bool
	span         Span
	inferredType Type
}

func NewCall(callee Expr, args []Expr, optChain bool, span Span) *CallExpr {
	return &CallExpr{Callee: callee, Args: args, OptChain: optChain, span: span, inferredType: nil}
}
func (e *CallExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Callee.Accept(v)
		for _, arg := range e.Args {
			arg.Accept(v)
		}
	}
	v.ExitExpr(e)
}

type IndexExpr struct {
	Object       Expr
	Index        Expr
	OptChain     bool
	span         Span
	inferredType Type
}

func NewIndex(object, index Expr, optChain bool, span Span) *IndexExpr {
	return &IndexExpr{Object: object, Index: index, OptChain: optChain, span: span, inferredType: nil}
}
func (e *IndexExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Object.Accept(v)
		e.Index.Accept(v)
	}
	v.ExitExpr(e)
}

type MemberExpr struct {
	Object       Expr
	Prop         *Ident
	OptChain     bool
	span         Span
	inferredType Type
}

func NewMember(object Expr, prop *Ident, optChain bool, span Span) *MemberExpr {
	return &MemberExpr{Object: object, Prop: prop, OptChain: optChain, span: span, inferredType: nil}
}
func (e *MemberExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Object.Accept(v)
	}
	v.ExitExpr(e)
}

type TupleExpr struct {
	Elems        []Expr
	span         Span
	inferredType Type
}

func NewArray(elems []Expr, span Span) *TupleExpr {
	return &TupleExpr{Elems: elems, span: span, inferredType: nil}
}
func (e *TupleExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		for _, elem := range e.Elems {
			elem.Accept(v)
		}
	}
	v.ExitExpr(e)
}

type ObjExprElem interface {
	isObjExprElem()
	Node
}

// TODO: rename these to CallableSig or something like that
func (*CallableExpr) isObjExprElem()    {}
func (*ConstructorExpr) isObjExprElem() {}
func (*MethodExpr) isObjExprElem()      {}
func (*GetterExpr) isObjExprElem()      {}
func (*SetterExpr) isObjExprElem()      {}
func (*PropertyExpr) isObjExprElem()    {}
func (*RestSpreadExpr) isObjExprElem()  {}

type CallableExpr struct {
	Fn   FuncExpr
	span Span
}

func (e *CallableExpr) Span() Span { return e.span }
func (e *CallableExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Fn.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type ConstructorExpr struct {
	Fn   FuncExpr
	span Span
}

func (e *ConstructorExpr) Span() Span { return e.span }
func (e *ConstructorExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Fn.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type MethodExpr struct {
	Name    ObjKey
	Fn      *FuncExpr
	MutSelf *bool // nil = no self, true = mut self, false = self
	span    Span
}

func NewMethod(name ObjKey, fn *FuncExpr, mutSelf *bool, span Span) *MethodExpr {
	return &MethodExpr{Name: name, Fn: fn, MutSelf: mutSelf, span: span}
}
func (e *MethodExpr) Span() Span { return e.span }
func (e *MethodExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Fn.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type GetterExpr struct {
	Name ObjKey
	Fn   *FuncExpr
	span Span
}

func NewGetter(name ObjKey, fn *FuncExpr, span Span) *GetterExpr {
	return &GetterExpr{Name: name, Fn: fn, span: span}
}
func (e *GetterExpr) Span() Span { return e.span }
func (e *GetterExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Fn.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type SetterExpr struct {
	Name ObjKey
	Fn   *FuncExpr
	span Span
}

func NewSetter(name ObjKey, fn *FuncExpr, span Span) *SetterExpr {
	return &SetterExpr{Name: name, Fn: fn, span: span}
}
func (e *SetterExpr) Span() Span { return e.span }
func (e *SetterExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Fn.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type PropertyExpr struct {
	Name     ObjKey
	Optional bool
	Readonly bool
	Value    Expr // optional
	span     Span
}

func NewProperty(name ObjKey, optional, readonly bool, value Expr, span Span) *PropertyExpr {
	return &PropertyExpr{Name: name, Optional: optional, Readonly: readonly, Value: value, span: span}
}
func (e *PropertyExpr) Span() Span { return e.span }
func (e *PropertyExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		switch key := e.Name.(type) {
		case *IdentExpr:
			// We don't want these keys to be treated as identifiers
			// key.Accept(v)
		case *StrLit:
			key.Accept(v)
		case *NumLit:
			key.Accept(v)
		case *ComputedKey:
			key.Accept(v)
		}

		if e.Value != nil {
			e.Value.Accept(v)
		}
	}
	v.ExitObjExprElem(e)
}

type RestSpreadExpr struct {
	Value Expr
	span  Span
}

func NewRestSpread(value Expr, span Span) *RestSpreadExpr {
	return &RestSpreadExpr{Value: value, span: span}
}
func (e *RestSpreadExpr) Span() Span { return e.span }
func (e *RestSpreadExpr) Accept(v Visitor) {
	if v.EnterObjExprElem(e) {
		e.Value.Accept(v)
	}
	v.ExitObjExprElem(e)
}

type ObjectExpr struct {
	Elems        []ObjExprElem
	span         Span
	inferredType Type
}

func NewObject(elems []ObjExprElem, span Span) *ObjectExpr {
	return &ObjectExpr{Elems: elems, span: span, inferredType: nil}
}
func (e *ObjectExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		for _, elem := range e.Elems {
			elem.Accept(v)
		}
	}
	v.ExitExpr(e)
}

type IfElseExpr struct {
	Cond         Expr
	Cons         Block
	Alt          *BlockOrExpr // optional
	span         Span
	inferredType Type
}

func NewIfElse(cond Expr, cons Block, alt *BlockOrExpr, span Span) *IfElseExpr {
	return &IfElseExpr{Cond: cond, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (e *IfElseExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Cond.Accept(v)
		e.Cons.Accept(v)
		if e.Alt != nil {
			alt := e.Alt
			if alt.Block != nil {
				alt.Block.Accept(v)
			}
			if alt.Expr != nil {
				alt.Expr.Accept(v)
			}
		}
	}
	v.ExitExpr(e)
}

type IfLetExpr struct {
	Pattern      Pat
	Target       Expr
	Cons         Block
	Alt          *BlockOrExpr // optional
	span         Span
	inferredType Type
}

func NewIfLet(pattern Pat, target Expr, cons Block, alt *BlockOrExpr, span Span) *IfLetExpr {
	return &IfLetExpr{Pattern: pattern, Target: target, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (e *IfLetExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Pattern.Accept(v)
		e.Target.Accept(v)
		e.Cons.Accept(v)
		if e.Alt != nil {
			alt := e.Alt
			if alt.Block != nil {
				alt.Block.Accept(v)
			}
			if alt.Expr != nil {
				alt.Expr.Accept(v)
			}
		}
	}
	v.ExitExpr(e)
}

type MatchCase struct {
	Pattern Pat
	Guard   Expr // optional
	Body    BlockOrExpr
	span    Span
}

func NewMatchCase(pattern Pat, guard Expr, body BlockOrExpr, span Span) *MatchCase {
	return &MatchCase{Pattern: pattern, Guard: guard, Body: body, span: span}
}

func (e *MatchCase) Span() Span { return e.span }

type MatchExpr struct {
	Target       Expr
	Cases        []*MatchCase
	span         Span
	inferredType Type
}

func NewMatch(target Expr, cases []*MatchCase, span Span) *MatchExpr {
	return &MatchExpr{Target: target, Cases: cases, span: span, inferredType: nil}
}
func (e *MatchExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Target.Accept(v)
		for _, matchCase := range e.Cases {
			matchCase.Pattern.Accept(v)
			if matchCase.Guard != nil {
				matchCase.Guard.Accept(v)
			}
			if matchCase.Body.Block != nil {
				matchCase.Body.Block.Accept(v)
			}
			if matchCase.Body.Expr != nil {
				matchCase.Body.Expr.Accept(v)
			}
		}
	}
	v.ExitExpr(e)
}

type AssignExpr struct {
	Left         Expr
	Right        Expr
	span         Span
	inferredType Type
}

func NewAssign(left, right Expr, span Span) *AssignExpr {
	return &AssignExpr{Left: left, Right: right, span: span, inferredType: nil}
}
func (e *AssignExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Left.Accept(v)
		e.Right.Accept(v)
	}
	v.ExitExpr(e)
}

type TryCatchExpr struct {
	Try          Block
	Catch        []*MatchCase // optional
	span         Span
	inferredType Type
}

func NewTryCatch(try Block, catch []*MatchCase, span Span) *TryCatchExpr {
	return &TryCatchExpr{Try: try, Catch: catch, span: span, inferredType: nil}
}
func (e *TryCatchExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Try.Accept(v)
		for _, matchCase := range e.Catch {
			matchCase.Pattern.Accept(v)
			if matchCase.Guard != nil {
				matchCase.Guard.Accept(v)
			}
			if matchCase.Body.Block != nil {
				matchCase.Body.Block.Accept(v)
			}
			if matchCase.Body.Expr != nil {
				matchCase.Body.Expr.Accept(v)
			}
		}
	}
	v.ExitExpr(e)
}

type ThrowExpr struct {
	Arg          Expr
	span         Span
	inferredType Type
}

func NewThrow(arg Expr, span Span) *ThrowExpr {
	return &ThrowExpr{Arg: arg, span: span, inferredType: nil}
}
func (e *ThrowExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Arg.Accept(v)
	}
	v.ExitExpr(e)
}

type DoExpr struct {
	Body         Block
	span         Span
	inferredType Type
}

func NewDo(body Block, span Span) *DoExpr {
	return &DoExpr{Body: body, span: span, inferredType: nil}
}
func (e *DoExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Body.Accept(v)
	}
	v.ExitExpr(e)
}

type AwaitExpr struct {
	Arg          Expr
	Throws       Type // filled in later
	span         Span
	inferredType Type
}

func NewAwait(arg Expr, span Span) *AwaitExpr {
	return &AwaitExpr{Arg: arg, Throws: nil, span: span, inferredType: nil}
}
func (e *AwaitExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Arg.Accept(v)
	}
	v.ExitExpr(e)
}

type TemplateLitExpr struct {
	Quasis       []*Quasi
	Exprs        []Expr
	span         Span
	inferredType Type
}

func NewTemplateLit(quasis []*Quasi, exprs []Expr, span Span) *TemplateLitExpr {
	return &TemplateLitExpr{Quasis: quasis, Exprs: exprs, span: span, inferredType: nil}
}
func (e *TemplateLitExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		for _, expr := range e.Exprs {
			expr.Accept(v)
		}
	}
	v.ExitExpr(e)
}

type TaggedTemplateLitExpr struct {
	Tag          Expr
	Quasis       []*Quasi
	Exprs        []Expr
	span         Span
	inferredType Type
}

func NewTaggedTemplateLit(tag Expr, quasis []*Quasi, exprs []Expr, span Span) *TaggedTemplateLitExpr {
	return &TaggedTemplateLitExpr{Tag: tag, Quasis: quasis, Exprs: exprs, span: span, inferredType: nil}
}
func (e *TaggedTemplateLitExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Tag.Accept(v)
		for _, expr := range e.Exprs {
			expr.Accept(v)
		}
	}
	v.ExitExpr(e)
}

type JSXElementExpr struct {
	Opening      *JSXOpening
	Closing      *JSXClosing // optional
	Children     []JSXChild
	span         Span
	inferredType Type
}

func NewJSXElement(opening *JSXOpening, closing *JSXClosing, children []JSXChild, span Span) *JSXElementExpr {
	return &JSXElementExpr{Opening: opening, Closing: closing, Children: children, span: span, inferredType: nil}
}
func (e *JSXElementExpr) Accept(v Visitor) {
	v.EnterExpr(e) // TODO: expand visitor to handle JSX
	v.ExitExpr(e)
}

type JSXFragmentExpr struct {
	Opening      *JSXOpening
	Closing      *JSXClosing
	Children     []JSXChild
	span         Span
	inferredType Type
}

func NewJSXFragment(opening *JSXOpening, closing *JSXClosing, children []JSXChild, span Span) *JSXFragmentExpr {
	return &JSXFragmentExpr{Opening: opening, Closing: closing, Children: children, span: span, inferredType: nil}
}
func (e *JSXFragmentExpr) Accept(v Visitor) {
	v.EnterExpr(e) // TODO: expand visitor to handle JSX
	v.ExitExpr(e)
}

type TypeCastExpr struct {
	Expr         Expr
	TypeAnn      TypeAnn
	span         Span
	inferredType Type
}

func NewTypeCast(expr Expr, typeAnn TypeAnn, span Span) *TypeCastExpr {
	return &TypeCastExpr{Expr: expr, TypeAnn: typeAnn, span: span, inferredType: nil}
}
func (e *TypeCastExpr) Accept(v Visitor) {
	if v.EnterExpr(e) {
		e.Expr.Accept(v)
		e.TypeAnn.Accept(v)
	}
	v.ExitExpr(e)
}

type Block struct {
	Stmts []Stmt
	Span  Span
}

func (b *Block) Accept(v Visitor) {
	if v.EnterBlock(*b) {
		for _, stmt := range b.Stmts {
			stmt.Accept(v)
		}
	}
	v.ExitBlock(*b)
}

type BlockOrExpr struct {
	Block *Block
	Expr  Expr
}
