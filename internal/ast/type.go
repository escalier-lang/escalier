package ast

//sumtype:decl
type Type interface {
	isType()
	Provenance() *Provenance
	SetProvenance(*Provenance)
}

func (*TypeVarType) isType()      {}
func (*TypeRefType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*UniqueSymbolType) isType() {}
func (*KeywordType) isType()      {}
func (*FuncType) isType()         {}
func (*ObjectType) isType()       {}
func (*TupleType) isType()        {}
func (*RestSpreadType) isType()   {}
func (*UnionType) isType()        {}
func (*IntersectionType) isType() {}
func (*KeyOfType) isType()        {}
func (*IndexType) isType()        {}
func (*CondType) isType()         {}
func (*InferType) isType()        {}
func (*WildcardType) isType()     {}
func (*ExtractType) isType()      {}
func (*TemplateLitType) isType()  {}
func (*IntrinsicType) isType()    {}

type TypeVarType struct {
	ID         int
	Instance   Type
	provenance *Provenance
}

func (t *TypeVarType) Provenance() *Provenance     { return t.provenance }
func (t *TypeVarType) SetProvenance(p *Provenance) { t.provenance = p }

type TypeRefType struct {
	Name       string // TODO: Make this a qualified identifier
	TypeArgs   []Type
	TypeAlias  Type // resolved type alias (definition)
	provenance *Provenance
}

func (t *TypeRefType) Provenance() *Provenance     { return t.provenance }
func (t *TypeRefType) SetProvenance(p *Provenance) { t.provenance = p }

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

func (t *PrimType) Provenance() *Provenance     { return t.provenance }
func (t *PrimType) SetProvenance(p *Provenance) { t.provenance = p }

type LitType struct {
	Lit        *Lit
	provenance *Provenance
}

func (t *LitType) Provenance() *Provenance     { return t.provenance }
func (t *LitType) SetProvenance(p *Provenance) { t.provenance = p }

type UniqueSymbolType struct {
	Value      int
	provenance *Provenance
}

func (t *UniqueSymbolType) Provenance() *Provenance     { return t.provenance }
func (t *UniqueSymbolType) SetProvenance(p *Provenance) { t.provenance = p }

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
	Return     Type
	Throws     Type
	provenance *Provenance
}

func (t *FuncType) Provenance() *Provenance     { return t.provenance }
func (t *FuncType) SetProvenance(p *Provenance) { t.provenance = p }

type PropName interface{ isPropName() }

func (*StrPropName) isPropName()    {}
func (*NumPropName) isPropName()    {}
func (*SymbolPropName) isPropName() {}

type StrPropName struct{ Value string }
type NumPropName struct{ Value float64 }
type SymbolPropName struct{ Value int }

type ObjectType struct {
	Elems      []*ObjTypeElem
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nomimal    bool // true for classes
	Interface  bool
	Extends    []*TypeRefType
	Implements []*TypeRefType
	provenance *Provenance
}

func (t *ObjectType) Provenance() *Provenance     { return t.provenance }
func (t *ObjectType) SetProvenance(p *Provenance) { t.provenance = p }

type TupleType struct {
	Elems      []Type
	provenance *Provenance
}

func (t *TupleType) Provenance() *Provenance     { return t.provenance }
func (t *TupleType) SetProvenance(p *Provenance) { t.provenance = p }

type RestSpreadType struct {
	Type       Type
	provenance *Provenance
}

func (t *RestSpreadType) Provenance() *Provenance     { return t.provenance }
func (t *RestSpreadType) SetProvenance(p *Provenance) { t.provenance = p }

type UnionType struct {
	Types      []Type
	provenance *Provenance
}

func (t *UnionType) Provenance() *Provenance     { return t.provenance }
func (t *UnionType) SetProvenance(p *Provenance) { t.provenance = p }

type IntersectionType struct {
	Types      []Type
	provenance *Provenance
}

func (t *IntersectionType) Provenance() *Provenance     { return t.provenance }
func (t *IntersectionType) SetProvenance(p *Provenance) { t.provenance = p }

type KeyOfType struct {
	Type       Type
	provenance *Provenance
}

func (t *KeyOfType) Provenance() *Provenance     { return t.provenance }
func (t *KeyOfType) SetProvenance(p *Provenance) { t.provenance = p }

type IndexType struct {
	Target     Type
	Index      Type
	provenance *Provenance
}

func (t *IndexType) Provenance() *Provenance     { return t.provenance }
func (t *IndexType) SetProvenance(p *Provenance) { t.provenance = p }

type CondType struct {
	Check      Type
	Extends    Type
	Cons       Type
	Alt        Type
	provenance *Provenance
}

func (t *CondType) Provenance() *Provenance     { return t.provenance }
func (t *CondType) SetProvenance(p *Provenance) { t.provenance = p }

type InferType struct {
	Name       string
	provenance *Provenance
}

func (t *InferType) Provenance() *Provenance     { return t.provenance }
func (t *InferType) SetProvenance(p *Provenance) { t.provenance = p }

type WildcardType struct {
	provenance *Provenance
}

func (t *WildcardType) Provenance() *Provenance     { return t.provenance }
func (t *WildcardType) SetProvenance(p *Provenance) { t.provenance = p }

type ExtractType struct {
	Extractor  Type
	Args       []Type
	provenance *Provenance
}

func (t *ExtractType) Provenance() *Provenance     { return t.provenance }
func (t *ExtractType) SetProvenance(p *Provenance) { t.provenance = p }

type TemplateLitType struct {
	Quasis     []*Quasi
	Types      []Type
	provenance *Provenance
}

func (t *TemplateLitType) Provenance() *Provenance     { return t.provenance }
func (t *TemplateLitType) SetProvenance(p *Provenance) { t.provenance = p }

type IntrinsicType struct {
	Name       string
	provenance *Provenance
}

func (t *IntrinsicType) Provenance() *Provenance     { return t.provenance }
func (t *IntrinsicType) SetProvenance(p *Provenance) { t.provenance = p }

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
