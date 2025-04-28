package codegen

import "github.com/escalier-lang/escalier/internal/checker"

func (b *Builder) BuildDefinitions(bindings map[string]checker.Binding) *Module {
	// TODO: implement BuildDefinitions
	return &Module{
		Stmts: []Stmt{},
	}
}
