package ast

import (
	"github.com/moznion/go-optional"
)

//sumtype:decl
type TypeAnn interface {
	isTypeAnn()
	Node
	Inferrable
}

func (*LitTypeAnn) isTypeAnn()          {}
func (*NumberTypeAnn) isTypeAnn()       {}
func (*StringTypeAnn) isTypeAnn()       {}
func (*BooleanTypeAnn) isTypeAnn()      {}
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
func (*WildcardTypeAnn) isTypeAnn()     {}
func (*TemplateLitTypeAnn) isTypeAnn()  {}
func (*IntrinsicTypeAnn) isTypeAnn()    {}
func (*ImportType) isTypeAnn()          {}

type LitTypeAnn struct {
	Lit          Lit
	span         Span
	inferredType Type
}

func NewLitTypeAnn(lit Lit, span Span) *LitTypeAnn {
	return &LitTypeAnn{Lit: lit, span: span, inferredType: nil}
}
func (t *LitTypeAnn) Span() Span               { return t.span }
func (t *LitTypeAnn) InferredType() Type       { return t.inferredType }
func (t *LitTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *LitTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		t.Lit.Accept(v)
	}
}

type NumberTypeAnn struct {
	span         Span
	inferredType Type
}

func NewNumberTypeAnn(span Span) *NumberTypeAnn {
	return &NumberTypeAnn{span: span, inferredType: nil}
}
func (t *NumberTypeAnn) Span() Span               { return t.span }
func (t *NumberTypeAnn) InferredType() Type       { return t.inferredType }
func (t *NumberTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *NumberTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type StringTypeAnn struct {
	span         Span
	inferredType Type
}

func NewStringTypeAnn(span Span) *StringTypeAnn {
	return &StringTypeAnn{span: span, inferredType: nil}
}
func (t *StringTypeAnn) Span() Span               { return t.span }
func (t *StringTypeAnn) InferredType() Type       { return t.inferredType }
func (t *StringTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *StringTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type BooleanTypeAnn struct {
	span         Span
	inferredType Type
}

func NewBooleanTypeAnn(span Span) *BooleanTypeAnn {
	return &BooleanTypeAnn{span: span, inferredType: nil}
}
func (t *BooleanTypeAnn) Span() Span               { return t.span }
func (t *BooleanTypeAnn) InferredType() Type       { return t.inferredType }
func (t *BooleanTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *BooleanTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type NullTypeAnn struct {
	span         Span
	inferredType Type
}

func NewNullTypeAnn(span Span) *NullTypeAnn {
	return &NullTypeAnn{span: span, inferredType: nil}
}
func (t *NullTypeAnn) Span() Span               { return t.span }
func (t *NullTypeAnn) InferredType() Type       { return t.inferredType }
func (t *NullTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *NullTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type UndefinedTypeAnn struct {
	span         Span
	inferredType Type
}

func NewUndefinedTypeAnn(span Span) *UndefinedTypeAnn {
	return &UndefinedTypeAnn{span: span, inferredType: nil}
}
func (t *UndefinedTypeAnn) Span() Span               { return t.span }
func (t *UndefinedTypeAnn) InferredType() Type       { return t.inferredType }
func (t *UndefinedTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *UndefinedTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type UnknownTypeAnn struct {
	span         Span
	inferredType Type
}

func NewUnknownTypeAnn(span Span) *UnknownTypeAnn {
	return &UnknownTypeAnn{span: span, inferredType: nil}
}
func (t *UnknownTypeAnn) Span() Span               { return t.span }
func (t *UnknownTypeAnn) InferredType() Type       { return t.inferredType }
func (t *UnknownTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *UnknownTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type NeverTypeAnn struct {
	span         Span
	inferredType Type
}

func NewNeverTypeAnn(span Span) *NeverTypeAnn {
	return &NeverTypeAnn{span: span, inferredType: nil}
}
func (t *NeverTypeAnn) Span() Span               { return t.span }
func (t *NeverTypeAnn) InferredType() Type       { return t.inferredType }
func (t *NeverTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *NeverTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

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
	// Name is used to rename keys in the mapped type
	// It must resolve to a type that can be used as a key
	Name     optional.Option[TypeAnn]
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
	Elems        []*ObjTypeAnnElem
	span         Span
	inferredType Type
}

func NewObjectTypeAnn(elems []*ObjTypeAnnElem, span Span) *ObjectTypeAnn {
	return &ObjectTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *ObjectTypeAnn) Span() Span               { return t.span }
func (t *ObjectTypeAnn) InferredType() Type       { return t.inferredType }
func (t *ObjectTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *ObjectTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, elem := range t.Elems {
			switch e := (*elem).(type) {
			case *CallableTypeAnn:
				e.Fn.Accept(v)
			case *ConstructorTypeAnn:
				e.Fn.Accept(v)
			case *MethodTypeAnn:
				e.Fn.Accept(v)
			case *GetterTypeAnn:
				e.Fn.Accept(v)
			case *SetterTypeAnn:
				e.Fn.Accept(v)
			case *PropertyTypeAnn:
				e.Value.IfSome(func(value TypeAnn) {
					value.Accept(v)
				})
			case *MappedTypeAnn:
				e.TypeParam.Constraint.Accept(v)
				e.Value.Accept(v)
			case *RestSpreadTypeAnn:
				e.Value.Accept(v)
			}
		}
	}
}

type TupleTypeAnn struct {
	Elems        []TypeAnn
	span         Span
	inferredType Type
}

func NewTupleTypeAnn(elems []TypeAnn, span Span) *TupleTypeAnn {
	return &TupleTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *TupleTypeAnn) Span() Span               { return t.span }
func (t *TupleTypeAnn) InferredType() Type       { return t.inferredType }
func (t *TupleTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *TupleTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, elem := range t.Elems {
			elem.Accept(v)
		}
	}
}

type UnionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType Type
}

func NewUnionTypeAnn(types []TypeAnn, span Span) *UnionTypeAnn {
	return &UnionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *UnionTypeAnn) Span() Span               { return t.span }
func (t *UnionTypeAnn) InferredType() Type       { return t.inferredType }
func (t *UnionTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *UnionTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, typ := range t.Types {
			typ.Accept(v)
		}
	}
}

type IntersectionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType Type
}

func NewIntersectionTypeAnn(types []TypeAnn, span Span) *IntersectionTypeAnn {
	return &IntersectionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *IntersectionTypeAnn) Span() Span               { return t.span }
func (t *IntersectionTypeAnn) InferredType() Type       { return t.inferredType }
func (t *IntersectionTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *IntersectionTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, typ := range t.Types {
			typ.Accept(v)
		}
	}
}

type TypeRefTypeAnn struct {
	Name         string
	TypeArgs     []TypeAnn
	span         Span
	inferredType Type
}

func NewRefTypeAnn(name string, typeArgs []TypeAnn, span Span) *TypeRefTypeAnn {
	return &TypeRefTypeAnn{Name: name, TypeArgs: typeArgs, span: span, inferredType: nil}
}
func (t *TypeRefTypeAnn) Span() Span               { return t.span }
func (t *TypeRefTypeAnn) InferredType() Type       { return t.inferredType }
func (t *TypeRefTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *TypeRefTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, typeArg := range t.TypeArgs {
			typeArg.Accept(v)
		}
	}
}

type FuncTypeAnn struct {
	TypeParams   optional.Option[[]TypeParam]
	Params       []*Param
	Return       TypeAnn
	Throws       optional.Option[TypeAnn]
	span         Span
	inferredType Type
}

func NewFuncTypeAnn(
	typeParams optional.Option[[]TypeParam],
	params []*Param,
	ret TypeAnn,
	throws optional.Option[TypeAnn],
	span Span,
) *FuncTypeAnn {
	return &FuncTypeAnn{
		TypeParams:   typeParams,
		Params:       params,
		Return:       ret,
		Throws:       throws,
		span:         span,
		inferredType: nil,
	}
}
func (t *FuncTypeAnn) Span() Span               { return t.span }
func (t *FuncTypeAnn) InferredType() Type       { return t.inferredType }
func (t *FuncTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *FuncTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, param := range t.Params {
			param.Pattern.Accept(v)
		}
		t.Return.Accept(v)
		t.Throws.IfSome(func(throws TypeAnn) {
			throws.Accept(v)
		})
	}
}

type KeyOfTypeAnn struct {
	Type         TypeAnn
	span         Span
	inferredType Type
}

func NewKeyOfTypeAnn(typ TypeAnn, span Span) *KeyOfTypeAnn {
	return &KeyOfTypeAnn{Type: typ, span: span, inferredType: nil}
}
func (t *KeyOfTypeAnn) Span() Span               { return t.span }
func (t *KeyOfTypeAnn) InferredType() Type       { return t.inferredType }
func (t *KeyOfTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *KeyOfTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		t.Type.Accept(v)
	}
}

type TypeOfTypeAnn struct {
	Value        QualIdent
	span         Span
	inferredType Type
}

func NewTypeOfTypeAnn(value QualIdent, span Span) *TypeOfTypeAnn {
	return &TypeOfTypeAnn{Value: value, span: span, inferredType: nil}
}
func (t *TypeOfTypeAnn) Span() Span               { return t.span }
func (t *TypeOfTypeAnn) InferredType() Type       { return t.inferredType }
func (t *TypeOfTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *TypeOfTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type IndexTypeAnn struct {
	Target       TypeAnn
	Index        TypeAnn
	span         Span
	inferredType Type
}

func NewIndexTypeAnn(target TypeAnn, index TypeAnn, span Span) *IndexTypeAnn {
	return &IndexTypeAnn{Target: target, Index: index, span: span, inferredType: nil}
}
func (t *IndexTypeAnn) Span() Span               { return t.span }
func (t *IndexTypeAnn) InferredType() Type       { return t.inferredType }
func (t *IndexTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *IndexTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		t.Target.Accept(v)
		t.Index.Accept(v)
	}
}

type CondTypeAnn struct {
	Check        TypeAnn
	Extends      TypeAnn
	Cons         TypeAnn
	Alt          TypeAnn
	span         Span
	inferredType Type
}

func NewCondTypeAnn(check, extends, cons, alt TypeAnn, span Span) *CondTypeAnn {
	return &CondTypeAnn{Check: check, Extends: extends, Cons: cons, Alt: alt, span: span, inferredType: nil}
}
func (t *CondTypeAnn) Span() Span               { return t.span }
func (t *CondTypeAnn) InferredType() Type       { return t.inferredType }
func (t *CondTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *CondTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		t.Check.Accept(v)
		t.Extends.Accept(v)
		t.Cons.Accept(v)
		t.Alt.Accept(v)
	}
}

type MatchTypeAnn struct {
	Target       TypeAnn
	Cases        []*MatchTypeAnnCase
	span         Span
	inferredType Type
}

type MatchTypeAnnCase struct {
	Extends TypeAnn
	Cons    TypeAnn
}

func NewMatchTypeAnn(target TypeAnn, cases []*MatchTypeAnnCase, span Span) *MatchTypeAnn {
	return &MatchTypeAnn{Target: target, Cases: cases, span: span, inferredType: nil}
}
func (*MatchTypeAnn) isTypeAnn()                 {}
func (t *MatchTypeAnn) Span() Span               { return t.span }
func (t *MatchTypeAnn) InferredType() Type       { return t.inferredType }
func (t *MatchTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *MatchTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		t.Target.Accept(v)
		for _, c := range t.Cases {
			c.Extends.Accept(v)
			c.Cons.Accept(v)
		}
	}
}

type InferTypeAnn struct {
	Name         string
	span         Span
	inferredType Type
}

func NewInferTypeAnn(name string, span Span) *InferTypeAnn {
	return &InferTypeAnn{Name: name, span: span, inferredType: nil}
}
func (t *InferTypeAnn) Span() Span               { return t.span }
func (t *InferTypeAnn) InferredType() Type       { return t.inferredType }
func (t *InferTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *InferTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type WildcardTypeAnn struct {
	span         Span
	inferredType Type
}

func NewWildcardTypeAnn(span Span) *WildcardTypeAnn {
	return &WildcardTypeAnn{span: span, inferredType: nil}
}
func (t *WildcardTypeAnn) Span() Span               { return t.span }
func (t *WildcardTypeAnn) InferredType() Type       { return t.inferredType }
func (t *WildcardTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *WildcardTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type Quasi struct {
	Value string
	Span  Span
}

type TemplateLitTypeAnn struct {
	Quasis       []*Quasi
	TypeAnns     []TypeAnn
	span         Span
	inferredType Type
}

func NewTemplateLitTypeAnn(quasis []*Quasi, typeAnns []TypeAnn, span Span) *TemplateLitTypeAnn {
	return &TemplateLitTypeAnn{Quasis: quasis, TypeAnns: typeAnns, span: span, inferredType: nil}
}
func (t *TemplateLitTypeAnn) Span() Span               { return t.span }
func (t *TemplateLitTypeAnn) InferredType() Type       { return t.inferredType }
func (t *TemplateLitTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *TemplateLitTypeAnn) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, typeAnn := range t.TypeAnns {
			typeAnn.Accept(v)
		}
	}
}

type IntrinsicTypeAnn struct {
	span         Span
	inferredType Type
}

func NewIntrinsicTypeAnn(span Span) *IntrinsicTypeAnn {
	return &IntrinsicTypeAnn{span: span, inferredType: nil}
}
func (t *IntrinsicTypeAnn) Span() Span               { return t.span }
func (t *IntrinsicTypeAnn) InferredType() Type       { return t.inferredType }
func (t *IntrinsicTypeAnn) SetInferredType(typ Type) { t.inferredType = typ }
func (t *IntrinsicTypeAnn) Accept(v Visitor) {
	v.VisitTypeAnn(t)
}

type ImportType struct {
	Source       string
	Qualifier    QualIdent // the import is like a namespace and the qualifier can be used to access imported symbols
	TypeArgs     []TypeAnn
	span         Span
	inferredType Type
}

func NewImportType(source string, qualifier QualIdent, typeArgs []TypeAnn, span Span) *ImportType {
	return &ImportType{Source: source, Qualifier: qualifier, TypeArgs: typeArgs, span: span, inferredType: nil}
}
func (t *ImportType) Span() Span               { return t.span }
func (t *ImportType) InferredType() Type       { return t.inferredType }
func (t *ImportType) SetInferredType(typ Type) { t.inferredType = typ }
func (t *ImportType) Accept(v Visitor) {
	if v.VisitTypeAnn(t) {
		for _, typeArg := range t.TypeArgs {
			typeArg.Accept(v)
		}
	}
}
