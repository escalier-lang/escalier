package ast

import "math/big"

// func NewExpr(kind ExprKind, span Span) *Expr {
// 	return &Expr{Kind: kind, span: span}
// }

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type Expr interface {
	isExpr()
	Node
	Inferrable
}

type BinaryOp int

const (
	Plus              BinaryOp = iota // +
	Minus                             // -
	Times                             // *
	Divide                            // /
	Modulo                            // %
	LessThan                          // <
	LessThanEqual                     // <=
	GreaterThan                       // >
	GreaterThanEqual                  // >=
	Equal                             // ==
	NotEqual                          // !=
	LogicalAnd                        // &&
	LogicalOr                         // ||
	NullishCoalescing                 // ??
)

type BinaryExpr struct {
	Left         Expr
	Op           BinaryOp
	Right        Expr
	span         Span
	inferredType *Type
}

func NewBinary(left, right Expr, op BinaryOp, span Span) *BinaryExpr {
	return &BinaryExpr{Left: left, Right: right, Op: op, span: span, inferredType: nil}
}
func (*BinaryExpr) isExpr()                   {}
func (e *BinaryExpr) Span() Span              { return e.span }
func (e *BinaryExpr) InferredType() *Type     { return e.inferredType }
func (e *BinaryExpr) SetInferredType(t *Type) { e.inferredType = t }

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
	inferredType *Type
}

func NewUnary(op UnaryOp, arg Expr, span Span) *UnaryExpr {
	return &UnaryExpr{Op: op, Arg: arg, span: span, inferredType: nil}
}
func (*UnaryExpr) isExpr()                   {}
func (e *UnaryExpr) Span() Span              { return e.span }
func (e *UnaryExpr) InferredType() *Type     { return e.inferredType }
func (e *UnaryExpr) SetInferredType(t *Type) { e.inferredType = t }

//sumtype:decl
type Lit interface{ isLiteral() }

func (*BoolLit) isLiteral()      {}
func (*NumLit) isLiteral()       {}
func (*StrLit) isLiteral()       {}
func (*BigIntLit) isLiteral()    {}
func (*NullLit) isLiteral()      {}
func (*UndefinedLit) isLiteral() {}

type BoolLit struct{ Value bool }
type NumLit struct{ Value float64 }
type StrLit struct{ Value string }
type BigIntLit struct{ Value big.Int }
type NullLit struct{}
type UndefinedLit struct{}

type LiteralExpr struct {
	Lit          Lit
	span         Span
	inferredType *Type
}

func NewNumber(value float64, span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &NumLit{Value: value}, span: span, inferredType: nil}
}
func NewString(value string, span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &StrLit{Value: value}, span: span, inferredType: nil}
}
func NewBoolean(value bool, span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &BoolLit{Value: value}, span: span, inferredType: nil}
}
func NewBigInt(value big.Int, span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &BigIntLit{Value: value}, span: span, inferredType: nil}
}
func NewNull(span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &NullLit{}, span: span, inferredType: nil}
}
func NewUndefined(span Span) *LiteralExpr {
	return &LiteralExpr{Lit: &UndefinedLit{}, span: span, inferredType: nil}
}

func (*LiteralExpr) isExpr()                   {}
func (e *LiteralExpr) Span() Span              { return e.span }
func (e *LiteralExpr) InferredType() *Type     { return e.inferredType }
func (e *LiteralExpr) SetInferredType(t *Type) { e.inferredType = t }

type IdentExpr struct {
	Name         string
	span         Span
	inferredType *Type
}

func NewIdent(name string, span Span) *IdentExpr {
	return &IdentExpr{Name: name, span: span, inferredType: nil}
}
func (*IdentExpr) isExpr()                   {}
func (e *IdentExpr) Span() Span              { return e.span }
func (e *IdentExpr) InferredType() *Type     { return e.inferredType }
func (e *IdentExpr) SetInferredType(t *Type) { e.inferredType = t }

type CallExpr struct {
	Callee       Expr
	Args         []Expr
	OptChain     bool
	span         Span
	inferredType *Type
}

func NewCall(callee Expr, args []Expr, optChain bool, span Span) *CallExpr {
	return &CallExpr{Callee: callee, Args: args, OptChain: optChain, span: span, inferredType: nil}
}
func (*CallExpr) isExpr()                   {}
func (e *CallExpr) Span() Span              { return e.span }
func (e *CallExpr) InferredType() *Type     { return e.inferredType }
func (e *CallExpr) SetInferredType(t *Type) { e.inferredType = t }

type IndexExpr struct {
	Object       Expr
	Index        Expr
	OptChain     bool
	span         Span
	inferredType *Type
}

func NewIndex(object, index Expr, optChain bool, span Span) *IndexExpr {
	return &IndexExpr{Object: object, Index: index, OptChain: optChain, span: span, inferredType: nil}
}
func (*IndexExpr) isExpr()                   {}
func (e *IndexExpr) Span() Span              { return e.span }
func (e *IndexExpr) InferredType() *Type     { return e.inferredType }
func (e *IndexExpr) SetInferredType(t *Type) { e.inferredType = t }

type MemberExpr struct {
	Object       Expr
	Prop         *Ident
	OptChain     bool
	span         Span
	inferredType *Type
}

func NewMember(object Expr, prop *Ident, optChain bool, span Span) *MemberExpr {
	return &MemberExpr{Object: object, Prop: prop, OptChain: optChain, span: span, inferredType: nil}
}
func (*MemberExpr) isExpr()                   {}
func (e *MemberExpr) Span() Span              { return e.span }
func (e *MemberExpr) InferredType() *Type     { return e.inferredType }
func (e *MemberExpr) SetInferredType(t *Type) { e.inferredType = t }

type EArray struct {
	Elems        []Expr
	span         Span
	inferredType *Type
}

func NewArray(elems []Expr, span Span) *EArray {
	return &EArray{Elems: elems, span: span, inferredType: nil}
}
func (*EArray) isExpr()                   {}
func (e *EArray) Span() Span              { return e.span }
func (e *EArray) InferredType() *Type     { return e.inferredType }
func (e *EArray) SetInferredType(t *Type) { e.inferredType = t }

type IgnoreExpr struct {
	span         Span
	inferredType *Type
}

func (*IgnoreExpr) isExpr()                   {}
func (e *IgnoreExpr) Span() Span              { return e.span }
func (e *IgnoreExpr) InferredType() *Type     { return e.inferredType }
func (e *IgnoreExpr) SetInferredType(t *Type) { e.inferredType = t }

type EmptyExpr struct {
	span         Span
	inferredType *Type
}

func NewEmpty(span Span) *EmptyExpr {
	return &EmptyExpr{span: span, inferredType: nil}
}
func (*EmptyExpr) isExpr()                 {}
func (e *EmptyExpr) Span() Span            { return e.span }
func (*EmptyExpr) InferredType() *Type     { return nil }
func (*EmptyExpr) SetInferredType(t *Type) {}
