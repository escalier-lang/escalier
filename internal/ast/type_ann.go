package ast

import (
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/moznion/go-optional"
)

//sumtype:decl
type TypeAnn interface {
	isTypeAnn()
	Node
	Inferrable
}

func (*LitTypeAnn) isTypeAnn()          {}
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
func (*WildcardTypeAnn) isTypeAnn()     {}
func (*TemplateLitTypeAnn) isTypeAnn()  {}
func (*IntrinsicTypeAnn) isTypeAnn()    {}
func (*ImportType) isTypeAnn()          {}

type LitTypeAnn struct {
	Lit          *Lit
	span         Span
	inferredType type_system.Type
}

func NewLitTypeAnn(lit *Lit, span Span) *LitTypeAnn {
	return &LitTypeAnn{Lit: lit, span: span, inferredType: nil}
}
func (t *LitTypeAnn) Span() Span                           { return t.span }
func (t *LitTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *LitTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type UnknownTypeAnn struct {
	span         Span
	inferredType type_system.Type
}

func NewUnknownTypeAnn(span Span) *UnknownTypeAnn {
	return &UnknownTypeAnn{span: span, inferredType: nil}
}
func (t *UnknownTypeAnn) Span() Span                           { return t.span }
func (t *UnknownTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *UnknownTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type NeverTypeAnn struct {
	span         Span
	inferredType type_system.Type
}

func NewNeverTypeAnn(span Span) *NeverTypeAnn {
	return &NeverTypeAnn{span: span, inferredType: nil}
}
func (t *NeverTypeAnn) Span() Span                           { return t.span }
func (t *NeverTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *NeverTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

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
	Value    optional.Option[TypeAnn]
}

type MappedTypeAnn struct {
	TypeParam *IndexParamTypeAnn
	Name      optional.Option[TypeAnn]
	Value     TypeAnn
	Optional  *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly  *MappedModifier
}
type IndexParamTypeAnn struct {
	Name       string
	Constraint type_system.Type
}

type RestSpreadTypeAnn struct {
	Value TypeAnn
}

type ObjectTypeAnn struct {
	Elems        []*ObjTypeAnnElem
	span         Span
	inferredType type_system.Type
}

func NewObjectTypeAnn(elems []*ObjTypeAnnElem, span Span) *ObjectTypeAnn {
	return &ObjectTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *ObjectTypeAnn) Span() Span                           { return t.span }
func (t *ObjectTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *ObjectTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type TupleTypeAnn struct {
	Elems        []TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewTupleTypeAnn(elems []TypeAnn, span Span) *TupleTypeAnn {
	return &TupleTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *TupleTypeAnn) Span() Span                           { return t.span }
func (t *TupleTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *TupleTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type UnionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewUnionTypeAnn(types []TypeAnn, span Span) *UnionTypeAnn {
	return &UnionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *UnionTypeAnn) Span() Span                           { return t.span }
func (t *UnionTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *UnionTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type IntersectionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewIntersectionTypeAnn(types []TypeAnn, span Span) *IntersectionTypeAnn {
	return &IntersectionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *IntersectionTypeAnn) Span() Span                           { return t.span }
func (t *IntersectionTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *IntersectionTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type TypeRefTypeAnn struct {
	TypeRef      *type_system.TypeRefType // TODO: replace this with an AST node
	span         Span
	inferredType type_system.Type
}

func NewRefTypeAnn(typeRef *type_system.TypeRefType, span Span) *TypeRefTypeAnn {
	return &TypeRefTypeAnn{TypeRef: typeRef, span: span, inferredType: nil}
}
func (t *TypeRefTypeAnn) Span() Span                           { return t.span }
func (t *TypeRefTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *TypeRefTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type FuncTypeAnn struct {
	Params       []Param
	Return       TypeAnn
	Throws       TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewFuncTypeAnn(params []Param, ret TypeAnn, throws TypeAnn, span Span) *FuncTypeAnn {
	return &FuncTypeAnn{Params: params, Return: ret, Throws: throws, span: span, inferredType: nil}
}
func (t *FuncTypeAnn) Span() Span                           { return t.span }
func (t *FuncTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *FuncTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type KeyOfTypeAnn struct {
	Type         TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewKeyOfTypeAnn(typ TypeAnn, span Span) *KeyOfTypeAnn {
	return &KeyOfTypeAnn{Type: typ, span: span, inferredType: nil}
}
func (t *KeyOfTypeAnn) Span() Span                           { return t.span }
func (t *KeyOfTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *KeyOfTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type TypeOfTypeAnn struct {
	Value        QualIdent
	span         Span
	inferredType type_system.Type
}

func NewTypeOfTypeAnn(value QualIdent, span Span) *TypeOfTypeAnn {
	return &TypeOfTypeAnn{Value: value, span: span, inferredType: nil}
}
func (t *TypeOfTypeAnn) Span() Span                           { return t.span }
func (t *TypeOfTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *TypeOfTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type IndexTypeAnn struct {
	Target       TypeAnn
	Index        TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewIndexTypeAnn(target TypeAnn, index TypeAnn, span Span) *IndexTypeAnn {
	return &IndexTypeAnn{Target: target, Index: index, span: span, inferredType: nil}
}
func (t *IndexTypeAnn) Span() Span                           { return t.span }
func (t *IndexTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *IndexTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type CondTypeAnn struct {
	Check        TypeAnn
	Extends      TypeAnn
	Cons         TypeAnn
	Alt          TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewCondTypeAnn(check, extends, cons, alt TypeAnn, span Span) *CondTypeAnn {
	return &CondTypeAnn{Check: check, Extends: extends, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (t *CondTypeAnn) Span() Span                           { return t.span }
func (t *CondTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *CondTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type MatchTypeAnn struct {
	Target       TypeAnn
	Cases        []*MatchTypeAnnCase
	span         Span
	inferredType type_system.Type
}

type MatchTypeAnnCase struct {
	Extends TypeAnn
	Cons    TypeAnn
}

func NewMatchTypeAnn(target TypeAnn, cases []*MatchTypeAnnCase, span Span) *MatchTypeAnn {
	return &MatchTypeAnn{Target: target, Cases: cases, span: span, inferredType: nil}
}
func (*MatchTypeAnn) isTypeAnn()                             {}
func (t *MatchTypeAnn) Span() Span                           { return t.span }
func (t *MatchTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *MatchTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type InferTypeAnn struct {
	Name         string
	span         Span
	inferredType type_system.Type
}

func NewInferTypeAnn(name string, span Span) *InferTypeAnn {
	return &InferTypeAnn{Name: name, span: span, inferredType: nil}
}
func (t *InferTypeAnn) Span() Span                           { return t.span }
func (t *InferTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *InferTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type WildcardTypeAnn struct {
	span         Span
	inferredType type_system.Type
}

func NewWildcardTypeAnn(span Span) *WildcardTypeAnn {
	return &WildcardTypeAnn{span: span, inferredType: nil}
}
func (t *WildcardTypeAnn) Span() Span                           { return t.span }
func (t *WildcardTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *WildcardTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type Quasi struct {
	Value string
	Span  Span
}

type TemplateLitTypeAnn struct {
	Quasis       []*Quasi
	TypeAnns     []TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewTemplateLitTypeAnn(quasis []*Quasi, typeAnns []TypeAnn, span Span) *TemplateLitTypeAnn {
	return &TemplateLitTypeAnn{Quasis: quasis, TypeAnns: typeAnns, span: span, inferredType: nil}
}
func (t *TemplateLitTypeAnn) Span() Span                           { return t.span }
func (t *TemplateLitTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *TemplateLitTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type IntrinsicTypeAnn struct {
	span         Span
	inferredType type_system.Type
}

func NewIntrinsicTypeAnn(span Span) *IntrinsicTypeAnn {
	return &IntrinsicTypeAnn{span: span, inferredType: nil}
}
func (t *IntrinsicTypeAnn) Span() Span                           { return t.span }
func (t *IntrinsicTypeAnn) InferredType() type_system.Type       { return t.inferredType }
func (t *IntrinsicTypeAnn) SetInferredType(typ type_system.Type) { t.inferredType = typ }

type ImportType struct {
	Source       string
	Qualifier    QualIdent // the import is like a namespace and the qualifier can be used to access imported symbols
	TypeArgs     []TypeAnn
	span         Span
	inferredType type_system.Type
}

func NewImportType(source string, qualifier QualIdent, typeArgs []TypeAnn, span Span) *ImportType {
	return &ImportType{Source: source, Qualifier: qualifier, TypeArgs: typeArgs, span: span, inferredType: nil}
}
func (t *ImportType) Span() Span                           { return t.span }
func (t *ImportType) InferredType() type_system.Type       { return t.inferredType }
func (t *ImportType) SetInferredType(typ type_system.Type) { t.inferredType = typ }
