package ast

import "github.com/escalier-lang/escalier/internal/provenance"

type DeclGetters interface {
	Export() bool
	SetExport(bool)
	Declare() bool
	Override() bool
	SetOverride(bool)
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
func (*DeclareModuleDecl) isDecl()    {}
func (*DeclareGlobalDecl) isDecl()    {}
func (*NamespaceDecl) isDecl()        {}

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
	// Else is the `else` block of a `let`-`else` binding (`val pat = init else
	// { … }`). It is non-nil only for that refutable form, where the pattern may
	// fail to match and the block runs and must diverge. A plain `val`/`var` leaves
	// it nil.
	Else         *Block
	Decorators   []*Decorator
	export       bool
	declare      bool
	override     bool
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
func (d *VarDecl) Export() bool       { return d.export }
func (d *VarDecl) SetExport(e bool)   { d.export = e }
func (d *VarDecl) Declare() bool      { return d.declare }
func (d *VarDecl) Override() bool     { return d.override }
func (d *VarDecl) SetOverride(o bool) { d.override = o }
func (d *VarDecl) Span() Span         { return d.span }
func (d *VarDecl) Accept(v Visitor) {
	// TODO(#634): traverse d.Decorators once Decorator has Accept.
	if v.EnterDecl(d) {
		d.Pattern.Accept(v)
		if d.TypeAnn != nil {
			d.TypeAnn.Accept(v)
		}
		if d.Init != nil {
			d.Init.Accept(v)
		}
		if d.Else != nil {
			d.Else.Accept(v)
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
	// Open marks an `open p` parameter: its usage-inferred object stays
	// row-polymorphic (inexact) instead of closing to exact, so callers may pass
	// objects with extra fields. The provisional `open` keyword is recognized only
	// before a parameter pattern; elsewhere `open` is an ordinary identifier.
	Open    bool
	TypeAnn TypeAnn // optional
}

func (p *Param) Span() Span {
	return p.Pattern.Span()
}

type FuncDecl struct {
	Name  *Ident
	VarID int // Set by the rename pass (liveness Phase 2)
	FuncSig
	Body       *Block // optional
	Decorators []*Decorator
	export     bool
	declare    bool
	override   bool
	span       Span
	provenance provenance.Provenance
}

func NewFuncDecl(
	name *Ident,
	lifetimeParams []*LifetimeAnn,
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
			LifetimeParams: lifetimeParams,
			TypeParams:     typeParams,
			Params:         params,
			Return:         returnType,
			Throws:         throwsType,
			Async:          async,
		},
		Body:       body,
		export:     export,
		declare:    declare,
		span:       span,
		provenance: nil,
	}
}
func (d *FuncDecl) Export() bool       { return d.export }
func (d *FuncDecl) SetExport(e bool)   { d.export = e }
func (d *FuncDecl) Declare() bool      { return d.declare }
func (d *FuncDecl) Override() bool     { return d.override }
func (d *FuncDecl) SetOverride(o bool) { d.override = o }
func (d *FuncDecl) Span() Span         { return d.span }
func (d *FuncDecl) Accept(v Visitor) {
	// TODO(#634): traverse d.Decorators once Decorator has Accept.
	// TODO(#635): once FuncSig has SelfParam, visit it before d.Params.
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
	override   bool
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
func (d *TypeDecl) Export() bool       { return d.export }
func (d *TypeDecl) SetExport(e bool)   { d.export = e }
func (d *TypeDecl) Declare() bool      { return d.declare }
func (d *TypeDecl) Override() bool     { return d.override }
func (d *TypeDecl) SetOverride(o bool) { d.override = o }
func (d *TypeDecl) Span() Span         { return d.span }
func (d *TypeDecl) Accept(v Visitor) {
	// TODO: visit type params
	if v.EnterDecl(d) {
		if d.TypeAnn != nil {
			d.TypeAnn.Accept(v)
		}
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
	Name           *Ident
	LifetimeParams []*LifetimeAnn
	TypeParams     []*TypeParam
	Extends        []*TypeRefTypeAnn
	TypeAnn        *ObjectTypeAnn
	export         bool
	declare        bool
	override       bool
	span           Span
	provenance     provenance.Provenance
}

func NewInterfaceDecl(name *Ident, lifetimeParams []*LifetimeAnn, typeParams []*TypeParam, extends []*TypeRefTypeAnn, typeAnn *ObjectTypeAnn, export, declare bool, span Span) *InterfaceDecl {
	return &InterfaceDecl{
		Name:           name,
		LifetimeParams: lifetimeParams,
		TypeParams:     typeParams,
		Extends:        extends,
		TypeAnn:        typeAnn,
		export:         export,
		declare:        declare,
		span:           span,
		provenance:     nil,
	}
}
func (d *InterfaceDecl) Export() bool       { return d.export }
func (d *InterfaceDecl) SetExport(e bool)   { d.export = e }
func (d *InterfaceDecl) Declare() bool      { return d.declare }
func (d *InterfaceDecl) Override() bool     { return d.override }
func (d *InterfaceDecl) SetOverride(o bool) { d.override = o }
func (d *InterfaceDecl) Span() Span         { return d.span }
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
	override   bool
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
func (d *EnumDecl) Export() bool       { return d.export }
func (d *EnumDecl) SetExport(e bool)   { d.export = e }
func (d *EnumDecl) Declare() bool      { return d.declare }
func (d *EnumDecl) Override() bool     { return d.override }
func (d *EnumDecl) SetOverride(o bool) { d.override = o }
func (d *EnumDecl) Span() Span         { return d.span }
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
	override   bool
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
func (e *ExportAssignmentStmt) Export() bool       { return true } // Always exported
func (e *ExportAssignmentStmt) SetExport(bool)     {}              // No-op, always exported
func (e *ExportAssignmentStmt) Declare() bool      { return e.declare }
func (e *ExportAssignmentStmt) Override() bool     { return e.override }
func (e *ExportAssignmentStmt) SetOverride(o bool) { e.override = o }
func (e *ExportAssignmentStmt) Span() Span         { return e.span }
func (e *ExportAssignmentStmt) Accept(v Visitor) {
	if v.EnterDecl(e) {
		// Nothing to walk - ExportAssignmentStmt has no child nodes to visit
	}
	v.ExitDecl(e)
}
func (e *ExportAssignmentStmt) Provenance() provenance.Provenance     { return e.provenance }
func (e *ExportAssignmentStmt) SetProvenance(p provenance.Provenance) { e.provenance = p }

// DeclareModuleDecl represents `declare module "<name>" { <decl>* }` written
// in Escalier source (.esc files), optionally prefixed by `override`.
//
// Note: .d.ts files are handled by an entirely separate pipeline
// (internal/dts_parser + internal/interop) that has its own
// dts_parser.ModuleDecl and dts_parser.GlobalDecl types. Those are
// classified and converted into Escalier AST nodes before they ever
// reach this package. DeclareModuleDecl and DeclareGlobalDecl below
// exist solely for the Escalier override-file format.
type DeclareModuleDecl struct {
	Name       *StrLit // module name as a string literal
	Decls      []Decl
	override   bool
	span       Span
	provenance provenance.Provenance
}

func NewDeclareModuleDecl(name *StrLit, decls []Decl, override bool, span Span) *DeclareModuleDecl {
	return &DeclareModuleDecl{
		Name:       name,
		Decls:      decls,
		override:   override,
		span:       span,
		provenance: nil,
	}
}
func (d *DeclareModuleDecl) Export() bool       { return false }
func (d *DeclareModuleDecl) SetExport(bool)     {}
func (d *DeclareModuleDecl) Declare() bool      { return true }
func (d *DeclareModuleDecl) Override() bool     { return d.override }
func (d *DeclareModuleDecl) SetOverride(o bool) { d.override = o }
func (d *DeclareModuleDecl) Span() Span         { return d.span }
func (d *DeclareModuleDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		for _, inner := range d.Decls {
			inner.Accept(v)
		}
	}
	v.ExitDecl(d)
}
func (d *DeclareModuleDecl) Provenance() provenance.Provenance     { return d.provenance }
func (d *DeclareModuleDecl) SetProvenance(p provenance.Provenance) { d.provenance = p }

// DeclareGlobalDecl represents `declare global { <decl>* }` written in
// Escalier source (.esc files), optionally prefixed by `override`. See the
// note on DeclareModuleDecl for how this differs from dts_parser.GlobalDecl.
type DeclareGlobalDecl struct {
	Decls      []Decl
	override   bool
	span       Span
	provenance provenance.Provenance
}

func NewDeclareGlobalDecl(decls []Decl, override bool, span Span) *DeclareGlobalDecl {
	return &DeclareGlobalDecl{
		Decls:      decls,
		override:   override,
		span:       span,
		provenance: nil,
	}
}
func (d *DeclareGlobalDecl) Export() bool       { return false }
func (d *DeclareGlobalDecl) SetExport(bool)     {}
func (d *DeclareGlobalDecl) Declare() bool      { return true }
func (d *DeclareGlobalDecl) Override() bool     { return d.override }
func (d *DeclareGlobalDecl) SetOverride(o bool) { d.override = o }
func (d *DeclareGlobalDecl) Span() Span         { return d.span }
func (d *DeclareGlobalDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		for _, inner := range d.Decls {
			inner.Accept(v)
		}
	}
	v.ExitDecl(d)
}
func (d *DeclareGlobalDecl) Provenance() provenance.Provenance     { return d.provenance }
func (d *DeclareGlobalDecl) SetProvenance(p provenance.Provenance) { d.provenance = p }

// NamespaceDecl represents `namespace Name { <decl>* }` inside a declare
// block (e.g. inside `declare module "x" { ... }` or `declare global { ... }`).
// Like DeclareModuleDecl/DeclareGlobalDecl, this is an Escalier-source construct;
// the dts_parser has its own dts_parser.NamespaceDecl for .d.ts files.
// Declare() always returns true because namespaces are inherently ambient.
type NamespaceDecl struct {
	Name       *Ident
	Decls      []Decl
	export     bool
	override   bool
	span       Span
	provenance provenance.Provenance
}

func NewNamespaceDecl(name *Ident, decls []Decl, export, override bool, span Span) *NamespaceDecl {
	return &NamespaceDecl{
		Name:       name,
		Decls:      decls,
		export:     export,
		override:   override,
		span:       span,
		provenance: nil,
	}
}
func (d *NamespaceDecl) Export() bool       { return d.export }
func (d *NamespaceDecl) SetExport(e bool)   { d.export = e }
func (d *NamespaceDecl) Declare() bool      { return true }
func (d *NamespaceDecl) Override() bool     { return d.override }
func (d *NamespaceDecl) SetOverride(o bool) { d.override = o }
func (d *NamespaceDecl) Span() Span         { return d.span }
func (d *NamespaceDecl) Accept(v Visitor) {
	if v.EnterDecl(d) {
		for _, inner := range d.Decls {
			inner.Accept(v)
		}
	}
	v.ExitDecl(d)
}
func (d *NamespaceDecl) Provenance() provenance.Provenance     { return d.provenance }
func (d *NamespaceDecl) SetProvenance(p provenance.Provenance) { d.provenance = p }
