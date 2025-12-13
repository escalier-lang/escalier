package dts_parser

import "github.com/escalier-lang/escalier/internal/ast"

// Node is the base interface for all AST nodes
type Node interface {
	Span() ast.Span
}

// ============================================================================
// Identifiers and References
// ============================================================================

type Ident struct {
	Name string
	span ast.Span
}

func NewIdent(name string, span ast.Span) *Ident {
	return &Ident{Name: name, span: span}
}

func (i *Ident) Span() ast.Span { return i.span }

type QualIdent interface {
	isQualIdent()
	Span() ast.Span
}

func (*Ident) isQualIdent()  {}
func (*Member) isQualIdent() {}

type Member struct {
	Left  QualIdent
	Right *Ident
}

func (m *Member) Span() ast.Span {
	return ast.Span{
		Start:    m.Left.Span().Start,
		End:      m.Right.Span().End,
		SourceID: m.Left.Span().SourceID,
	}
}

// ============================================================================
// Top-level Structure
// ============================================================================

type Module struct {
	Statements []Statement
}

// Statement represents any top-level declaration or statement in a .d.ts file
type Statement interface {
	isStatement()
	Node
}

func (*DeclareVariable) isStatement()  {}
func (*DeclareFunction) isStatement()  {}
func (*DeclareClass) isStatement()     {}
func (*DeclareInterface) isStatement() {}
func (*DeclareTypeAlias) isStatement() {}
func (*DeclareEnum) isStatement()      {}
func (*DeclareNamespace) isStatement() {}
func (*DeclareModule) isStatement()    {}
func (*ExportDecl) isStatement()       {}
func (*ImportDecl) isStatement()       {}
func (*AmbientDecl) isStatement()      {}

// ============================================================================
// Declarations
// ============================================================================

type DeclareVariable struct {
	Name     *Ident
	TypeAnn  TypeAnn
	Readonly bool // for const declarations
	span     ast.Span
}

func (d *DeclareVariable) Span() ast.Span { return d.span }

type DeclareFunction struct {
	Name       *Ident
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	span       ast.Span
}

func (d *DeclareFunction) Span() ast.Span { return d.span }

type DeclareClass struct {
	Name       *Ident
	TypeParams []*TypeParam
	Extends    TypeAnn // optional
	Implements []TypeAnn
	Members    []ClassMember
	Abstract   bool
	span       ast.Span
}

func (d *DeclareClass) Span() ast.Span { return d.span }

type DeclareInterface struct {
	Name       *Ident
	TypeParams []*TypeParam
	Extends    []TypeAnn
	Members    []InterfaceMember
	span       ast.Span
}

func (d *DeclareInterface) Span() ast.Span { return d.span }

type DeclareTypeAlias struct {
	Name       *Ident
	TypeParams []*TypeParam
	TypeAnn    TypeAnn
	span       ast.Span
}

func (d *DeclareTypeAlias) Span() ast.Span { return d.span }

type DeclareEnum struct {
	Name    *Ident
	Members []*EnumMember
	Const   bool
	span    ast.Span
}

func (d *DeclareEnum) Span() ast.Span { return d.span }

type EnumMember struct {
	Name  *Ident
	Value Literal // optional, can be number or string literal
	span  ast.Span
}

func (e *EnumMember) Span() ast.Span { return e.span }

type DeclareNamespace struct {
	Name       *Ident
	Statements []Statement
	span       ast.Span
}

func (d *DeclareNamespace) Span() ast.Span { return d.span }

type DeclareModule struct {
	Name       string // module name as string literal
	Statements []Statement
	span       ast.Span
}

func (d *DeclareModule) Span() ast.Span { return d.span }

type AmbientDecl struct {
	Declaration Statement
	span        ast.Span
}

func (d *AmbientDecl) Span() ast.Span { return d.span }

// ============================================================================
// Import/Export
// ============================================================================

type ImportDecl struct {
	DefaultImport *Ident // optional
	NamedImports  []*ImportSpecifier
	NamespaceAs   *Ident // optional, for `* as name`
	From          string // module specifier
	TypeOnly      bool
	span          ast.Span
}

func (i *ImportDecl) Span() ast.Span { return i.span }

type ImportSpecifier struct {
	Imported *Ident
	Local    *Ident // optional, same as Imported if not aliased
	span     ast.Span
}

func (i *ImportSpecifier) Span() ast.Span { return i.span }

type ExportDecl struct {
	Declaration   Statement          // optional, for `export ...`
	NamedExports  []*ExportSpecifier // optional, for `export { ... }`
	From          string             // optional, for re-exports
	ExportDefault bool               // for `export default`
	ExportAll     bool               // for `export *`
	TypeOnly      bool
	span          ast.Span
}

func (e *ExportDecl) Span() ast.Span { return e.span }

type ExportSpecifier struct {
	Local    *Ident
	Exported *Ident // optional, same as Local if not aliased
	span     ast.Span
}

func (e *ExportSpecifier) Span() ast.Span { return e.span }

// ============================================================================
// Class and Interface Members
// ============================================================================

type ClassMember interface {
	isClassMember()
	Node
}

func (*ConstructorDecl) isClassMember() {}
func (*MethodDecl) isClassMember()      {}
func (*PropertyDecl) isClassMember()    {}
func (*GetterDecl) isClassMember()      {}
func (*SetterDecl) isClassMember()      {}
func (*IndexSignature) isClassMember()  {}

type InterfaceMember interface {
	isInterfaceMember()
	Node
}

func (*CallSignature) isInterfaceMember()      {}
func (*ConstructSignature) isInterfaceMember() {}
func (*MethodSignature) isInterfaceMember()    {}
func (*PropertySignature) isInterfaceMember()  {}
func (*IndexSignature) isInterfaceMember()     {}
func (*GetterSignature) isInterfaceMember()    {}
func (*SetterSignature) isInterfaceMember()    {}

type ConstructorDecl struct {
	Params    []*Param
	Modifiers Modifiers
	span      ast.Span
}

func (c *ConstructorDecl) Span() ast.Span { return c.span }

type MethodDecl struct {
	Name       PropertyKey
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	Modifiers  Modifiers
	Optional   bool
	span       ast.Span
}

func (m *MethodDecl) Span() ast.Span { return m.span }

type PropertyDecl struct {
	Name      PropertyKey
	TypeAnn   TypeAnn
	Modifiers Modifiers
	Optional  bool
	span      ast.Span
}

func (p *PropertyDecl) Span() ast.Span { return p.span }

type GetterDecl struct {
	Name       PropertyKey
	ReturnType TypeAnn
	Modifiers  Modifiers
	span       ast.Span
}

func (g *GetterDecl) Span() ast.Span { return g.span }

type SetterDecl struct {
	Name      PropertyKey
	Param     *Param
	Modifiers Modifiers
	span      ast.Span
}

func (s *SetterDecl) Span() ast.Span { return s.span }

type CallSignature struct {
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	span       ast.Span
}

func (c *CallSignature) Span() ast.Span { return c.span }

type ConstructSignature struct {
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	span       ast.Span
}

func (c *ConstructSignature) Span() ast.Span { return c.span }

type MethodSignature struct {
	Name       PropertyKey
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	Optional   bool
	span       ast.Span
}

func (m *MethodSignature) Span() ast.Span { return m.span }

type PropertySignature struct {
	Name     PropertyKey
	TypeAnn  TypeAnn
	Optional bool
	Readonly bool
	span     ast.Span
}

func (p *PropertySignature) Span() ast.Span { return p.span }

type GetterSignature struct {
	Name       PropertyKey
	ReturnType TypeAnn
	span       ast.Span
}

func (g *GetterSignature) Span() ast.Span { return g.span }

type SetterSignature struct {
	Name  PropertyKey
	Param *Param
	span  ast.Span
}

func (s *SetterSignature) Span() ast.Span { return s.span }

type IndexSignature struct {
	KeyName   *Ident
	KeyType   TypeAnn // must be string, number, or symbol
	ValueType TypeAnn
	Readonly  bool
	span      ast.Span
}

func (i *IndexSignature) Span() ast.Span { return i.span }

// PropertyKey represents a property key, which can be an identifier, string, number, or computed
type PropertyKey interface {
	isPropertyKey()
	Node
}

func (*Ident) isPropertyKey()         {}
func (*StringLiteral) isPropertyKey() {}
func (*NumberLiteral) isPropertyKey() {}
func (*ComputedKey) isPropertyKey()   {}

type ComputedKey struct {
	Expr TypeAnn // in .d.ts, computed keys use type expressions
	span ast.Span
}

func (c *ComputedKey) Span() ast.Span { return c.span }

// Modifiers represents access modifiers and other flags
type Modifiers struct {
	Public    bool
	Private   bool
	Protected bool
	Static    bool
	Readonly  bool
	Abstract  bool
	Async     bool
	Declare   bool
}

// ============================================================================
// Type Annotations
// ============================================================================

type TypeAnn interface {
	isTypeAnn()
	Node
}

func (*PrimitiveType) isTypeAnn()       {}
func (*LiteralType) isTypeAnn()         {}
func (*TypeReference) isTypeAnn()       {}
func (*ArrayType) isTypeAnn()           {}
func (*TupleType) isTypeAnn()           {}
func (*UnionType) isTypeAnn()           {}
func (*IntersectionType) isTypeAnn()    {}
func (*FunctionType) isTypeAnn()        {}
func (*ConstructorType) isTypeAnn()     {}
func (*ObjectType) isTypeAnn()          {}
func (*ParenthesizedType) isTypeAnn()   {}
func (*IndexedAccessType) isTypeAnn()   {}
func (*ConditionalType) isTypeAnn()     {}
func (*InferType) isTypeAnn()           {}
func (*MappedType) isTypeAnn()          {}
func (*TemplateLiteralType) isTypeAnn() {}
func (*KeyOfType) isTypeAnn()           {}
func (*TypeOfType) isTypeAnn()          {}
func (*ImportType) isTypeAnn()          {}
func (*TypePredicate) isTypeAnn()       {}
func (*ThisType) isTypeAnn()            {}
func (*RestType) isTypeAnn()            {}
func (*OptionalType) isTypeAnn()        {}

// PrimitiveType represents basic TypeScript primitive types
type PrimitiveType struct {
	Kind PrimitiveKind
	span ast.Span
}

func (p *PrimitiveType) Span() ast.Span { return p.span }

type PrimitiveKind int

const (
	PrimAny       PrimitiveKind = iota // TypeScript 'any' type
	PrimUnknown                        // TypeScript 'unknown' type
	PrimVoid                           // TypeScript 'void' type
	PrimNull                           // TypeScript 'null' type
	PrimUndefined                      // TypeScript 'undefined' type
	PrimNever                          // TypeScript 'never' type
	PrimString                         // TypeScript 'string' type
	PrimNumber                         // TypeScript 'number' type
	PrimBoolean                        // TypeScript 'boolean' type
	PrimBigInt                         // TypeScript 'bigint' type
	PrimSymbol                         // TypeScript 'symbol' type
	PrimObject                         // TypeScript 'object' type
)

// LiteralType represents literal types (string, number, boolean, bigint literals)
type LiteralType struct {
	Literal Literal
	span    ast.Span
}

func (l *LiteralType) Span() ast.Span { return l.span }

type Literal interface {
	isLiteral()
	Node
}

func (*StringLiteral) isLiteral()  {}
func (*NumberLiteral) isLiteral()  {}
func (*BooleanLiteral) isLiteral() {}
func (*BigIntLiteral) isLiteral()  {}

type StringLiteral struct {
	Value string
	span  ast.Span
}

func (s *StringLiteral) Span() ast.Span { return s.span }

type NumberLiteral struct {
	Value float64
	span  ast.Span
}

func (n *NumberLiteral) Span() ast.Span { return n.span }

type BooleanLiteral struct {
	Value bool
	span  ast.Span
}

func (b *BooleanLiteral) Span() ast.Span { return b.span }

type BigIntLiteral struct {
	Value string // stored as string to preserve precision
	span  ast.Span
}

func (b *BigIntLiteral) Span() ast.Span { return b.span }

// TypeReference represents a reference to a named type with optional type arguments
type TypeReference struct {
	Name     QualIdent
	TypeArgs []TypeAnn
	span     ast.Span
}

func (t *TypeReference) Span() ast.Span { return t.span }

// ArrayType represents T[] or readonly T[]
type ArrayType struct {
	ElementType TypeAnn
	Readonly    bool
	span        ast.Span
}

func (a *ArrayType) Span() ast.Span { return a.span }

// TupleType represents [T1, T2, ...]
type TupleType struct {
	Elements []TupleElement
	span     ast.Span
}

func (t *TupleType) Span() ast.Span { return t.span }

type TupleElement struct {
	Name     *Ident // optional label
	Type     TypeAnn
	Optional bool
	Rest     bool // for ...T[]
	span     ast.Span
}

func (t *TupleElement) Span() ast.Span { return t.span }

// UnionType represents T1 | T2 | ...
type UnionType struct {
	Types []TypeAnn
	span  ast.Span
}

func (u *UnionType) Span() ast.Span { return u.span }

// IntersectionType represents T1 & T2 & ...
type IntersectionType struct {
	Types []TypeAnn
	span  ast.Span
}

func (i *IntersectionType) Span() ast.Span { return i.span }

// FunctionType represents (params) => ReturnType
type FunctionType struct {
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	span       ast.Span
}

func (f *FunctionType) Span() ast.Span { return f.span }

// ConstructorType represents new (params) => ReturnType or abstract new (params) => ReturnType
type ConstructorType struct {
	Abstract   bool
	TypeParams []*TypeParam
	Params     []*Param
	ReturnType TypeAnn
	span       ast.Span
}

func (c *ConstructorType) Span() ast.Span { return c.span }

// ObjectType represents { members }
type ObjectType struct {
	Members []InterfaceMember
	span    ast.Span
}

func (o *ObjectType) Span() ast.Span { return o.span }

// ParenthesizedType represents (T)
type ParenthesizedType struct {
	Type TypeAnn
	span ast.Span
}

func (p *ParenthesizedType) Span() ast.Span { return p.span }

// IndexedAccessType represents T[K]
type IndexedAccessType struct {
	ObjectType TypeAnn
	IndexType  TypeAnn
	span       ast.Span
}

func (i *IndexedAccessType) Span() ast.Span { return i.span }

// ConditionalType represents T extends U ? X : Y
type ConditionalType struct {
	CheckType   TypeAnn
	ExtendsType TypeAnn
	TrueType    TypeAnn
	FalseType   TypeAnn
	span        ast.Span
}

func (c *ConditionalType) Span() ast.Span { return c.span }

// InferType represents infer T
type InferType struct {
	TypeParam *TypeParam
	span      ast.Span
}

func (i *InferType) Span() ast.Span { return i.span }

// MappedType represents { [K in T]: U }
type MappedType struct {
	TypeParam *TypeParam
	ValueType TypeAnn
	Optional  OptionalModifier
	Readonly  ReadonlyModifier
	AsClause  TypeAnn // optional, for `as` clause in key remapping
	span      ast.Span
}

func (m *MappedType) Span() ast.Span { return m.span }

type OptionalModifier int

const (
	OptionalNone   OptionalModifier = iota
	OptionalAdd                     // +?
	OptionalRemove                  // -?
)

type ReadonlyModifier int

const (
	ReadonlyNone   ReadonlyModifier = iota
	ReadonlyAdd                     // +readonly
	ReadonlyRemove                  // -readonly
)

// TemplateLiteralType represents `${T}...`
type TemplateLiteralType struct {
	Parts []TemplatePart
	span  ast.Span
}

func (t *TemplateLiteralType) Span() ast.Span { return t.span }

type TemplatePart interface {
	isTemplatePart()
	Node
}

func (*TemplateString) isTemplatePart() {}
func (*TemplateType) isTemplatePart()   {}

type TemplateString struct {
	Value string
	span  ast.Span
}

func (t *TemplateString) Span() ast.Span { return t.span }

type TemplateType struct {
	Type TypeAnn
	span ast.Span
}

func (t *TemplateType) Span() ast.Span { return t.span }

// KeyOfType represents keyof T
type KeyOfType struct {
	Type TypeAnn
	span ast.Span
}

func (k *KeyOfType) Span() ast.Span { return k.span }

// TypeOfType represents typeof expr
type TypeOfType struct {
	Expr QualIdent // in .d.ts, typeof is limited to identifiers
	span ast.Span
}

func (t *TypeOfType) Span() ast.Span { return t.span }

// ImportType represents import("module").Type
type ImportType struct {
	Module   string    // module specifier
	Name     QualIdent // optional, for accessing specific export
	TypeArgs []TypeAnn // optional
	span     ast.Span
}

func (i *ImportType) Span() ast.Span { return i.span }

// TypePredicate represents arg is Type
type TypePredicate struct {
	ParamName *Ident
	Asserts   bool    // for `asserts` predicates
	Type      TypeAnn // optional, for `asserts arg is Type`
	span      ast.Span
}

func (t *TypePredicate) Span() ast.Span { return t.span }

// ThisType represents `this` type
type ThisType struct {
	span ast.Span
}

func (t *ThisType) Span() ast.Span { return t.span }

// RestType represents ...T (in function parameters or tuples)
type RestType struct {
	Type TypeAnn
	span ast.Span
}

func (r *RestType) Span() ast.Span { return r.span }

// OptionalType represents T? (primarily in tuples)
type OptionalType struct {
	Type TypeAnn
	span ast.Span
}

func (o *OptionalType) Span() ast.Span { return o.span }

// ============================================================================
// Supporting Structures
// ============================================================================

// TypeParam represents a type parameter with optional constraint and default
type TypeParam struct {
	Name       *Ident
	Constraint TypeAnn // optional
	Default    TypeAnn // optional
	span       ast.Span
}

func (t *TypeParam) Span() ast.Span { return t.span }

// Param represents a function parameter
type Param struct {
	Name     *Ident
	Type     TypeAnn
	Optional bool
	Rest     bool
	span     ast.Span
}

func (p *Param) Span() ast.Span { return p.span }
