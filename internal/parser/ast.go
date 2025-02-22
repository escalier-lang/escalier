package parser

type Identifier struct {
	Name string
	// TODO: include location information
}

type Expr struct {
	Kind E
	// TODO: include location information
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
	Plus             BinaryOp = iota // +
	Minus                            // -
	Times                            // *
	Divide                           // /
	Modulo                           // %
	LessThan                         // <
	LessThanEqual                    // <=
	GreaterThan                      // >
	GreaterThanEqual                 // >=
	Equal                            // ==
	NotEqual                         // !=
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

type EIdentifier = Identifier

type Decl struct {
	Kind D
	// TODO: include location information
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type D interface{ isDecl() }

func (*DVariable) isDecl() {}
func (*DFunction) isDecl() {}

type DVariable struct {
	Name *Identifier // TODO: replace with Pattern
	Init E
}

type Param struct {
	Name *Identifier // TODO: replace with Pattern
	// TODO: include type annotation
}

type DFunction struct {
	Name   *Identifier
	Params []*Param
	Body   []Stmt
}

type Stmt struct {
	Kind S
	// TODO: include location information
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

// TODO: add more statement types
// - for loops
