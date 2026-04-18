package checker

import (
	"context"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/type_system"
	gqlast "github.com/vektah/gqlparser/v2/ast"
)

type Checker struct {
	ctx                   context.Context            // Context for timeout/cancellation support (#457)
	TypeVarID             int
	SymbolID              int
	CustomMatcherSymbolID int                        // Symbol ID for Symbol.customMatcher (used for enum destructuring)
	Schema                *gqlast.Schema
	OverloadDecls         map[string][]*ast.FuncDecl // Tracks overloaded function declarations for codegen
	PackageRegistry       *PackageRegistry           // Registry for package namespaces (separate from scope chain)
	GlobalScope           *Scope                     // Explicit reference to global scope (contains globals like Array, Promise, etc.)
	FileScopes            map[int]*Scope             // Populated by InferModule: SourceID → file-specific scope
	expandCache           expandSeen                 // Cross-call cache for getMemberType's expansion loop (#453)
	substCache            expandSeen                 // Cross-call cache for expandTypeRef's SubstituteTypeParams (#461)
	memberCache           memberCache                // Per-property cache for lazy member substitution (#461)
}

func NewChecker(ctx context.Context) *Checker {
	return &Checker{
		ctx:                   ctx,
		TypeVarID:             0,
		SymbolID:              0,
		CustomMatcherSymbolID: -1,
		Schema:                nil,
		OverloadDecls:         make(map[string][]*ast.FuncDecl),
		PackageRegistry:       NewPackageRegistry(),
		GlobalScope:           nil, // Will be set by initializeGlobalScope() during prelude loading
		expandCache:           make(expandSeen),
		substCache:            make(expandSeen),
		memberCache:           make(memberCache),
	}
}

// checkTimeout panics with a TypeCheckTimeoutError if the checker's context
// has been cancelled (e.g. deadline exceeded). The panic is recovered at the
// top-level entry points (InferScript, InferModule, InferDepGraph) and
// converted into a regular error. Using panic ensures timeout errors bubble
// through all call sites, including trial unification and ExpandType calls
// that intentionally discard errors.
func (c *Checker) checkTimeout() {
	select {
	case <-c.ctx.Done():
		panic(TypeCheckTimeoutError{})
	default:
	}
}

// recoverTimeout catches a TypeCheckTimeoutError panic and appends it to the
// provided error slice. Must be called via defer at top-level entry points.
func recoverTimeout(errors *[]Error) {
	if r := recover(); r != nil {
		if _, ok := r.(TypeCheckTimeoutError); ok {
			*errors = append(*errors, TypeCheckTimeoutError{})
		} else {
			panic(r) // re-panic for non-timeout panics
		}
	}
}

func (c *Checker) FreshVar(provenance provenance.Provenance) *type_system.TypeVarType {
	c.TypeVarID++
	return type_system.NewTypeVarType(provenance, c.TypeVarID)
}

// findCustomMatcherMethod finds the [Symbol.customMatcher] method on an
// extractor's object type. Returns:
//   - (methodElem, extObj) when the method is found
//   - (nil, extObj) when the extractor is an ObjectType but has no
//     Symbol.customMatcher method
//   - (nil, nil) when the extractor is not an ObjectType
func (c *Checker) findCustomMatcherMethod(ext *type_system.ExtractorType) (*type_system.MethodElem, *type_system.ObjectType) {
	extractor := type_system.Prune(ext.Extractor)
	extObj, ok := extractor.(*type_system.ObjectType)
	if !ok {
		return nil, nil
	}
	for _, elem := range extObj.Elems {
		methodElem, ok := elem.(*type_system.MethodElem)
		if !ok {
			continue
		}
		if methodElem.Name.Kind == type_system.SymObjTypeKeyKind && methodElem.Name.Sym == c.CustomMatcherSymbolID {
			return methodElem, extObj
		}
	}
	return nil, extObj
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
	// AwaitThrowTypes collects throw types from await expressions during inference.
	// Pointer so block scopes share state within a function.
	AwaitThrowTypes *[]type_system.Type
	// Generator tracking:
	// ContainsYield and YieldedTypes are pointers because they are accumulators
	// mutated during traversal — block scopes must share the same underlying
	// values so that yields inside if/while/etc. propagate to the enclosing
	// function. Nested functions allocate fresh pointers to isolate their state.
	ContainsYield *bool
	YieldedTypes  *[]type_system.Type
	// GeneratorNextType is the TNext type for the current generator function,
	// controlling what type yield expressions evaluate to. Currently always nil
	// (yield evaluates to never, TNext is never) because most generators are
	// consumed via for...in loops rather than manual .next(value) calls. If we
	// later support explicit generator type annotations like
	// fn foo(): Generator<number, void, string>, this field would be set to
	// the annotated TNext so that yield expressions evaluate to that type.
	GeneratorNextType type_system.Type
	// InFuncBody is true when we're inside a function body. Used to suppress
	// generalization of nested FuncExprs, which must defer generalization to
	// the outermost enclosing function.
	InFuncBody bool
	// CallSites collects synthetic FuncTypes created when a TypeVarType is called
	// as a function. Keyed by TypeVar ID. Pointer so nested scopes share state
	// with the outermost enclosing function.
	CallSites *map[int][]*type_system.FuncType
	// CallSiteTypeVars maps TypeVar ID → the TypeVarType pointer, so we can
	// bind it when resolving call sites. Pointer for the same reason as CallSites.
	CallSiteTypeVars *map[int]*type_system.TypeVarType
}

func (ctx *Context) AddYieldedType(t type_system.Type) {
	if ctx.YieldedTypes != nil {
		*ctx.YieldedTypes = append(*ctx.YieldedTypes, t)
	}
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
		AwaitThrowTypes:        ctx.AwaitThrowTypes,
		ContainsYield:          ctx.ContainsYield,
		YieldedTypes:           ctx.YieldedTypes,
		GeneratorNextType:      ctx.GeneratorNextType,
		InFuncBody:             ctx.InFuncBody,
		CallSites:              ctx.CallSites,
		CallSiteTypeVars:       ctx.CallSiteTypeVars,
	}
}

// WithNewScopeAndNamespace creates a new context for entering a module namespace.
// AllowUndefinedTypeRefs and TypeRefsToUpdate are intentionally omitted here
// because callers manage those fields directly on the returned context.
func (ctx *Context) WithNewScopeAndNamespace(ns *type_system.Namespace) Context {
	return Context{
		Scope:             ctx.Scope.WithNewScopeAndNamespace(ns),
		IsAsync:           ctx.IsAsync,
		IsPatMatch:        ctx.IsPatMatch,
		FileScopes:        ctx.FileScopes,
		Module:            ctx.Module,
		AwaitThrowTypes:   ctx.AwaitThrowTypes,
		ContainsYield:     ctx.ContainsYield,
		YieldedTypes:      ctx.YieldedTypes,
		GeneratorNextType: ctx.GeneratorNextType,
		InFuncBody:        ctx.InFuncBody,
		CallSites:         ctx.CallSites,
		CallSiteTypeVars:  ctx.CallSiteTypeVars,
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
		AwaitThrowTypes:        ctx.AwaitThrowTypes,
		ContainsYield:          ctx.ContainsYield,
		YieldedTypes:           ctx.YieldedTypes,
		GeneratorNextType:      ctx.GeneratorNextType,
		InFuncBody:             ctx.InFuncBody,
		CallSites:              ctx.CallSites,
		CallSiteTypeVars:       ctx.CallSiteTypeVars,
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
