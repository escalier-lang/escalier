package ast

import (
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/moznion/go-optional"
)

//sumtype:decl
type Type interface {
	isType()
	Provenance() *Provenance
	SetProvenance(*Provenance)
	Equal(Type) bool
	Accept(TypeVisitor)
}

func (*TypeVarType) isType()      {}
func (*TypeRefType) isType()      {}
func (*PrimType) isType()         {}
func (*LitType) isType()          {}
func (*UniqueSymbolType) isType() {}
func (*UnknownType) isType()      {}
func (*NeverType) isType()        {}
func (*GlobalThisType) isType()   {}
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

func Prune(t Type) Type {
	switch t := t.(type) {
	case *TypeVarType:
		if t.Instance != nil {
			newInstance := Prune(t.Instance)
			t.Instance = newInstance
			return newInstance
		}
		return t
	default:
		return t
	}
}

type TypeVarType struct {
	ID         int
	Instance   Type
	provenance *Provenance
}

func (t *TypeVarType) Provenance() *Provenance     { return t.provenance }
func (t *TypeVarType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *TypeVarType) Equal(other Type) bool {
	if other, ok := other.(*TypeVarType); ok {
		return t.ID == other.ID
	}
	return false
}
func (t *TypeVarType) Accept(v TypeVisitor) {
	v.VisitType(Prune(t))
}

type TypeRefType struct {
	Name       string // TODO: Make this a qualified identifier
	TypeArgs   []Type
	TypeAlias  Type // resolved type alias (definition)
	provenance *Provenance
}

func (t *TypeRefType) Provenance() *Provenance     { return t.provenance }
func (t *TypeRefType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *TypeRefType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *TypeRefType) Equal(other Type) bool {
	if other, ok := other.(*TypeRefType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TypeRefType{}, "provenance"))
	}
	return false
}

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

func NewNumType() *PrimType {
	return &PrimType{
		Prim:       NumPrim,
		provenance: nil,
	}
}
func NewStrType() *PrimType {
	return &PrimType{
		Prim:       StrPrim,
		provenance: nil,
	}
}
func NewBoolType() *PrimType {
	return &PrimType{
		Prim:       BoolPrim,
		provenance: nil,
	}
}
func (t *PrimType) Provenance() *Provenance     { return t.provenance }
func (t *PrimType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *PrimType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *PrimType) Equal(other Type) bool {
	if other, ok := other.(*PrimType); ok {
		return t.Prim == other.Prim
	}
	return false
}

type LitType struct {
	Lit        Lit
	provenance *Provenance
}

func (t *LitType) Provenance() *Provenance     { return t.provenance }
func (t *LitType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *LitType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *LitType) Equal(other Type) bool {
	if other, ok := other.(*LitType); ok {
		return t.Lit.Equal(other.Lit)
	}
	return false
}

type UniqueSymbolType struct {
	Value      int
	provenance *Provenance
}

func (t *UniqueSymbolType) Provenance() *Provenance     { return t.provenance }
func (t *UniqueSymbolType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *UniqueSymbolType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *UniqueSymbolType) Equal(other Type) bool {
	if other, ok := other.(*UniqueSymbolType); ok {
		return t.Value == other.Value
	}
	return false
}

type UnknownType struct {
	provenance *Provenance
}

func (t *UnknownType) Provenance() *Provenance     { return t.provenance }
func (t *UnknownType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *UnknownType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *UnknownType) Equal(other Type) bool {
	if _, ok := other.(*UnknownType); ok {
		return true
	}
	return false
}

type NeverType struct {
	provenance *Provenance
}

func NewNeverType() *NeverType                   { return &NeverType{provenance: nil} }
func (t *NeverType) Provenance() *Provenance     { return t.provenance }
func (t *NeverType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *NeverType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *NeverType) Equal(other Type) bool {
	if _, ok := other.(*NeverType); ok {
		return true
	}
	return false
}

type GlobalThisType struct {
	provenance *Provenance
}

func (t *GlobalThisType) Provenance() *Provenance     { return t.provenance }
func (t *GlobalThisType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *GlobalThisType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *GlobalThisType) Equal(other Type) bool {
	if _, ok := other.(*GlobalThisType); ok {
		return true
	}
	return false
}

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

func NewFuncParam(name string, typ Type) *FuncParam {
	return &FuncParam{
		Name:     name,
		Type:     typ,
		Optional: false,
		Default:  nil,
	}
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
func (t *FuncType) Accept(v TypeVisitor) {
	for _, param := range t.Params {
		param.Type.Accept(v)
	}
	if t.Return != nil {
		t.Return.Accept(v)
	}
	if t.Throws != nil {
		t.Throws.Accept(v)
	}
	v.VisitType(t)
}
func (t *FuncType) Equal(other Type) bool {
	if other, ok := other.(*FuncType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(FuncType{}, "provenance"))
	}
	return false
}

type ObjTypeKey interface{ isObjTypeKey() }

func (*StrObjTypeKey) isObjTypeKey()    {}
func (*NumObjTypeKey) isObjTypeKey()    {}
func (*SymbolObjTypeKey) isObjTypeKey() {}

type StrObjTypeKey struct{ Value string }
type NumObjTypeKey struct{ Value float64 }
type SymbolObjTypeKey struct{ Value int }

type ObjTypeElem interface {
	isObjTypeElem()
	Accept(TypeVisitor)
}

type CallableElemType struct{ Fn Type }
type ConstructorElemType struct{ Fn Type }
type MethodElemType struct {
	Name ObjTypeKey
	Fn   Type
}
type GetterElemType struct {
	Name ObjTypeKey
	Fn   Type
}
type SetterElemType struct {
	Name ObjTypeKey
	Fn   Type
}
type PropertyElemType struct {
	Name     ObjTypeKey
	Optional bool
	Readonly bool
	Value    optional.Option[Type]
}
type MappedElemType struct {
	TypeParam *IndexParamType
	Name      optional.Option[Type]
	Value     Type
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly  *MappedModifier
}
type IndexParamType struct {
	Name       string
	Constraint Type
}
type RestSpreadElemType struct{ Value Type }

func (*CallableElemType) isObjTypeElem()    {}
func (*ConstructorElemType) isObjTypeElem() {}
func (*MethodElemType) isObjTypeElem()      {}
func (*GetterElemType) isObjTypeElem()      {}
func (*SetterElemType) isObjTypeElem()      {}
func (*PropertyElemType) isObjTypeElem()    {}
func (*MappedElemType) isObjTypeElem()      {}
func (*RestSpreadElemType) isObjTypeElem()  {}

func (c *CallableElemType) Accept(v TypeVisitor) {
	c.Fn.Accept(v)
}
func (c *ConstructorElemType) Accept(v TypeVisitor) {
	c.Fn.Accept(v)
}
func (m *MethodElemType) Accept(v TypeVisitor) {
	m.Fn.Accept(v)
}
func (g *GetterElemType) Accept(v TypeVisitor) {
	g.Fn.Accept(v)
}
func (s *SetterElemType) Accept(v TypeVisitor) {
	s.Fn.Accept(v)
}
func (p *PropertyElemType) Accept(v TypeVisitor) {
	p.Value.IfSome(func(value Type) {
		value.Accept(v)
	})
}
func (m *MappedElemType) Accept(v TypeVisitor) {
	m.TypeParam.Constraint.Accept(v)
	m.Name.IfSome(func(name Type) {
		name.Accept(v)
	})
	m.Value.Accept(v)
}
func (r *RestSpreadElemType) Accept(v TypeVisitor) {
	r.Value.Accept(v)
}

type ObjectType struct {
	Elems      []optional.Option[ObjTypeElem]
	Exact      bool // Can't be true if any of Interface, Implements, or Extends are true
	Immutable  bool // true for `#{...}`, false for `{...}`
	Mutable    bool // true for `mut {...}`, false for `{...}`
	Nomimal    bool // true for classes
	Interface  bool
	Extends    []*TypeRefType
	Implements []*TypeRefType
	provenance *Provenance // TODO: use optional.Option for this
}

func (t *ObjectType) Provenance() *Provenance     { return t.provenance }
func (t *ObjectType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *ObjectType) Accept(v TypeVisitor) {
	for _, elem := range t.Elems {
		elem.IfSome(func(e ObjTypeElem) {
			e.Accept(v)
		})
	}
	for _, ext := range t.Extends {
		ext.Accept(v)
	}
	for _, impl := range t.Implements {
		impl.Accept(v)
	}
	v.VisitType(t)
}
func (t *ObjectType) Equal(other Type) bool {
	if other, ok := other.(*ObjectType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(ObjectType{}, "provenance"))
	}
	return false
}

type TupleType struct {
	Elems      []Type
	provenance *Provenance
}

func (t *TupleType) Provenance() *Provenance     { return t.provenance }
func (t *TupleType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *TupleType) Accept(v TypeVisitor) {
	for _, elem := range t.Elems {
		elem.Accept(v)
	}
	v.VisitType(t)
}
func (t *TupleType) Equal(other Type) bool {
	if other, ok := other.(*TupleType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TupleType{}, "provenance"))
	}
	return false
}

type RestSpreadType struct {
	Type       Type
	provenance *Provenance
}

func (t *RestSpreadType) Provenance() *Provenance     { return t.provenance }
func (t *RestSpreadType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *RestSpreadType) Accept(v TypeVisitor) {
	v.VisitType(t)
	t.Type.Accept(v)
}
func (t *RestSpreadType) Equal(other Type) bool {
	if other, ok := other.(*RestSpreadType); ok {
		return t.Type.Equal(other.Type)
	}
	return false
}

type UnionType struct {
	Types      []Type
	provenance *Provenance
}

func (t *UnionType) Provenance() *Provenance     { return t.provenance }
func (t *UnionType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *UnionType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
}
func (t *UnionType) Equal(other Type) bool {
	if other, ok := other.(*UnionType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(UnionType{}, "provenance"))
	}
	return false
}

type IntersectionType struct {
	Types      []Type
	provenance *Provenance
}

func NewIntersectionType(types ...Type) *IntersectionType {
	return &IntersectionType{
		Types:      types,
		provenance: nil,
	}
}
func (t *IntersectionType) Provenance() *Provenance     { return t.provenance }
func (t *IntersectionType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *IntersectionType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
}
func (t *IntersectionType) Equal(other Type) bool {
	if other, ok := other.(*IntersectionType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(IntersectionType{}, "provenance"))
	}
	return false
}

type KeyOfType struct {
	Type       Type
	provenance *Provenance
}

func (t *KeyOfType) Provenance() *Provenance     { return t.provenance }
func (t *KeyOfType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *KeyOfType) Accept(v TypeVisitor) {
	t.Type.Accept(v)
	v.VisitType(t)
}
func (t *KeyOfType) Equal(other Type) bool {
	if other, ok := other.(*KeyOfType); ok {
		return t.Type.Equal(other.Type)
	}
	return false
}

type IndexType struct {
	Target     Type
	Index      Type
	provenance *Provenance
}

func (t *IndexType) Provenance() *Provenance     { return t.provenance }
func (t *IndexType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *IndexType) Accept(v TypeVisitor) {
	t.Target.Accept(v)
	t.Index.Accept(v)
	v.VisitType(t)
}
func (t *IndexType) Equal(other Type) bool {
	if other, ok := other.(*IndexType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(IndexType{}, "provenance"))
	}
	return false
}

type CondType struct {
	Check      Type
	Extends    Type
	Cons       Type
	Alt        Type
	provenance *Provenance
}

func (t *CondType) Provenance() *Provenance     { return t.provenance }
func (t *CondType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *CondType) Accept(v TypeVisitor) {
	t.Check.Accept(v)
	t.Extends.Accept(v)
	t.Cons.Accept(v)
	t.Alt.Accept(v)
	v.VisitType(t)
}
func (t *CondType) Equal(other Type) bool {
	if other, ok := other.(*CondType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(CondType{}, "provenance"))
	}
	return false
}

type InferType struct {
	Name       string
	provenance *Provenance
}

func (t *InferType) Provenance() *Provenance     { return t.provenance }
func (t *InferType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *InferType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *InferType) Equal(other Type) bool {
	if other, ok := other.(*InferType); ok {
		return t.Name == other.Name
	}
	return false
}

type WildcardType struct {
	provenance *Provenance
}

func (t *WildcardType) Provenance() *Provenance     { return t.provenance }
func (t *WildcardType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *WildcardType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *WildcardType) Equal(other Type) bool {
	if _, ok := other.(*WildcardType); ok {
		return true
	}
	return false
}

type ExtractType struct {
	Extractor  Type
	Args       []Type
	provenance *Provenance
}

func (t *ExtractType) Provenance() *Provenance     { return t.provenance }
func (t *ExtractType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *ExtractType) Accept(v TypeVisitor) {
	t.Extractor.Accept(v)
	for _, arg := range t.Args {
		arg.Accept(v)
	}
	v.VisitType(t)
}
func (t *ExtractType) Equal(other Type) bool {
	if other, ok := other.(*ExtractType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(ExtractType{}, "provenance"))
	}
	return false
}

type TemplateLitType struct {
	Quasis     []*Quasi
	Types      []Type
	provenance *Provenance
}

func (t *TemplateLitType) Provenance() *Provenance     { return t.provenance }
func (t *TemplateLitType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *TemplateLitType) Accept(v TypeVisitor) {
	for _, typ := range t.Types {
		typ.Accept(v)
	}
	v.VisitType(t)
}
func (t *TemplateLitType) Equal(other Type) bool {
	if other, ok := other.(*TemplateLitType); ok {
		// nolint: exhaustruct
		return cmp.Equal(t, other, cmpopts.IgnoreFields(TemplateLitType{}, "provenance"))
	}
	return false
}

type IntrinsicType struct {
	Name       string
	provenance *Provenance
}

func (t *IntrinsicType) Provenance() *Provenance     { return t.provenance }
func (t *IntrinsicType) SetProvenance(p *Provenance) { t.provenance = p }
func (t *IntrinsicType) Accept(v TypeVisitor)        { v.VisitType(t) }
func (t *IntrinsicType) Equal(other Type) bool {
	if other, ok := other.(*IntrinsicType); ok {
		return t.Name == other.Name
	}
	return false
}

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
