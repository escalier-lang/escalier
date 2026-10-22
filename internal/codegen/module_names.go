package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// moduleNamesCollector gathers every identifier a tree binds or reads, at any depth.
type moduleNamesCollector struct {
	ast.DefaultVisitor
	names set.Set[string]
}

func (c *moduleNamesCollector) EnterExpr(e ast.Expr) bool {
	if ident, ok := e.(*ast.IdentExpr); ok {
		c.names.Add(ident.Name)
	}
	return true
}

func (c *moduleNamesCollector) EnterPat(p ast.Pat) bool {
	if ident, ok := p.(*ast.IdentPat); ok {
		c.names.Add(ident.Name)
	}
	return true
}

func (c *moduleNamesCollector) EnterDecl(d ast.Decl) bool {
	var name *ast.Ident
	switch d := d.(type) {
	case *ast.FuncDecl:
		name = d.Name
	case *ast.ClassDecl:
		name = d.Name
	case *ast.EnumDecl:
		name = d.Name
	case *ast.NamespaceDecl:
		name = d.Name
	}
	if name != nil {
		c.names.Add(name.Name)
	}
	return true
}

// moduleNames returns every identifier the given nodes bind or read, including those
// in nested scopes.
func moduleNames(nodes []ast.Node) set.Set[string] {
	c := &moduleNamesCollector{names: set.NewSet[string]()}
	for _, node := range nodes {
		node.Accept(c)
	}
	return c.names
}
