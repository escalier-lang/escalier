package ast

type Node interface {
	Span() Span
}

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Identifier struct {
	Name string
	span Span
}

func NewIdentifier(name string, span Span) *Identifier {
	return &Identifier{Name: name, span: span}
}

func (i *Identifier) Span() Span {
	return i.span
}

type Expr struct {
	Kind ExprKind
	span Span
}

func NewExpr(kind ExprKind, span Span) *Expr {
	return &Expr{Kind: kind, span: span}
}

func (e *Expr) Span() Span {
	return e.span
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
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
func (*EIgnore) isExpr()     {}
func (*EEmpty) isExpr()      {}

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

type EIgnore struct{}

type EEmpty struct{}

type Decl struct {
	Kind    DeclKind
	Export  bool
	Declare bool
	span    Span
}

func NewDecl(kind DeclKind, export bool, declare bool, span Span) *Decl {
	return &Decl{Kind: kind, Export: export, Declare: declare, span: span}
}

func (e *Decl) Span() Span {
	return e.span
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
	Kind StmtKind
	span Span
}

func NewStmt(kind StmtKind, span Span) *Stmt {
	return &Stmt{Kind: kind, span: span}
}

func (e *Stmt) Span() Span {
	return e.span
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
