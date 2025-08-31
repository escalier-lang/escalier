package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

type Node interface {
	Span() *Span
	SetSpan(span *Span)
	Source() ast.Node
}

type Lit interface {
	isLiteral()
	Node
}

func (*NumLit) isLiteral()       {}
func (*StrLit) isLiteral()       {}
func (*RegexLit) isLiteral()     {}
func (*BoolLit) isLiteral()      {}
func (*NullLit) isLiteral()      {}
func (*UndefinedLit) isLiteral() {}

type NumLit struct {
	Value  float64
	span   *Span
	source ast.Node
}

func NewNumLit(value float64, source ast.Node) *NumLit {
	return &NumLit{Value: value, source: source, span: nil}
}
func (e *NumLit) Span() *Span        { return e.span }
func (e *NumLit) SetSpan(span *Span) { e.span = span }
func (e *NumLit) Source() ast.Node   { return e.source }

type StrLit struct {
	Value  string
	span   *Span
	source ast.Node
}

func NewStrLit(value string, source ast.Node) *StrLit {
	return &StrLit{Value: value, source: source, span: nil}
}
func (e *StrLit) Span() *Span        { return e.span }
func (e *StrLit) SetSpan(span *Span) { e.span = span }
func (e *StrLit) Source() ast.Node   { return e.source }

type RegexLit struct {
	Value  string
	span   *Span
	source ast.Node
}

func NewRegexLit(value string, source ast.Node) *RegexLit {
	return &RegexLit{Value: value, source: source, span: nil}
}
func (e *RegexLit) Span() *Span        { return e.span }
func (e *RegexLit) SetSpan(span *Span) { e.span = span }
func (e *RegexLit) Source() ast.Node   { return e.source }

type BoolLit struct {
	Value  bool
	span   *Span
	source ast.Node
}

func NewBoolLit(value bool, source ast.Node) *BoolLit {
	return &BoolLit{Value: value, source: source, span: nil}
}
func (e *BoolLit) Span() *Span        { return e.span }
func (e *BoolLit) SetSpan(span *Span) { e.span = span }
func (e *BoolLit) Source() ast.Node   { return e.source }

type NullLit struct {
	span   *Span
	source ast.Node
}

func NewNullLit(source ast.Node) *NullLit {
	return &NullLit{source: source, span: nil}
}
func (e *NullLit) Span() *Span        { return e.span }
func (e *NullLit) SetSpan(span *Span) { e.span = span }
func (e *NullLit) Source() ast.Node   { return e.source }

type UndefinedLit struct {
	span   *Span
	source ast.Node
}

func NewUndefinedLit(source ast.Node) *UndefinedLit {
	return &UndefinedLit{source: source, span: nil}
}
func (e *UndefinedLit) Span() *Span        { return e.span }
func (e *UndefinedLit) SetSpan(span *Span) { e.span = span }
func (e *UndefinedLit) Source() ast.Node   { return e.source }

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
// TODO: Dedupe with Ident
type Identifier struct {
	Name   string
	span   *Span // gets filled in by the printer
	source ast.Node
}

func NewIdentifier(name string, source ast.Node) *Identifier {
	return &Identifier{Name: name, source: source, span: nil}
}
func (i *Identifier) Span() *Span        { return i.span }
func (i *Identifier) SetSpan(span *Span) { i.span = span }
func (i *Identifier) Source() ast.Node   { return i.source }

//sumtype:decl
type Expr interface {
	isExpr()
	Node
}

func (*BinaryExpr) isExpr() {}
func (*LitExpr) isExpr()    {}
func (*IdentExpr) isExpr()  {}
func (*UnaryExpr) isExpr()  {}
func (*CallExpr) isExpr()   {}
func (*FuncExpr) isExpr()   {}
func (*IndexExpr) isExpr()  {}
func (*MemberExpr) isExpr() {}
func (*ArrayExpr) isExpr()  {}
func (*ObjectExpr) isExpr() {}
func (*MatchExpr) isExpr()  {}
func (*AwaitExpr) isExpr()  {}

type MemberExpr struct {
	Object   Expr
	Prop     *Identifier
	OptChain bool
	span     *Span
	source   ast.Node
}

func NewMemberExpr(object Expr, prop *Identifier, optChain bool, source ast.Node) *MemberExpr {
	return &MemberExpr{Object: object, Prop: prop, OptChain: optChain, source: source, span: nil}
}
func (e *MemberExpr) Span() *Span        { return e.span }
func (e *MemberExpr) SetSpan(span *Span) { e.span = span }
func (e *MemberExpr) Source() ast.Node   { return e.source }

type IndexExpr struct {
	Object   Expr
	Index    Expr
	OptChain bool
	span     *Span
	source   ast.Node
}

func NewIndexExpr(object Expr, index Expr, optChain bool, source ast.Node) *IndexExpr {
	return &IndexExpr{
		Object: object, Index: index, OptChain: optChain, source: source, span: nil,
	}
}
func (e *IndexExpr) Span() *Span        { return e.span }
func (e *IndexExpr) SetSpan(span *Span) { e.span = span }
func (e *IndexExpr) Source() ast.Node   { return e.source }

type CallExpr struct {
	Callee   Expr
	Args     []Expr
	OptChain bool
	span     *Span
	source   ast.Node
}

func NewCallExpr(callee Expr, args []Expr, optChain bool, source ast.Node) *CallExpr {
	return &CallExpr{Callee: callee, Args: args, OptChain: optChain, source: source, span: nil}
}
func (e *CallExpr) Span() *Span        { return e.span }
func (e *CallExpr) SetSpan(span *Span) { e.span = span }
func (e *CallExpr) Source() ast.Node   { return e.source }

type FuncExpr struct {
	Params []*Param
	Body   []Stmt
	async  bool
	span   *Span
	source ast.Node
}

func NewFuncExpr(params []*Param, body []Stmt, async bool, source ast.Node) *FuncExpr {
	return &FuncExpr{Params: params, Body: body, async: async, source: source, span: nil}
}
func (e *FuncExpr) Async() bool        { return e.async }
func (e *FuncExpr) Span() *Span        { return e.span }
func (e *FuncExpr) SetSpan(span *Span) { e.span = span }
func (e *FuncExpr) Source() ast.Node   { return e.source }

type ArrayExpr struct {
	Elems  []Expr
	span   *Span
	source ast.Node
}

func NewArrayExpr(elems []Expr, source ast.Node) *ArrayExpr {
	return &ArrayExpr{Elems: elems, source: source, span: nil}
}
func (e *ArrayExpr) Span() *Span        { return e.span }
func (e *ArrayExpr) SetSpan(span *Span) { e.span = span }
func (e *ArrayExpr) Source() ast.Node   { return e.source }

type ObjKey interface {
	isObjKey()
	Node
}

func (*IdentExpr) isObjKey()   {}
func (*StrLit) isObjKey()      {}
func (*NumLit) isObjKey()      {}
func (*ComputedKey) isObjKey() {}

type ComputedKey struct {
	Expr   Expr
	span   *Span
	source ast.Node
}

func (c *ComputedKey) Span() *Span        { return c.span }
func (c *ComputedKey) SetSpan(span *Span) { c.span = span }
func (c *ComputedKey) Source() ast.Node   { return c.Expr.Source() }
func NewComputedKey(expr Expr, source ast.Node) *ComputedKey {
	return &ComputedKey{Expr: expr, source: source, span: nil}
}

type ObjectExpr struct {
	Elems  []ObjExprElem
	span   *Span
	source ast.Node
}

func NewObjectExpr(elems []ObjExprElem, source ast.Node) *ObjectExpr {
	return &ObjectExpr{Elems: elems, source: source, span: nil}
}
func (e *ObjectExpr) Span() *Span        { return e.span }
func (e *ObjectExpr) SetSpan(span *Span) { e.span = span }
func (e *ObjectExpr) Source() ast.Node   { return e.source }

type ObjExprElem interface {
	isObjExprElem()
	Node
}

func (*MethodExpr) isObjExprElem()     {}
func (*GetterExpr) isObjExprElem()     {}
func (*SetterExpr) isObjExprElem()     {}
func (*PropertyExpr) isObjExprElem()   {}
func (*RestSpreadExpr) isObjExprElem() {}

type MethodExpr struct {
	Name   ObjKey
	Params []*Param
	Body   []Stmt
	source ast.Node
	span   *Span
}

func NewMethodExpr(name ObjKey, params []*Param, body []Stmt, source ast.Node) *MethodExpr {
	return &MethodExpr{Name: name, Params: params, Body: body, source: source, span: nil}
}
func (e *MethodExpr) Span() *Span        { return e.span }
func (e *MethodExpr) SetSpan(span *Span) { e.span = span }
func (e *MethodExpr) Source() ast.Node   { return e.source }

type GetterExpr struct {
	Name   ObjKey
	Body   []Stmt
	source ast.Node
	span   *Span
}

func NewGetterExpr(name ObjKey, body []Stmt, source ast.Node) *GetterExpr {
	return &GetterExpr{Name: name, Body: body, source: source, span: nil}
}
func (e *GetterExpr) Span() *Span        { return e.span }
func (e *GetterExpr) SetSpan(span *Span) { e.span = span }
func (e *GetterExpr) Source() ast.Node   { return e.source }

type SetterExpr struct {
	Name   ObjKey
	Params []*Param
	Body   []Stmt
	source ast.Node
	span   *Span
}

func NewSetterExpr(name ObjKey, params []*Param, body []Stmt, source ast.Node) *SetterExpr {
	return &SetterExpr{Name: name, Params: params, Body: body, source: source, span: nil}
}
func (e *SetterExpr) Span() *Span        { return e.span }
func (e *SetterExpr) SetSpan(span *Span) { e.span = span }
func (e *SetterExpr) Source() ast.Node   { return e.source }

type PropertyExpr struct {
	Key    ObjKey
	Value  Expr
	source ast.Node
	span   *Span
}

func NewPropertyExpr(key ObjKey, value Expr, source ast.Node) *PropertyExpr {
	return &PropertyExpr{Key: key, Value: value, source: source, span: nil}
}
func (e *PropertyExpr) Span() *Span        { return e.span }
func (e *PropertyExpr) SetSpan(span *Span) { e.span = span }
func (e *PropertyExpr) Source() ast.Node   { return e.source }

type RestSpreadExpr struct {
	Arg    Expr
	source ast.Node
	span   *Span
}

func NewRestSpreadExpr(arg Expr, source ast.Node) *RestSpreadExpr {
	return &RestSpreadExpr{Arg: arg, source: source, span: nil}
}
func (e *RestSpreadExpr) Span() *Span        { return e.span }
func (e *RestSpreadExpr) SetSpan(span *Span) { e.span = span }
func (e *RestSpreadExpr) Source() ast.Node   { return e.source }

type BinaryOp string

const (
	Plus              BinaryOp = "+"
	Minus             BinaryOp = "-"
	Times             BinaryOp = "*"
	Divide            BinaryOp = "/"
	Modulo            BinaryOp = "%"
	Concatenation     BinaryOp = "++"
	LessThan          BinaryOp = "<"
	LessThanEqual     BinaryOp = "<="
	GreaterThan       BinaryOp = ">"
	GreaterThanEqual  BinaryOp = ">="
	EqualEqual        BinaryOp = "=="
	NotEqual          BinaryOp = "!="
	LogicalAnd        BinaryOp = "&&"
	LogicalOr         BinaryOp = "||"
	NullishCoalescing BinaryOp = "??"
	In                BinaryOp = "in"
	Assign            BinaryOp = "="
)

type BinaryExpr struct {
	Left   Expr
	Op     BinaryOp
	Right  Expr
	span   *Span
	source ast.Node
}

func NewBinaryExpr(left Expr, op BinaryOp, right Expr, source ast.Node) *BinaryExpr {
	return &BinaryExpr{Left: left, Op: op, Right: right, source: source, span: nil}
}
func (e *BinaryExpr) Span() *Span        { return e.span }
func (e *BinaryExpr) SetSpan(span *Span) { e.span = span }
func (e *BinaryExpr) Source() ast.Node   { return e.source }

type UnaryOp int

const (
	UnaryPlus  UnaryOp = iota // +
	UnaryMinus                // -
	LogicalNot                // !
	TypeOf                    // typeof
)

type UnaryExpr struct {
	Op     UnaryOp
	Arg    Expr
	span   *Span
	source ast.Node
}

func NewUnaryExpr(op UnaryOp, arg Expr, source ast.Node) *UnaryExpr {
	return &UnaryExpr{Op: op, Arg: arg, source: source, span: nil}
}
func (e *UnaryExpr) Span() *Span        { return e.span }
func (e *UnaryExpr) SetSpan(span *Span) { e.span = span }
func (e *UnaryExpr) Source() ast.Node   { return e.source }

type LitExpr struct {
	Lit    Lit
	span   *Span
	source ast.Node
}

func NewLitExpr(lit Lit, source ast.Node) *LitExpr {
	return &LitExpr{Lit: lit, source: source, span: nil}
}
func (e *LitExpr) Span() *Span        { return e.span }
func (e *LitExpr) SetSpan(span *Span) { e.span = span }
func (e *LitExpr) Source() ast.Node   { return e.source }

type IdentExpr struct {
	Name      string
	Namespace string
	span      *Span
	source    ast.Node
}

func NewIdentExpr(name string, namespace string, source ast.Node) *IdentExpr {
	return &IdentExpr{Name: name, Namespace: namespace, source: source, span: nil}
}
func (e *IdentExpr) Span() *Span        { return e.span }
func (e *IdentExpr) SetSpan(span *Span) { e.span = span }
func (e *IdentExpr) Source() ast.Node   { return e.source }

// Match expressions and related types
type MatchExpr struct {
	Target Expr
	Cases  []*MatchCase
	span   *Span
	source ast.Node
}

func NewMatchExpr(target Expr, cases []*MatchCase, source ast.Node) *MatchExpr {
	return &MatchExpr{Target: target, Cases: cases, source: source, span: nil}
}
func (e *MatchExpr) Span() *Span        { return e.span }
func (e *MatchExpr) SetSpan(span *Span) { e.span = span }
func (e *MatchExpr) Source() ast.Node   { return e.source }

type MatchCase struct {
	Pattern Pat
	Guard   Expr // optional
	Body    []Stmt
	span    *Span
	source  ast.Node
}

func NewMatchCase(pattern Pat, guard Expr, body []Stmt, source ast.Node) *MatchCase {
	return &MatchCase{Pattern: pattern, Guard: guard, Body: body, source: source, span: nil}
}
func (c *MatchCase) Span() *Span        { return c.span }
func (c *MatchCase) SetSpan(span *Span) { c.span = span }
func (c *MatchCase) Source() ast.Node   { return c.source }

type AwaitExpr struct {
	Arg    Expr
	span   *Span
	source ast.Node
}

func NewAwaitExpr(arg Expr, source ast.Node) *AwaitExpr {
	return &AwaitExpr{Arg: arg, source: source, span: nil}
}
func (e *AwaitExpr) Span() *Span        { return e.span }
func (e *AwaitExpr) SetSpan(span *Span) { e.span = span }
func (e *AwaitExpr) Source() ast.Node   { return e.source }

//sumtype:decl
type Decl interface {
	isDecl()
	Export() bool
	Declare() bool
	Node
}

func (*VarDecl) isDecl()       {}
func (*FuncDecl) isDecl()      {}
func (*TypeDecl) isDecl()      {}
func (*NamespaceDecl) isDecl() {}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

type Declarator struct {
	Pattern Pat
	TypeAnn TypeAnn
	Init    Expr // TODO: make this an optional
}

// TODO: support multiple declarators
// TODO: support optional type annotations
type VarDecl struct {
	Kind    VariableKind
	Decls   []*Declarator
	export  bool
	declare bool
	span    *Span
	source  ast.Node
}

func (d *VarDecl) Export() bool       { return d.export }
func (d *VarDecl) Declare() bool      { return d.declare }
func (d *VarDecl) Span() *Span        { return d.span }
func (d *VarDecl) SetSpan(span *Span) { d.span = span }
func (d *VarDecl) Source() ast.Node   { return d.source }

type Param struct {
	Pattern  Pat
	Optional bool
	TypeAnn  TypeAnn // optional
}

// TODO: add support for type params
type FuncDecl struct {
	Name    *Identifier
	Params  []*Param
	Body    []Stmt  // optional
	TypeAnn TypeAnn // optional, return type annotation, required if `declare` is true
	export  bool
	declare bool
	async   bool
	span    *Span
	source  ast.Node
}

func (d *FuncDecl) Export() bool       { return d.export }
func (d *FuncDecl) Declare() bool      { return d.declare }
func (d *FuncDecl) Async() bool        { return d.async }
func (d *FuncDecl) Span() *Span        { return d.span }
func (d *FuncDecl) SetSpan(span *Span) { d.span = span }
func (d *FuncDecl) Source() ast.Node   { return d.source }

//sumtype:decl
type Stmt interface {
	isStmt()
	Node
}

func (*ExprStmt) isStmt()   {}
func (*DeclStmt) isStmt()   {}
func (*ReturnStmt) isStmt() {}
func (*BlockStmt) isStmt()  {}
func (*IfStmt) isStmt()     {}
func (*ThrowStmt) isStmt()  {}

type ExprStmt struct {
	Expr   Expr
	span   *Span
	source ast.Node
}

func (s *ExprStmt) Span() *Span        { return s.span }
func (s *ExprStmt) SetSpan(span *Span) { s.span = span }
func (s *ExprStmt) Source() ast.Node   { return s.source }

type DeclStmt struct {
	Decl   Decl
	span   *Span
	source ast.Node
}

func (s *DeclStmt) Span() *Span        { return s.span }
func (s *DeclStmt) SetSpan(span *Span) { s.span = span }
func (s *DeclStmt) Source() ast.Node   { return s.source }

type ReturnStmt struct {
	Expr   Expr // optional
	span   *Span
	source ast.Node
}

func (s *ReturnStmt) Span() *Span        { return s.span }
func (s *ReturnStmt) SetSpan(span *Span) { s.span = span }
func (s *ReturnStmt) Source() ast.Node   { return s.source }

type BlockStmt struct {
	Stmts  []Stmt
	span   *Span
	source ast.Node
}

func NewBlockStmt(stmts []Stmt, source ast.Node) *BlockStmt {
	return &BlockStmt{Stmts: stmts, source: source, span: nil}
}
func (s *BlockStmt) Span() *Span        { return s.span }
func (s *BlockStmt) SetSpan(span *Span) { s.span = span }
func (s *BlockStmt) Source() ast.Node   { return s.source }

type IfStmt struct {
	Test   Expr
	Cons   Stmt
	Alt    Stmt // optional, can be nil
	span   *Span
	source ast.Node
}

func NewIfStmt(test Expr, cons Stmt, alt Stmt, source ast.Node) *IfStmt {
	return &IfStmt{Test: test, Cons: cons, Alt: alt, source: source, span: nil}
}
func (s *IfStmt) Span() *Span        { return s.span }
func (s *IfStmt) SetSpan(span *Span) { s.span = span }
func (s *IfStmt) Source() ast.Node   { return s.source }

type ThrowStmt struct {
	Expr   Expr
	span   *Span
	source ast.Node
}

func NewThrowStmt(expr Expr, source ast.Node) *ThrowStmt {
	return &ThrowStmt{Expr: expr, source: source, span: nil}
}
func (s *ThrowStmt) Span() *Span        { return s.span }
func (s *ThrowStmt) SetSpan(span *Span) { s.span = span }
func (s *ThrowStmt) Source() ast.Node   { return s.source }

// TODO add support for imports and exports
type Module struct {
	Stmts []Stmt
}

type Pat interface {
	isPat()
	Node
	SetSpan(span *Span)
}

func (*IdentPat) isPat()     {}
func (*LitPat) isPat()       {}
func (*ObjectPat) isPat()    {}
func (*TuplePat) isPat()     {}
func (*RestPat) isPat()      {}
func (*WildcardPat) isPat()  {}
func (*ExtractorPat) isPat() {}

type IdentPat struct {
	Name    string
	Default Expr // optionaln
	span    *Span
	source  ast.Node
}

func NewIdentPat(name string, _default Expr, source ast.Node) *IdentPat {
	return &IdentPat{Name: name, Default: _default, source: source, span: nil}
}
func (p *IdentPat) Span() *Span        { return p.span }
func (p *IdentPat) SetSpan(span *Span) { p.span = span }
func (p *IdentPat) Source() ast.Node   { return p.source }

type LitPat struct {
	Lit    Lit
	span   *Span
	source ast.Node
}

func NewLitPat(lit Lit, source ast.Node) *LitPat {
	return &LitPat{Lit: lit, source: source, span: nil}
}
func (p *LitPat) Span() *Span        { return p.span }
func (p *LitPat) SetSpan(span *Span) { p.span = span }
func (p *LitPat) Source() ast.Node   { return p.source }

type ObjPatElem interface {
	isObjPatElem()
	Node
}

func (*ObjKeyValuePat) isObjPatElem()  {}
func (*ObjShorthandPat) isObjPatElem() {}
func (*ObjRestPat) isObjPatElem()      {}

type ObjKeyValuePat struct {
	Key     string
	Value   Pat
	Default Expr // optional
	source  ast.Node
	span    *Span
}

func NewObjKeyValuePat(key string, value Pat, _default Expr, source ast.Node) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value, Default: _default, source: source, span: nil}
}
func (p *ObjKeyValuePat) Span() *Span        { return p.span }
func (p *ObjKeyValuePat) SetSpan(span *Span) { p.span = span }
func (p *ObjKeyValuePat) Source() ast.Node   { return p.source }

type ObjShorthandPat struct {
	Key     string
	Default Expr // optional
	source  ast.Node
	span    *Span
}

func NewObjShorthandPat(key string, _default Expr, source ast.Node) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key, Default: _default, source: source, span: nil}
}
func (p *ObjShorthandPat) Span() *Span        { return p.span }
func (p *ObjShorthandPat) SetSpan(span *Span) { p.span = span }
func (p *ObjShorthandPat) Source() ast.Node   { return p.source }

type ObjRestPat struct {
	Pattern Pat
	source  ast.Node
	span    *Span
}

func NewObjRestPat(pattern Pat, source ast.Node) *ObjRestPat {
	return &ObjRestPat{Pattern: pattern, source: source, span: nil}
}
func (p *ObjRestPat) Span() *Span        { return p.span }
func (p *ObjRestPat) SetSpan(span *Span) { p.span = span }
func (p *ObjRestPat) Source() ast.Node   { return p.source }

type ObjectPat struct {
	Elems  []ObjPatElem
	span   *Span
	source ast.Node
}

func NewObjectPat(elems []ObjPatElem, source ast.Node) *ObjectPat {
	return &ObjectPat{Elems: elems, source: source, span: nil}
}
func (p *ObjectPat) Span() *Span        { return p.span }
func (p *ObjectPat) SetSpan(span *Span) { p.span = span }
func (p *ObjectPat) Source() ast.Node   { return p.source }

type TuplePatElem interface {
	isTuplePatElem()
	Node
}

func (*TupleElemPat) isTuplePatElem() {}
func (*TupleRestPat) isTuplePatElem() {}

type TupleElemPat struct {
	Pattern Pat
	Default Expr // optional
	source  ast.Node
	span    *Span
}

func NewTupleElemPat(pattern Pat, _default Expr, source ast.Node) *TupleElemPat {
	return &TupleElemPat{Pattern: pattern, Default: _default, source: source, span: nil}
}
func (p *TupleElemPat) Span() *Span        { return p.span }
func (p *TupleElemPat) SetSpan(span *Span) { p.span = span }
func (p *TupleElemPat) Source() ast.Node   { return p.source }

type TupleRestPat struct {
	Pattern Pat
	source  ast.Node
	span    *Span
}

func NewTupleRestPat(pattern Pat, source ast.Node) *TupleRestPat {
	return &TupleRestPat{Pattern: pattern, source: source, span: nil}
}
func (p *TupleRestPat) Span() *Span        { return p.span }
func (p *TupleRestPat) SetSpan(span *Span) { p.span = span }
func (p *TupleRestPat) Source() ast.Node   { return p.source }

type TuplePat struct {
	Elems  []Pat
	span   *Span
	source ast.Node
}

func NewTuplePat(elems []Pat, source ast.Node) *TuplePat {
	return &TuplePat{Elems: elems, source: source, span: nil}
}
func (p *TuplePat) Span() *Span        { return p.span }
func (p *TuplePat) SetSpan(span *Span) { p.span = span }
func (p *TuplePat) Source() ast.Node   { return p.source }

type RestPat struct {
	Pattern Pat
	span    *Span
	source  ast.Node
}

func NewRestPat(pattern Pat, source ast.Node) *RestPat {
	return &RestPat{Pattern: pattern, source: source, span: nil}
}
func (p *RestPat) Span() *Span        { return p.span }
func (p *RestPat) SetSpan(span *Span) { p.span = span }
func (p *RestPat) Source() ast.Node   { return p.source }

type WildcardPat struct {
	span   *Span
	source ast.Node
}

func NewWildcardPat(source ast.Node) *WildcardPat {
	return &WildcardPat{source: source, span: nil}
}
func (p *WildcardPat) Span() *Span        { return p.span }
func (p *WildcardPat) SetSpan(span *Span) { p.span = span }
func (p *WildcardPat) Source() ast.Node   { return p.source }

type ExtractorPat struct {
	Name   string
	Args   []Pat
	span   *Span
	source ast.Node
}

func NewExtractorPat(name string, args []Pat, source ast.Node) *ExtractorPat {
	return &ExtractorPat{Name: name, Args: args, source: source, span: nil}
}
func (p *ExtractorPat) Span() *Span        { return p.span }
func (p *ExtractorPat) SetSpan(span *Span) { p.span = span }
func (p *ExtractorPat) Source() ast.Node   { return p.source }

type TypeDecl struct {
	Name       *Identifier
	TypeParams []*TypeParam
	TypeAnn    TypeAnn
	Interface  bool
	export     bool
	declare    bool
	span       *Span
	source     ast.Node
}

func (d *TypeDecl) Export() bool       { return d.export }
func (d *TypeDecl) Declare() bool      { return d.declare }
func (d *TypeDecl) Span() *Span        { return d.span }
func (d *TypeDecl) SetSpan(span *Span) { d.span = span }
func (d *TypeDecl) Source() ast.Node   { return d.source }

type NamespaceDecl struct {
	Name    *Identifier
	Body    []Stmt
	export  bool
	declare bool
	span    *Span
	source  ast.Node
}

func (d *NamespaceDecl) Export() bool       { return d.export }
func (d *NamespaceDecl) Declare() bool      { return d.declare }
func (d *NamespaceDecl) Span() *Span        { return d.span }
func (d *NamespaceDecl) SetSpan(span *Span) { d.span = span }
func (d *NamespaceDecl) Source() ast.Node   { return d.source }
