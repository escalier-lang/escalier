package ast

type Pat interface {
	isPat()
	Node
	Inferrable
}

func (*IdentPat) isPat()     {}
func (*ObjectPat) isPat()    {}
func (*TuplePat) isPat()     {}
func (*ExtractorPat) isPat() {}
func (*LitPat) isPat()       {}
func (*IsPat) isPat()        {}
func (*WildcardPat) isPat()  {}

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

func (*ObjKeyValuePat) isObjPatElem()  {}
func (*ObjShorthandPat) isObjPatElem() {}
func (*ObjRestPat) isObjPatElem()      {}

type ObjKeyValuePat struct {
	Key          string
	Value        Pat
	Default      Expr // optional
	span         Span
	inferredType Type
}

func NewObjKeyValuePat(key string, value Pat, _default Expr, span Span) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value, Default: _default, span: span, inferredType: nil}
}
func (p *ObjKeyValuePat) Span() Span { return p.span }

type ObjShorthandPat struct {
	Key     string
	Default Expr // optional
	span    Span
}

func NewObjShorthandPat(key string, _default Expr, span Span) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key, Default: _default, span: span}
}
func (p *ObjShorthandPat) Span() Span { return p.span }

type ObjRestPat struct {
	Pattern Pat
	span    Span
}

func NewObjRestPat(pattern Pat, span Span) *ObjRestPat {
	return &ObjRestPat{Pattern: pattern, span: span}
}
func (p *ObjRestPat) Span() Span { return p.span }

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

type TuplePatElem interface{ isTuplePatElem() }

func (*TupleElemPat) isTuplePatElem() {}
func (*TupleRestPat) isTuplePatElem() {}

type TupleElemPat struct {
	Pattern Pat
	Default Expr // optional
	span    Span
}

func NewTupleElemPat(pattern Pat, _default Expr, span Span) *TupleElemPat {
	return &TupleElemPat{Pattern: pattern, Default: _default, span: span}
}
func (p *TupleElemPat) Span() Span { return p.span }

type TupleRestPat struct {
	Pattern Pat
	span    Span
}

func NewTupleRestPat(pattern Pat, span Span) *TupleRestPat {
	return &TupleRestPat{Pattern: pattern, span: span}
}
func (p *TupleRestPat) Span() Span { return p.span }

type TuplePat struct {
	Elems        []TuplePatElem
	span         Span
	inferredType Type
}

func NewTuplePat(elems []TuplePatElem, span Span) *TuplePat {
	return &TuplePat{Elems: elems, span: span, inferredType: nil}
}
func (p *TuplePat) Span() Span             { return p.span }
func (p *TuplePat) InferredType() Type     { return p.inferredType }
func (p *TuplePat) SetInferredType(t Type) { p.inferredType = t }

type ExtractorPatArg interface {
	isExtractorPatArg()
}

func (*ExtractorArgPat) isExtractorPatArg()     {}
func (*ExtractorRestArgPat) isExtractorPatArg() {}

type ExtractorArgPat struct {
	Pattern Pat
	Default Expr // optional
	span    Span
}

func NewExtractorArgPat(pattern Pat, _default Expr, span Span) *ExtractorArgPat {
	return &ExtractorArgPat{Pattern: pattern, Default: _default, span: span}
}
func (p *ExtractorArgPat) Span() Span { return p.span }

type ExtractorRestArgPat struct {
	Pattern Pat
	span    Span
}

func NewExtractorRestArgPat(pattern Pat, span Span) *ExtractorRestArgPat {
	return &ExtractorRestArgPat{Pattern: pattern, span: span}
}
func (p *ExtractorRestArgPat) Span() Span { return p.span }

type ExtractorPat struct {
	Name         string // TODO: QualIdent
	Args         []ExtractorPatArg
	span         Span
	inferredType Type
}

func NewExtractorPat(name string, args []ExtractorPatArg, span Span) *ExtractorPat {
	return &ExtractorPat{Name: name, Args: args, span: span, inferredType: nil}
}
func (p *ExtractorPat) Span() Span             { return p.span }
func (p *ExtractorPat) InferredType() Type     { return p.inferredType }
func (p *ExtractorPat) SetInferredType(t Type) { p.inferredType = t }

type LitPat struct {
	Lit          Lit
	span         Span
	inferredType Type
}

func NewLitPat(lit Lit, span Span) *LitPat {
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
