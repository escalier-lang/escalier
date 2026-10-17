package ast

// Func is a function written in source with its signature and its body: a function
// expression or a function declaration. The body is nil for a declaration without one.
type Func interface {
	Node
	Signature() *FuncSig
	FuncBody() *Block
}

// Signature returns the function expression's signature.
func (e *FuncExpr) Signature() *FuncSig { return &e.FuncSig }

// FuncBody returns the function expression's body.
func (e *FuncExpr) FuncBody() *Block { return e.Body }

// Signature returns the function declaration's signature.
func (d *FuncDecl) Signature() *FuncSig { return &d.FuncSig }

// FuncBody returns the function declaration's body, nil for a declaration without one.
func (d *FuncDecl) FuncBody() *Block { return d.Body }
