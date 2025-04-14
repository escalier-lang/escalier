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

type Param struct {
	Pattern Pat
	// TODO: include type annotation
}

func (p *Param) Span() Span {
	return p.Pattern.Span()
}

type FuncDecl struct {
	Name    *Ident
	Params  []*Param
	Return  optional.Option[TypeAnn] // required when `declare` is true
	Throws  optional.Option[TypeAnn] // required when `declare` is true
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
		Name:    name,
		Params:  params,
		Return:  nil, // TODO
		Throws:  nil, // TODO
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
