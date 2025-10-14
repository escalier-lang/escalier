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
func (*EnumDecl) isDecl() {}

func (*VarDecl) isNode()  {}
func (*FuncDecl) isNode() {}
func (*TypeDecl) isNode() {}
func (*EnumDecl) isNode() {}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

type VarDecl struct {
	Kind         VariableKind
	Pattern      Pat
	TypeAnn      TypeAnn // optional
	Init         Expr    // optional
	export       bool
	declare      bool
	span         Span
	InferredType Type // optional, used to store the inferred pattern type
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
		Kind:         kind,
		Pattern:      pattern,
		TypeAnn:      typeAnn,
		Init:         init,
		export:       export,
		declare:      declare,
		span:         span,
		InferredType: nil,
	}
}
func (d *VarDecl) Export() bool  { return d.export }
func (d *VarDecl) Declare() bool { return d.declare }
func (d *VarDecl) Span() Span    { return d.span }
func (d *VarDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		d.Pattern.Accept(v)
		if d.Init != nil {
			d.Init.Accept(v)
		}
	}
	v.ExitDecl(d)
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
	typeParams []*TypeParam,
	params []*Param,
	returnType TypeAnn, // optional
	throwsType TypeAnn, // optional
	body *Block,
	export,
	declare,
	async bool,
	span Span,
) *FuncDecl {
	return &FuncDecl{
		Name: name,
		FuncSig: FuncSig{
			TypeParams: typeParams,
			Params:     params,
			Return:     returnType,
			Throws:     throwsType,
			Async:      async,
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
	if v.EnterDecl(d) {
		for _, param := range d.Params {
			param.Pattern.Accept(v)
		}
		if d.Return != nil {
			d.Return.Accept(v)
		}
		if d.Body != nil {
			d.Body.Accept(v)
		}
	}
	v.ExitDecl(d)
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
	if v.EnterDecl(d) {
		d.TypeAnn.Accept(v)
	}
	v.ExitDecl(d)
}

// EnumVariant represents a single variant of an enum
// e.g., Some(T) or None
// EnumElem is an interface for enum elements (variants or spreads)
type EnumElem interface {
	IsEnumElem()
	Span() Span
}

type EnumVariant struct {
	Name   *Ident
	Params []TypeAnn // optional tuple parameters
	span   Span
}

func NewEnumVariant(name *Ident, params []TypeAnn, span Span) *EnumVariant {
	return &EnumVariant{
		Name:   name,
		Params: params,
		span:   span,
	}
}
func (v *EnumVariant) Span() Span  { return v.span }
func (v *EnumVariant) IsEnumElem() {}

// EnumSpread represents a spread notation in an enum
// e.g., ...OtherEnum
type EnumSpread struct {
	Arg  *Ident
	span Span
}

func NewEnumSpread(arg *Ident, span Span) *EnumSpread {
	return &EnumSpread{
		Arg:  arg,
		span: span,
	}
}
func (s *EnumSpread) Span() Span  { return s.span }
func (s *EnumSpread) IsEnumElem() {}

// EnumDecl represents an enum declaration
// e.g., enum Maybe<T> { Some(T), None }
type EnumDecl struct {
	Name       *Ident
	TypeParams []*TypeParam
	Elems      []EnumElem // variants and spreads
	export     bool
	declare    bool
	span       Span
}

func NewEnumDecl(name *Ident, typeParams []*TypeParam, elems []EnumElem, export, declare bool, span Span) *EnumDecl {
	return &EnumDecl{
		Name:       name,
		TypeParams: typeParams,
		Elems:      elems,
		export:     export,
		declare:    declare,
		span:       span,
	}
}
func (d *EnumDecl) Export() bool  { return d.export }
func (d *EnumDecl) Declare() bool { return d.declare }
func (d *EnumDecl) Span() Span    { return d.span }
func (d *EnumDecl) Accept(v Visitor) {
	// TODO: visit type params
	if v.EnterDecl(d) {
		for _, elem := range d.Elems {
			switch e := elem.(type) {
			case *EnumVariant:
				e.Name.Accept(v)
				for _, param := range e.Params {
					param.Accept(v)
				}
			case *EnumSpread:
				e.Arg.Accept(v)
			}
		}
	}
	v.ExitDecl(d)
}
