package ast

import "github.com/moznion/go-optional"

type ObjExprElem interface{ isObjExprElem() }

func (*Callable[FuncExpr]) isObjExprElem()           {}
func (*Constructor[FuncExpr]) isObjExprElem()        {}
func (*Method[FuncExpr, ObjExprKey]) isObjExprElem() {}
func (*Getter[FuncExpr, ObjExprKey]) isObjExprElem() {}
func (*Setter[FuncExpr, ObjExprKey]) isObjExprElem() {}
func (*Property[Expr, ObjExprKey]) isObjExprElem()   {}
func (*RestSpread[Expr]) isObjExprElem()             {}

type ObjTypeElem interface{ isObjTypeElem() }

func (*Callable[FuncType]) isObjTypeElem()           {}
func (*Constructor[FuncType]) isObjTypeElem()        {}
func (*Method[FuncType, ObjTypeKey]) isObjTypeElem() {}
func (*Getter[FuncType, ObjTypeKey]) isObjTypeElem() {}
func (*Setter[FuncType, ObjTypeKey]) isObjTypeElem() {}
func (*Property[Type, ObjTypeKey]) isObjTypeElem()   {}
func (*Mapped[Type]) isObjTypeElem()                 {}
func (*RestSpread[Type]) isObjTypeElem()             {}

type ObjTypeAnnElem interface{ isObjTypeAnnElem() }

func (*Callable[FuncTypeAnn]) isObjTypeAnnElem()           {}
func (*Constructor[FuncTypeAnn]) isObjTypeAnnElem()        {}
func (*Method[FuncTypeAnn, ObjExprKey]) isObjTypeAnnElem() {}
func (*Getter[FuncTypeAnn, ObjExprKey]) isObjTypeAnnElem() {}
func (*Setter[FuncTypeAnn, ObjExprKey]) isObjTypeAnnElem() {}
func (*Property[TypeAnn, ObjExprKey]) isObjTypeAnnElem()   {}
func (*Mapped[TypeAnn]) isObjTypeAnnElem()                 {}
func (*RestSpread[TypeAnn]) isObjTypeAnnElem()             {}

type Callable[T any] struct{ Fn T }
type Constructor[T any] struct{ Fn T }
type Method[T any, PN any] struct {
	Name PN
	Fn   T
}
type Getter[T any, PN any] struct {
	Name PN
	Fn   T
}
type Setter[T any, PN any] struct {
	Name PN
	Fn   T
}

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

// TODO: include span
type Property[T any, PN any] struct {
	Name     PN
	Optional bool
	Readonly bool
	Value    T
}

func NewProperty[T any, PN any](name PN, value T) *Property[T, PN] {
	return &Property[T, PN]{
		Name:     name,
		Value:    value,
		Optional: false, // TODO
		Readonly: false, // TODO
	}
}

type IndexParam[T Node] struct {
	Name       string
	Constraint T
}

type Mapped[T Node] struct {
	TypeParam *IndexParam[T]
	Name      optional.Option[T]
	Value     T
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly  *MappedModifier
}

type RestSpread[T Node] struct {
	Value T
}

type ObjExprKey interface {
	isObjExprKey()
	Node
}

func (*IdentExpr) isObjExprKey()   {}
func (*StrLit) isObjExprKey()      {}
func (*NumLit) isObjExprKey()      {}
func (*ComputedKey) isObjExprKey() {}

type ComputedKey struct {
	Expr Expr
}

func NewComputedKey(expr Expr) *ComputedKey {
	return &ComputedKey{Expr: expr}
}
func (c *ComputedKey) Span() Span { return c.Expr.Span() }
