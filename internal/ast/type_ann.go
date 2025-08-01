//go:generate go run ../../tools/gen_ast/gen_ast.go -p ./type_ann.go

package ast

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
func (*AnyTypeAnn) isTypeAnn()          {}
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
func (*MatchTypeAnn) isTypeAnn()        {}
func (*MutableTypeAnn) isTypeAnn()      {}
func (*EmptyTypeAnn) isTypeAnn()        {}

type LitTypeAnn struct {
	Lit          Lit
	span         Span
	inferredType Type
}

func NewLitTypeAnn(lit Lit, span Span) *LitTypeAnn {
	return &LitTypeAnn{Lit: lit, span: span, inferredType: nil}
}
func (t *LitTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Lit.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type NumberTypeAnn struct {
	span         Span
	inferredType Type
}

func NewNumberTypeAnn(span Span) *NumberTypeAnn {
	return &NumberTypeAnn{span: span, inferredType: nil}
}
func (t *NumberTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type StringTypeAnn struct {
	span         Span
	inferredType Type
}

func NewStringTypeAnn(span Span) *StringTypeAnn {
	return &StringTypeAnn{span: span, inferredType: nil}
}
func (t *StringTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type BooleanTypeAnn struct {
	span         Span
	inferredType Type
}

func NewBooleanTypeAnn(span Span) *BooleanTypeAnn {
	return &BooleanTypeAnn{span: span, inferredType: nil}
}
func (t *BooleanTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type AnyTypeAnn struct {
	span         Span
	inferredType Type
}

func NewAnyTypeAnn(span Span) *AnyTypeAnn {
	return &AnyTypeAnn{span: span, inferredType: nil}
}
func (t *AnyTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type UnknownTypeAnn struct {
	span         Span
	inferredType Type
}

func NewUnknownTypeAnn(span Span) *UnknownTypeAnn {
	return &UnknownTypeAnn{span: span, inferredType: nil}
}
func (t *UnknownTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type NeverTypeAnn struct {
	span         Span
	inferredType Type
}

func NewNeverTypeAnn(span Span) *NeverTypeAnn {
	return &NeverTypeAnn{span: span, inferredType: nil}
}
func (t *NeverTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
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

type CallableTypeAnn struct{ Fn *FuncTypeAnn }
type ConstructorTypeAnn struct{ Fn *FuncTypeAnn }
type MethodTypeAnn struct {
	Name ObjKey
	Fn   *FuncTypeAnn
}
type GetterTypeAnn struct {
	Name ObjKey
	Fn   *FuncTypeAnn
}
type SetterTypeAnn struct {
	Name ObjKey
	Fn   *FuncTypeAnn
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
	Elems        []ObjTypeAnnElem
	span         Span
	inferredType Type
}

func NewObjectTypeAnn(elems []ObjTypeAnnElem, span Span) *ObjectTypeAnn {
	return &ObjectTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *ObjectTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, elem := range t.Elems {
			switch e := (elem).(type) {
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
				e.Value.Accept(v)
			case *MappedTypeAnn:
				e.TypeParam.Constraint.Accept(v)
				e.Value.Accept(v)
			case *RestSpreadTypeAnn:
				e.Value.Accept(v)
			}
		}
	}
	v.ExitTypeAnn(t)
}

type TupleTypeAnn struct {
	Elems        []TypeAnn
	span         Span
	inferredType Type
}

func NewTupleTypeAnn(elems []TypeAnn, span Span) *TupleTypeAnn {
	return &TupleTypeAnn{Elems: elems, span: span, inferredType: nil}
}
func (t *TupleTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, elem := range t.Elems {
			elem.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type UnionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType Type
}

func NewUnionTypeAnn(types []TypeAnn, span Span) *UnionTypeAnn {
	return &UnionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *UnionTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, typ := range t.Types {
			typ.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type IntersectionTypeAnn struct {
	Types        []TypeAnn
	span         Span
	inferredType Type
}

func NewIntersectionTypeAnn(types []TypeAnn, span Span) *IntersectionTypeAnn {
	return &IntersectionTypeAnn{Types: types, span: span, inferredType: nil}
}
func (t *IntersectionTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, typ := range t.Types {
			typ.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type TypeRefTypeAnn struct {
	Name         QualIdent
	TypeArgs     []TypeAnn
	span         Span
	inferredType Type
}

func NewRefTypeAnn(name QualIdent, typeArgs []TypeAnn, span Span) *TypeRefTypeAnn {
	return &TypeRefTypeAnn{Name: name, TypeArgs: typeArgs, span: span, inferredType: nil}
}
func (t *TypeRefTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, typeArg := range t.TypeArgs {
			typeArg.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type FuncTypeAnn struct {
	TypeParams   []*TypeParam // optional
	Params       []*Param
	Return       TypeAnn
	Throws       TypeAnn // optionanl
	span         Span
	inferredType Type
}

func NewFuncTypeAnn(
	typeParams []*TypeParam,
	params []*Param,
	ret TypeAnn,
	throws TypeAnn,
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
func (t *FuncTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, param := range t.Params {
			param.Pattern.Accept(v)
		}
		t.Return.Accept(v)
		if t.Throws != nil {
			t.Throws.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type KeyOfTypeAnn struct {
	Type         TypeAnn
	span         Span
	inferredType Type
}

func NewKeyOfTypeAnn(typ TypeAnn, span Span) *KeyOfTypeAnn {
	return &KeyOfTypeAnn{Type: typ, span: span, inferredType: nil}
}
func (t *KeyOfTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Type.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type TypeOfTypeAnn struct {
	Value        QualIdent
	span         Span
	inferredType Type
}

func NewTypeOfTypeAnn(value QualIdent, span Span) *TypeOfTypeAnn {
	return &TypeOfTypeAnn{Value: value, span: span, inferredType: nil}
}
func (t *TypeOfTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
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
func (t *IndexTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Target.Accept(v)
		t.Index.Accept(v)
	}
	v.ExitTypeAnn(t)
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
func (t *CondTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Check.Accept(v)
		t.Extends.Accept(v)
		t.Cons.Accept(v)
		t.Alt.Accept(v)
	}
	v.ExitTypeAnn(t)
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
func (t *MatchTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Target.Accept(v)
		for _, c := range t.Cases {
			c.Extends.Accept(v)
			c.Cons.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type InferTypeAnn struct {
	Name         string
	span         Span
	inferredType Type
}

func NewInferTypeAnn(name string, span Span) *InferTypeAnn {
	return &InferTypeAnn{Name: name, span: span, inferredType: nil}
}
func (t *InferTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type WildcardTypeAnn struct {
	span         Span
	inferredType Type
}

func NewWildcardTypeAnn(span Span) *WildcardTypeAnn {
	return &WildcardTypeAnn{span: span, inferredType: nil}
}
func (t *WildcardTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
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
func (t *TemplateLitTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, typeAnn := range t.TypeAnns {
			typeAnn.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type IntrinsicTypeAnn struct {
	span         Span
	inferredType Type
}

func NewIntrinsicTypeAnn(span Span) *IntrinsicTypeAnn {
	return &IntrinsicTypeAnn{span: span, inferredType: nil}
}
func (t *IntrinsicTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
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
func (t *ImportType) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		for _, typeArg := range t.TypeArgs {
			typeArg.Accept(v)
		}
	}
	v.ExitTypeAnn(t)
}

type MutableTypeAnn struct {
	Target       TypeAnn
	span         Span
	inferredType Type
}

func NewMutableTypeAnn(target TypeAnn, span Span) *MutableTypeAnn {
	return &MutableTypeAnn{Target: target, span: span, inferredType: nil}
}
func (t *MutableTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Target.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type EmptyTypeAnn struct {
	span         Span
	inferredType Type
}

func NewEmptyTypeAnn(span Span) *EmptyTypeAnn {
	return &EmptyTypeAnn{span: span, inferredType: nil}
}
func (t *EmptyTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}
