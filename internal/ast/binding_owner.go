package ast

// BindingOwner is the declaration that introduces a top-level value binding.
// IdentExpr.Owner holds one, and codegen type-switches on the concrete kinds
// that implement it to read their decorators.
type BindingOwner interface {
	IsBindingOwner()
}

// VarDecl, FuncDecl, and ClassDecl are the AST kinds that introduce a value
// binding and can carry decorators per §3.3 of the builtins plan.
func (*VarDecl) IsBindingOwner()   {}
func (*FuncDecl) IsBindingOwner()  {}
func (*ClassDecl) IsBindingOwner() {}
