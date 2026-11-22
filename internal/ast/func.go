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

// IsComplete reports whether the signature states the function's whole type: every
// parameter and the return type are written, and no type in it is the `_` placeholder.
// A complete signature gives the function its type without its body.
func (sig *FuncSig) IsComplete() bool {
	return sig.Return != nil && sig.IsCompleteExceptReturn()
}

// IsCompleteExceptReturn is IsComplete for a function whose return type is not written,
// such as a setter's. It reports whether every parameter is written and no type the
// signature writes is the `_` placeholder.
func (sig *FuncSig) IsCompleteExceptReturn() bool {
	anns := []TypeAnn{sig.Return, sig.Throws}
	for _, p := range sig.Params {
		if p.TypeAnn == nil {
			return false
		}
		anns = append(anns, p.TypeAnn)
	}
	for _, tp := range sig.TypeParams {
		anns = append(anns, tp.LowerBound, tp.UpperBound, tp.Default)
	}
	finder := &wildcardFinder{}
	for _, ta := range anns {
		if ta != nil {
			ta.Accept(finder)
		}
	}
	return !finder.found
}

// wildcardFinder records whether a type annotation contains the `_` placeholder.
type wildcardFinder struct {
	DefaultVisitor
	found bool
}

func (v *wildcardFinder) EnterTypeAnn(t TypeAnn) bool {
	if _, ok := t.(*WildcardTypeAnn); ok {
		v.found = true
	}
	return !v.found
}
