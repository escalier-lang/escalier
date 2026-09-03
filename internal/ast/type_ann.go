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
func (*SymbolTypeAnn) isTypeAnn()       {}
func (*UniqueSymbolTypeAnn) isTypeAnn() {}
func (*BigintTypeAnn) isTypeAnn()       {}
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
func (*NegationTypeAnn) isTypeAnn()     {}
func (*TypeOfTypeAnn) isTypeAnn()       {}
func (*IndexTypeAnn) isTypeAnn()        {}
func (*CondTypeAnn) isTypeAnn()         {}
func (*InferTypeAnn) isTypeAnn()        {}
func (*WildcardTypeAnn) isTypeAnn()     {}
func (*TemplateLitTypeAnn) isTypeAnn()  {}
func (*IntrinsicTypeAnn) isTypeAnn()    {}
func (*ImportTypeAnn) isTypeAnn()       {}
func (*MatchTypeAnn) isTypeAnn()        {}
func (*MutableTypeAnn) isTypeAnn()      {}
func (*RefTypeAnn) isTypeAnn()          {}
func (*ErrorTypeAnn) isTypeAnn()        {}
func (*RestSpreadTypeAnn) isTypeAnn()   {}

type LitTypeAnn struct {
	Lit          Lit
	span         Span
	inferredType Type
	commentSlots
}

func NewLitTypeAnn(lit Lit, span Span) *LitTypeAnn {
	return &LitTypeAnn{Lit: lit, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewNumberTypeAnn(span Span) *NumberTypeAnn {
	return &NumberTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *NumberTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type StringTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewStringTypeAnn(span Span) *StringTypeAnn {
	return &StringTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *StringTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type BooleanTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewBooleanTypeAnn(span Span) *BooleanTypeAnn {
	return &BooleanTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *BooleanTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type SymbolTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewSymbolTypeAnn(span Span) *SymbolTypeAnn {
	return &SymbolTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *SymbolTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type UniqueSymbolTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewUniqueSymbolTypeAnn(span Span) *UniqueSymbolTypeAnn {
	return &UniqueSymbolTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *UniqueSymbolTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type BigintTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewBigintTypeAnn(span Span) *BigintTypeAnn {
	return &BigintTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *BigintTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type AnyTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewAnyTypeAnn(span Span) *AnyTypeAnn {
	return &AnyTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *AnyTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type UnknownTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewUnknownTypeAnn(span Span) *UnknownTypeAnn {
	return &UnknownTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *UnknownTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type NeverTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewNeverTypeAnn(span Span) *NeverTypeAnn {
	return &NeverTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *NeverTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type ObjTypeAnnElem interface {
	isObjTypeAnnElem()
	Commented
	// Doc returns the leading JSDoc retained on the elem, verbatim
	// with `/** ... */` delimiters, or "" if absent. Variants that
	// don't conceptually carry a doc (CallableTypeAnn,
	// ConstructorTypeAnn, MappedTypeAnn) return "" from a no-op
	// implementation; SetDoc on those is a no-op.
	Doc() string
	SetDoc(string)
}

func (*CallableTypeAnn) isObjTypeAnnElem()    {}
func (*ConstructorTypeAnn) isObjTypeAnnElem() {}
func (*MethodTypeAnn) isObjTypeAnnElem()      {}
func (*GetterTypeAnn) isObjTypeAnnElem()      {}
func (*SetterTypeAnn) isObjTypeAnnElem()      {}
func (*PropertyTypeAnn) isObjTypeAnnElem()    {}
func (*MappedTypeAnn) isObjTypeAnnElem()      {}
func (*RestSpreadTypeAnn) isObjTypeAnnElem()  {}

// No-op Doc/SetDoc impls for the variants that don't carry a JSDoc.
func (*CallableTypeAnn) Doc() string      { return "" }
func (*CallableTypeAnn) SetDoc(string)    {}
func (*ConstructorTypeAnn) Doc() string   { return "" }
func (*ConstructorTypeAnn) SetDoc(string) {}
func (*MappedTypeAnn) Doc() string        { return "" }
func (*MappedTypeAnn) SetDoc(string)      {}

type CallableTypeAnn struct {
	Fn *FuncTypeAnn
	commentSlots
}
type ConstructorTypeAnn struct {
	Fn *FuncTypeAnn
	commentSlots
}
type MethodTypeAnn struct {
	declDoc
	Name     ObjKey
	Fn       *FuncTypeAnn
	Receiver *MethodReceiver // nil if no receiver
	commentSlots
}

func (m *MethodTypeAnn) Span() Span { return m.Name.Span() }

type GetterTypeAnn struct {
	declDoc
	Name     ObjKey
	Fn       *FuncTypeAnn
	Receiver *MethodReceiver // nil if no receiver
	commentSlots
}

func (g *GetterTypeAnn) Span() Span { return g.Name.Span() }

type SetterTypeAnn struct {
	declDoc
	Name     ObjKey
	Fn       *FuncTypeAnn
	Receiver *MethodReceiver // nil if no receiver
	commentSlots
}

func (s *SetterTypeAnn) Span() Span { return s.Name.Span() }

type MappedModifier string

const (
	MMAdd    MappedModifier = "add"
	MMRemove MappedModifier = "remove"
)

// TODO: include a dedicated span covering the full property declaration
// (including modifiers like `readonly`/`?`); for now Span() returns the
// name's span so callers get a per-member position instead of the
// enclosing container's span.
type PropertyTypeAnn struct {
	declDoc
	Name     ObjKey
	Optional bool
	Readonly bool
	Value    TypeAnn
	commentSlots
}

func (p *PropertyTypeAnn) Span() Span { return p.Name.Span() }

type MappedTypeAnn struct {
	TypeParam *IndexParamTypeAnn
	// Name is used to rename keys in the mapped type
	// It must resolve to a type that can be used as a key
	Name     TypeAnn // optional
	Value    TypeAnn
	Optional *MappedModifier // TODO: replace with `?`, `!`, or nothing
	ReadOnly *MappedModifier
	Check    TypeAnn
	Extends  TypeAnn
	// Shorthand records that the source wrote the key constraint inside the brackets as
	// `[K: Keys]: Value` rather than in a trailing `for K in Keys`. The two spellings lower to the
	// same type, so this only tells the printer which one to write back.
	Shorthand bool
	commentSlots
}
type IndexParamTypeAnn struct {
	Name       string
	Constraint TypeAnn
}

type RestSpreadTypeAnn struct {
	// A rest spread keeps a real doc field, where CallableTypeAnn,
	// ConstructorTypeAnn, and MappedTypeAnn return a constant `""`. A
	// hand-authored `interface F { /** doc */ ...Bar }` round-trips its JSDoc.
	declDoc
	Value        TypeAnn
	span         Span
	inferredType Type
	commentSlots
}

func NewRestSpreadTypeAnn(value TypeAnn, span Span) *RestSpreadTypeAnn {
	return &RestSpreadTypeAnn{Value: value, span: span, inferredType: nil, commentSlots: commentSlots{}}
}

func (t *RestSpreadTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Value.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type ObjectTypeAnn struct {
	Elems        []ObjTypeAnnElem
	Inexact      bool // trailing `...` marker: `{x: number, ...}` tolerates extra fields
	span         Span
	inferredType Type
	commentSlots
}

func NewObjectTypeAnn(elems []ObjTypeAnnElem, span Span) *ObjectTypeAnn {
	return &ObjectTypeAnn{Elems: elems, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
				if e.Value != nil {
					e.Value.Accept(v)
				}
			case *MappedTypeAnn:
				e.TypeParam.Constraint.Accept(v)
				if e.Name != nil {
					e.Name.Accept(v)
				}
				e.Value.Accept(v)
				if e.Check != nil {
					e.Check.Accept(v)
				}
				if e.Extends != nil {
					e.Extends.Accept(v)
				}
			case *RestSpreadTypeAnn:
				e.Value.Accept(v)
			}
		}
	}
	v.ExitTypeAnn(t)
}

type TupleTypeAnn struct {
	Elems        []TypeAnn
	Inexact      bool // trailing `...` marker: `[number, ...]` tolerates extra elements
	span         Span
	inferredType Type
	commentSlots
}

func NewTupleTypeAnn(elems []TypeAnn, span Span) *TupleTypeAnn {
	return &TupleTypeAnn{Elems: elems, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewUnionTypeAnn(types []TypeAnn, span Span) *UnionTypeAnn {
	return &UnionTypeAnn{Types: types, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewIntersectionTypeAnn(types []TypeAnn, span Span) *IntersectionTypeAnn {
	return &IntersectionTypeAnn{Types: types, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	LifetimeArgs []LifetimeAnnNode // bare lifetime args in `<>` (e.g. `View<'a>`)
	Lifetime     LifetimeAnnNode   // optional, e.g. 'a in `'a Point` or `mut 'a Point`
	span         Span
	inferredType Type
	commentSlots
}

func NewRefTypeAnn(name QualIdent, typeArgs []TypeAnn, span Span) *TypeRefTypeAnn {
	return &TypeRefTypeAnn{Name: name, TypeArgs: typeArgs, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	LifetimeParams []*LifetimeParam // optional, e.g. <'a, 'b: 'a>
	TypeParams     []*TypeParam     // optional
	Params         []*Param
	Return         TypeAnn
	Throws         TypeAnn // optionanl
	Inexact        bool    // trailing `...` marker: fn(a, ...) tolerates extra args (#677 §4.1)
	span           Span
	inferredType   Type
	commentSlots
}

func NewFuncTypeAnn(
	lifetimeParams []*LifetimeParam,
	typeParams []*TypeParam,
	params []*Param,
	ret TypeAnn,
	throws TypeAnn,
	span Span,
) *FuncTypeAnn {
	return &FuncTypeAnn{
		LifetimeParams: lifetimeParams,
		TypeParams:     typeParams,
		Params:         params,
		Return:         ret,
		Throws:         throws,
		span:           span,
		inferredType:   nil,
		commentSlots:   commentSlots{},
	}
}
func (t *FuncTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		// Visit type parameters and their constraints
		for _, tp := range t.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(v)
			}
			if tp.Default != nil {
				tp.Default.Accept(v)
			}
		}
		for _, param := range t.Params {
			param.Pattern.Accept(v)
			if param.TypeAnn != nil {
				param.TypeAnn.Accept(v)
			}
		}
		// A setter written in an object type annotation returns nothing and so writes no
		// `-> R`, which is the one signature form that leaves Return nil.
		if t.Return != nil {
			t.Return.Accept(v)
		}
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
	commentSlots
}

func NewKeyOfTypeAnn(typ TypeAnn, span Span) *KeyOfTypeAnn {
	return &KeyOfTypeAnn{Type: typ, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *KeyOfTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Type.Accept(v)
	}
	v.ExitTypeAnn(t)
}

// NegationTypeAnn is the prefix `~T`, the set-theoretic complement admitting every value
// its operand rejects. It resolves to a soltype.NegationType.
type NegationTypeAnn struct {
	Type         TypeAnn
	span         Span
	inferredType Type
	commentSlots
}

func NewNegationTypeAnn(typ TypeAnn, span Span) *NegationTypeAnn {
	return &NegationTypeAnn{Type: typ, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *NegationTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Type.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type TypeOfTypeAnn struct {
	Value        QualIdent
	span         Span
	inferredType Type
	commentSlots
}

func NewTypeOfTypeAnn(value QualIdent, span Span) *TypeOfTypeAnn {
	return &TypeOfTypeAnn{Value: value, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewIndexTypeAnn(target TypeAnn, index TypeAnn, span Span) *IndexTypeAnn {
	return &IndexTypeAnn{Target: target, Index: index, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	Then         TypeAnn
	Else         TypeAnn
	span         Span
	inferredType Type
	commentSlots
}

func NewCondTypeAnn(check, extends, _then, _else TypeAnn, span Span) *CondTypeAnn {
	return &CondTypeAnn{Check: check, Extends: extends, Then: _then, Else: _else, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *CondTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Check.Accept(v)
		t.Extends.Accept(v)
		t.Then.Accept(v)
		t.Else.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type MatchTypeAnn struct {
	Target       TypeAnn
	Cases        []*MatchTypeAnnCase
	span         Span
	inferredType Type
	commentSlots
}

type MatchTypeAnnCase struct {
	Extends TypeAnn
	Cons    TypeAnn
}

func NewMatchTypeAnn(target TypeAnn, cases []*MatchTypeAnnCase, span Span) *MatchTypeAnn {
	return &MatchTypeAnn{Target: target, Cases: cases, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewInferTypeAnn(name string, span Span) *InferTypeAnn {
	return &InferTypeAnn{Name: name, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *InferTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type WildcardTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewWildcardTypeAnn(span Span) *WildcardTypeAnn {
	return &WildcardTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewTemplateLitTypeAnn(quasis []*Quasi, typeAnns []TypeAnn, span Span) *TemplateLitTypeAnn {
	return &TemplateLitTypeAnn{Quasis: quasis, TypeAnns: typeAnns, span: span, inferredType: nil, commentSlots: commentSlots{}}
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
	commentSlots
}

func NewIntrinsicTypeAnn(span Span) *IntrinsicTypeAnn {
	return &IntrinsicTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *IntrinsicTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}

type ImportTypeAnn struct {
	Source       string
	Qualifier    QualIdent // the import is like a namespace and the qualifier can be used to access imported symbols
	TypeArgs     []TypeAnn
	span         Span
	inferredType Type
	commentSlots
}

func NewImportType(source string, qualifier QualIdent, typeArgs []TypeAnn, span Span) *ImportTypeAnn {
	return &ImportTypeAnn{Source: source, Qualifier: qualifier, TypeArgs: typeArgs, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *ImportTypeAnn) Accept(v Visitor) {
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
	commentSlots
}

func NewMutableTypeAnn(target TypeAnn, span Span) *MutableTypeAnn {
	return &MutableTypeAnn{Target: target, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *MutableTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Target.Accept(v)
	}
	v.ExitTypeAnn(t)
}

// RefTypeAnn is a borrow annotation written with a prefix `&`:
//
//	&{x}         → RefTypeAnn{Mut: false}
//	&mut {x}     → RefTypeAnn{Mut: true}
//	&'a {x}      → RefTypeAnn{Mut: false, Lifetime: 'a}
//	&'a mut {x}  → RefTypeAnn{Mut: true,  Lifetime: 'a}
//
// The prefix `&` binds tight to a single atom, so `&A | B` is `(&A) | B`.
// A borrow of a compound is written with explicit parens, `&(A | B)`.
type RefTypeAnn struct {
	Mut          bool
	Lifetime     LifetimeAnnNode // optional
	Inner        TypeAnn
	span         Span
	inferredType Type
	commentSlots
}

func NewBorrowTypeAnn(mut bool, lifetime LifetimeAnnNode, inner TypeAnn, span Span) *RefTypeAnn {
	return &RefTypeAnn{Mut: mut, Lifetime: lifetime, Inner: inner, span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *RefTypeAnn) Accept(v Visitor) {
	if v.EnterTypeAnn(t) {
		t.Inner.Accept(v)
	}
	v.ExitTypeAnn(t)
}

type ErrorTypeAnn struct {
	span         Span
	inferredType Type
	commentSlots
}

func NewErrorTypeAnn(span Span) *ErrorTypeAnn {
	return &ErrorTypeAnn{span: span, inferredType: nil, commentSlots: commentSlots{}}
}
func (t *ErrorTypeAnn) Accept(v Visitor) {
	v.EnterTypeAnn(t)
	v.ExitTypeAnn(t)
}
