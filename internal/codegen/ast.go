package codegen

import "github.com/escalier-lang/escalier/internal/ast"

type Node interface {
	Span() *Span
	SetSpan(span *Span)
	Source() ast.Node
}

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Identifier struct {
	Name   string
	span   *Span // gets filled in by the printer
	source ast.Node
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
func (*NumExpr) isExpr()    {}
func (*StrExpr) isExpr()    {}
func (*BoolExpr) isExpr()   {}
func (*IdentExpr) isExpr()  {}
func (*UnaryExpr) isExpr()  {}
func (*CallExpr) isExpr()   {}
func (*FuncExpr) isExpr()   {}
func (*IndexExpr) isExpr()  {}
func (*MemberExpr) isExpr() {}
func (*ArrayExpr) isExpr()  {}

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
	span   *Span
	source ast.Node
}

func NewFuncExpr(params []*Param, body []Stmt, source ast.Node) *FuncExpr {
	return &FuncExpr{Params: params, Body: body, source: source, span: nil}
}
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

type BinaryOp int

const (
	Plus              BinaryOp = iota // +
	Minus                             // -
	Times                             // *
	Divide                            // /
	Modulo                            // %
	LessThan                          // <
	LessThanEqual                     // <=
	GreaterThan                       // >
	GreaterThanEqual                  // >=
	Equal                             // ==
	NotEqual                          // !=
	LogicalAnd                        // &&
	LogicalOr                         // ||
	NullishCoalescing                 // ??
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

type NumExpr struct {
	Value  float64
	span   *Span
	source ast.Node
}

func NewNumExpr(value float64, source ast.Node) *NumExpr {
	return &NumExpr{Value: value, source: source, span: nil}
}
func (e *NumExpr) Span() *Span        { return e.span }
func (e *NumExpr) SetSpan(span *Span) { e.span = span }
func (e *NumExpr) Source() ast.Node   { return e.source }

type StrExpr struct {
	Value  string
	span   *Span
	source ast.Node
}

func NewStrExpr(value string, source ast.Node) *StrExpr {
	return &StrExpr{Value: value, source: source, span: nil}
}
func (e *StrExpr) Span() *Span        { return e.span }
func (e *StrExpr) SetSpan(span *Span) { e.span = span }
func (e *StrExpr) Source() ast.Node   { return e.source }

type BoolExpr struct {
	Value  bool
	span   *Span
	source ast.Node
}

func NewBoolExpr(value bool, source ast.Node) *BoolExpr {
	return &BoolExpr{Value: value, source: source, span: nil}
}
func (e *BoolExpr) Span() *Span        { return e.span }
func (e *BoolExpr) SetSpan(span *Span) { e.span = span }
func (e *BoolExpr) Source() ast.Node   { return e.source }

type IdentExpr struct {
	Name   string
	span   *Span
	source ast.Node
}

func NewIdentExpr(name string, source ast.Node) *IdentExpr {
	return &IdentExpr{Name: name, source: source, span: nil}
}
func (e *IdentExpr) Span() *Span        { return e.span }
func (e *IdentExpr) SetSpan(span *Span) { e.span = span }
func (e *IdentExpr) Source() ast.Node   { return e.source }

//sumtype:decl
type Decl interface {
	isDecl()
	Export() bool
	Declare() bool
	Node
}

func (*VarDecl) isDecl()  {}
func (*FuncDecl) isDecl() {}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

// TODO: support multiple declarators
type VarDecl struct {
	Kind    VariableKind
	Pattern Pat
	Init    Expr
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
	Pattern Pat
}

type FuncDecl struct {
	Name    *Identifier
	Params  []*Param
	Body    []Stmt
	export  bool
	declare bool
	span    *Span
	source  ast.Node
}

func (d *FuncDecl) Export() bool       { return d.export }
func (d *FuncDecl) Declare() bool      { return d.declare }
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
	Expr   Expr
	span   *Span
	source ast.Node
}

func (s *ReturnStmt) Span() *Span        { return s.span }
func (s *ReturnStmt) SetSpan(span *Span) { s.span = span }
func (s *ReturnStmt) Source() ast.Node   { return s.source }

// TODO add support for imports and exports
type Module struct {
	Stmts []Stmt
}

type Pat interface {
	isPat()
	Node
	SetSpan(span *Span)
}

func (*IdentPat) isPat()  {}
func (*ObjectPat) isPat() {}
func (*TuplePat) isPat()  {}

type IdentPat struct {
	Name   string
	span   *Span
	source ast.Node
}

func NewIdentPat(name string, source ast.Node) *IdentPat {
	return &IdentPat{Name: name, source: source, span: nil}
}
func (p *IdentPat) Span() *Span        { return p.span }
func (p *IdentPat) SetSpan(span *Span) { p.span = span }
func (p *IdentPat) Source() ast.Node   { return p.source }

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
	Elems  []TuplePatElem
	span   *Span
	source ast.Node
}

func NewTuplePat(elems []TuplePatElem, source ast.Node) *TuplePat {
	return &TuplePat{Elems: elems, source: source, span: nil}
}
func (p *TuplePat) Span() *Span        { return p.span }
func (p *TuplePat) SetSpan(span *Span) { p.span = span }
func (p *TuplePat) Source() ast.Node   { return p.source }
