package ast

type ObjExprElem interface{ isObjExprElem() }

func (*Callable[FuncExpr]) isObjExprElem()    {}
func (*Constructor[FuncExpr]) isObjExprElem() {}
func (*Method[FuncExpr]) isObjExprElem()      {}
func (*Getter[FuncExpr]) isObjExprElem()      {}
func (*Setter[FuncExpr]) isObjExprElem()      {}
func (*Property[Expr]) isObjExprElem()        {}
func (*RestSpread[Expr]) isObjExprElem()      {}

type ObjTypeElem interface{ isObjTypeElem() }

func (*Callable[FuncType]) isObjTypeElem()    {}
func (*Constructor[FuncType]) isObjTypeElem() {}
func (*Method[FuncType]) isObjTypeElem()      {}
func (*Getter[FuncType]) isObjTypeElem()      {}
func (*Setter[FuncType]) isObjTypeElem()      {}
func (*Mapped[Type]) isObjTypeElem()          {}
func (*Property[Type]) isObjTypeElem()        {}
func (*RestSpread[Type]) isObjTypeElem()      {}

type ObjTypeAnnElem interface{ isObjTypeAnnElem() }

func (*Callable[FuncTypeAnn]) isObjTypeAnnElem()    {}
func (*Constructor[FuncTypeAnn]) isObjTypeAnnElem() {}
func (*Method[FuncTypeAnn]) isObjTypeAnnElem()      {}
func (*Getter[FuncTypeAnn]) isObjTypeAnnElem()      {}
func (*Setter[FuncTypeAnn]) isObjTypeAnnElem()      {}
func (*Mapped[TypeAnn]) isObjTypeAnnElem()          {}
func (*Property[TypeAnn]) isObjTypeAnnElem()        {}
func (*RestSpread[TypeAnn]) isObjTypeAnnElem()      {}

type Callable[T any] struct{ Fn T }
type Constructor[T any] struct{ Fn T }
type Method[T any] struct {
	Name string // TODO: use PropName
	Fn   T
}
type Getter[T any] struct {
	Name string // TODO: use PropName
	Fn   T
}
type Setter[T any] struct {
	Name string // TODO: use PropName
	Fn   T
}
type IndexParam[T any] struct {
	Name       string
	Constraint T
}

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

type Mapped[T any] struct {
	TypeParam *IndexParam[T]
	Name      T // optional, used for renaming keys
	Value     T
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly  *MappedModifier
}
type Property[T any] struct {
	Name     string // TODO: use PropName
	Optional bool
	Readonly bool
	Value    T
}
type RestSpread[T any] struct {
	Value T
}
