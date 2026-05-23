package ast

// VarDecl, FuncDecl, and ClassDecl are the AST kinds that introduce a
// value binding and can carry decorators per §3.3 of the builtins plan.
// They opt in to `type_system.BindingOwner` here so a `Binding.Owner`
// is guaranteed to be a value-introducing decl — codegen can safely
// type-switch on the concrete kinds below to read decorators.

func (*VarDecl) IsBindingOwner()   {}
func (*FuncDecl) IsBindingOwner()  {}
func (*ClassDecl) IsBindingOwner() {}
