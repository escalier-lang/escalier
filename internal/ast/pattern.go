package ast

type Pat interface {
	isPat()
	Node
	Inferrable
}

func (*IdentPat) isPat()    {}
func (*ObjectPat) isPat()   {}
func (*TuplePat) isPat()    {}
func (*ExtractPat) isPat()  {}
func (*LitPat) isPat()      {}
func (*IsPat) isPat()       {}
func (*WildcardPat) isPat() {}

type IdentPat struct {
	Name         string
	span         Span
	inferredType Type
}

func NewIdentPat(name string, span Span) *IdentPat {
	return &IdentPat{Name: name, span: span, inferredType: nil}
}
func (p *IdentPat) Span() Span             { return p.span }
func (p *IdentPat) InferredType() Type     { return p.inferredType }
func (p *IdentPat) SetInferredType(t Type) { p.inferredType = t }

type ObjPatElem interface{ isObjPatElem() }

func (*KeyValuePat) isObjPatElem()  {}
func (*ShorthandPat) isObjPatElem() {}
func (*RestPat) isObjPatElem()      {}

type KeyValuePat struct {
	Key          string
	Value        Pat
	Default      Expr // optional
	span         Span
	inferredType Type
}

func NewKeyValuePat(key string, value Pat, span Span) *KeyValuePat {
	return &KeyValuePat{Key: key, Value: value, Default: nil, span: span, inferredType: nil}
}
func (p *KeyValuePat) Span() Span { return p.span }

type ShorthandPat struct {
	Key     string
	Default Expr // optional
	span    Span
}

func NewShorthandPat(key string, span Span) *ShorthandPat {
	return &ShorthandPat{Key: key, Default: nil, span: span}
}
func (p *ShorthandPat) Span() Span { return p.span }

type RestPat struct {
	Pattern Pat
	span    Span
}

func NewRestPat(pattern Pat, span Span) *RestPat {
	return &RestPat{Pattern: pattern, span: span}
}
func (p *RestPat) Span() Span { return p.span }

type ObjectPat struct {
	Elems        []ObjPatElem
	span         Span
	inferredType Type
}

func NewObjectPat(elems []ObjPatElem, span Span) *ObjectPat {
	return &ObjectPat{Elems: elems, span: span, inferredType: nil}
}
func (p *ObjectPat) Span() Span             { return p.span }
func (p *ObjectPat) InferredType() Type     { return p.inferredType }
func (p *ObjectPat) SetInferredType(t Type) { p.inferredType = t }

// type PObjectElem interface{ isPObjectElem() }

type TuplePat struct {
	Elems        []Pat
	span         Span
	inferredType Type
}

func NewTuplePat(elems []Pat, span Span) *TuplePat {
	return &TuplePat{Elems: elems, span: span, inferredType: nil}
}
func (p *TuplePat) Span() Span             { return p.span }
func (p *TuplePat) InferredType() Type     { return p.inferredType }
func (p *TuplePat) SetInferredType(t Type) { p.inferredType = t }

type ExtractPat struct {
	Name         string // TODO: QualIdent
	Args         []Pat
	span         Span
	inferredType Type
}

func NewExtractPat(name string, args []Pat, span Span) *ExtractPat {
	return &ExtractPat{Name: name, Args: args, span: span, inferredType: nil}
}
func (p *ExtractPat) Span() Span             { return p.span }
func (p *ExtractPat) InferredType() Type     { return p.inferredType }
func (p *ExtractPat) SetInferredType(t Type) { p.inferredType = t }

type LitPat struct {
	Lit          *Lit
	span         Span
	inferredType Type
}

func NewLitPat(lit *Lit, span Span) *LitPat {
	return &LitPat{Lit: lit, span: span, inferredType: nil}
}
func (p *LitPat) Span() Span             { return p.span }
func (p *LitPat) InferredType() Type     { return p.inferredType }
func (p *LitPat) SetInferredType(t Type) { p.inferredType = t }

type IsPat struct {
	Name         *Ident
	Type         TypeAnn
	span         Span
	inferredType Type
}

func NewIsPat(name *Ident, typ TypeAnn, span Span) *IsPat {
	return &IsPat{Name: name, Type: typ, span: span, inferredType: nil}
}
func (p *IsPat) Span() Span             { return p.span }
func (p *IsPat) InferredType() Type     { return p.inferredType }
func (p *IsPat) SetInferredType(t Type) { p.inferredType = t }

type WildcardPat struct {
	span         Span
	inferredType Type
}

func NewWildcardPat(span Span) *WildcardPat {
	return &WildcardPat{span: span, inferredType: nil}
}
func (p *WildcardPat) Span() Span             { return p.span }
func (p *WildcardPat) InferredType() Type     { return p.inferredType }
func (p *WildcardPat) SetInferredType(t Type) { p.inferredType = t }
