package checker

import (
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/vektah/gqlparser/v2/ast"
)

type Checker struct {
	TypeVarID int
	SymbolID  int
	Schema    *ast.Schema
}

func NewChecker() *Checker {
	return &Checker{
		TypeVarID: 0,
		SymbolID:  0,
		Schema:    nil,
	}
}

func (c *Checker) FreshVar(provenance provenance.Provenance) *type_system.TypeVarType {
	c.TypeVarID++
	return type_system.NewTypeVarType(provenance, c.TypeVarID)
}

type Ref[T any] struct {
	Value T
}

type Context struct {
	Scope                  *Scope
	IsAsync                bool
	IsPatMatch             bool
	AllowUndefinedTypeRefs bool
	TypeRefsToUpdate       *Ref[[]*type_system.TypeRefType]
}

func (ctx *Context) WithNewScope() Context {
	return Context{
		Scope:                  ctx.Scope.WithNewScope(),
		IsAsync:                ctx.IsAsync,
		IsPatMatch:             ctx.IsPatMatch,
		AllowUndefinedTypeRefs: ctx.AllowUndefinedTypeRefs,
		TypeRefsToUpdate:       ctx.TypeRefsToUpdate,
	}
}

func (ctx *Context) WithNewScopeAndNamespace(ns *type_system.Namespace) Context {
	return Context{
		Scope:      ctx.Scope.WithNewScopeAndNamespace(ns),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
	}
}
