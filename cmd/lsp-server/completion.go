package main

import (
	"sort"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/type_system"
)

const maxCompletionItems = 100

func (s *Server) textDocumentCompletion(context *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := params.TextDocument.URI

	s.mu.RLock()
	script := s.astCache[uri]
	scope := s.scopeCache[uri]
	s.mu.RUnlock()

	if script == nil || scope == nil {
		return nil, nil
	}

	loc := posToLoc(params.Position)
	node, parent := findNodeAndParent(script, loc)

	if node == nil {
		return nil, nil
	}

	var items []protocol.CompletionItem

	switch n := node.(type) {
	case *ast.MemberExpr:
		// Case A: cursor right after `.` — MemberExpr with empty property
		// Case B: cursor on partial property like `foo.ba|` — MemberExpr with non-empty property
		objType := n.Object.InferredType()
		if objType != nil {
			if n.OptChain {
				objType = stripNullUndefined(objType)
			}
			items = completionsFromType(objType, scope)
			if n.Prop.Name != "" {
				items = filterByPrefix(items, n.Prop.Name)
			}
		}
	case *ast.IdentExpr:
		if _, ok := parent.(*ast.MemberExpr); ok {
			// Shouldn't normally reach here — MemberExpr case handles it.
			// But just in case, fall through to scope completions.
		}
		// Case C: standalone identifier — scope-based completions
		items = completionsFromScope(script, scope, loc)
		items = filterByPrefix(items, n.Name)
	default:
		// No completions for other node types
	}

	items = sortAndLimit(items)
	isIncomplete := len(items) >= maxCompletionItems

	return &protocol.CompletionList{
		IsIncomplete: isIncomplete,
		Items:        items,
	}, nil
}

// completionsFromType returns completion items for member access on a type.
func completionsFromType(t type_system.Type, scope *checker.Scope) []protocol.CompletionItem {
	t = type_system.Prune(t)

	switch t := t.(type) {
	case *type_system.ObjectType:
		return completionsFromObjectType(t)
	case *type_system.NamespaceType:
		return completionsFromNamespace(t.Namespace)
	case *type_system.PrimType:
		wrapperName := primWrapperName(t.Prim)
		if wrapperName == "" {
			return nil
		}
		alias := scope.GetTypeAlias(wrapperName)
		if alias == nil {
			return nil
		}
		return completionsFromType(alias.Type, scope)
	case *type_system.LitType:
		var wrapperName string
		switch t.Lit.(type) {
		case *type_system.BoolLit:
			wrapperName = "Boolean"
		case *type_system.NumLit:
			wrapperName = "Number"
		case *type_system.StrLit:
			wrapperName = "String"
		default:
			return nil
		}
		alias := scope.GetTypeAlias(wrapperName)
		if alias == nil {
			return nil
		}
		return completionsFromType(alias.Type, scope)
	case *type_system.TupleType:
		alias := scope.GetTypeAlias("Array")
		if alias == nil {
			return nil
		}
		return completionsFromType(alias.Type, scope)
	case *type_system.FuncType:
		alias := scope.GetTypeAlias("Function")
		if alias == nil {
			return nil
		}
		return completionsFromType(alias.Type, scope)
	case *type_system.UnionType:
		return completionsFromUnionType(t, scope)
	case *type_system.IntersectionType:
		return completionsFromIntersectionType(t, scope)
	case *type_system.TypeRefType:
		if t.TypeAlias != nil {
			return completionsFromType(t.TypeAlias.Type, scope)
		}
		return nil
	case *type_system.TypeVarType:
		// Unresolved type variable — no completions
		return nil
	case *type_system.ErrorType, *type_system.NeverType, *type_system.AnyType:
		return nil
	default:
		return nil
	}
}

func completionsFromObjectType(obj *type_system.ObjectType) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *type_system.PropertyElem:
			if elem.Name.Kind != type_system.StrObjTypeKeyKind {
				continue
			}
			label := elem.Name.Str
			if elem.Optional {
				label += "?"
			}
			kind := protocol.CompletionItemKindProperty
			detail := elem.Value.String()
			items = append(items, protocol.CompletionItem{
				Label:      label,
				Kind:       &kind,
				Detail:     &detail,
				FilterText: &elem.Name.Str,
			})
		case *type_system.MethodElem:
			if elem.Name.Kind != type_system.StrObjTypeKeyKind {
				continue
			}
			kind := protocol.CompletionItemKindMethod
			detail := elem.Fn.String()
			items = append(items, protocol.CompletionItem{
				Label:      elem.Name.Str,
				Kind:       &kind,
				Detail:     &detail,
				FilterText: &elem.Name.Str,
			})
		case *type_system.GetterElem:
			if elem.Name.Kind != type_system.StrObjTypeKeyKind {
				continue
			}
			kind := protocol.CompletionItemKindProperty
			detail := elem.Fn.Return.String()
			items = append(items, protocol.CompletionItem{
				Label:      elem.Name.Str,
				Kind:       &kind,
				Detail:     &detail,
				FilterText: &elem.Name.Str,
			})
		case *type_system.SetterElem:
			if elem.Name.Kind != type_system.StrObjTypeKeyKind {
				continue
			}
			kind := protocol.CompletionItemKindProperty
			detail := "setter"
			items = append(items, protocol.CompletionItem{
				Label:      elem.Name.Str,
				Kind:       &kind,
				Detail:     &detail,
				FilterText: &elem.Name.Str,
			})
		}
	}
	return items
}

func completionsFromNamespace(ns *type_system.Namespace) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for name, binding := range ns.Values {
		kind := protocol.CompletionItemKindVariable
		detail := safeTypeString(binding.Type)
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   &kind,
			Detail: &detail,
		})
	}
	for name, alias := range ns.Types {
		kind := completionKindForTypeAlias(alias)
		detail := safeTypeString(alias.Type)
		items = append(items, protocol.CompletionItem{
			Label:  name,
			Kind:   &kind,
			Detail: &detail,
		})
	}
	for name := range ns.Namespaces {
		kind := protocol.CompletionItemKindModule
		items = append(items, protocol.CompletionItem{
			Label: name,
			Kind:  &kind,
		})
	}
	return items
}

func completionsFromUnionType(u *type_system.UnionType, scope *checker.Scope) []protocol.CompletionItem {
	// Collect members from each variant, return only common ones
	var allSets []map[string]protocol.CompletionItem
	for _, variant := range u.Types {
		variant = type_system.Prune(variant)
		// Skip null/undefined in unions
		if isNullOrUndefined(variant) {
			continue
		}
		memberItems := completionsFromType(variant, scope)
		set := make(map[string]protocol.CompletionItem, len(memberItems))
		for _, item := range memberItems {
			set[item.Label] = item
		}
		allSets = append(allSets, set)
	}
	if len(allSets) == 0 {
		return nil
	}
	// Keep only items present in all sets
	var items []protocol.CompletionItem
	for label, item := range allSets[0] {
		common := true
		for _, set := range allSets[1:] {
			if _, ok := set[label]; !ok {
				common = false
				break
			}
		}
		if common {
			items = append(items, item)
		}
	}
	return items
}

func completionsFromIntersectionType(inter *type_system.IntersectionType, scope *checker.Scope) []protocol.CompletionItem {
	// Merge members from all parts
	seen := map[string]bool{}
	var items []protocol.CompletionItem
	for _, part := range inter.Types {
		partItems := completionsFromType(part, scope)
		for _, item := range partItems {
			if !seen[item.Label] {
				seen[item.Label] = true
				items = append(items, item)
			}
		}
	}
	return items
}

// completionsFromScope collects in-scope bindings for standalone identifier completion.
func completionsFromScope(script *ast.Script, scope *checker.Scope, cursor ast.Location) []protocol.CompletionItem {
	seen := map[string]bool{}
	var items []protocol.CompletionItem

	// Step 1: Collect local bindings from AST traversal (position-dependent)
	for _, stmt := range script.Stmts {
		// Import statements are always visible
		if importStmt, ok := stmt.(*ast.ImportStmt); ok {
			for _, spec := range importStmt.Specifiers {
				name := spec.Name
				if spec.Alias != "" {
					name = spec.Alias
				}
				if !seen[name] {
					seen[name] = true
					kind := protocol.CompletionItemKindModule
					items = append(items, protocol.CompletionItem{
						Label: name,
						Kind:  &kind,
					})
				}
			}
			continue
		}

		// Hoisted function declarations are always visible in scripts
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if funcDecl, ok := declStmt.Decl.(*ast.FuncDecl); ok {
				name := funcDecl.Name.Name
				if name != "" && !seen[name] {
					seen[name] = true
					kind := protocol.CompletionItemKindFunction
					var detail *string
					if binding := scope.Namespace.Values[name]; binding != nil {
						d := binding.Type.String()
						detail = &d
					}
					items = append(items, protocol.CompletionItem{
						Label:  name,
						Kind:   &kind,
						Detail: detail,
					})
				}
				continue
			}
		}

		// For other statements, only include if declared before cursor
		if stmt.Span().Start.Line > cursor.Line ||
			(stmt.Span().Start.Line == cursor.Line && stmt.Span().Start.Column > cursor.Column) {
			continue
		}

		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				collectPatternBindings(varDecl.Pattern, seen, &items)
			}
		}
	}

	// Step 2: Walk the scope chain for outer scopes
	// Skip the script scope (already handled by AST traversal above)
	// Walk from parent (global scope) upward
	if scope.Parent != nil {
		collectScopeBindings(scope.Parent, seen, &items)
	}

	return items
}

func collectPatternBindings(pat ast.Pat, seen map[string]bool, items *[]protocol.CompletionItem) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		if pat.Name != "" && !seen[pat.Name] {
			seen[pat.Name] = true
			kind := protocol.CompletionItemKindVariable
			var detail *string
			if pat.InferredType() != nil {
				d := pat.InferredType().String()
				detail = &d
			}
			*items = append(*items, protocol.CompletionItem{
				Label:  pat.Name,
				Kind:   &kind,
				Detail: detail,
			})
		}
	case *ast.ObjectPat:
		for _, elem := range pat.Elems {
			switch elem := elem.(type) {
			case *ast.ObjKeyValuePat:
				collectPatternBindings(elem.Value, seen, items)
			case *ast.ObjShorthandPat:
				name := elem.Key.Name
				if name != "" && !seen[name] {
					seen[name] = true
					kind := protocol.CompletionItemKindVariable
					*items = append(*items, protocol.CompletionItem{
						Label: name,
						Kind:  &kind,
					})
				}
			case *ast.ObjRestPat:
				collectPatternBindings(elem.Pattern, seen, items)
			}
		}
	case *ast.TuplePat:
		for _, elem := range pat.Elems {
			collectPatternBindings(elem, seen, items)
		}
	}
}

func collectScopeBindings(scope *checker.Scope, seen map[string]bool, items *[]protocol.CompletionItem) {
	if scope == nil {
		return
	}
	ns := scope.Namespace
	for name, binding := range ns.Values {
		if !seen[name] {
			seen[name] = true
			kind := protocol.CompletionItemKindVariable
			detail := safeTypeString(binding.Type)
			*items = append(*items, protocol.CompletionItem{
				Label:  name,
				Kind:   &kind,
				Detail: &detail,
			})
		}
	}
	for name, alias := range ns.Types {
		if !seen[name] {
			seen[name] = true
			kind := completionKindForTypeAlias(alias)
			detail := safeTypeString(alias.Type)
			*items = append(*items, protocol.CompletionItem{
				Label:  name,
				Kind:   &kind,
				Detail: &detail,
			})
		}
	}
	for name := range ns.Namespaces {
		if !seen[name] {
			seen[name] = true
			kind := protocol.CompletionItemKindModule
			*items = append(*items, protocol.CompletionItem{
				Label: name,
				Kind:  &kind,
			})
		}
	}
	collectScopeBindings(scope.Parent, seen, items)
}

// Helper functions

// safeTypeString returns a string representation of a type, avoiding
// infinite recursion on NamespaceType (whose String() recurses into
// sub-namespace bindings).
func safeTypeString(t type_system.Type) string {
	if _, ok := t.(*type_system.NamespaceType); ok {
		return "namespace"
	}
	return t.String()
}

func completionKindForTypeAlias(alias *type_system.TypeAlias) protocol.CompletionItemKind {
	if obj, ok := alias.Type.(*type_system.ObjectType); ok {
		if obj.Nominal {
			return protocol.CompletionItemKindClass
		}
		if obj.Interface {
			return protocol.CompletionItemKindInterface
		}
	}
	return protocol.CompletionItemKindStruct
}

func primWrapperName(prim type_system.Prim) string {
	switch prim {
	case type_system.BoolPrim:
		return "Boolean"
	case type_system.NumPrim:
		return "Number"
	case type_system.StrPrim:
		return "String"
	case type_system.BigIntPrim:
		return "BigInt"
	case type_system.SymbolPrim:
		return "Symbol"
	default:
		return ""
	}
}

func stripNullUndefined(t type_system.Type) type_system.Type {
	union, ok := t.(*type_system.UnionType)
	if !ok {
		return t
	}
	var filtered []type_system.Type
	for _, variant := range union.Types {
		if !isNullOrUndefined(variant) {
			filtered = append(filtered, variant)
		}
	}
	if len(filtered) == 0 {
		return t
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return type_system.NewUnionType(nil, filtered...)
}

func isNullOrUndefined(t type_system.Type) bool {
	if lit, ok := t.(*type_system.LitType); ok {
		switch lit.Lit.(type) {
		case *type_system.NullLit, *type_system.UndefinedLit:
			return true
		}
	}
	return false
}

func filterByPrefix(items []protocol.CompletionItem, prefix string) []protocol.CompletionItem {
	if prefix == "" {
		return items
	}
	lowerPrefix := strings.ToLower(prefix)
	var filtered []protocol.CompletionItem
	for _, item := range items {
		label := item.Label
		// Strip trailing ? for filtering
		label = strings.TrimSuffix(label, "?")
		if strings.HasPrefix(strings.ToLower(label), lowerPrefix) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func sortAndLimit(items []protocol.CompletionItem) []protocol.CompletionItem {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Label < items[j].Label
	})
	if len(items) > maxCompletionItems {
		items = items[:maxCompletionItems]
	}
	return items
}
