package ast

//sumtype:decl
type Type interface {
	isType()
	Provenance() *Provenance
	SetProvenance(*Provenance)
}

type TVar struct {
	ID         int
	Instance   Type
	provenance *Provenance
}

func (*TVar) isType()                       {}
func (t *TVar) Provenance() *Provenance     { return t.provenance }
func (t *TVar) SetProvenance(p *Provenance) { t.provenance = p }

type TypeRef struct {
	Name       string // TODO: Make this a qualified identifier
	TypeArgs   []Type
	TypeAlias  Type // resolved type alias (definition)
	provenance *Provenance
}

func (*TypeRef) isType()                       {}
func (t *TypeRef) Provenance() *Provenance     { return t.provenance }
func (t *TypeRef) SetProvenance(p *Provenance) { t.provenance = p }

type Prim string

const (
	BoolPrim   Prim = "boolean"
	NumPrim    Prim = "number"
	StrPrim    Prim = "string"
	BigIntPrim Prim = "bigint"
	SymbolPrim Prim = "symbol"
)

type PrimType struct {
	Prim       Prim
	provenance *Provenance
}

func (*PrimType) isType()                       {}
func (t *PrimType) Provenance() *Provenance     { return t.provenance }
func (t *PrimType) SetProvenance(p *Provenance) { t.provenance = p }

type LitType struct {
	Lit        *Lit
	provenance *Provenance
}

func (*LitType) isType()                       {}
func (t *LitType) Provenance() *Provenance     { return t.provenance }
func (t *LitType) SetProvenance(p *Provenance) { t.provenance = p }

type Keyword string

const (
	KObject     Keyword = "object"
	KUnknown    Keyword = "unknown"
	KNever      Keyword = "never"
	KGlobalThis Keyword = "globalThis"
)

type KeywordType struct {
	Keyword    Keyword
	provenance *Provenance
}

func (*KeywordType) isType()                       {}
func (t *KeywordType) Provenance() *Provenance     { return t.provenance }
func (t *KeywordType) SetProvenance(p *Provenance) { t.provenance = p }

type TypeParam struct {
	Name       string
	Constraint Type
	Default    Type
}

type FuncParam struct {
	Name     string // TODO: update to Pattern
	Type     Type
	Optional bool
	Default  *Expr
}

type FuncType struct {
	TypeParams []*TypeParam
	Self       Type
	Params     []*FuncParam
	Ret        Type
	Throws     Type
	provenance *Provenance
}

func (*FuncType) isType()                       {}
func (t *FuncType) Provenance() *Provenance     { return t.provenance }
func (t *FuncType) SetProvenance(p *Provenance) { t.provenance = p }

type PropName interface{ isPropName() }

func (*StrPropName) isPropName()    {}
func (*NumPropName) isPropName()    {}
func (*SymbolPropName) isPropName() {}

type StrPropName struct{ Value string }
type NumPropName struct{ Value float64 }
type SymbolPropName struct{ Value int }

type ObjTypeElem interface{ isObjTypeElem() }

func (*Callable) isObjTypeElem()    {}
func (*Constructor) isObjTypeElem() {}
func (*Method) isObjTypeElem()      {}
func (*Getter) isObjTypeElem()      {}
func (*Setter) isObjTypeElem()      {}
func (*Mapped) isObjTypeElem()      {}
func (*Property) isObjTypeElem()    {}
func (*RestSpread) isObjTypeElem()  {}

type Callable struct{ Fn FuncType }
type Constructor struct{ Fn FuncType }
type Method struct {
	Name string // TODO: use PropName
	Fn   FuncType
}
type Getter struct {
	Name string // TODO: use PropName
	Fn   FuncType
}
type Setter struct {
	Name string // TODO: use PropName
	Fn   FuncType
}
type IndexParam struct {
	Name       string
	Constraint Type
}

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

type Mapped struct {
	TypeParam *IndexParam
	NameType  Type
	TypeAnn   Type
	Optional  *MappedModifier
	ReadOnly  *MappedModifier
}
type Property struct {
	Name     string // TODO: use PropName
	Optional bool
	Readonly bool
	Type     Type
}
type RestSpread struct {
	Type Type
}

type ObjectType struct {
	Elems      []*ObjTypeElem
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nomimal    bool // true for classes
	Interface  bool
	Extends    *[]*TypeRef
	Implements *[]*TypeRef
	provenance *Provenance
}

func (*ObjectType) isType()                       {}
func (t *ObjectType) Provenance() *Provenance     { return t.provenance }
func (t *ObjectType) SetProvenance(p *Provenance) { t.provenance = p }

type TupleType struct {
	Elems      []Type
	provenance *Provenance
}

func (*TupleType) isType()                       {}
func (t *TupleType) Provenance() *Provenance     { return t.provenance }
func (t *TupleType) SetProvenance(p *Provenance) { t.provenance = p }

//sumtype:decl
type Provenance interface{ isProvenance() }

func (*TypeProvenance) isProvenance() {}
func (*ExprProvenance) isProvenance() {}

type TypeProvenance struct {
	Type Type
}

type ExprProvenance struct {
	Expr *Expr
}
