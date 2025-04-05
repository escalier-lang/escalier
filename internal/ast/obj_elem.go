package ast

type ObjExprElem interface{ isObjExprElem() }

func (*Callable[FuncExpr]) isObjExprElem()                {}
func (*Constructor[FuncExpr]) isObjExprElem()             {}
func (*Method[FuncExpr, ObjExprPropName]) isObjExprElem() {}
func (*Getter[FuncExpr, ObjExprPropName]) isObjExprElem() {}
func (*Setter[FuncExpr, ObjExprPropName]) isObjExprElem() {}
func (*Property[Expr, ObjExprPropName]) isObjExprElem()   {}
func (*RestSpread[Expr]) isObjExprElem()                  {}

type ObjTypeElem interface{ isObjTypeElem() }

func (*Callable[FuncType]) isObjTypeElem()                {}
func (*Constructor[FuncType]) isObjTypeElem()             {}
func (*Method[FuncType, ObjTypePropName]) isObjTypeElem() {}
func (*Getter[FuncType, ObjTypePropName]) isObjTypeElem() {}
func (*Setter[FuncType, ObjTypePropName]) isObjTypeElem() {}
func (*Property[Type, ObjTypePropName]) isObjTypeElem()   {}
func (*Mapped[Type]) isObjTypeElem()                      {}
func (*RestSpread[Type]) isObjTypeElem()                  {}

type ObjTypeAnnElem interface{ isObjTypeAnnElem() }

func (*Callable[FuncTypeAnn]) isObjTypeAnnElem()                {}
func (*Constructor[FuncTypeAnn]) isObjTypeAnnElem()             {}
func (*Method[FuncTypeAnn, ObjExprPropName]) isObjTypeAnnElem() {}
func (*Getter[FuncTypeAnn, ObjExprPropName]) isObjTypeAnnElem() {}
func (*Setter[FuncTypeAnn, ObjExprPropName]) isObjTypeAnnElem() {}
func (*Property[TypeAnn, ObjExprPropName]) isObjTypeAnnElem()   {}
func (*Mapped[TypeAnn]) isObjTypeAnnElem()                      {}
func (*RestSpread[TypeAnn]) isObjTypeAnnElem()                  {}

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

type IndexParam[T any] struct {
	Name       string
	Constraint T
}

type Mapped[T any] struct {
	TypeParam *IndexParam[T]
	Name      T // optional, used for renaming keys
	Value     T
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly  *MappedModifier
}

type RestSpread[T any] struct {
	Value T
}

type ObjExprPropName interface{ isObjExprPropName() }

func (*IdentExpr) isObjExprPropName()        {}
func (*StrLit) isObjExprPropName()           {}
func (*NumLit) isObjExprPropName()           {}
func (*ComputedPropName) isObjExprPropName() {}

type ComputedPropName struct {
	Expr Expr
}

func NewComputedPropName(expr Expr) *ComputedPropName {
	return &ComputedPropName{Expr: expr}
}
