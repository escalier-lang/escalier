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
	loc := posToLoc(params.Position)

	s.mu.RLock()
	module := s.moduleCache
	moduleScope := s.moduleScopeCache
	fileScopes := s.fileScopeCache
	script := s.astCache[uri]
	scope := s.scopeCache[uri]
	s.mu.RUnlock()

	// Try module-aware completions first.
	if module != nil && moduleScope != nil && s.isModuleFile(uri) {
		sourceID, ok := getSourceIDForModule(module, s.relPath(uri))
		if ok {
			return s.moduleCompletion(module, sourceID, moduleScope, fileScopes, loc)
		}
	}

	// Fall back to script-based completions.
	if script == nil || scope == nil {
		return nil, nil
	}

	node, parent := findNodeAndParent(script, loc)

	var items []protocol.CompletionItem

	if node == nil {
		// Cursor on whitespace/blank — provide scope-based completions
		items = completionsFromScope(script, scope, loc)
	} else {
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
			if memberExpr, ok := parent.(*ast.MemberExpr); ok {
				// The cursor landed on the property identifier of a MemberExpr.
				// Provide member completions filtered by the partial property name.
				objType := memberExpr.Object.InferredType()
				if objType != nil {
					if memberExpr.OptChain {
						objType = stripNullUndefined(objType)
					}
					items = completionsFromType(objType, scope)
					items = filterByPrefix(items, n.Name)
				}
			} else {
				// Case C: standalone identifier — scope-based completions
				items = completionsFromScope(script, scope, loc)
				items = filterByPrefix(items, n.Name)
			}
		default:
			// Other node types — provide scope-based completions
			items = completionsFromScope(script, scope, loc)
		}
	}

	totalBeforeLimit := len(items)
	items = sortAndLimit(items)
	isIncomplete := totalBeforeLimit > maxCompletionItems

	return &protocol.CompletionList{
		IsIncomplete: isIncomplete,
		Items:        items,
	}, nil
}

// moduleCompletion handles completions for files within a module.
func (s *Server) moduleCompletion(
	module *ast.Module,
	sourceID int,
	moduleScope *checker.Scope,
	fileScopes map[int]*checker.Scope,
	loc ast.Location,
) (any, error) {
	fileScope := fileScopes[sourceID]
	// Use the module scope for type lookups (member completions need it for
	// wrapper type aliases like String, Number, etc.)
	lookupScope := moduleScope
	if lookupScope.Parent != nil {
		lookupScope = lookupScope.Parent // prelude scope has the wrapper types
	}

	node, parent := findNodeAndParentInFile(module, sourceID, loc)

	var items []protocol.CompletionItem

	if node == nil {
		items = completionsFromModuleScope(module, sourceID, fileScope, moduleScope, loc)
	} else {
		switch n := node.(type) {
		case *ast.MemberExpr:
			objType := n.Object.InferredType()
			if objType != nil {
				if n.OptChain {
					objType = stripNullUndefined(objType)
				}
				items = completionsFromType(objType, lookupScope)
				if n.Prop.Name != "" {
					items = filterByPrefix(items, n.Prop.Name)
				}
			}
		case *ast.IdentExpr:
			if memberExpr, ok := parent.(*ast.MemberExpr); ok {
				objType := memberExpr.Object.InferredType()
				if objType != nil {
					if memberExpr.OptChain {
						objType = stripNullUndefined(objType)
					}
					items = completionsFromType(objType, lookupScope)
					items = filterByPrefix(items, n.Name)
				}
			} else {
				items = completionsFromModuleScope(module, sourceID, fileScope, moduleScope, loc)
				items = filterByPrefix(items, n.Name)
			}
		default:
			items = completionsFromModuleScope(module, sourceID, fileScope, moduleScope, loc)
		}
	}

	totalBeforeLimit := len(items)
	items = sortAndLimit(items)
	isIncomplete := totalBeforeLimit > maxCompletionItems

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

// TODO: show only value-level completions or only type-level completions when
// it makes sense to do so.
func completionsFromNamespace(ns *type_system.Namespace) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	for name, binding := range ns.Values {
		kind := completionKindForValueType(binding.Type)
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

// completionDedupeKey returns the canonical name for deduplication, using
// FilterText (the raw property name) when available, otherwise Label.
func completionDedupeKey(item protocol.CompletionItem) string {
	if item.FilterText != nil {
		return *item.FilterText
	}
	return item.Label
}

func completionsFromUnionType(u *type_system.UnionType, scope *checker.Scope) []protocol.CompletionItem {
	// Collect members from each variant. Properties that exist on at least one
	// variant are included. If a property is missing from some variants, its
	// detail type is shown with "| undefined".
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
			set[completionDedupeKey(item)] = item
		}
		allSets = append(allSets, set)
	}
	if len(allSets) == 0 {
		return nil
	}
	// Gather all unique keys across all sets.
	seen := map[string]bool{}
	var items []protocol.CompletionItem
	for _, set := range allSets {
		for key, item := range set {
			if seen[key] {
				continue
			}
			seen[key] = true
			// Check if this property is absent or optional in any variant.
			needsUndefined := false
			for _, other := range allSets {
				otherItem, ok := other[key]
				if !ok {
					needsUndefined = true
					break
				}
				if isOptionalCompletion(otherItem) {
					needsUndefined = true
					break
				}
			}
			if needsUndefined && item.Detail != nil {
				detail := *item.Detail + " | undefined"
				item.Detail = &detail
			}
			items = append(items, item)
		}
	}
	return items
}

func completionsFromIntersectionType(inter *type_system.IntersectionType, scope *checker.Scope) []protocol.CompletionItem {
	// Only keys present in ALL parts are accessible. If a key has different
	// value types across parts, its detail shows the intersection of those types.
	var allSets []map[string]protocol.CompletionItem
	for _, part := range inter.Types {
		memberItems := completionsFromType(part, scope)
		set := make(map[string]protocol.CompletionItem, len(memberItems))
		for _, item := range memberItems {
			set[completionDedupeKey(item)] = item
		}
		allSets = append(allSets, set)
	}
	if len(allSets) == 0 {
		return nil
	}
	// Keep only items present in all sets.
	var items []protocol.CompletionItem
	for key, item := range allSets[0] {
		presentInAll := true
		for _, set := range allSets[1:] {
			if _, ok := set[key]; !ok {
				presentInAll = false
				break
			}
		}
		if !presentInAll {
			continue
		}
		// Collect distinct detail strings across all parts for this key.
		if item.Detail != nil {
			seen := map[string]bool{*item.Detail: true}
			var details []string
			details = append(details, *item.Detail)
			for _, set := range allSets[1:] {
				other := set[key]
				if other.Detail != nil && !seen[*other.Detail] {
					seen[*other.Detail] = true
					details = append(details, *other.Detail)
				}
			}
			if len(details) > 1 {
				detail := strings.Join(details, " & ")
				item.Detail = &detail
			}
		}
		items = append(items, item)
	}
	return items
}

// completionsFromScope collects in-scope bindings for standalone identifier completion.
// It walks the AST ancestor chain from the cursor position, collecting bindings from
// each enclosing scope: function params, match/catch pattern bindings, for-in loop
// variables, if-let patterns, and block-local variable declarations.
func completionsFromScope(script *ast.Script, scope *checker.Scope, cursor ast.Location) []protocol.CompletionItem {
	seen := map[string]bool{}
	var items []protocol.CompletionItem

	// Find the ancestor chain from root to the cursor's deepest node.
	_, ancestors := findNodeWithAncestors(script, cursor)

	// Process ancestors innermost-first so inner bindings shadow outer ones.
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch a := ancestors[i].(type) {
		case *ast.FuncDecl:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(param.Pattern, seen, &items)
				}
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.FuncExpr:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(param.Pattern, seen, &items)
				}
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.MatchExpr:
			for _, matchCase := range a.Cases {
				if matchCase.Span().Contains(cursor) {
					collectPatternBindings(matchCase.Pattern, seen, &items)
					if matchCase.Body.Block != nil {
						collectBlockBindings(matchCase.Body.Block.Stmts, cursor, false, seen, &items)
					}
					break
				}
			}
		case *ast.TryCatchExpr:
			if a.Try.Span.Contains(cursor) {
				collectBlockBindings(a.Try.Stmts, cursor, false, seen, &items)
			} else {
				for _, catchCase := range a.Catch {
					if catchCase.Span().Contains(cursor) {
						collectPatternBindings(catchCase.Pattern, seen, &items)
						if catchCase.Body.Block != nil {
							collectBlockBindings(catchCase.Body.Block.Stmts, cursor, false, seen, &items)
						}
						break
					}
				}
			}
		case *ast.IfLetExpr:
			if a.Cons.Span.Contains(cursor) {
				collectPatternBindings(a.Pattern, seen, &items)
				collectBlockBindings(a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.IfElseExpr:
			if a.Cons.Span.Contains(cursor) {
				collectBlockBindings(a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.DoExpr:
			if a.Body.Span.Contains(cursor) {
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.ForInStmt:
			if a.Body.Span.Contains(cursor) {
				collectPatternBindings(a.Pattern, seen, &items)
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		}
	}

	// Collect from top-level script statements.
	// Scripts (bin/) don't hoist — only modules (lib/) do.
	collectBlockBindings(script.Stmts, cursor, false, seen, &items)

	// Collect type and namespace symbols from the current scope.
	// Values are already collected by collectBlockBindings above.
	collectScopeTypeBindings(scope, seen, &items)

	// Walk the parent scope chain for global/prelude bindings.
	if scope.Parent != nil {
		collectScopeBindings(scope.Parent, seen, &items)
	}

	return items
}

// completionsFromModuleScope collects in-scope bindings for a cursor position
// within a module file. It handles file-scoped imports, cross-file declarations,
// and position-dependent filtering for declarations inside a block scope.
func completionsFromModuleScope(
	module *ast.Module,
	sourceID int,
	fileScope *checker.Scope,
	moduleScope *checker.Scope,
	cursor ast.Location,
) []protocol.CompletionItem {
	seen := map[string]bool{}
	var items []protocol.CompletionItem

	// 1. Walk ancestor chain for inner scope bindings (function params, etc.)
	_, ancestors := findNodeWithAncestorsInFile(module, sourceID, cursor)
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch a := ancestors[i].(type) {
		case *ast.FuncDecl:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(param.Pattern, seen, &items)
				}
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.FuncExpr:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(param.Pattern, seen, &items)
				}
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.MatchExpr:
			for _, matchCase := range a.Cases {
				if matchCase.Span().Contains(cursor) {
					collectPatternBindings(matchCase.Pattern, seen, &items)
					if matchCase.Body.Block != nil {
						collectBlockBindings(matchCase.Body.Block.Stmts, cursor, false, seen, &items)
					}
					break
				}
			}
		case *ast.TryCatchExpr:
			if a.Try.Span.Contains(cursor) {
				collectBlockBindings(a.Try.Stmts, cursor, false, seen, &items)
			} else {
				for _, catchCase := range a.Catch {
					if catchCase.Span().Contains(cursor) {
						collectPatternBindings(catchCase.Pattern, seen, &items)
						if catchCase.Body.Block != nil {
							collectBlockBindings(catchCase.Body.Block.Stmts, cursor, false, seen, &items)
						}
						break
					}
				}
			}
		case *ast.IfLetExpr:
			if a.Cons.Span.Contains(cursor) {
				collectPatternBindings(a.Pattern, seen, &items)
				collectBlockBindings(a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.IfElseExpr:
			if a.Cons.Span.Contains(cursor) {
				collectBlockBindings(a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.DoExpr:
			if a.Body.Span.Contains(cursor) {
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.ForInStmt:
			if a.Body.Span.Contains(cursor) {
				collectPatternBindings(a.Pattern, seen, &items)
				collectBlockBindings(a.Body.Stmts, cursor, false, seen, &items)
			}
		}
	}

	// 2. Collect module-level value declarations with position filtering.
	// All top-level declarations in a module are always visible from other files
	// inside the same module.
	collectModuleDeclBindings(module, sourceID, moduleScope, seen, &items)

	// 3. Collect types and namespaces from the module scope.
	collectScopeTypeBindings(moduleScope, seen, &items)

	// 4. Collect file-scoped import bindings (values, types, namespaces).
	if fileScope != nil {
		collectFileImportBindings(module, sourceID, fileScope, seen, &items)
	}

	// 5. Walk parent scope chain for prelude/global bindings.
	if moduleScope.Parent != nil {
		collectScopeBindings(moduleScope.Parent, seen, &items)
	}

	return items
}

// collectModuleDeclBindings collects value bindings from module declarations.
// All declarations are visible regardless of position since the DepGraph reorders
// them before type checking. All declarations from the current file and other
// files are visible.
func collectModuleDeclBindings(
	module *ast.Module,
	sourceID int,
	moduleScope *checker.Scope,
	seen map[string]bool,
	items *[]protocol.CompletionItem,
) {
	// Find the file's namespace name
	var fileNsName string
	for _, file := range module.Files {
		if file.SourceID == sourceID {
			fileNsName = file.Namespace
			break
		}
	}

	// Get the type_system.Namespace for this file's namespace
	tsNs := moduleScope.Namespace
	if fileNsName != "" {
		for _, part := range strings.Split(fileNsName, ".") {
			child, ok := tsNs.GetNamespace(part)
			if !ok {
				return
			}
			tsNs = child
		}
	}

	// Walk declarations in the file's AST namespace
	astNs, exists := module.Namespaces.Get(fileNsName)
	if !exists {
		return
	}

	for _, decl := range astNs.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			// Function declarations are always visible (hoisted)
			name := d.Name.Name
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			kind := protocol.CompletionItemKindFunction
			var detail *string
			if binding, ok := tsNs.Values[name]; ok {
				d := safeTypeString(binding.Type)
				detail = &d
			}
			*items = append(*items, protocol.CompletionItem{
				Label:  name,
				Kind:   &kind,
				Detail: detail,
			})
		case *ast.VarDecl:
			// Val declarations: all top-level declarations are visible anywhere in the file
			// because the DepGraph reorders them before type checking
			collectPatternBindings(d.Pattern, seen, items)
		}
	}
}

// collectFileImportBindings collects only bindings introduced by import
// statements in the current file (values, types, namespaces).
func collectFileImportBindings(
	module *ast.Module,
	sourceID int,
	scope *checker.Scope,
	seen map[string]bool,
	items *[]protocol.CompletionItem,
) {
	if module == nil || scope == nil {
		return
	}

	var file *ast.File
	for _, f := range module.Files {
		if f.SourceID == sourceID {
			file = f
			break
		}
	}
	if file == nil {
		return
	}

	ns := scope.Namespace
	for _, importStmt := range file.Imports {
		for _, spec := range importStmt.Specifiers {
			localName := spec.Name
			if spec.Alias != "" {
				localName = spec.Alias
			}
			if localName == "" || localName == "*" || seen[localName] {
				continue
			}

			if binding, ok := ns.Values[localName]; ok {
				seen[localName] = true
				kind := completionKindForValueType(binding.Type)
				detail := safeTypeString(binding.Type)
				*items = append(*items, protocol.CompletionItem{
					Label:  localName,
					Kind:   &kind,
					Detail: &detail,
				})
				continue
			}

			if alias, ok := ns.Types[localName]; ok {
				seen[localName] = true
				kind := completionKindForTypeAlias(alias)
				detail := safeTypeString(alias.Type)
				*items = append(*items, protocol.CompletionItem{
					Label:  localName,
					Kind:   &kind,
					Detail: &detail,
				})
				continue
			}

			if _, ok := ns.GetNamespace(localName); ok {
				seen[localName] = true
				kind := protocol.CompletionItemKindModule
				*items = append(*items, protocol.CompletionItem{
					Label: localName,
					Kind:  &kind,
				})
			}
		}
	}
}

// collectBlockBindings collects imports, declarations, and variable bindings
// from a block's statements. When hoistFuncs is true, function declarations
// and imports are visible regardless of cursor position (for top-level module
// statements). Otherwise, all declarations are position-dependent.
func collectBlockBindings(stmts []ast.Stmt, cursor ast.Location, hoistFuncs bool, seen map[string]bool, items *[]protocol.CompletionItem) {
	if hoistFuncs {
		// Pass 1: imports and hoisted function declarations (always visible)
		for _, stmt := range stmts {
			if importStmt, ok := stmt.(*ast.ImportStmt); ok {
				for _, spec := range importStmt.Specifiers {
					name := spec.Name
					if spec.Alias != "" {
						name = spec.Alias
					}
					if !seen[name] {
						seen[name] = true
						kind := protocol.CompletionItemKindModule
						*items = append(*items, protocol.CompletionItem{
							Label: name,
							Kind:  &kind,
						})
					}
				}
			}
			if declStmt, ok := stmt.(*ast.DeclStmt); ok {
				if funcDecl, ok := declStmt.Decl.(*ast.FuncDecl); ok {
					name := funcDecl.Name.Name
					if name != "" && !seen[name] {
						seen[name] = true
						kind := protocol.CompletionItemKindFunction
						*items = append(*items, protocol.CompletionItem{
							Label: name,
							Kind:  &kind,
						})
					}
				}
			}
		}
	}

	// Declarations before the cursor (variable and, when not hoisted, function/import)
	for _, stmt := range stmts {
		if stmt.Span().Start.Line > cursor.Line ||
			(stmt.Span().Start.Line == cursor.Line && stmt.Span().Start.Column > cursor.Column) {
			continue
		}
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				collectPatternBindings(varDecl.Pattern, seen, items)
			}
			if !hoistFuncs {
				if funcDecl, ok := declStmt.Decl.(*ast.FuncDecl); ok {
					name := funcDecl.Name.Name
					if name != "" && !seen[name] {
						seen[name] = true
						kind := protocol.CompletionItemKindFunction
						*items = append(*items, protocol.CompletionItem{
							Label: name,
							Kind:  &kind,
						})
					}
				}
			}
		}
		if !hoistFuncs {
			if importStmt, ok := stmt.(*ast.ImportStmt); ok {
				for _, spec := range importStmt.Specifiers {
					name := spec.Name
					if spec.Alias != "" {
						name = spec.Alias
					}
					if !seen[name] {
						seen[name] = true
						kind := protocol.CompletionItemKindModule
						*items = append(*items, protocol.CompletionItem{
							Label: name,
							Kind:  &kind,
						})
					}
				}
			}
		}
	}
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

// collectScopeTypeBindings collects only type and namespace symbols from a
// single scope level, without recursing into parents. This is used for the
// current scope where values are already collected via AST walking.
func collectScopeTypeBindings(scope *checker.Scope, seen map[string]bool, items *[]protocol.CompletionItem) {
	if scope == nil {
		return
	}
	ns := scope.Namespace
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
}

func collectScopeBindings(scope *checker.Scope, seen map[string]bool, items *[]protocol.CompletionItem) {
	if scope == nil {
		return
	}
	ns := scope.Namespace
	for name, binding := range ns.Values {
		if !seen[name] {
			seen[name] = true
			kind := completionKindForValueType(binding.Type)
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

func completionKindForValueType(t type_system.Type) protocol.CompletionItemKind {
	t = type_system.Prune(t)
	if _, ok := t.(*type_system.FuncType); ok {
		return protocol.CompletionItemKindFunction
	}
	return protocol.CompletionItemKindVariable
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

// isOptionalCompletion returns true if the completion item represents an
// optional property (label ends with "?" while FilterText is the raw name).
func isOptionalCompletion(item protocol.CompletionItem) bool {
	return item.FilterText != nil && strings.HasSuffix(item.Label, "?")
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

// TODO: Sort by relevance (e.g. prefix-match score) instead of alphabetically,
// so that truncation keeps the most relevant items rather than the first alphabetically.
func sortAndLimit(items []protocol.CompletionItem) []protocol.CompletionItem {
	sort.Slice(items, func(i, j int) bool {
		return items[i].Label < items[j].Label
	})
	if len(items) > maxCompletionItems {
		items = items[:maxCompletionItems]
	}
	return items
}
