package parser

// If `Name` is an empty string it means that the identifier is missing in
// the expression.
type Identifier struct {
	Name string
	Span Span
}

type Expr struct {
	Kind E
	Span Span
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type E interface{ isExpr() }

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

type EIgnore struct {
	Token *Token
}

type EEmpty struct{}

type Decl struct {
	Kind    D
	Export  bool
	Declare bool
	Span    Span
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type D interface{ isDecl() }

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
	Kind S
	Span Span
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type S interface{ isStmt() }

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
