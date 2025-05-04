package codegen

import "github.com/escalier-lang/escalier/internal/checker"

func (b *Builder) BuildDefinitions(bindings map[string]checker.Binding) *Module {
	// for _, binding := range bindings {
	// 	t := binding.Type

	// }
	// TODO: implement BuildDefinitions
	return &Module{
		Stmts: []Stmt{},
	}
}
