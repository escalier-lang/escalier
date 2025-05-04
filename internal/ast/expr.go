package ast

import (
	"math/big"

	"github.com/moznion/go-optional"
)

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

type IgnoreExpr struct {
	span         Span
	inferredType Type
}

func (e *IgnoreExpr) Span() Span             { return e.span }
func (e *IgnoreExpr) InferredType() Type     { return e.inferredType }
func (e *IgnoreExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *IgnoreExpr) Accept(v Visitor) {
	v.VisitExpr(e)
}

type EmptyExpr struct {
	span         Span
	inferredType Type
}

func NewEmpty(span Span) *EmptyExpr {
	return &EmptyExpr{span: span, inferredType: nil}
}
func (e *EmptyExpr) Span() Span           { return e.span }
func (*EmptyExpr) InferredType() Type     { return nil }
func (*EmptyExpr) SetInferredType(t Type) {}
func (e *EmptyExpr) Accept(v Visitor) {
	v.VisitExpr(e)
}

type BinaryOp string

const (
	Plus              BinaryOp = "+"
	Minus             BinaryOp = "-"
	Times             BinaryOp = "*"
	Divide            BinaryOp = "/"
	Modulo            BinaryOp = "%"
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
func (e *BinaryExpr) Span() Span             { return e.span }
func (e *BinaryExpr) InferredType() Type     { return e.inferredType }
func (e *BinaryExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *BinaryExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Left.Accept(v)
		e.Right.Accept(v)
	}
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
func (e *UnaryExpr) Span() Span             { return e.span }
func (e *UnaryExpr) InferredType() Type     { return e.inferredType }
func (e *UnaryExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *UnaryExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Arg.Accept(v)
	}
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
	v.VisitLit(l)
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
	v.VisitLit(l)
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
	v.VisitLit(l)
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
	v.VisitLit(l)
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
	v.VisitLit(l)
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
	v.VisitLit(l)
}

func (e *LiteralExpr) Span() Span             { return e.span }
func (e *LiteralExpr) InferredType() Type     { return e.inferredType }
func (e *LiteralExpr) SetInferredType(t Type) { e.inferredType = t }

func NewLitExpr(lit Lit) *LiteralExpr {
	return &LiteralExpr{Lit: lit, span: lit.Span(), inferredType: nil}
}
func (e *LiteralExpr) Accept(v Visitor) {
	v.VisitExpr(e)
}

type IdentExpr struct {
	Name         string
	span         Span
	inferredType Type
}

func NewIdent(name string, span Span) *IdentExpr {
	return &IdentExpr{Name: name, span: span, inferredType: nil}
}
func (e *IdentExpr) Span() Span             { return e.span }
func (e *IdentExpr) InferredType() Type     { return e.inferredType }
func (e *IdentExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *IdentExpr) Accept(v Visitor) {
	v.VisitExpr(e)
}

type TypeParam struct {
	Name       string
	Constraint optional.Option[TypeAnn]
	Default    optional.Option[TypeAnn]
}

func NewTypeParam(name string, constraint, defaultType optional.Option[TypeAnn]) TypeParam {
	return TypeParam{Name: name, Constraint: constraint, Default: defaultType}
}

type FuncSig struct {
	TypeParams []*TypeParam
	Params     []*Param
	Return     optional.Option[TypeAnn]
	Throws     optional.Option[TypeAnn]
}

type FuncExpr struct {
	FuncSig
	Body         Block
	span         Span
	inferredType Type
}

func NewFuncExpr(
	typeParams []*TypeParam,
	params []*Param,
	ret optional.Option[TypeAnn],
	throws optional.Option[TypeAnn],
	body Block,
	span Span,
) *FuncExpr {
	return &FuncExpr{
		FuncSig: FuncSig{
			TypeParams: typeParams,
			Params:     params,
			Return:     ret,
			Throws:     throws,
		},
		Body:         body,
		span:         span,
		inferredType: nil,
	}
}
func (e *FuncExpr) Span() Span             { return e.span }
func (e *FuncExpr) InferredType() Type     { return e.inferredType }
func (e *FuncExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *FuncExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, param := range e.Params {
			param.Pattern.Accept(v)
		}
		e.Return.IfSome(func(ret TypeAnn) {
			ret.Accept(v)
		})
		e.Throws.IfSome(func(throws TypeAnn) {
			throws.Accept(v)
		})
		for _, stmt := range e.Body.Stmts {
			stmt.Accept(v)
		}
	}
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
func (e *CallExpr) Span() Span             { return e.span }
func (e *CallExpr) InferredType() Type     { return e.inferredType }
func (e *CallExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *CallExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Callee.Accept(v)
		for _, arg := range e.Args {
			arg.Accept(v)
		}
	}
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
func (e *IndexExpr) Span() Span             { return e.span }
func (e *IndexExpr) InferredType() Type     { return e.inferredType }
func (e *IndexExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *IndexExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Object.Accept(v)
		e.Index.Accept(v)
	}
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
func (e *MemberExpr) Span() Span             { return e.span }
func (e *MemberExpr) InferredType() Type     { return e.inferredType }
func (e *MemberExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *MemberExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Object.Accept(v)
	}
}

type TupleExpr struct {
	Elems        []Expr
	span         Span
	inferredType Type
}

func NewArray(elems []Expr, span Span) *TupleExpr {
	return &TupleExpr{Elems: elems, span: span, inferredType: nil}
}
func (e *TupleExpr) Span() Span             { return e.span }
func (e *TupleExpr) InferredType() Type     { return e.inferredType }
func (e *TupleExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *TupleExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, elem := range e.Elems {
			elem.Accept(v)
		}
	}
}

type ObjExprElem interface {
	isObjExprElem()
	Node
}

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
	if v.VisitObjExprElem(e) {
		e.Fn.Accept(v)
	}
}

type ConstructorExpr struct {
	Fn   FuncExpr
	span Span
}

func (e *ConstructorExpr) Span() Span { return e.span }
func (e *ConstructorExpr) Accept(v Visitor) {
	if v.VisitObjExprElem(e) {
		e.Fn.Accept(v)
	}
}

type MethodExpr struct {
	Name ObjKey
	Fn   *FuncExpr
	span Span
}

func NewMethod(name ObjKey, fn *FuncExpr, span Span) *MethodExpr {
	return &MethodExpr{Name: name, Fn: fn, span: span}
}
func (e *MethodExpr) Span() Span { return e.span }
func (e *MethodExpr) Accept(v Visitor) {
	if v.VisitObjExprElem(e) {
		e.Fn.Accept(v)
	}
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
	if v.VisitObjExprElem(e) {
		e.Fn.Accept(v)
	}
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
	if v.VisitObjExprElem(e) {
		e.Fn.Accept(v)
	}
}

type PropertyExpr struct {
	Name     ObjKey
	Optional bool
	Readonly bool
	Value    optional.Option[Expr]
	span     Span
}

func NewProperty(name ObjKey, optional, readonly bool, value optional.Option[Expr], span Span) *PropertyExpr {
	return &PropertyExpr{Name: name, Optional: optional, Readonly: readonly, Value: value, span: span}
}
func (e *PropertyExpr) Span() Span { return e.span }
func (e *PropertyExpr) Accept(v Visitor) {
	if v.VisitObjExprElem(e) {
		e.Value.IfSome(func(value Expr) {
			value.Accept(v)
		})
	}
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
	if v.VisitObjExprElem(e) {
		e.Value.Accept(v)
	}
}

type ObjectExpr struct {
	Elems        []ObjExprElem
	span         Span
	inferredType Type
}

func NewObject(elems []ObjExprElem, span Span) *ObjectExpr {
	return &ObjectExpr{Elems: elems, span: span, inferredType: nil}
}
func (e *ObjectExpr) Span() Span             { return e.span }
func (e *ObjectExpr) InferredType() Type     { return e.inferredType }
func (e *ObjectExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *ObjectExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, elem := range e.Elems {
			elem.Accept(v)
		}
	}
}

type IfElseExpr struct {
	Cond         Expr
	Cons         Block
	Alt          optional.Option[BlockOrExpr]
	span         Span
	inferredType Type
}

func NewIfElse(cond Expr, cons Block, alt optional.Option[BlockOrExpr], span Span) *IfElseExpr {
	return &IfElseExpr{Cond: cond, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (e *IfElseExpr) Span() Span             { return e.span }
func (e *IfElseExpr) InferredType() Type     { return e.inferredType }
func (e *IfElseExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *IfElseExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Cond.Accept(v)
		for _, stmt := range e.Cons.Stmts {
			stmt.Accept(v)
		}
		e.Alt.IfSome(func(alt BlockOrExpr) {
			if alt.Block != nil {
				for _, stmt := range alt.Block.Stmts {
					stmt.Accept(v)
				}
			}
			if alt.Expr != nil {
				alt.Expr.Accept(v)
			}
		})
	}
}

type IfLetExpr struct {
	Pattern      Pat
	Target       Expr
	Cons         Block
	Alt          optional.Option[BlockOrExpr]
	span         Span
	inferredType Type
}

func NewIfLet(pattern Pat, target Expr, cons Block, alt optional.Option[BlockOrExpr], span Span) *IfLetExpr {
	return &IfLetExpr{Pattern: pattern, Target: target, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (e *IfLetExpr) Span() Span             { return e.span }
func (e *IfLetExpr) InferredType() Type     { return e.inferredType }
func (e *IfLetExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *IfLetExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Pattern.Accept(v)
		e.Target.Accept(v)
		for _, stmt := range e.Cons.Stmts {
			stmt.Accept(v)
		}
		e.Alt.IfSome(func(alt BlockOrExpr) {
			if alt.Block != nil {
				for _, stmt := range alt.Block.Stmts {
					stmt.Accept(v)
				}
			}
			if alt.Expr != nil {
				alt.Expr.Accept(v)
			}
		})
	}
}

type MatchCase struct {
	Pattern Pat
	Guard   optional.Option[Expr]
	Body    BlockOrExpr
	span    Span
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
func (e *MatchExpr) Span() Span             { return e.span }
func (e *MatchExpr) InferredType() Type     { return e.inferredType }
func (e *MatchExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *MatchExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Target.Accept(v)
		for _, matchCase := range e.Cases {
			matchCase.Pattern.Accept(v)
			matchCase.Guard.IfSome(func(guard Expr) {
				guard.Accept(v)
			})
			if matchCase.Body.Block != nil {
				for _, stmt := range matchCase.Body.Block.Stmts {
					stmt.Accept(v)
				}
			}
			if matchCase.Body.Expr != nil {
				matchCase.Body.Expr.Accept(v)
			}
		}
	}
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
func (e *AssignExpr) Span() Span             { return e.span }
func (e *AssignExpr) InferredType() Type     { return e.inferredType }
func (e *AssignExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *AssignExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Left.Accept(v)
		e.Right.Accept(v)
	}
}

type TryCatchExpr struct {
	Try          Block
	Catch        []*MatchCase // optional
	Finally      optional.Option[*Block]
	span         Span
	inferredType Type
}

func NewTryCatch(try Block, catch []*MatchCase, finally optional.Option[*Block], span Span) *TryCatchExpr {
	return &TryCatchExpr{Try: try, Catch: catch, Finally: finally, span: span, inferredType: nil}
}
func (e *TryCatchExpr) Span() Span             { return e.span }
func (e *TryCatchExpr) InferredType() Type     { return e.inferredType }
func (e *TryCatchExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *TryCatchExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, stmt := range e.Try.Stmts {
			stmt.Accept(v)
		}
		for _, matchCase := range e.Catch {
			matchCase.Pattern.Accept(v)
			matchCase.Guard.IfSome(func(guard Expr) {
				guard.Accept(v)
			})
			if matchCase.Body.Block != nil {
				for _, stmt := range matchCase.Body.Block.Stmts {
					stmt.Accept(v)
				}
			}
			if matchCase.Body.Expr != nil {
				matchCase.Body.Expr.Accept(v)
			}
		}
		e.Finally.IfSome(func(finally *Block) {
			for _, stmt := range finally.Stmts {
				stmt.Accept(v)
			}
		})
	}
}

type ThrowExpr struct {
	Arg          Expr
	span         Span
	inferredType Type
}

func NewThrow(arg Expr, span Span) *ThrowExpr {
	return &ThrowExpr{Arg: arg, span: span, inferredType: nil}
}
func (e *ThrowExpr) Span() Span             { return e.span }
func (e *ThrowExpr) InferredType() Type     { return e.inferredType }
func (e *ThrowExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *ThrowExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Arg.Accept(v)
	}
}

type DoExpr struct {
	Body         Block
	span         Span
	inferredType Type
}

func NewDo(body Block, span Span) *DoExpr {
	return &DoExpr{Body: body, span: span, inferredType: nil}
}
func (e *DoExpr) Span() Span             { return e.span }
func (e *DoExpr) InferredType() Type     { return e.inferredType }
func (e *DoExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *DoExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, stmt := range e.Body.Stmts {
			stmt.Accept(v)
		}
	}
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
func (e *AwaitExpr) Span() Span             { return e.span }
func (e *AwaitExpr) InferredType() Type     { return e.inferredType }
func (e *AwaitExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *AwaitExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Arg.Accept(v)
	}
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
func (e *TemplateLitExpr) Span() Span             { return e.span }
func (e *TemplateLitExpr) InferredType() Type     { return e.inferredType }
func (e *TemplateLitExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *TemplateLitExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		for _, expr := range e.Exprs {
			expr.Accept(v)
		}
	}
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
func (e *TaggedTemplateLitExpr) Span() Span             { return e.span }
func (e *TaggedTemplateLitExpr) InferredType() Type     { return e.inferredType }
func (e *TaggedTemplateLitExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *TaggedTemplateLitExpr) Accept(v Visitor) {
	if v.VisitExpr(e) {
		e.Tag.Accept(v)
		for _, expr := range e.Exprs {
			expr.Accept(v)
		}
	}
}

type JSXElementExpr struct {
	Opening      *JSXOpening
	Closing      optional.Option[*JSXClosing]
	Children     []JSXChild
	span         Span
	inferredType Type
}

func NewJSXElement(opening *JSXOpening, closing optional.Option[*JSXClosing], children []JSXChild, span Span) optional.Option[*JSXElementExpr] {
	return optional.Some(
		&JSXElementExpr{Opening: opening, Closing: closing, Children: children, span: span, inferredType: nil},
	)
}
func (e *JSXElementExpr) Span() Span             { return e.span }
func (e *JSXElementExpr) InferredType() Type     { return e.inferredType }
func (e *JSXElementExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *JSXElementExpr) Accept(v Visitor) {
	v.VisitExpr(e) // TODO: expand visitor to handle JSX
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
func (e *JSXFragmentExpr) Span() Span             { return e.span }
func (e *JSXFragmentExpr) InferredType() Type     { return e.inferredType }
func (e *JSXFragmentExpr) SetInferredType(t Type) { e.inferredType = t }
func (e *JSXFragmentExpr) Accept(v Visitor) {
	v.VisitExpr(e) // TODO: expand visitor to handle JSX
}

type Block struct {
	Stmts []Stmt
	Span  Span
}

type BlockOrExpr struct {
	Block *Block
	Expr  Expr
}
