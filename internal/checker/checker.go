package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

type Checker struct {
	ID int
}

func NewChecker() *Checker {
	return &Checker{
		ID: 0,
	}
}

func (c *Checker) FreshVar() *ast.TypeVarType {
	c.ID++
	return &ast.TypeVarType{
		ID:       c.ID,
		Instance: nil,
	}
}

type Context struct {
	Filename   string
	Scope      *Scope
	IsAsync    bool
	IsPatMatch bool
}

func (ctx *Context) WithScope(scope *Scope) Context {
	return Context{
		Filename:   ctx.Filename,
		Scope:      NewScope(optional.PtrFromNillable(ctx.Scope)),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
	}
}
