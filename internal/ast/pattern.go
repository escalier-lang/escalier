//go:generate go run ../../tools/gen_ast/gen_ast.go -p ./pattern.go

package ast

import "github.com/escalier-lang/escalier/internal/set"

type Pat interface {
	isPat()
	Node
	Inferrable
}

func (*IdentPat) isPat()     {}
func (*ObjectPat) isPat()    {}
func (*TuplePat) isPat()     {}
func (*ExtractorPat) isPat() {}
func (*InstancePat) isPat()  {}
func (*RestPat) isPat()      {}
func (*LitPat) isPat()       {}
func (*WildcardPat) isPat()  {}

type IdentPat struct {
	Name         string
	TypeAnn      TypeAnn // optional
	Default      Expr    // optional
	span         Span
	inferredType Type
}

func NewIdentPat(name string, typeAnn TypeAnn, _default Expr, span Span) *IdentPat {
	return &IdentPat{Name: name, TypeAnn: typeAnn, Default: _default, span: span, inferredType: nil}
}
func (p *IdentPat) Accept(v Visitor) {
	if v.EnterPat(p) {
		if p.TypeAnn != nil {
			p.TypeAnn.Accept(v)
		}
		if p.Default != nil {
			p.Default.Accept(v)
		}
	}
	v.ExitPat(p)
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
	Default      Expr // optional
	span         Span
	inferredType Type
}

func NewObjKeyValuePat(key *Ident, value Pat, _default Expr, span Span) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value, Default: _default, span: span, inferredType: nil}
}
func (p *ObjKeyValuePat) Accept(v Visitor) {
	// TODO
}

type ObjShorthandPat struct {
	Key     *Ident
	TypeAnn TypeAnn // optional
	Default Expr    // optional
	span    Span
}

func NewObjShorthandPat(key *Ident, typeAnn TypeAnn, _default Expr, span Span) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key, TypeAnn: typeAnn, Default: _default, span: span}
}
func (p *ObjShorthandPat) Span() Span { return p.span }
func (p *ObjShorthandPat) Accept(v Visitor) {
	// TODO - individual ObjPatElem Accept methods are not used,
	// visiting is handled by ObjectPat.Accept
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
	if v.EnterPat(p) {
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ObjKeyValuePat:
				e.Value.Accept(v)
			case *ObjShorthandPat:
				if e.TypeAnn != nil {
					e.TypeAnn.Accept(v)
				}
				if e.Default != nil {
					e.Default.Accept(v)
				}
			case *ObjRestPat:
				e.Pattern.Accept(v)
			}
		}
	}
	v.ExitPat(p)
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
	if v.EnterPat(p) {
		for _, elem := range p.Elems {
			elem.Accept(v)
		}
	}
	v.ExitPat(p)
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
	if v.EnterPat(p) {
		for _, arg := range p.Args {
			arg.Accept(v)
		}
	}
	v.ExitPat(p)
}

type InstancePat struct {
	ClassName    string // TODO: QualIdent
	Object       *ObjectPat
	span         Span
	inferredType Type
}

func NewInstancePat(className string, object *ObjectPat, span Span) *InstancePat {
	return &InstancePat{ClassName: className, Object: object, span: span, inferredType: nil}
}
func (p *InstancePat) Accept(v Visitor) {
	if v.EnterPat(p) {
		p.Object.Accept(v)
	}
	v.ExitPat(p)
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
	if v.EnterPat(p) {
		p.Pattern.Accept(v)
	}
	v.ExitPat(p)
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
	if v.EnterPat(p) {
		p.Lit.Accept(v)
	}
	v.ExitPat(p)
}

type WildcardPat struct {
	span         Span
	inferredType Type
}

func NewWildcardPat(span Span) *WildcardPat {
	return &WildcardPat{span: span, inferredType: nil}
}
func (p *WildcardPat) Accept(v Visitor) {
	v.EnterPat(p)
	v.ExitPat(p)
}

type BindingVisitor struct {
	DefaultVisitor
	Bindings set.Set[string]
}

func (v *BindingVisitor) EnterPat(pat Pat) bool {
	switch pat := pat.(type) {
	case *IdentPat:
		v.Bindings.Add(pat.Name)
	case *ObjectPat:
		for _, elem := range pat.Elems {
			switch elem := elem.(type) {
			case *ObjShorthandPat:
				v.Bindings.Add(elem.Key.Name)
			}
		}
	}
	return true
}

func (v *BindingVisitor) EnterStmt(stmt Stmt) bool               { return false }
func (v *BindingVisitor) EnterExpr(expr Expr) bool               { return false }
func (v *BindingVisitor) EnterDecl(decl Decl) bool               { return false }
func (v *BindingVisitor) EnterObjExprElem(elem ObjExprElem) bool { return false }
func (v *BindingVisitor) EnterTypeAnn(t TypeAnn) bool            { return false }
func (v *BindingVisitor) EnterLit(lit Lit) bool                  { return false }

func FindBindings(pat Pat) set.Set[string] {
	visitor := &BindingVisitor{
		DefaultVisitor: DefaultVisitor{},
		Bindings:       set.NewSet[string](),
	}
	pat.Accept(visitor)

	return visitor.Bindings
}
