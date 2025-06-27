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

func (*VarDecl) isDecl()  {}
func (*FuncDecl) isDecl() {}
func (*TypeDecl) isDecl() {}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

type VarDecl struct {
	Kind    VariableKind
	Pattern Pat
	TypeAnn TypeAnn // optional
	Init    Expr    // optional
	export  bool
	declare bool
	span    Span
}

func NewVarDecl(
	kind VariableKind,
	pattern Pat,
	typeAnn TypeAnn,
	init Expr,
	export,
	declare bool,
	span Span,
) *VarDecl {
	return &VarDecl{
		Kind:    kind,
		Pattern: pattern,
		TypeAnn: typeAnn,
		Init:    init,
		export:  export,
		declare: declare,
		span:    span,
	}
}
func (d *VarDecl) Export() bool  { return d.export }
func (d *VarDecl) Declare() bool { return d.declare }
func (d *VarDecl) Span() Span    { return d.span }
func (d *VarDecl) Accept(v Visitor) {
	if v.VisitDecl(d) {
		d.Pattern.Accept(v)
		if d.Init != nil {
			d.Init.Accept(v)
		}
	}
}

type Param struct {
	Pattern  Pat
	Optional bool
	TypeAnn  TypeAnn // optional
}

func (p *Param) Span() Span {
	return p.Pattern.Span()
}

type FuncDecl struct {
	Name *Ident
	FuncSig
	Body    *Block // optional
	export  bool
	declare bool
	span    Span
}

func NewFuncDecl(
	name *Ident,
	params []*Param,
	body *Block,
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
func (d *FuncDecl) Export() bool  { return d.export }
func (d *FuncDecl) Declare() bool { return d.declare }
func (d *FuncDecl) Span() Span    { return d.span }
func (d *FuncDecl) Accept(v Visitor) {
	if v.VisitDecl(d) {
		for _, param := range d.Params {
			param.Pattern.Accept(v)
		}
		if d.Body != nil {
			for _, stmt := range d.Body.Stmts {
				stmt.Accept(v)
			}
		}
	}
}

type TypeDecl struct {
	Name       *Ident
	TypeParams []*TypeParam
	TypeAnn    TypeAnn
	export     bool
	declare    bool
	span       Span
}

func NewTypeDecl(name *Ident, typeParams []*TypeParam, typeAnn TypeAnn, export, declare bool, span Span) *TypeDecl {
	return &TypeDecl{
		Name:       name,
		TypeParams: typeParams,
		TypeAnn:    typeAnn,
		export:     export,
		declare:    declare,
		span:       span,
	}
}
func (d *TypeDecl) Export() bool  { return d.export }
func (d *TypeDecl) Declare() bool { return d.declare }
func (d *TypeDecl) Span() Span    { return d.span }
func (d *TypeDecl) Accept(v Visitor) {
	// TODO: visit type params
	if v.VisitDecl(d) {
		d.TypeAnn.Accept(v)
	}
}
