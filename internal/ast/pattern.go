//go:generate go run ../../tools/gen_ast/gen_ast.go -p ./pattern.go

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
func (p *ObjectPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ObjKeyValuePat:
				e.Value.Accept(v)
			case *ObjShorthandPat:
				e.Default.IfSome(func(expr Expr) {
					expr.Accept(v)
				})
			case *ObjRestPat:
				e.Pattern.Accept(v)
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
func (p *TuplePat) Accept(v Visitor) {
	if v.VisitPat(p) {
		for _, elem := range p.Elems {
			elem.Accept(v)
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
func (p *LitPat) Accept(v Visitor) {
	if v.VisitPat(p) {
		p.Lit.Accept(v)
	}
}

type WildcardPat struct {
	span         Span
	inferredType Type
}

func NewWildcardPat(span Span) *WildcardPat {
	return &WildcardPat{span: span, inferredType: nil}
}
func (p *WildcardPat) Accept(v Visitor) {
	v.VisitPat(p)
}
