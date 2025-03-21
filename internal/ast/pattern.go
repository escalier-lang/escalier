package ast

type Pat interface {
	isPat()
	Node
	Inferrable
}

type IdentPat struct {
	Name         string
	span         Span
	inferredType *Type
}

func NewIdentPat(name string, span Span) *IdentPat {
	return &IdentPat{Name: name, span: span, inferredType: nil}
}
func (*IdentPat) isPat()                {}
func (p *IdentPat) Span() Span          { return p.span }
func (p *IdentPat) InferredType() *Type { return p.inferredType }
func (p *IdentPat) SetInferredType(t *Type) {
	p.inferredType = t
}

type ObjectPat struct {
	Elems        []*PObjectElem
	span         Span
	inferredType *Type
}

func NewObjectPat(elems []*PObjectElem, span Span) *ObjectPat {
	return &ObjectPat{Elems: elems, span: span, inferredType: nil}
}
func (*ObjectPat) isPat()                {}
func (p *ObjectPat) Span() Span          { return p.span }
func (p *ObjectPat) InferredType() *Type { return p.inferredType }
func (p *ObjectPat) SetInferredType(t *Type) {
	p.inferredType = t
}

type PObjectElem interface{ isPObjectElem() }

type TuplePat struct {
	Elems        []*Pat
	span         Span
	inferredType *Type
}

func NewTuplePat(elems []*Pat, span Span) *TuplePat {
	return &TuplePat{Elems: elems, span: span, inferredType: nil}
}
func (*TuplePat) isPat()                {}
func (p *TuplePat) Span() Span          { return p.span }
func (p *TuplePat) InferredType() *Type { return p.inferredType }
func (p *TuplePat) SetInferredType(t *Type) {
	p.inferredType = t
}

type ExtractPat struct {
	Name         string // TODO: QualIdent
	Args         []*Pat
	span         Span
	inferredType *Type
}

func NewExtractPat(name string, args []*Pat, span Span) *ExtractPat {
	return &ExtractPat{Name: name, Args: args, span: span, inferredType: nil}
}
func (*ExtractPat) isPat()                {}
func (p *ExtractPat) Span() Span          { return p.span }
func (p *ExtractPat) InferredType() *Type { return p.inferredType }
func (p *ExtractPat) SetInferredType(t *Type) {
	p.inferredType = t
}

type LitPat struct {
	Lit          *Lit
	span         Span
	inferredType *Type
}

func NewLitPat(lit *Lit, span Span) *LitPat {
	return &LitPat{Lit: lit, span: span, inferredType: nil}
}
func (*LitPat) isPat()                {}
func (p *LitPat) Span() Span          { return p.span }
func (p *LitPat) InferredType() *Type { return p.inferredType }
func (p *LitPat) SetInferredType(t *Type) {
	p.inferredType = t
}

type RestPat struct {
	Pattern      *Pat
	span         Span
	inferredType *Type
}

func NewRestPat(pattern *Pat, span Span) *RestPat {
	return &RestPat{Pattern: pattern, span: span, inferredType: nil}
}
func (*RestPat) isPat()                {}
func (p *RestPat) Span() Span          { return p.span }
func (p *RestPat) InferredType() *Type { return p.inferredType }
func (p *RestPat) SetInferredType(t *Type) {
	p.inferredType = t
}

type WildcardPat struct {
	span         Span
	inferredType *Type
}

func NewWildcardPat(span Span) *WildcardPat {
	return &WildcardPat{span: span, inferredType: nil}
}
func (*WildcardPat) isPat()                {}
func (p *WildcardPat) Span() Span          { return p.span }
func (p *WildcardPat) InferredType() *Type { return p.inferredType }
func (p *WildcardPat) SetInferredType(t *Type) {
	p.inferredType = t
}
