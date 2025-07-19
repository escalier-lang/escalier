package checker

import (
	"github.com/escalier-lang/escalier/internal/type_system"
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

func (ctx *Context) WithNewScope() Context {
	return Context{
		Filename:   ctx.Filename,
		Scope:      ctx.Scope.WithNewScope(),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
	}
}

func (ctx *Context) WithNewScopeAndNamespace(ns *type_system.Namespace) Context {
	return Context{
		Filename:   ctx.Filename,
		Scope:      ctx.Scope.WithNewScopeAndNamespace(ns),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
	}
}
