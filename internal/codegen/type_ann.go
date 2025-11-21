package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

//sumtype:decl
type TypeAnn interface {
	isTypeAnn()
	Node
}

func (*LitTypeAnn) isTypeAnn()          {}
func (*NumberTypeAnn) isTypeAnn()       {}
func (*StringTypeAnn) isTypeAnn()       {}
func (*BooleanTypeAnn) isTypeAnn()      {}
func (*SymbolTypeAnn) isTypeAnn()       {}
func (*UniqueSymbolTypeAnn) isTypeAnn() {}
func (*NullTypeAnn) isTypeAnn()         {}
func (*UndefinedTypeAnn) isTypeAnn()    {}
func (*UnknownTypeAnn) isTypeAnn()      {}
func (*NeverTypeAnn) isTypeAnn()        {}
func (*ObjectTypeAnn) isTypeAnn()       {}
func (*TupleTypeAnn) isTypeAnn()        {}
func (*UnionTypeAnn) isTypeAnn()        {}
func (*IntersectionTypeAnn) isTypeAnn() {}
func (*TypeRefTypeAnn) isTypeAnn()      {}
func (*FuncTypeAnn) isTypeAnn()         {}
func (*KeyOfTypeAnn) isTypeAnn()        {}
func (*TypeOfTypeAnn) isTypeAnn()       {}
func (*IndexTypeAnn) isTypeAnn()        {}
func (*CondTypeAnn) isTypeAnn()         {}
func (*InferTypeAnn) isTypeAnn()        {}
func (*AnyTypeAnn) isTypeAnn()          {}
func (*TemplateLitTypeAnn) isTypeAnn()  {}
func (*IntrinsicTypeAnn) isTypeAnn()    {}
func (*ImportType) isTypeAnn()          {}

type LitTypeAnn struct {
	Lit  Lit
	span *Span
}

func NewLitTypeAnn(lit Lit) *LitTypeAnn {
	return &LitTypeAnn{Lit: lit, span: nil}
}
func (t *LitTypeAnn) Span() *Span        { return t.span }
func (t *LitTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *LitTypeAnn) Source() ast.Node   { return t.Lit.Source() }

type NumberTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewNumberTypeAnn(span *Span) *NumberTypeAnn {
	return &NumberTypeAnn{span: nil, source: nil}
}
func (t *NumberTypeAnn) Span() *Span        { return t.span }
func (t *NumberTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *NumberTypeAnn) Source() ast.Node   { return t.source }

type StringTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewStringTypeAnn(span *Span) *StringTypeAnn {
	return &StringTypeAnn{span: nil, source: nil}
}
func (t *StringTypeAnn) Span() *Span        { return t.span }
func (t *StringTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *StringTypeAnn) Source() ast.Node   { return t.source }

type BooleanTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewBooleanTypeAnn(span *Span) *BooleanTypeAnn {
	return &BooleanTypeAnn{span: nil, source: nil}
}
func (t *BooleanTypeAnn) Span() *Span        { return t.span }
func (t *BooleanTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *BooleanTypeAnn) Source() ast.Node   { return t.source }

type SymbolTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewSymbolTypeAnn(span *Span) *SymbolTypeAnn {
	return &SymbolTypeAnn{span: nil, source: nil}
}
func (t *SymbolTypeAnn) Span() *Span        { return t.span }
func (t *SymbolTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *SymbolTypeAnn) Source() ast.Node   { return t.source }

type UniqueSymbolTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewUniqueSymbolTypeAnn(span *Span) *UniqueSymbolTypeAnn {
	return &UniqueSymbolTypeAnn{span: nil, source: nil}
}
func (t *UniqueSymbolTypeAnn) Span() *Span        { return t.span }
func (t *UniqueSymbolTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *UniqueSymbolTypeAnn) Source() ast.Node   { return t.source }

type NullTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewNullTypeAnn(span *Span) *NullTypeAnn {
	return &NullTypeAnn{span: nil, source: nil}
}
func (t *NullTypeAnn) Span() *Span        { return t.span }
func (t *NullTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *NullTypeAnn) Source() ast.Node   { return t.source }

type UndefinedTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewUndefinedTypeAnn(span *Span) *UndefinedTypeAnn {
	return &UndefinedTypeAnn{span: nil, source: nil}
}
func (t *UndefinedTypeAnn) Span() *Span        { return t.span }
func (t *UndefinedTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *UndefinedTypeAnn) Source() ast.Node   { return t.source }

type UnknownTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewUnknownTypeAnn(span *Span) *UnknownTypeAnn {
	return &UnknownTypeAnn{span: nil, source: nil}
}
func (t *UnknownTypeAnn) Span() *Span        { return t.span }
func (t *UnknownTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *UnknownTypeAnn) Source() ast.Node   { return t.source }

type NeverTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewNeverTypeAnn(span *Span) *NeverTypeAnn {
	return &NeverTypeAnn{span: nil, source: nil}
}
func (t *NeverTypeAnn) Span() *Span        { return t.span }
func (t *NeverTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *NeverTypeAnn) Source() ast.Node   { return t.source }

type ObjTypeAnnElem interface{ isObjTypeAnnElem() }

func (*CallableTypeAnn) isObjTypeAnnElem()    {}
func (*ConstructorTypeAnn) isObjTypeAnnElem() {}
func (*MethodTypeAnn) isObjTypeAnnElem()      {}
func (*GetterTypeAnn) isObjTypeAnnElem()      {}
func (*SetterTypeAnn) isObjTypeAnnElem()      {}
func (*PropertyTypeAnn) isObjTypeAnnElem()    {}
func (*MappedTypeAnn) isObjTypeAnnElem()      {}
func (*RestSpreadTypeAnn) isObjTypeAnnElem()  {}

type CallableTypeAnn struct{ Fn FuncTypeAnn }
type ConstructorTypeAnn struct{ Fn FuncTypeAnn }
type MethodTypeAnn struct {
	Name ObjKey
	Fn   FuncTypeAnn
}
type GetterTypeAnn struct {
	Name ObjKey
	Fn   FuncTypeAnn
}
type SetterTypeAnn struct {
	Name ObjKey
	Fn   FuncTypeAnn
}

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

// TODO: include span
type PropertyTypeAnn struct {
	Name     ObjKey
	Optional bool
	Readonly bool
	Value    TypeAnn
}

type MappedTypeAnn struct {
	TypeParam *IndexParamTypeAnn
	// Name is used to rename keys in the mapped type
	// It must resolve to a type that can be used as a key
	Name     TypeAnn // optional
	Value    TypeAnn
	Optional *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly *MappedModifier
}
type IndexParamTypeAnn struct {
	Name       string
	Constraint TypeAnn
}

type RestSpreadTypeAnn struct {
	Value TypeAnn
}

type ObjectTypeAnn struct {
	Elems  []ObjTypeAnnElem
	span   *Span
	source ast.Node
}

func NewObjectTypeAnn(elems []ObjTypeAnnElem) *ObjectTypeAnn {
	return &ObjectTypeAnn{Elems: elems, span: nil, source: nil}
}
func (t *ObjectTypeAnn) Span() *Span        { return t.span }
func (t *ObjectTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *ObjectTypeAnn) Source() ast.Node   { return t.source }

type TupleTypeAnn struct {
	Elems  []TypeAnn
	span   *Span
	source ast.Node
}

func NewTupleTypeAnn(elems []TypeAnn) *TupleTypeAnn {
	return &TupleTypeAnn{Elems: elems, span: nil, source: nil}
}
func (t *TupleTypeAnn) Span() *Span        { return t.span }
func (t *TupleTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *TupleTypeAnn) Source() ast.Node   { return t.source }

type UnionTypeAnn struct {
	Types  []TypeAnn
	span   *Span
	source ast.Node
}

func NewUnionTypeAnn(types []TypeAnn) *UnionTypeAnn {
	return &UnionTypeAnn{Types: types, span: nil, source: nil}
}
func (t *UnionTypeAnn) Span() *Span        { return t.span }
func (t *UnionTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *UnionTypeAnn) Source() ast.Node   { return t.source }

type IntersectionTypeAnn struct {
	Types  []TypeAnn
	span   *Span
	source ast.Node
}

func NewIntersectionTypeAnn(types []TypeAnn) *IntersectionTypeAnn {
	return &IntersectionTypeAnn{Types: types, span: nil, source: nil}
}
func (t *IntersectionTypeAnn) Span() *Span        { return t.span }
func (t *IntersectionTypeAnn) Source() ast.Node   { return t.source }
func (t *IntersectionTypeAnn) SetSpan(span *Span) { t.span = span }

type TypeRefTypeAnn struct {
	Name     string
	TypeArgs []TypeAnn
	span     *Span
	source   ast.Node
}

func NewRefTypeAnn(name string, typeArgs []TypeAnn) *TypeRefTypeAnn {
	return &TypeRefTypeAnn{Name: name, TypeArgs: typeArgs, span: nil, source: nil}
}
func (t *TypeRefTypeAnn) Span() *Span        { return t.span }
func (t *TypeRefTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *TypeRefTypeAnn) Source() ast.Node   { return t.source }

type TypeParam struct {
	Name       string
	Constraint TypeAnn // optional
	Default    TypeAnn // optional
}

type FuncTypeAnn struct {
	TypeParams []*TypeParam // optional
	Params     []*Param
	Return     TypeAnn
	Throws     TypeAnn // optional
	span       *Span
	source     ast.Node
}

func NewFuncTypeAnn(
	typeParams []*TypeParam, // optional
	params []*Param,
	ret TypeAnn,
	throws TypeAnn, // optional
	span *Span,
) *FuncTypeAnn {
	return &FuncTypeAnn{
		TypeParams: typeParams,
		Params:     params,
		Return:     ret,
		Throws:     throws,
		span:       span,
		source:     nil,
	}
}
func (t *FuncTypeAnn) Span() *Span        { return t.span }
func (t *FuncTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *FuncTypeAnn) Source() ast.Node   { return t.source }

type KeyOfTypeAnn struct {
	Type   TypeAnn
	span   *Span
	source ast.Node
}

func NewKeyOfTypeAnn(typ TypeAnn) *KeyOfTypeAnn {
	return &KeyOfTypeAnn{Type: typ, span: nil, source: nil}
}
func (t *KeyOfTypeAnn) Span() *Span        { return t.span }
func (t *KeyOfTypeAnn) Source() ast.Node   { return t.source }
func (t *KeyOfTypeAnn) SetSpan(span *Span) { t.span = span }

type TypeOfTypeAnn struct {
	Value  QualIdent
	span   *Span
	source ast.Node
}

func NewTypeOfTypeAnn(value QualIdent) *TypeOfTypeAnn {
	return &TypeOfTypeAnn{Value: value, span: nil, source: nil}
}
func (t *TypeOfTypeAnn) Span() *Span        { return t.span }
func (t *TypeOfTypeAnn) Source() ast.Node   { return t.source }
func (t *TypeOfTypeAnn) SetSpan(span *Span) { t.span = span }

type IndexTypeAnn struct {
	Target TypeAnn
	Index  TypeAnn
	span   *Span
	source ast.Node
}

func NewIndexTypeAnn(target TypeAnn, index TypeAnn) *IndexTypeAnn {
	return &IndexTypeAnn{Target: target, Index: index, span: nil, source: nil}
}
func (t *IndexTypeAnn) Span() *Span        { return t.span }
func (t *IndexTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *IndexTypeAnn) Source() ast.Node   { return t.source }

type CondTypeAnn struct {
	Check   TypeAnn
	Extends TypeAnn
	Cons    TypeAnn
	Alt     TypeAnn
	span    *Span
	source  ast.Node
}

func NewCondTypeAnn(check, extends, cons, alt TypeAnn) *CondTypeAnn {
	return &CondTypeAnn{Check: check, Extends: extends, Cons: cons, Alt: alt, span: nil, source: nil}
}
func (t *CondTypeAnn) Span() *Span        { return t.span }
func (t *CondTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *CondTypeAnn) Source() ast.Node   { return t.source }

type InferTypeAnn struct {
	Name   string
	span   *Span
	source ast.Node
}

func NewInferTypeAnn(name string) *InferTypeAnn {
	return &InferTypeAnn{Name: name, span: nil, source: nil}
}
func (t *InferTypeAnn) Span() *Span        { return t.span }
func (t *InferTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *InferTypeAnn) Source() ast.Node   { return t.source }

type AnyTypeAnn struct {
	span   *Span
	source ast.Node
}

func NewAnyTypeAnn(span *Span) *AnyTypeAnn {
	return &AnyTypeAnn{span: nil, source: nil}
}
func (t *AnyTypeAnn) Span() *Span        { return t.span }
func (t *AnyTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *AnyTypeAnn) Source() ast.Node   { return t.source }

type Quasi struct {
	Value string
	Span  *Span
}

type TemplateLitTypeAnn struct {
	Quasis   []*Quasi
	TypeAnns []TypeAnn
	span     *Span
	source   ast.Node
}

func NewTemplateLitTypeAnn(quasis []*Quasi, typeAnns []TypeAnn) *TemplateLitTypeAnn {
	return &TemplateLitTypeAnn{Quasis: quasis, TypeAnns: typeAnns, span: nil, source: nil}
}
func (t *TemplateLitTypeAnn) Span() *Span        { return t.span }
func (t *TemplateLitTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *TemplateLitTypeAnn) Source() ast.Node   { return t.source }

type IntrinsicTypeAnn struct {
	Name string
	span *Span
}

func NewIntrinsicTypeAnn(name string, span *Span) *IntrinsicTypeAnn {
	return &IntrinsicTypeAnn{Name: name, span: nil}
}
func (t *IntrinsicTypeAnn) Span() *Span        { return t.span }
func (t *IntrinsicTypeAnn) SetSpan(span *Span) { t.span = span }
func (t *IntrinsicTypeAnn) Source() ast.Node   { return nil }

type ImportType struct {
	Path      string
	Qualifier QualIdent // the import is like a namespace and the qualifier can be used to access imported symbols
	TypeArgs  []TypeAnn
	span      *Span
	source    ast.Node
}

func NewImportType(path string, qualifier QualIdent, typeArgs []TypeAnn) *ImportType {
	return &ImportType{
		Path:      path,
		Qualifier: qualifier,
		TypeArgs:  typeArgs,
		span:      nil,
		source:    nil,
	}
}
func (t *ImportType) Span() *Span        { return t.span }
func (t *ImportType) SetSpan(span *Span) { t.span = span }
func (t *ImportType) Source() ast.Node   { return t.source }

// TODO: Dedupe with `Identifier`
type Ident struct {
	Name string
	span Span
}

func NewIdent(name string, span Span) *Ident {
	return &Ident{Name: name, span: span}
}

type QualIdent interface{ isQualIdent() }

func (*Ident) isQualIdent()  {}
func (*Member) isQualIdent() {}

type Member struct {
	Left  QualIdent
	Right *Ident
}

func (i *Ident) Span() Span {
	return i.span
}
