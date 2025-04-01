package codegen

import "github.com/escalier-lang/escalier/internal/ast"

type Node interface {
	Span() *Span
	Source() ast.Node
}

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Identifier struct {
	Name   string
	span   *Span // gets filled in by the printer
	source ast.Node
}

func (i *Identifier) Span() *Span {
	return i.span
}

func (i *Identifier) Source() ast.Node {
	return i.source
}

type Expr struct {
	Kind   ExprKind
	span   *Span // gets filled in by the printer
	source ast.Node
}

func (e *Expr) Span() *Span {
	return e.span
}

func (e *Expr) Source() ast.Node {
	return e.source
}

//sumtype:decl
type ExprKind interface{ isExpr() }

func (*EBinary) isExpr()     {}
func (*ENumber) isExpr()     {}
func (*EString) isExpr()     {}
func (*EBool) isExpr()       {}
func (*EIdentifier) isExpr() {}
func (*EUnary) isExpr()      {}
func (*ECall) isExpr()       {}
func (*EFunction) isExpr()   {}
func (*EIndex) isExpr()      {}
func (*EMember) isExpr()     {}
func (*EArray) isExpr()      {}

// func (*EIgnore) isExpr()     {}
// func (*EEmpty) isExpr()      {}

type EMember struct {
	Object   *Expr
	Prop     *Identifier
	OptChain bool
}

type EIndex struct {
	Object   *Expr
	Index    *Expr
	OptChain bool
}

type ECall struct {
	Callee   *Expr
	Args     []*Expr
	OptChain bool
}

type EFunction struct {
	Params []*Param
	Body   []*Stmt
}

type EArray struct {
	Elems []*Expr
}

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

type EBinary struct {
	Left  *Expr
	Op    BinaryOp
	Right *Expr
}

type UnaryOp int

const (
	UnaryPlus  UnaryOp = iota // +
	UnaryMinus                // -
	LogicalNot                // !
)

type EUnary struct {
	Op  UnaryOp
	Arg *Expr
}

type ENumber struct {
	Value float64
}

type EString struct {
	Value string
}

type EBool struct {
	Value bool
}

type EIdentifier struct {
	Name string
}

type Decl struct {
	Kind    DeclKind
	Export  bool
	Declare bool
	span    *Span
	source  ast.Node
}

func (d *Decl) Span() *Span {
	return d.span
}

func (d *Decl) Source() ast.Node {
	return d.source
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type DeclKind interface{ isDecl() }

func (*DVariable) isDecl() {}
func (*DFunction) isDecl() {}

type VariableKind int

const (
	ValKind VariableKind = iota
	VarKind
)

// TODO: support multiple declarators
type DVariable struct {
	Kind    VariableKind
	Pattern Pat
	Init    *Expr
}

type Param struct {
	Pattern Pat
}

type DFunction struct {
	Name   *Identifier
	Params []*Param
	Body   []*Stmt
}

type Stmt struct {
	Kind   StmtKind
	span   *Span
	source ast.Node
}

func (s *Stmt) Span() *Span {
	return s.span
}

func (s *Stmt) Source() ast.Node {
	return s.source
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type StmtKind interface{ isStmt() }

func (*SExpr) isStmt()   {}
func (*SDecl) isStmt()   {}
func (*SReturn) isStmt() {}

type SExpr struct {
	Expr *Expr
}

type SDecl struct {
	Decl *Decl
}

type SReturn struct {
	Expr *Expr
}

// TODO add support for imports and exports
type Module struct {
	Stmts []*Stmt
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
	source ast.Node
	span   *Span
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
	Default *Expr // optional
	source  ast.Node
	span    *Span
}

func NewObjKeyValuePat(key string, value Pat, _default *Expr, source ast.Node) *ObjKeyValuePat {
	return &ObjKeyValuePat{Key: key, Value: value, Default: _default, source: source, span: nil}
}
func (p *ObjKeyValuePat) Span() *Span      { return p.span }
func (p *ObjKeyValuePat) Source() ast.Node { return p.source }

type ObjShorthandPat struct {
	Key     string
	Default *Expr // optional
	source  ast.Node
	span    *Span
}

func NewObjShorthandPat(key string, _default *Expr, source ast.Node) *ObjShorthandPat {
	return &ObjShorthandPat{Key: key, Default: _default, source: source, span: nil}
}
func (p *ObjShorthandPat) Span() *Span      { return p.span }
func (p *ObjShorthandPat) Source() ast.Node { return p.source }

type ObjRestPat struct {
	Pattern Pat
	source  ast.Node
	span    *Span
}

func NewObjRestPat(pattern Pat, source ast.Node) *ObjRestPat {
	return &ObjRestPat{Pattern: pattern, source: source, span: nil}
}
func (p *ObjRestPat) Span() *Span      { return p.span }
func (p *ObjRestPat) Source() ast.Node { return p.source }

type ObjectPat struct {
	Elems  []ObjPatElem
	source ast.Node
	span   *Span
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
	Default *Expr // optional
	source  ast.Node
	span    *Span
}

func NewTupleElemPat(pattern Pat, _default *Expr, source ast.Node) *TupleElemPat {
	return &TupleElemPat{Pattern: pattern, Default: _default, source: source, span: nil}
}
func (p *TupleElemPat) Span() *Span      { return p.span }
func (p *TupleElemPat) Source() ast.Node { return p.source }

type TupleRestPat struct {
	Pattern Pat
	source  ast.Node
	span    *Span
}

func NewTupleRestPat(pattern Pat, source ast.Node) *TupleRestPat {
	return &TupleRestPat{Pattern: pattern, source: source, span: nil}
}
func (p *TupleRestPat) Span() *Span      { return p.span }
func (p *TupleRestPat) Source() ast.Node { return p.source }

type TuplePat struct {
	Elems  []TuplePatElem
	source ast.Node
	span   *Span
}

func NewTuplePat(elems []TuplePatElem, source ast.Node) *TuplePat {
	return &TuplePat{Elems: elems, source: source, span: nil}
}
func (p *TuplePat) Span() *Span        { return p.span }
func (p *TuplePat) SetSpan(span *Span) { p.span = span }
func (p *TuplePat) Source() ast.Node   { return p.source }
