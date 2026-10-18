package main

import (
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/lexer_util"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
)

// scopeView is one level of a lexical scope as completions read it. A name a level binds
// is reported by that level alone, never by its parent.
type scopeView interface {
	// parent returns the enclosing level, or nil at the root.
	parent() scopeView
	// isBuiltin reports whether the level holds the builtins every file sees rather
	// than names the package declared.
	isBuiltin() bool
	// valueNames, typeNames, and namespaceNames return the names the level binds in
	// each sort.
	valueNames() []string
	typeNames() []string
	namespaceNames() []string
	// valueKind and valueDetail describe the value the level binds under name, and
	// report false when it binds none.
	valueKind(name string) (protocol.CompletionItemKind, bool)
	valueDetail(name string) (string, bool)
	// typeKind and typeDetail are valueKind and valueDetail for the type sort.
	typeKind(name string) (protocol.CompletionItemKind, bool)
	typeDetail(name string) (string, bool)
	// namespace returns the namespace the level binds under name.
	namespace(name string) (*solver.Namespace, bool)
	// lookupNamespace returns the namespace name resolves to from the level, walking
	// up to the root the way the solver resolves a name in the namespace sort.
	lookupNamespace(name string) (*solver.Namespace, bool)
	// memberValueDetail returns the detail of the value a module's namespace nsPath
	// declares under name, where nsPath is dotted and empty for the root namespace.
	memberValueDetail(nsPath, name string) (string, bool)
}

// lookupDetail returns the detail of the binding name resolves to from view, walking
// up to the root. A name a nearer level binds in any sort hides the same name further out.
func lookupDetail(view scopeView, name string) string {
	for cur := view; cur != nil; cur = cur.parent() {
		if detail, ok := cur.valueDetail(name); ok {
			return detail
		}
		if detail, ok := cur.typeDetail(name); ok {
			return detail
		}
		if _, ok := cur.namespace(name); ok {
			return "namespace"
		}
	}
	return ""
}

// solverScopeView adapts a *solver.Scope. info is a table from the same run, which
// expands a type alias for display.
type solverScopeView struct {
	scope *solver.Scope
	info  *solver.Info
}

// newSolverScopeView returns a view of scope, or nil when scope is nil.
func newSolverScopeView(scope *solver.Scope, info *solver.Info) scopeView {
	if scope == nil {
		return nil
	}
	return &solverScopeView{scope: scope, info: info}
}

func (v *solverScopeView) parent() scopeView {
	return newSolverScopeView(v.scope.Parent(), v.info)
}

func (v *solverScopeView) isBuiltin() bool { return v.scope.IsBuiltin() }

// The solver binds a few names a program cannot write, such as `unary -` for an
// operator's signature, so each list keeps only identifiers.

func (v *solverScopeView) valueNames() []string { return identifiers(v.scope.ValueNames()) }

func (v *solverScopeView) typeNames() []string { return identifiers(v.scope.TypeNames()) }

func (v *solverScopeView) namespaceNames() []string {
	return identifiers(v.scope.NamespaceNames())
}

func (v *solverScopeView) valueKind(name string) (protocol.CompletionItemKind, bool) {
	binding, ok := v.scope.LocalValue(name)
	if !ok {
		return 0, false
	}
	return completionKindForSolValue(binding.DisplayType()), true
}

func (v *solverScopeView) valueDetail(name string) (string, bool) {
	binding, ok := v.scope.LocalValue(name)
	if !ok {
		return "", false
	}
	return printSolType(binding.DisplayType()), true
}

func (v *solverScopeView) typeKind(name string) (protocol.CompletionItemKind, bool) {
	binding, ok := v.scope.LocalType(name)
	if !ok {
		return 0, false
	}
	return completionKindForSolType(binding), true
}

func (v *solverScopeView) typeDetail(name string) (string, bool) {
	binding, ok := v.scope.LocalType(name)
	if !ok {
		return "", false
	}
	t := binding.Type
	if v.info != nil {
		// A type binds a handle carrying its name, so an alias renders as the type it
		// stands for rather than as its own name.
		t = v.info.Expand(t)
	}
	return printSolType(t), true
}

func (v *solverScopeView) namespace(name string) (*solver.Namespace, bool) {
	return v.scope.OwnNamespace(name)
}

func (v *solverScopeView) lookupNamespace(name string) (*solver.Namespace, bool) {
	return v.scope.GetNamespace(name)
}

func (v *solverScopeView) memberValueDetail(nsPath, name string) (string, bool) {
	// A module scope keys a declaration a namespace holds under its qualified name.
	qualified := name
	if nsPath != "" {
		qualified = nsPath + "." + name
	}
	binding, ok := v.scope.OwnValue(qualified)
	if !ok {
		return "", false
	}
	return printSolType(binding.DisplayType()), true
}

// printSolType renders t as an Escalier type annotation, naming each type parameter
// generalization kept. A nil type renders as the empty string.
func printSolType(t soltype.Type) string {
	if t == nil {
		return ""
	}
	return soltype.PrintAsScheme(t)
}

// completionKindForSolValue returns the completion kind of a value of type t. A function,
// or an overload set rendered as an intersection of functions, is a function.
func completionKindForSolValue(t soltype.Type) protocol.CompletionItemKind {
	switch t := t.(type) {
	case *soltype.FuncType:
		return protocol.CompletionItemKindFunction
	case *soltype.IntersectionType:
		for _, part := range t.Types {
			if _, ok := part.(*soltype.FuncType); !ok {
				return protocol.CompletionItemKindVariable
			}
		}
		return protocol.CompletionItemKindFunction
	}
	return protocol.CompletionItemKindVariable
}

// completionKindForSolType returns the completion kind of a type binding, read from the
// declaration that made it when it has one and from the type it binds otherwise.
func completionKindForSolType(binding solver.TypeBinding) protocol.CompletionItemKind {
	for _, src := range binding.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		switch np.Node.(type) {
		case *ast.ClassDecl:
			return protocol.CompletionItemKindClass
		case *ast.InterfaceDecl:
			return protocol.CompletionItemKindInterface
		case *ast.EnumDecl:
			return protocol.CompletionItemKindEnum
		}
	}
	if _, ok := binding.Type.(*soltype.ClassType); ok {
		return protocol.CompletionItemKindClass
	}
	return protocol.CompletionItemKindStruct
}

// identifiers returns the names in names that the lexer reads as one identifier, in
// their original order.
func identifiers(names []string) []string {
	out := names[:0:0]
	for _, name := range names {
		if isIdentifier(name) {
			out = append(out, name)
		}
	}
	return out
}

// isIdentifier reports whether the lexer reads name as one identifier.
func isIdentifier(name string) bool {
	for i, r := range name {
		if i == 0 && !lexer_util.IsIdentStart(r) {
			return false
		}
		if i > 0 && !lexer_util.IsIdentContinue(r) {
			return false
		}
	}
	return name != ""
}

// firstBuiltin returns the nearest level from view outward that holds builtins, or nil
// when none does.
func firstBuiltin(view scopeView) scopeView {
	for view != nil && !view.isBuiltin() {
		view = view.parent()
	}
	return view
}
