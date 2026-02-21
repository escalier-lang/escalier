package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
	gqlast "github.com/vektah/gqlparser/v2/ast"
)

type Checker struct {
	TypeVarID       int
	SymbolID        int
	Schema          *gqlast.Schema
	OverloadDecls   map[string][]*ast.FuncDecl // Tracks overloaded function declarations for codegen
	PackageRegistry *PackageRegistry           // Registry for package namespaces (separate from scope chain)
	GlobalScope     *Scope                     // Explicit reference to global scope (contains globals like Array, Promise, etc.)
	Warnings        []Warning                  // Accumulated warnings (non-fatal diagnostics)
}

func NewChecker() *Checker {
	return &Checker{
		TypeVarID:       0,
		SymbolID:        0,
		Schema:          nil,
		OverloadDecls:   make(map[string][]*ast.FuncDecl),
		PackageRegistry: NewPackageRegistry(),
		GlobalScope:     nil, // Will be set by initializeGlobalScope() during prelude loading
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
	// FileScopes maps SourceID to file-specific scope.
	// Used for file-scoped imports in modules.
	FileScopes map[int]*Scope
	// Module is the current module being processed (for file path lookup).
	Module *ast.Module
}

func (ctx *Context) WithNewScope() Context {
	return Context{
		Scope:                  ctx.Scope.WithNewScope(),
		IsAsync:                ctx.IsAsync,
		IsPatMatch:             ctx.IsPatMatch,
		AllowUndefinedTypeRefs: ctx.AllowUndefinedTypeRefs,
		TypeRefsToUpdate:       ctx.TypeRefsToUpdate,
		FileScopes:             ctx.FileScopes,
		Module:                 ctx.Module,
	}
}

func (ctx *Context) WithNewScopeAndNamespace(ns *type_system.Namespace) Context {
	return Context{
		Scope:      ctx.Scope.WithNewScopeAndNamespace(ns),
		IsAsync:    ctx.IsAsync,
		IsPatMatch: ctx.IsPatMatch,
		FileScopes: ctx.FileScopes,
		Module:     ctx.Module,
	}
}

// WithScope creates a new context with a different scope.
func (ctx *Context) WithScope(scope *Scope) Context {
	return Context{
		Scope:                  scope,
		IsAsync:                ctx.IsAsync,
		IsPatMatch:             ctx.IsPatMatch,
		AllowUndefinedTypeRefs: ctx.AllowUndefinedTypeRefs,
		TypeRefsToUpdate:       ctx.TypeRefsToUpdate,
		FileScopes:             ctx.FileScopes,
		Module:                 ctx.Module,
	}
}

// GetFileScope returns the file scope for a given SourceID.
// If no file scope exists for the SourceID, returns the base module scope.
func (ctx *Context) GetFileScope(sourceID int) *Scope {
	if ctx.FileScopes != nil {
		if scope, ok := ctx.FileScopes[sourceID]; ok {
			return scope
		}
	}
	return ctx.Scope
}
