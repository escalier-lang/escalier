package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
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

func (c *Checker) FreshVar() *type_system.TypeVarType {
	c.ID++
	return &type_system.TypeVarType{
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

func (ctx *Context) WithParentScope() Context {
	return Context{
		Filename:   ctx.Filename,
		Scope:      NewScope(optional.PtrFromNillable(ctx.Scope)),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
	}
}
