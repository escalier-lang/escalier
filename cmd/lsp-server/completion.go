package main

import (
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/lexer_util"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
)

const maxCompletionItems = 100

// completionResolveData is stored in CompletionItem.Data for deferred detail resolution.
type completionResolveData struct {
	// Scope identifies where to look up the binding: "prelude", "module", "file", or "script".
	Scope string `json:"scope"`
	// Name is the binding name to look up.
	Name string `json:"name"`
	// URI is the document URI (used for script-scoped lookups).
	URI string `json:"uri,omitempty"`
}

// buildPreludeCompletions computes completion items for all prelude/global scope
// bindings. Detail is deferred to completionItem/resolve.
func buildPreludeCompletions(view scopeView) []protocol.CompletionItem {
	if view == nil {
		return nil
	}
	var items []protocol.CompletionItem
	buildScopeCompletionsNoDetail(view, &items, "prelude")
	return items
}

// buildScopeCompletionsNoDetail recursively collects completion items from a scope
// chain without computing Detail strings. Sets Data for deferred resolution.
func buildScopeCompletionsNoDetail(view scopeView, items *[]protocol.CompletionItem, scopeID string) {
	for cur := view; cur != nil; cur = cur.parent() {
		for _, name := range cur.valueNames() {
			kind, _ := cur.valueKind(name)
			*items = append(*items, protocol.CompletionItem{
				Label: name,
				Kind:  &kind,
				Data:  completionResolveData{Scope: scopeID, Name: name},
			})
		}
		for _, name := range cur.typeNames() {
			kind, _ := cur.typeKind(name)
			*items = append(*items, protocol.CompletionItem{
				Label: name,
				Kind:  &kind,
				Data:  completionResolveData{Scope: scopeID, Name: name},
			})
		}
		for _, name := range cur.namespaceNames() {
			kind := protocol.CompletionItemKindModule
			*items = append(*items, protocol.CompletionItem{
				Label: name,
				Kind:  &kind,
				Data:  completionResolveData{Scope: scopeID, Name: name},
			})
		}
	}
}

// getPreludeCompletions returns cached completion items for the builtins, building them
// on first access from preludeView, the first builtin level of a script's or module's
// scope chain.
func (s *Server) getPreludeCompletions(preludeView scopeView) []protocol.CompletionItem {
	s.mu.RLock()
	cached := s.preludeCompletions
	s.mu.RUnlock()
	if cached != nil {
		return cached
	}

	items := buildPreludeCompletions(preludeView)

	s.mu.Lock()
	// Double-check in case another goroutine built it concurrently.
	if s.preludeCompletions == nil {
		s.preludeCompletions = items
	}
	cached = s.preludeCompletions
	s.mu.Unlock()
	return cached
}

// completionItemResolve handles completionItem/resolve requests by computing
// the Detail string on demand.
//
// Errors during Data decoding are intentionally swallowed: we return the item
// unchanged rather than propagating an error. Returning an error from resolve
// would cause LSP clients to show error popups for what is purely a cosmetic
// failure (missing type detail). The item remains usable without its detail.
func (s *Server) completionItemResolve(context *glsp.Context, params *protocol.CompletionItem) (*protocol.CompletionItem, error) {
	if params.Detail != nil {
		// Already resolved.
		return params, nil
	}
	if params.Data == nil {
		return params, nil
	}

	// params.Data arrives as a json.RawMessage or map; re-marshal to decode.
	raw, err := json.Marshal(params.Data)
	if err != nil {
		return params, nil
	}
	var data completionResolveData
	if err := json.Unmarshal(raw, &data); err != nil {
		return params, nil
	}
	if data.Scope == "" || data.Name == "" {
		return params, nil
	}

	detail := s.resolveDetail(data)
	if detail == "" {
		return params, nil
	}

	result := &protocol.CompletionItem{
		Label: params.Label,
		Kind:  params.Kind,
	}
	if detail != "" {
		result.Detail = &detail
	}
	return result, nil
}

// resolveDetail looks up the type detail string for a deferred completion item.
// It selects the appropriate scope (prelude, script, or module) based on
// data.Scope and searches for the binding by name.
func (s *Server) resolveDetail(data completionResolveData) string {
	s.mu.RLock()
	var view scopeView
	switch data.Scope {
	case "prelude":
		if co := s.checkOutput; co != nil {
			view = builtinScopeView(co)
		}
	case "script":
		if co := s.checkOutput; co != nil {
			view = scriptScopeView(co, s.sourceIDForURI(protocol.DocumentUri(data.URI)))
		}
	case "module":
		if co := s.checkOutput; co != nil {
			view = moduleScopeView(co)
		}
	}
	s.mu.RUnlock()
	if view == nil {
		return ""
	}
	s.solverMu.RLock()
	defer s.solverMu.RUnlock()
	return lookupDetail(view, data.Name)
}

// scriptScopeView returns a view of the scope a bin/ script was checked in, or nil when
// co holds none for sourceID.
func scriptScopeView(co *compiler.CheckOutput, sourceID int) scopeView {
	return newSolverScopeView(co.SolverScriptScopes[sourceID], co.ScriptInfos[sourceID])
}

// moduleScopeView returns a view of the lib/ module's scope, or nil when co holds none.
func moduleScopeView(co *compiler.CheckOutput) scopeView {
	return newSolverScopeView(co.SolverModuleScope, co.ModuleInfo)
}

// builtinScopeView returns a view of the first level that holds builtins in the scope
// chains co holds, or nil when co holds no scope. Every lib/ and bin/ file of a check
// reaches the same builtins, so any one chain answers.
func builtinScopeView(co *compiler.CheckOutput) scopeView {
	if view := moduleScopeView(co); view != nil {
		return firstBuiltin(view)
	}
	for sourceID := range co.Scripts {
		if view := scriptScopeView(co, sourceID); view != nil {
			return firstBuiltin(view)
		}
	}
	return nil
}

// fileScopeView returns a view of the import scope of the lib/ file sourceID names, or
// nil when co holds none for it.
func fileScopeView(co *compiler.CheckOutput, sourceID int) scopeView {
	return newSolverScopeView(co.SolverFileScopes[sourceID], co.ModuleInfo)
}

// shouldSuppressCompletions checks if the cursor is on a node where completions
// should be suppressed (e.g. IdentPat, which introduces a new binding).
// The caller (textDocumentCompletion) waits for in-flight validation before
// calling this, so the cached ASTs are up to date.
func shouldSuppressCompletions(script *ast.Script, module *ast.Module, sourceID int, isModule bool, loc ast.Location) bool {
	var ancestors []ast.Node
	if isModule {
		if module == nil || sourceID < 0 {
			return false
		}
		_, ancestors = findNodeWithAncestorsInFile(module, sourceID, loc)
	} else {
		if script == nil {
			return false
		}
		_, ancestors = findNodeWithAncestors(script, loc)
	}

	for _, ancestor := range ancestors {
		if varDecl, ok := ancestor.(*ast.VarDecl); ok {
			if varDecl.Pattern != nil && varDecl.Pattern.Span().Contains(loc) {
				return true
			}
		}
	}
	return false
}

func (s *Server) textDocumentCompletion(context *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := params.TextDocument.URI

	// Wait for an in-flight validation to complete if the cached AST is
	// stale (i.e. the document has been updated but validation hasn't
	// finished yet). Times out after 2s to avoid blocking indefinitely.
	deadline := time.Now().Add(2 * time.Second)
	s.mu.RLock()
	for s.isCacheStale(uri) && time.Now().Before(deadline) {
		// Puts the gorountine to sleep until `Broadcast()` is called.
		s.validated.Wait()
	}
	var module *ast.Module
	var script *ast.Script
	var checkedSrc *ast.Source
	var info *solver.Info
	var moduleView, fileView, scriptView scopeView
	sourceID := s.sourceIDForURI(uri)
	if co := s.checkOutput; co != nil {
		module = co.Module
		script = co.Scripts[sourceID]
		checkedSrc = co.SourceByID(sourceID)
		info = co.InfoFor(sourceID)
		moduleView = moduleScopeView(co)
		fileView = fileScopeView(co, sourceID)
		scriptView = scriptScopeView(co, sourceID)
	}
	doc, opened := s.documents[uri]
	s.mu.RUnlock()

	// The cursor comes from the same snapshot as the AST, so the offset the
	// position converts to indexes the text the AST was parsed from. Reading
	// the document before the wait above would leave the two describing
	// different revisions of the file.
	lineMap := lineMapFor(checkedSrc)
	if opened {
		lineMap = ast.NewLineMap(doc.Text)
	}
	loc := posToLoc(lineMap, params.Position)

	isModule := s.isModuleFile(uri)

	// Every path below may query a run's Info or its scopes.
	s.solverMu.RLock()
	defer s.solverMu.RUnlock()

	// Check if the cursor is on a node where completions should be suppressed
	// (e.g. IdentPat). This runs after waiting for validation so the cached
	// ASTs are up to date.
	if shouldSuppressCompletions(script, module, sourceID, isModule, loc) {
		return &protocol.CompletionList{Items: []protocol.CompletionItem{}}, nil
	}

	// Try module-aware completions first.
	if module != nil && isModule && moduleView != nil {
		return s.moduleCompletion(module, sourceID, moduleView, fileView, info, loc, doc.Text)
	}

	// Fall back to script-based completions.
	if script == nil || scriptView == nil {
		return nil, nil
	}

	node, parent := findNodeAndParent(script, loc)

	var items []protocol.CompletionItem

	if node == nil {
		// Cursor on whitespace/blank — provide scope-based completions
		items = s.completionsFromScope(script, scriptView, info, loc, string(uri))
	} else {
		switch n := node.(type) {
		case *ast.MemberExpr:
			// Case A: cursor right after `.` — MemberExpr with empty property
			// Case B: cursor on partial property like `foo.ba|` — MemberExpr with non-empty property
			items = s.memberCompletions(n, info, scriptView)
			if n.Prop.Name != "" {
				items = filterByPrefix(items, n.Prop.Name)
			}
		case *ast.IdentExpr:
			if memberExpr, ok := parent.(*ast.MemberExpr); ok {
				// The cursor landed on the property identifier of a MemberExpr.
				// Provide member completions filtered by the partial property name.
				items = s.memberCompletions(memberExpr, info, scriptView)
				items = filterByPrefix(items, n.Name)
			} else {
				// Case C: standalone identifier — scope-based completions
				items = s.completionsFromScope(script, scriptView, info, loc, string(uri))
				items = filterByPrefix(items, n.Name)
			}
		case *ast.TypeRefTypeAnn:
			// Cursor is in a type annotation — return only type completions.
			items = typeCompletionsFromScope(scriptView, "script", string(uri))
			if n.Name != nil {
				if ident, ok := n.Name.(*ast.Ident); ok && ident.Name != "" {
					items = filterByPrefix(items, ident.Name)
				}
			}
		default:
			// Other node types — provide scope-based completions
			items = s.completionsFromScope(script, scriptView, info, loc, string(uri))
			if prefix := wordAtCursor(doc.Text, loc); prefix != "" {
				items = filterByPrefix(items, prefix)
			}
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
	moduleView scopeView,
	fileView scopeView,
	info *solver.Info,
	loc ast.Location,
	docText string,
) (any, error) {
	// A lib/ file's import scope sits between the module scope and the file, so a
	// namespace the file imports resolves from it.
	nsView := fileView
	if nsView == nil {
		nsView = moduleView
	}

	node, parent := findNodeAndParentInFile(module, sourceID, loc)

	var items []protocol.CompletionItem

	if node == nil {
		items = s.completionsFromModuleScope(module, sourceID, fileView, moduleView, info, loc)
	} else {
		switch n := node.(type) {
		case *ast.MemberExpr:
			items = s.memberCompletions(n, info, nsView)
			if n.Prop.Name != "" {
				items = filterByPrefix(items, n.Prop.Name)
			}
		case *ast.IdentExpr:
			if memberExpr, ok := parent.(*ast.MemberExpr); ok {
				items = s.memberCompletions(memberExpr, info, nsView)
				items = filterByPrefix(items, n.Name)
			} else {
				items = s.completionsFromModuleScope(module, sourceID, fileView, moduleView, info, loc)
				items = filterByPrefix(items, n.Name)
			}
		case *ast.TypeRefTypeAnn:
			// Cursor is in a type annotation — return only type completions.
			// Include file-scoped imports so imported types are visible.
			seen := map[string]bool{}
			// URI is empty because resolveModuleDetail looks up s.moduleScopeCache
			// directly and doesn't need a per-file URI.
			items = typeCompletionsFromScope(moduleView, "module", "")
			for _, item := range items {
				seen[item.Label] = true
			}
			collectFileImportBindings(module, sourceID, fileView, seen, &items)
			// Filter out value-only imports (keep types and namespaces).
			items = filterTypeItems(items)
			if n.Name != nil {
				if ident, ok := n.Name.(*ast.Ident); ok && ident.Name != "" {
					items = filterByPrefix(items, ident.Name)
				}
			}
		default:
			items = s.completionsFromModuleScope(module, sourceID, fileView, moduleView, info, loc)
			if prefix := wordAtCursor(docText, loc); prefix != "" {
				items = filterByPrefix(items, prefix)
			}
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

// memberCompletions returns completion items for the members of e's receiver, read from
// info, or nil when info is nil. view is the scope e is written in.
func (s *Server) memberCompletions(e *ast.MemberExpr, info *solver.Info, view scopeView) []protocol.CompletionItem {
	if info == nil {
		return nil
	}
	if t := info.DisplayTypeOf(e.Object); t != nil {
		return completionsFromSolType(t, info, info.LendsMut(e.Object))
	}
	// A namespace is not a value, so the check records no type for one, and its members
	// are read from the scope that binds it instead.
	if ns, ok := namespaceAt(e.Object, view, info); ok {
		return completionsFromNamespace(ns, info)
	}
	return nil
}

// namespaceAt returns the namespace expr names, or false when it names none. An
// identifier names the namespace view resolves it to. A member expression such as
// `a.b` names the namespace its property selects from the namespace its object names.
// An identifier info resolved to a value names no namespace, since a value hides a
// namespace of the same name.
func namespaceAt(expr ast.Expr, view scopeView, info *solver.Info) (*solver.Namespace, bool) {
	switch expr := expr.(type) {
	case *ast.IdentExpr:
		if _, isValue := info.DeclOf(expr); isValue || view == nil {
			return nil, false
		}
		return view.lookupNamespace(expr.Name)
	case *ast.MemberExpr:
		outer, ok := namespaceAt(expr.Object, view, info)
		if !ok || expr.Prop == nil {
			return nil, false
		}
		nested, ok := outer.Nested[expr.Prop.Name]
		return nested, ok
	}
	return nil, false
}

// completionsFromNamespace returns a completion item for each value, type, and nested
// namespace ns holds. info expands a type alias for display.
func completionsFromNamespace(ns *solver.Namespace, info *solver.Info) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	add := func(name string, kind protocol.CompletionItemKind, detail string) {
		item := protocol.CompletionItem{Label: name, Kind: &kind}
		if detail != "" {
			item.Detail = &detail
		}
		items = append(items, item)
	}
	for _, name := range identifiers(slices.Sorted(maps.Keys(ns.Values))) {
		t := ns.Values[name].DisplayType()
		// A member bound with no type yet is one a read of it cannot resolve.
		if t == nil {
			continue
		}
		add(name, completionKindForSolValue(t), printSolType(t))
	}
	for _, name := range identifiers(slices.Sorted(maps.Keys(ns.Types))) {
		binding := ns.Types[name]
		add(name, completionKindForSolType(binding), printSolType(info.Expand(binding.Type)))
	}
	for _, name := range identifiers(slices.Sorted(maps.Keys(ns.Nested))) {
		add(name, protocol.CompletionItemKindModule, "")
	}
	return items
}

// completionsFromSolType returns completion items for the members of a value of type t.
// A member of a union is offered when any variant has it, and a member of an
// intersection when every part has it. receiverMut says whether the value lends mutable
// access, without which a `&mut self` method or a setter is left out.
func completionsFromSolType(t soltype.Type, info *solver.Info, receiverMut bool) []protocol.CompletionItem {
	// A union or an intersection may sit behind an alias or a borrow.
	switch t := info.Expand(t).(type) {
	case *soltype.UnionType:
		var sets [][]protocol.CompletionItem
		for _, variant := range t.Types {
			switch variant.(type) {
			case *soltype.NullType, *soltype.UndefinedType:
				// `a?.` and `a.` over `T | undefined` both offer T's members.
				continue
			}
			sets = append(sets, completionsFromSolType(variant, info, receiverMut))
		}
		return unionCompletions(sets)
	case *soltype.IntersectionType:
		sets := make([][]protocol.CompletionItem, len(t.Types))
		for i, part := range t.Types {
			sets[i] = completionsFromSolType(part, info, receiverMut)
		}
		return intersectionCompletions(sets)
	}
	obj, ok := info.MemberObject(t)
	if !ok {
		return nil
	}
	return completionsFromSolObject(obj, receiverMut)
}

// completionsFromSolObject returns a completion item for each member of obj named by a
// string. A member keyed by a symbol, such as `[Symbol.iterator]`, has no name to type
// after the dot and is left out.
func completionsFromSolObject(obj *soltype.ObjectType, receiverMut bool) []protocol.CompletionItem {
	var items []protocol.CompletionItem
	add := func(name, label string, kind protocol.CompletionItemKind, detail string) {
		items = append(items, protocol.CompletionItem{
			Label:      label,
			Kind:       &kind,
			Detail:     &detail,
			FilterText: &name,
		})
	}
	for _, elem := range obj.Elems {
		name := soltype.ObjElemName(elem)
		if name == "" || soltype.IsSymbolMemberName(name) {
			continue
		}
		if !receiverMut && solver.NeedsMutReceiver(elem) {
			continue
		}
		switch elem := elem.(type) {
		case *soltype.PropertyElem:
			label := name
			if elem.Optional {
				label += "?"
			}
			add(name, label, protocol.CompletionItemKindProperty, soltype.Print(elem.Type))
		case *soltype.MethodElem:
			add(name, name, protocol.CompletionItemKindMethod, methodDetail(elem))
		case *soltype.GetterElem:
			add(name, name, protocol.CompletionItemKindProperty, soltype.Print(elem.Type))
		case *soltype.SetterElem:
			add(name, name, protocol.CompletionItemKindProperty, "setter")
		}
	}
	return items
}

// methodDetail renders a method's signature without its receiver, which the caller
// supplies by writing the value before the dot. An overloaded method renders each arm,
// joined by ` & `.
func methodDetail(m *soltype.MethodElem) string {
	arms := make([]string, len(m.Signatures))
	for i, sig := range m.Signatures {
		withoutSelf := *sig
		withoutSelf.SelfParam = nil
		arms[i] = soltype.Print(&withoutSelf)
	}
	return strings.Join(arms, " & ")
}

// completionDedupeKey returns the canonical name for deduplication, using
// FilterText (the raw property name) when available, otherwise Label.
func completionDedupeKey(item protocol.CompletionItem) string {
	if item.FilterText != nil {
		return *item.FilterText
	}
	return item.Label
}

// unionCompletions merges the member completions of a union's variants, one set per
// variant. A member any variant has is offered. When a variant lacks the member or
// declares it optional, its detail gains `| undefined`.
func unionCompletions(sets [][]protocol.CompletionItem) []protocol.CompletionItem {
	allSets := keyedCompletionSets(sets)
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

// intersectionCompletions merges the member completions of an intersection's parts, one
// set per part. Only a member every part has is offered. When the parts give it
// different types, its detail joins them with ` & `.
func intersectionCompletions(sets [][]protocol.CompletionItem) []protocol.CompletionItem {
	allSets := keyedCompletionSets(sets)
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

// keyedCompletionSets keys each set's items by completionDedupeKey.
func keyedCompletionSets(sets [][]protocol.CompletionItem) []map[string]protocol.CompletionItem {
	keyed := make([]map[string]protocol.CompletionItem, len(sets))
	for i, items := range sets {
		keyed[i] = make(map[string]protocol.CompletionItem, len(items))
		for _, item := range items {
			key := completionDedupeKey(item)
			// A getter and a setter share a name. The getter's item carries the type a
			// read yields, so a setter's item never replaces it.
			if _, taken := keyed[i][key]; taken && item.Detail != nil && *item.Detail == "setter" {
				continue
			}
			keyed[i][key] = item
		}
	}
	return keyed
}

// completionsFromScope collects in-scope bindings for standalone identifier completion.
// It walks the AST ancestor chain from the cursor position, collecting bindings from
// each enclosing scope: function params, match/catch pattern bindings, for-in loop
// variables, if-val patterns, and block-local variable declarations.
func (s *Server) completionsFromScope(script *ast.Script, view scopeView, info *solver.Info, cursor ast.Location, uri string) []protocol.CompletionItem {
	if view == nil {
		return nil
	}
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
					collectPatternBindings(info, param.Pattern, seen, &items)
				}
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.FuncExpr:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(info, param.Pattern, seen, &items)
				}
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.MatchExpr:
			for _, matchCase := range a.Cases {
				if matchCase.Span().Contains(cursor) {
					collectPatternBindings(info, matchCase.Pattern, seen, &items)
					if matchCase.Body.Block != nil {
						collectBlockBindings(info, matchCase.Body.Block.Stmts, cursor, false, seen, &items)
					}
					break
				}
			}
		case *ast.TryCatchExpr:
			if a.Try.Span.Contains(cursor) {
				collectBlockBindings(info, a.Try.Stmts, cursor, false, seen, &items)
			} else {
				for _, catchCase := range a.Catch {
					if catchCase.Span().Contains(cursor) {
						collectPatternBindings(info, catchCase.Pattern, seen, &items)
						if catchCase.Body.Block != nil {
							collectBlockBindings(info, catchCase.Body.Block.Stmts, cursor, false, seen, &items)
						}
						break
					}
				}
			}
		case *ast.IfValExpr:
			if a.Cons.Span.Contains(cursor) {
				collectPatternBindings(info, a.Pattern, seen, &items)
				collectBlockBindings(info, a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(info, a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.IfElseExpr:
			if a.Cons.Span.Contains(cursor) {
				collectBlockBindings(info, a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(info, a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.DoExpr:
			if a.Body.Span.Contains(cursor) {
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.ForInStmt:
			if a.Body.Span.Contains(cursor) {
				collectPatternBindings(info, a.Pattern, seen, &items)
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		}
	}

	// Collect from top-level script statements.
	// Scripts (bin/) don't hoist — only modules (lib/) do.
	collectBlockBindings(info, script.Stmts, cursor, false, seen, &items)

	// Collect type and namespace symbols from the current scope.
	// Values are already collected by collectBlockBindings above.
	collectScopeTypeBindings(view, seen, &items)

	// Walk up the scope chain to collect bindings from intermediate scopes
	// (e.g., the lib namespace scope between the script scope and prelude).
	// Skip the builtins — they're handled separately below with caching.
	for cur := view.parent(); cur != nil && !cur.isBuiltin(); cur = cur.parent() {
		for _, name := range cur.valueNames() {
			if !seen[name] {
				seen[name] = true
				kind, _ := cur.valueKind(name)
				items = append(items, protocol.CompletionItem{
					Label: name,
					Kind:  &kind,
					Data:  completionResolveData{Scope: "script", Name: name, URI: uri},
				})
			}
		}
	}

	// Append cached prelude/global bindings, skipping any that are shadowed.
	for _, item := range s.getPreludeCompletions(firstBuiltin(view)) {
		if !seen[item.Label] {
			seen[item.Label] = true
			items = append(items, item)
		}
	}

	return items
}

// completionsFromModuleScope collects in-scope bindings for a cursor position
// within a module file. It handles file-scoped imports, cross-file declarations,
// and position-dependent filtering for declarations inside a block scope.
func (s *Server) completionsFromModuleScope(
	module *ast.Module,
	sourceID int,
	fileView scopeView,
	moduleView scopeView,
	info *solver.Info,
	cursor ast.Location,
) []protocol.CompletionItem {
	if moduleView == nil {
		return nil
	}
	seen := map[string]bool{}
	var items []protocol.CompletionItem

	// 1. Walk ancestor chain for inner scope bindings (function params, etc.)
	_, ancestors := findNodeWithAncestorsInFile(module, sourceID, cursor)
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch a := ancestors[i].(type) {
		case *ast.FuncDecl:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(info, param.Pattern, seen, &items)
				}
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.FuncExpr:
			if a.Body != nil && a.Body.Span.Contains(cursor) {
				for _, param := range a.Params {
					collectPatternBindings(info, param.Pattern, seen, &items)
				}
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.MatchExpr:
			for _, matchCase := range a.Cases {
				if matchCase.Span().Contains(cursor) {
					collectPatternBindings(info, matchCase.Pattern, seen, &items)
					if matchCase.Body.Block != nil {
						collectBlockBindings(info, matchCase.Body.Block.Stmts, cursor, false, seen, &items)
					}
					break
				}
			}
		case *ast.TryCatchExpr:
			if a.Try.Span.Contains(cursor) {
				collectBlockBindings(info, a.Try.Stmts, cursor, false, seen, &items)
			} else {
				for _, catchCase := range a.Catch {
					if catchCase.Span().Contains(cursor) {
						collectPatternBindings(info, catchCase.Pattern, seen, &items)
						if catchCase.Body.Block != nil {
							collectBlockBindings(info, catchCase.Body.Block.Stmts, cursor, false, seen, &items)
						}
						break
					}
				}
			}
		case *ast.IfValExpr:
			if a.Cons.Span.Contains(cursor) {
				collectPatternBindings(info, a.Pattern, seen, &items)
				collectBlockBindings(info, a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(info, a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.IfElseExpr:
			if a.Cons.Span.Contains(cursor) {
				collectBlockBindings(info, a.Cons.Stmts, cursor, false, seen, &items)
			} else if a.Alt != nil && a.Alt.Block != nil && a.Alt.Block.Span.Contains(cursor) {
				collectBlockBindings(info, a.Alt.Block.Stmts, cursor, false, seen, &items)
			}
		case *ast.DoExpr:
			if a.Body.Span.Contains(cursor) {
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		case *ast.ForInStmt:
			if a.Body.Span.Contains(cursor) {
				collectPatternBindings(info, a.Pattern, seen, &items)
				collectBlockBindings(info, a.Body.Stmts, cursor, false, seen, &items)
			}
		}
	}

	// 2. Collect module-level value declarations with position filtering.
	// All top-level declarations in a module are always visible from other files
	// inside the same module.
	collectModuleDeclBindings(module, sourceID, moduleView, info, seen, &items)

	// 3. Collect types and namespaces from the module scope.
	collectScopeTypeBindings(moduleView, seen, &items)

	// 4. Collect file-scoped import bindings (values, types, namespaces).
	if fileView != nil {
		collectFileImportBindings(module, sourceID, fileView, seen, &items)
	}

	// 5. Append cached prelude/global bindings, skipping any that are shadowed.
	for _, item := range s.getPreludeCompletions(firstBuiltin(moduleView)) {
		if !seen[item.Label] {
			seen[item.Label] = true
			items = append(items, item)
		}
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
	moduleView scopeView,
	info *solver.Info,
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
			if d, ok := moduleView.memberValueDetail(fileNsName, name); ok {
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
			collectPatternBindings(info, d.Pattern, seen, items)
		}
	}
}

// collectFileImportBindings collects only bindings introduced by import
// statements in the current file (values, types, namespaces).
func collectFileImportBindings(
	module *ast.Module,
	sourceID int,
	view scopeView,
	seen map[string]bool,
	items *[]protocol.CompletionItem,
) {
	if module == nil || view == nil {
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

	for _, importStmt := range file.Imports {
		{
			localName := importStmt.LocalName()
			if localName == "" || seen[localName] {
				continue
			}

			if kind, ok := view.valueKind(localName); ok {
				seen[localName] = true
				detail, _ := view.valueDetail(localName)
				*items = append(*items, protocol.CompletionItem{
					Label:  localName,
					Kind:   &kind,
					Detail: &detail,
				})
				continue
			}

			if kind, ok := view.typeKind(localName); ok {
				seen[localName] = true
				detail, _ := view.typeDetail(localName)
				*items = append(*items, protocol.CompletionItem{
					Label:  localName,
					Kind:   &kind,
					Detail: &detail,
				})
				continue
			}

			if _, ok := view.namespace(localName); ok {
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
func collectBlockBindings(info *solver.Info, stmts []ast.Stmt, cursor ast.Location, hoistFuncs bool, seen map[string]bool, items *[]protocol.CompletionItem) {
	if hoistFuncs {
		// Pass 1: imports and hoisted function declarations (always visible)
		for _, stmt := range stmts {
			if importStmt, ok := stmt.(*ast.ImportStmt); ok {
				{
					name := importStmt.LocalName()
					if name != "" && !seen[name] {
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
		if stmt.Span().Start.Offset > cursor.Offset {
			continue
		}
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if varDecl, ok := declStmt.Decl.(*ast.VarDecl); ok {
				collectPatternBindings(info, varDecl.Pattern, seen, items)
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
				{
					name := importStmt.LocalName()
					if name != "" && !seen[name] {
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

func collectPatternBindings(info *solver.Info, pat ast.Pat, seen map[string]bool, items *[]protocol.CompletionItem) {
	switch pat := pat.(type) {
	case *ast.IdentPat:
		if pat.Name != "" && !seen[pat.Name] {
			seen[pat.Name] = true
			kind := protocol.CompletionItemKindVariable
			detail := patternDetail(info, pat)
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
				collectPatternBindings(info, elem.Value, seen, items)
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
				collectPatternBindings(info, elem.Pattern, seen, items)
			}
		}
	case *ast.TuplePat:
		for _, elem := range pat.Elems {
			collectPatternBindings(info, elem, seen, items)
		}
	}
}

// patternDetail returns the type info recorded for the name pat binds, or nil when info
// is nil or recorded none.
func patternDetail(info *solver.Info, pat *ast.IdentPat) *string {
	if info == nil {
		return nil
	}
	t := info.DisplayTypeOf(pat)
	if t == nil {
		return nil
	}
	detail := printSolType(t)
	return &detail
}

// collectScopeTypeBindings collects only type and namespace symbols from a
// single scope level, without recursing into parents. This is used for the
// current scope where values are already collected via AST walking.
func collectScopeTypeBindings(view scopeView, seen map[string]bool, items *[]protocol.CompletionItem) {
	if view == nil {
		return
	}
	for _, name := range view.typeNames() {
		if !seen[name] {
			seen[name] = true
			kind, _ := view.typeKind(name)
			detail, _ := view.typeDetail(name)
			*items = append(*items, protocol.CompletionItem{
				Label:  name,
				Kind:   &kind,
				Detail: &detail,
			})
		}
	}
	for _, name := range view.namespaceNames() {
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

// typeCompletionsFromScope collects only type aliases and namespaces from the
// entire scope chain (current scope + parents). Used when the cursor is in a
// type annotation position. Detail is deferred to completionItem/resolve.
func typeCompletionsFromScope(view scopeView, scopeID string, uri string) []protocol.CompletionItem {
	seen := map[string]bool{}
	var items []protocol.CompletionItem
	for cur := view; cur != nil; cur = cur.parent() {
		for _, name := range cur.typeNames() {
			if !seen[name] {
				seen[name] = true
				kind, _ := cur.typeKind(name)
				items = append(items, protocol.CompletionItem{
					Label: name,
					Kind:  &kind,
					Data:  completionResolveData{Scope: scopeID, Name: name, URI: uri},
				})
			}
		}
		for _, name := range cur.namespaceNames() {
			if !seen[name] {
				seen[name] = true
				kind := protocol.CompletionItemKindModule
				items = append(items, protocol.CompletionItem{
					Label: name,
					Kind:  &kind,
					Data:  completionResolveData{Scope: scopeID, Name: name, URI: uri},
				})
			}
		}
	}
	return items
}

// filterTypeItems returns only completion items whose Kind is a type-like or
// namespace kind (Class, Interface, Enum, Struct, TypeParameter, Module).
// Used to strip value-only items from a mixed list.
func filterTypeItems(items []protocol.CompletionItem) []protocol.CompletionItem {
	var out []protocol.CompletionItem
	for _, item := range items {
		if item.Kind == nil {
			continue
		}
		switch *item.Kind {
		case protocol.CompletionItemKindClass,
			protocol.CompletionItemKindInterface,
			protocol.CompletionItemKindEnum,
			protocol.CompletionItemKindStruct,
			protocol.CompletionItemKindTypeParameter,
			protocol.CompletionItemKindModule:
			out = append(out, item)
		}
	}
	return out
}

// wordAtCursor extracts the partial identifier at the cursor position from
// the document text. Returns "" if the cursor is not on an identifier.
//
// loc is a byte offset into text, so the walk back from the cursor decodes one
// rune at a time. It uses the lexer's own identifier rule, so a name the lexer
// accepts is a name the cursor can sit at the end of, `café` included.
func wordAtCursor(text string, loc ast.Location) string {
	at := loc.Offset
	if at < 0 || at > len(text) {
		return ""
	}

	// Walk backwards from cursor to find the start of the word.
	start := at
	for start > 0 {
		r, width := utf8.DecodeLastRuneInString(text[:start])
		if !lexer_util.IsIdentContinue(r) {
			break
		}
		start -= width
	}
	if start == at {
		return ""
	}
	return text[start:at]
}

// Helper functions

// isOptionalCompletion returns true if the completion item represents an
// optional property (label ends with "?" while FilterText is the raw name).
func isOptionalCompletion(item protocol.CompletionItem) bool {
	return item.FilterText != nil && strings.HasSuffix(item.Label, "?")
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
