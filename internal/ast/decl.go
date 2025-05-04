package ast

import "github.com/moznion/go-optional"

type DeclGetters interface {
	Export() bool
	Declare() bool
}

//sumtype:decl
type Decl interface {
	isDecl()
	DeclGetters
	Node
}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

type VarDecl struct {
	Kind    VariableKind
	Pattern Pat
	Init    optional.Option[Expr]
	export  bool
	declare bool
	span    Span
}

func NewVarDecl(kind VariableKind, pattern Pat, init optional.Option[Expr], export, declare bool, span Span) *VarDecl {
	return &VarDecl{Kind: kind, Pattern: pattern, Init: init, export: export, declare: declare, span: span}
}
func (*VarDecl) isDecl()         {}
func (d *VarDecl) Export() bool  { return d.export }
func (d *VarDecl) Declare() bool { return d.declare }
func (d *VarDecl) Span() Span    { return d.span }
func (d *VarDecl) Accept(v Visitor) {
	if v.VisitDecl(d) {
		d.Pattern.Accept(v)
		d.Init.IfSome(func(expr Expr) {
			expr.Accept(v)
		})
	}
}

type Param struct {
	Pattern  Pat
	Optional bool
	TypeAnn  optional.Option[TypeAnn]
}

func (p *Param) Span() Span {
	return p.Pattern.Span()
}

type FuncDecl struct {
	Name *Ident
	FuncSig
	Body    optional.Option[Block]
	export  bool
	declare bool
	span    Span
}

func NewFuncDecl(
	name *Ident,
	params []*Param,
	body optional.Option[Block],
	export,
	declare bool,
	span Span,
) *FuncDecl {
	return &FuncDecl{
		Name: name,
		FuncSig: FuncSig{
			TypeParams: []*TypeParam{}, // TODO
			Params:     params,
			Return:     nil, // TODO
			Throws:     nil, // TODO
		},
		Body:    body,
		export:  export,
		declare: declare,
		span:    span,
	}
}
func (*FuncDecl) isDecl()         {}
func (d *FuncDecl) Export() bool  { return d.export }
func (d *FuncDecl) Declare() bool { return d.declare }
func (d *FuncDecl) Span() Span    { return d.span }
func (d *FuncDecl) Accept(v Visitor) {
	if v.VisitDecl(d) {
		for _, param := range d.Params {
			param.Pattern.Accept(v)
		}
		d.Body.IfSome(func(body Block) {
			for _, stmt := range body.Stmts {
				stmt.Accept(v)
			}
		})
	}
}
