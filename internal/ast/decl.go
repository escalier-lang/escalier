package ast

import "github.com/escalier-lang/escalier/internal/provenance"

type DeclGetters interface {
	Export() bool
	SetExport(bool)
	Declare() bool
	Provenance() provenance.Provenance
	SetProvenance(p provenance.Provenance)
}

//sumtype:decl

type Decl interface {
	isDecl()
	DeclGetters
	Node
}

func (*VarDecl) isDecl()              {}
func (*FuncDecl) isDecl()             {}
func (*TypeDecl) isDecl()             {}
func (*InterfaceDecl) isDecl()        {}
func (*EnumDecl) isDecl()             {}
func (*ExportAssignmentStmt) isDecl() {}

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
	provenance   provenance.Provenance
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
		provenance:   nil,
	}
}
func (d *VarDecl) Export() bool      { return d.export }
func (d *VarDecl) SetExport(e bool)  { d.export = e }
func (d *VarDecl) Declare() bool     { return d.declare }
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
func (d *VarDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *VarDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
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
	Body       *Block // optional
	export     bool
	declare    bool
	span       Span
	provenance provenance.Provenance
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
		Body:       body,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (d *FuncDecl) Export() bool      { return d.export }
func (d *FuncDecl) SetExport(e bool)  { d.export = e }
func (d *FuncDecl) Declare() bool     { return d.declare }
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
func (d *FuncDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *FuncDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
}

type TypeDecl struct {
	Name       *Ident
	TypeParams []*TypeParam
	TypeAnn    TypeAnn
	export     bool
	declare    bool
	span       Span
	provenance provenance.Provenance
}

func NewTypeDecl(name *Ident, typeParams []*TypeParam, typeAnn TypeAnn, export, declare bool, span Span) *TypeDecl {
	return &TypeDecl{
		Name:       name,
		TypeParams: typeParams,
		TypeAnn:    typeAnn,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (d *TypeDecl) Export() bool      { return d.export }
func (d *TypeDecl) SetExport(e bool)  { d.export = e }
func (d *TypeDecl) Declare() bool     { return d.declare }
func (d *TypeDecl) Span() Span    { return d.span }
func (d *TypeDecl) Accept(v Visitor) {
	// TODO: visit type params
	if v.EnterDecl(d) {
		d.TypeAnn.Accept(v)
	}
	v.ExitDecl(d)
}
func (d *TypeDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *TypeDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
}

type InterfaceDecl struct {
	Name       *Ident
	TypeParams []*TypeParam
	Extends    []*TypeRefTypeAnn
	TypeAnn    *ObjectTypeAnn
	export     bool
	declare    bool
	span       Span
	provenance provenance.Provenance
}

func NewInterfaceDecl(name *Ident, typeParams []*TypeParam, extends []*TypeRefTypeAnn, typeAnn *ObjectTypeAnn, export, declare bool, span Span) *InterfaceDecl {
	return &InterfaceDecl{
		Name:       name,
		TypeParams: typeParams,
		Extends:    extends,
		TypeAnn:    typeAnn,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (d *InterfaceDecl) Export() bool      { return d.export }
func (d *InterfaceDecl) SetExport(e bool)  { d.export = e }
func (d *InterfaceDecl) Declare() bool     { return d.declare }
func (d *InterfaceDecl) Span() Span    { return d.span }
func (d *InterfaceDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		for _, tp := range d.TypeParams {
			if tp.Constraint != nil {
				tp.Constraint.Accept(v)
			}
			if tp.Default != nil {
				tp.Default.Accept(v)
			}
		}
		for _, ext := range d.Extends {
			ext.Accept(v)
		}
		d.TypeAnn.Accept(v)
	}
	v.ExitDecl(d)
}
func (d *InterfaceDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *InterfaceDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
}

// EnumVariant represents a single variant of an enum
// e.g., Some(T) or None or Circle {center: Point, radius: number}
// EnumElem is an interface for enum elements (variants or spreads)
type EnumElem interface {
	IsEnumElem()
	Span() Span
	Node
}

type EnumVariant struct {
	Name   *Ident
	Params []*Param // optional tuple parameters, e.g., Some(value: T)
	span   Span
}

func NewEnumVariant(name *Ident, params []*Param, span Span) *EnumVariant {
	return &EnumVariant{
		Name:   name,
		Params: params,
		span:   span,
	}
}
func (v *EnumVariant) Span() Span  { return v.span }
func (v *EnumVariant) IsEnumElem() {}
func (v *EnumVariant) Accept(vis Visitor) {
	// TODO
}

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
func (s *EnumSpread) Accept(v Visitor) {
	// TODO
}

// EnumDecl represents an enum declaration
// e.g., enum Maybe<T> { Some(T), None }
type EnumDecl struct {
	Name       *Ident
	TypeParams []*TypeParam
	Elems      []EnumElem // variants and spreads
	export     bool
	declare    bool
	span       Span
	provenance provenance.Provenance
}

func NewEnumDecl(name *Ident, typeParams []*TypeParam, elems []EnumElem, export, declare bool, span Span) *EnumDecl {
	return &EnumDecl{
		Name:       name,
		TypeParams: typeParams,
		Elems:      elems,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (d *EnumDecl) Export() bool      { return d.export }
func (d *EnumDecl) SetExport(e bool)  { d.export = e }
func (d *EnumDecl) Declare() bool     { return d.declare }
func (d *EnumDecl) Span() Span    { return d.span }
func (d *EnumDecl) Accept(v Visitor) {
	// TODO: visit type params
	if v.EnterDecl(d) {
		for _, elem := range d.Elems {
			switch e := elem.(type) {
			case *EnumVariant:
				e.Name.Accept(v)
				for _, param := range e.Params {
					param.Pattern.Accept(v)
				}
			case *EnumSpread:
				e.Arg.Accept(v)
			}
		}
	}
	v.ExitDecl(d)
}
func (d *EnumDecl) Provenance() provenance.Provenance {
	return d.provenance
}
func (d *EnumDecl) SetProvenance(p provenance.Provenance) {
	d.provenance = p
}

// ExportAssignmentStmt represents: export = identifier (TypeScript interop only)
// This is used when converting TypeScript .d.ts files that use the CommonJS-style
// export assignment pattern. Escalier's parser does not produce this node.
type ExportAssignmentStmt struct {
	Name       *Ident
	declare    bool
	span       Span
	provenance provenance.Provenance
}

func NewExportAssignmentStmt(name *Ident, declare bool, span Span) *ExportAssignmentStmt {
	return &ExportAssignmentStmt{
		Name:       name,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (e *ExportAssignmentStmt) Export() bool                          { return true } // Always exported
func (e *ExportAssignmentStmt) SetExport(bool)                        {}              // No-op, always exported
func (e *ExportAssignmentStmt) Declare() bool                         { return e.declare }
func (e *ExportAssignmentStmt) Span() Span                            { return e.span }
func (e *ExportAssignmentStmt) Accept(v Visitor)                      { v.EnterDecl(e); v.ExitDecl(e) }
func (e *ExportAssignmentStmt) Provenance() provenance.Provenance     { return e.provenance }
func (e *ExportAssignmentStmt) SetProvenance(p provenance.Provenance) { e.provenance = p }
