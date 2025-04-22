package ast

import (
	"github.com/moznion/go-optional"
)

type Pat interface {
	isPat()
	Node
	Inferrable
}

func (*IdentPat) isPat()     {}
func (*ObjectPat) isPat()    {}
func (*TuplePat) isPat()     {}
func (*ExtractorPat) isPat() {}
func (*RestPat) isPat()      {}
func (*LitPat) isPat()       {}
func (*WildcardPat) isPat()  {}

type IdentPat struct {
	Name         string
	Default      optional.Option[Expr]
	span         Span
	inferredType Type
}

func NewIdentPat(name string, _default optional.Option[Expr], span Span) *IdentPat {
	return &IdentPat{Name: name, Default: _default, span: span, inferredType: nil}
}
func (p *IdentPat) Span() Span             { return p.span }
func (p *IdentPat) InferredType() Type     { return p.inferredType }
func (p *IdentPat) SetInferredType(t Type) { p.inferredType = t }
func (p *IdentPat) Accept(v Visitor) {
	v.VisitPat(p)
}

type ObjPatElem interface {
	isObjPatElem()
	Node
}

func (*ObjKeyValuePat) isObjPatElem()  {}
func (*ObjShorthandPat) isObjPatElem() {}
func (*ObjRestPat) isObjPatElem()      {}

type ObjKeyValuePat struct {
	Key          *Ident
	Value        Pat
	Default      optional.Option[Expr]
	span         Span
	inferredType Type
}

func NewObjKeyValuePat(key *Ident, value Pat, _default optional.Option[Expr], span Span) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value, Default: _default, span: span, inferredType: nil}
}
func (p *ObjKeyValuePat) Span() Span { return p.span }
func (p *ObjKeyValuePat) Accept(v Visitor) {
	// TODO
}

type ObjShorthandPat struct {
	Key     *Ident
	Default optional.Option[Expr]
	span    Span
}

func NewObjShorthandPat(key *Ident, _default optional.Option[Expr], span Span) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key, Default: _default, span: span}
}
func (p *ObjShorthandPat) Span() Span { return p.span }
func (p *ObjShorthandPat) Accept(v Visitor) {
	// TODO
}

type ObjRestPat struct {
	Pattern Pat
	span    Span
}

func NewObjRestPat(pattern Pat, span Span) *ObjRestPat {
	return &ObjRestPat{Pattern: pattern, span: span}
}
func (p *ObjRestPat) Span() Span { return p.span }
func (p *ObjRestPat) Accept(v Visitor) {
	// TODO
}

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
func (p *ObjectPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ObjKeyValuePat:
				v.VisitPat(e.Value)
			case *ObjShorthandPat:
				e.Default.IfSome(func(expr Expr) {
					v.VisitExpr(expr)
				})
			case *ObjRestPat:
				v.VisitPat(e.Pattern)
			}
		}
	}
}

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
func (p *TuplePat) Accept(v Visitor) {
	if v.VisitPat(p) {
		for _, elem := range p.Elems {
			v.VisitPat(elem)
		}
	}
}

type ExtractorPat struct {
	Name         string // TODO: QualIdent
	Args         []Pat
	span         Span
	inferredType Type
}

func NewExtractorPat(name string, args []Pat, span Span) *ExtractorPat {
	return &ExtractorPat{Name: name, Args: args, span: span, inferredType: nil}
}
func (p *ExtractorPat) Span() Span             { return p.span }
func (p *ExtractorPat) InferredType() Type     { return p.inferredType }
func (p *ExtractorPat) SetInferredType(t Type) { p.inferredType = t }
func (p *ExtractorPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		for _, arg := range p.Args {
			v.VisitPat(arg)
		}
	}
}

type RestPat struct {
	Pattern      Pat
	span         Span
	inferredType Type
}

func NewRestPat(pattern Pat, span Span) *RestPat {
	return &RestPat{Pattern: pattern, span: span, inferredType: nil}
}
func (p *RestPat) Span() Span             { return p.span }
func (p *RestPat) InferredType() Type     { return p.inferredType }
func (p *RestPat) SetInferredType(t Type) { p.inferredType = t }
func (p *RestPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		v.VisitPat(p.Pattern)
	}
}

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
func (p *LitPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		v.VisitLit(p.Lit)
	}
}

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
func (p *WildcardPat) Accept(v Visitor) {
	v.VisitPat(p)
}
