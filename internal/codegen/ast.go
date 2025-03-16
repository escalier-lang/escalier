package codegen

import "github.com/escalier-lang/escalier/internal/parser"

type Node interface {
	Span() *Span
	Source() parser.Node
}

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Identifier struct {
	Name   string
	span   *Span // gets filled in by the printer
	source parser.Node
}

func (i *Identifier) Span() *Span {
	return i.span
}

func (i *Identifier) Source() parser.Node {
	return i.source
}

type Expr struct {
	Kind   ExprKind
	span   *Span // gets filled in by the printer
	source parser.Node
}

func (e *Expr) Span() *Span {
	return e.span
}

func (e *Expr) Source() parser.Node {
	return e.source
}

//sumtype:decl
type ExprKind interface{ isExpr() }

func (*EBinary) isExpr()     {}
func (*ENumber) isExpr()     {}
func (*EString) isExpr()     {}
func (*EIdentifier) isExpr() {}
func (*EUnary) isExpr()      {}
func (*ECall) isExpr()       {}
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

type EIdentifier struct {
	Name string
}

type Decl struct {
	Kind    DeclKind
	Export  bool
	Declare bool
	span    *Span
	source  parser.Node
}

func (d *Decl) Span() *Span {
	return d.span
}

func (d *Decl) Source() parser.Node {
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
	Kind VariableKind
	Name *Identifier // TODO: replace with Pattern
	Init *Expr
}

type Param struct {
	Name *Identifier // TODO: replace with Pattern
	// TODO: include type annotation
}

type DFunction struct {
	Name   *Identifier
	Params []*Param
	Body   []*Stmt
}

type Stmt struct {
	Kind   StmtKind
	span   *Span
	source parser.Node
}

func (s *Stmt) Span() *Span {
	return s.span
}

func (s *Stmt) Source() parser.Node {
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
