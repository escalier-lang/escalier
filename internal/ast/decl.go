package ast

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
	Name    *Ident // TODO: replace with Pattern
	Init    Expr
	export  bool
	declare bool
	span    Span
}

func NewVarDecl(kind VariableKind, name *Ident, init Expr, export, declare bool, span Span) *VarDecl {
	return &VarDecl{Kind: kind, Name: name, Init: init, export: export, declare: declare, span: span}
}
func (*VarDecl) isDecl()         {}
func (d *VarDecl) Export() bool  { return d.export }
func (d *VarDecl) Declare() bool { return d.declare }
func (d *VarDecl) Span() Span    { return d.span }

type Param struct {
	Name *Ident // TODO: replace with Pattern
	// TODO: include type annotation
}

type FuncDecl struct {
	Name    *Ident
	Params  []*Param
	Body    Block
	export  bool
	declare bool
	span    Span
}

func NewFuncDecl(name *Ident, params []*Param, body Block, export, declare bool, span Span) *FuncDecl {
	return &FuncDecl{Name: name, Params: params, Body: body, export: export, declare: declare, span: span}
}
func (*FuncDecl) isDecl()         {}
func (d *FuncDecl) Export() bool  { return d.export }
func (d *FuncDecl) Declare() bool { return d.declare }
func (d *FuncDecl) Span() Span    { return d.span }
