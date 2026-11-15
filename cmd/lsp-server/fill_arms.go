package main

import (
	"maps"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/escalier-lang/escalier/internal/solver/ucs"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// fillMatchArmsTitle and fillCatchArmsTitle are the titles of the code action that adds the
// arms a `match` or a `catch` is missing.
const (
	fillMatchArmsTitle = "Add missing match arms"
	fillCatchArmsTitle = "Add missing catch arms"
)

// fillArmsRefactorKind is the kind the action is offered under when it is requested at the
// cursor rather than for a diagnostic.
const fillArmsRefactorKind = protocol.CodeActionKindRefactorRewrite

// todoArmBody is the body of each arm the action adds. A `throw` has type `never`, so it
// fits whatever type the other arms produce. The prelude binds no `todo()` helper and no
// `Error` class, so a thrown string is the shortest body that resolves in every file.
const todoArmBody = `throw "todo"`

// fillArmsActions returns the actions that add missing arms in the document uri. It returns
// a quick fix for each diagnostic in diags that reports a non-exhaustive `match`, or a value
// a `catch` rethrows that its enclosing `throws` clause does not admit. When only asks for
// refactors, it also returns a refactor for the innermost `match` or `try` containing rng's
// start that leaves a value no arm names, unless a quick fix already covers it. It returns
// none while the document has changed since the last check, since the edits are offsets into
// the text that check read.
func (s *Server) fillArmsActions(
	uri protocol.DocumentUri,
	rng protocol.Range,
	diags []protocol.Diagnostic,
	only []protocol.CodeActionKind,
) []protocol.CodeAction {
	// Return early when the request asks for neither kind, before taking any lock.
	wantsFix := wantsCodeActionKind(only, protocol.CodeActionKindQuickFix) && len(diags) > 0
	wantsRefactor := wantsCodeActionKind(only, fillArmsRefactorKind)
	if !wantsFix && !wantsRefactor {
		return nil
	}

	// Collect the file's `match` and `try` targets from the last check, keyed by their node
	// so a diagnostic can find its target.
	file, fileErrs, ok := s.checkedFileAt(uri)
	if !ok {
		return nil
	}
	s.solverMu.RLock()
	defer s.solverMu.RUnlock()

	targets := armTargets(file, fileErrs)
	if len(targets) == 0 {
		return nil
	}
	byNode := map[ast.Node]armTarget{}
	for _, target := range targets {
		byNode[target.node] = target
	}

	lineMap := file.src.LineMap()
	var actions []protocol.CodeAction
	fixed := set.NewSet[ast.Node]()

	// Offer a quick fix for each request diagnostic that reports a target. A diagnostic is
	// matched to its error by range and message, and the error names its target's node.
	if wantsFix {
		for _, diag := range diags {
			for _, err := range fileErrs {
				if spanToRange(lineMap, err.Span()) != diag.Range || err.Message() != diag.Message {
					continue
				}
				target, ok := byNode[diagnosedNode(err)]
				if !ok {
					continue
				}
				action, ok := fillArmsAction(file, uri, target)
				if !ok {
					continue
				}
				action.Kind = addr(protocol.CodeActionKindQuickFix)
				action.Diagnostics = []protocol.Diagnostic{diag}
				action.IsPreferred = addr(true)
				actions = append(actions, action)
				fixed.Add(target.node)
			}
		}
	}
	// Offer a refactor for the innermost target around the cursor, unless a quick fix above
	// already covers it.
	if wantsRefactor {
		loc := posToLoc(lineMap, rng.Start)
		var innermost *armTarget
		for i, target := range targets {
			if !target.span.Contains(loc) {
				continue
			}
			if innermost == nil || innermost.span.ContainsSpan(target.span) {
				innermost = &targets[i]
			}
		}
		if innermost != nil && !fixed.Contains(innermost.node) {
			if action, ok := fillArmsAction(file, uri, *innermost); ok {
				action.Kind = addr(fillArmsRefactorKind)
				actions = append(actions, action)
			}
		}
	}
	return actions
}

// armTarget is a `match` or a `try` that leaves values no arm names.
type armTarget struct {
	// node is the *ast.MatchExpr or *ast.TryCatchExpr.
	node ast.Node
	// span runs from node's start to just past the `}` that closes its arms.
	span ast.Span
	// closing is the offset of the `}` that closes node's arms.
	closing int
	cases   []*ast.MatchCase
	// unnamed holds the values no arm names, one arm each.
	unnamed []soltype.Type
	// catchAll says a catch-all arm is needed as well.
	catchAll bool
	title    string
}

// armTargets returns the targets in file: each `match` a NonExhaustiveMatchError in errs
// reports, and each `try` whose block throws a value no catch arm names.
func armTargets(file checkedFile, errs []compiler.Diagnostic) []armTarget {
	var targets []armTarget
	// Each non-exhaustive `match` is a target, with the values its error reports.
	for _, err := range errs {
		ne, ok := err.(*solver.NonExhaustiveMatchError)
		if !ok {
			continue
		}
		match, ok := ne.Origin.Node.(*ast.MatchExpr)
		if !ok {
			continue
		}
		// A `match` span ends just past the `}` that closes its arms.
		targets = append(targets, armTarget{
			node:     match,
			span:     match.Span(),
			closing:  match.Span().End.Offset - 1,
			cases:    match.Cases,
			unnamed:  ne.Unmatched,
			catchAll: ne.OpenTail != nil,
			title:    fillMatchArmsTitle,
		})
	}
	// Each `try` the check recorded uncaught throws for is a target, with those throws.
	for _, try := range file.info.TriesWithUncaughtThrows(file.id) {
		if len(try.Catch) == 0 {
			continue
		}
		// A `try` span ends with its last arm, before any trailing comma and the `}`.
		closing := closingBraceAfter(file.src.Contents, armEndOffset(file.src.Contents, try.Catch[len(try.Catch)-1]))
		if closing < 0 {
			continue
		}
		targets = append(targets, armTarget{
			node:    try,
			span:    ast.Span{Start: try.Span().Start, End: ast.Location{Offset: closing + 1}},
			closing: closing,
			cases:   try.Catch,
			unnamed: file.info.UncaughtThrows(try),
			title:   fillCatchArmsTitle,
		})
	}
	return targets
}

// diagnosedNode returns the `match` or `try` whose missing arms err reports, and nil when err
// reports none.
func diagnosedNode(err compiler.Diagnostic) ast.Node {
	switch err := err.(type) {
	case *solver.NonExhaustiveMatchError:
		if match, ok := err.Origin.Node.(*ast.MatchExpr); ok {
			return match
		}
	case *solver.UnhandledRethrowError:
		return err.Try
	}
	return nil
}

// checkedFileAt returns the checked file the document uri names, with the type errors the
// last check reported in it. It returns false when there is no check output for the file or
// the document has changed since the last check.
func (s *Server) checkedFileAt(uri protocol.DocumentUri) (checkedFile, []compiler.Diagnostic, bool) {
	sourceID := s.sourceIDForURI(uri)
	var file checkedFile
	var errs []compiler.Diagnostic
	// validateBinScript replaces a script's entries in the check output under mu, so the
	// entries for this file are read together under it.
	s.mu.RLock()
	if co := s.checkOutput; co != nil && !s.isCacheStale(uri) {
		file = checkedFileOf(co, sourceID)
		for _, err := range co.TypeErrors {
			if err.Span().SourceID == sourceID {
				errs = append(errs, err)
			}
		}
	}
	s.mu.RUnlock()
	if file.src == nil || file.info == nil {
		return checkedFile{}, nil, false
	}
	return file, errs, true
}

// checkedFileOf returns the file sourceID names in co, with the records the check made
// for it.
func checkedFileOf(co *compiler.CheckOutput, sourceID int) checkedFile {
	file := checkedFile{
		id:     sourceID,
		src:    co.SourceByID(sourceID),
		info:   co.InfoFor(sourceID),
		script: co.Scripts[sourceID],
		module: co.Module,
	}
	if file.script != nil {
		file.scope = co.ScriptScopes[sourceID]
		return file
	}
	file.scope = co.FileScopes[sourceID]
	if co.Module != nil {
		for _, f := range co.Module.Files {
			if f.SourceID == sourceID {
				file.namespace = f.Namespace
			}
		}
	}
	return file
}

// fillArmsAction returns the code action that adds an arm to target for each value no arm
// names, and a catch-all arm when target needs one. The returned action carries no kind. It
// returns false when file does not hold target's braces where target places them.
func fillArmsAction(file checkedFile, uri protocol.DocumentUri, target armTarget) (protocol.CodeAction, bool) {
	// Write a pattern for each value no arm names, then `_` when a catch-all is needed.
	patterns := make([]string, 0, len(target.unnamed)+1)
	for _, t := range target.unnamed {
		// A `boolean` that no arm names holds two values, and a literal arm for each reads
		// better than an annotated binding.
		if prim, ok := t.(*soltype.PrimType); ok && prim.Prim == soltype.BoolPrim {
			patterns = append(patterns, "true", "false")
			continue
		}
		patterns = append(patterns, witnessPattern(file, t))
	}
	if target.catchAll {
		patterns = append(patterns, "_")
	}
	if len(patterns) == 0 {
		return protocol.CodeAction{}, false
	}
	// Give each pattern the placeholder body and work out where the arms go.
	arms := make([]string, len(patterns))
	for i, p := range patterns {
		arms[i] = p + " => " + todoArmBody
	}
	edits, ok := armInsertion(file.src.Contents, target.span.Start.Offset, target.closing, target.cases, arms)
	if !ok {
		return protocol.CodeAction{}, false
	}
	// Convert the byte-offset edits to LSP ranges.
	lineMap := file.src.LineMap()
	textEdits := make([]protocol.TextEdit, len(edits))
	for i, e := range edits {
		span := ast.Span{Start: ast.Location{Offset: e.start}, End: ast.Location{Offset: e.end}}
		textEdits[i] = protocol.TextEdit{Range: spanToRange(lineMap, span), NewText: e.text}
	}
	return protocol.CodeAction{
		Title: target.title,
		Edit: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{uri: textEdits},
		},
	}, true
}

// textEdit replaces the bytes from start to end of a file with text.
type textEdit struct {
	start, end int
	text       string
}

// armInsertion returns the edits that add arms after the last of cases, inside the braces
// of a construct in contents. start is the construct's first byte and closing is the offset
// of the `}` that closes its arms. The new arms copy the layout of the existing ones:
//
//   - Arms written one per line get one new line each, at the last arm's indentation.
//   - Arms written on one line get the new arms on that line.
//   - A trailing comma after the last arm is kept, and each new arm gets one. Without it, a
//     comma separates each new arm from the one before and the last new arm gets none.
//
// With no cases, the arms go one per line, indented one level past the line the construct
// starts on, each with a trailing comma. It returns false when contents does not hold the
// braces where start, closing and cases place them.
func armInsertion(contents string, start, closing int, cases []*ast.MatchCase, arms []string) ([]textEdit, bool) {
	if closing < 0 || closing >= len(contents) || contents[closing] != '}' {
		return nil, false
	}

	// With no arms, replace whatever lies between the braces with one arm per line.
	if len(cases) == 0 {
		opening := skipSpaceBefore(contents, closing) - 1
		if opening < 0 || contents[opening] != '{' {
			return nil, false
		}
		indent := lineIndent(contents, start)
		inner := indent + indentUnit(contents)
		var b strings.Builder
		for _, arm := range arms {
			b.WriteString("\n" + inner + arm + ",")
		}
		b.WriteString("\n" + indent)
		return []textEdit{{start: opening + 1, end: closing, text: b.String()}}, true
	}

	// Find where the last arm ends and whether a trailing comma follows it.
	last := cases[len(cases)-1]
	armEnd := armEndOffset(contents, last)
	if armEnd > closing {
		return nil, false
	}
	after := armEnd
	for after < closing && (contents[after] == ' ' || contents[after] == '\t') {
		after++
	}
	trailingComma := contents[after] == ','
	if trailingComma {
		after++
	} else {
		after = armEnd
	}

	// With the last arm on the closing brace's line, add the new arms on that line.
	if !strings.Contains(contents[after:closing], "\n") {
		if trailingComma {
			return []textEdit{{start: after, end: after, text: " " + strings.Join(arms, ", ") + ","}}, true
		}
		return []textEdit{{start: after, end: after, text: ", " + strings.Join(arms, ", ")}}, true
	}

	// Otherwise add one arm per line. The arms go at the end of the line the last arm ends
	// on, after any comment there, so a line comment stays with the arm it follows.
	lineEnd := after + strings.IndexByte(contents[after:closing], '\n')
	indent := lineIndent(contents, last.Pattern.Span().Start.Offset)
	var b strings.Builder
	for i, arm := range arms {
		b.WriteString("\n" + indent + arm)
		if trailingComma || i < len(arms)-1 {
			b.WriteString(",")
		}
	}
	if trailingComma {
		return []textEdit{{start: lineEnd, end: lineEnd, text: b.String()}}, true
	}
	// The comma goes right after the last arm, ahead of any comment. With nothing but
	// whitespace between the arm and the line's end, one edit writes both, since two
	// insertions at one offset leave their order to the client.
	if strings.TrimSpace(contents[armEnd:lineEnd]) == "" {
		return []textEdit{{start: armEnd, end: lineEnd, text: "," + b.String()}}, true
	}
	return []textEdit{
		{start: armEnd, end: armEnd, text: ","},
		{start: lineEnd, end: lineEnd, text: b.String()},
	}, true
}

// armEndOffset returns the offset just past the end of arm's body in contents, or of its
// pattern when it has no body.
func armEndOffset(contents string, arm *ast.MatchCase) int {
	body, ok := ucs.BodySpan(arm.Body)
	if !ok {
		return arm.Pattern.Span().End.Offset
	}
	// The span of a parenthesized body leaves out its parentheses, so `(x + 1)` would end
	// before the `)`.
	return parenthesizedEnd(contents, body)
}

// closingBraceAfter returns the offset of the first `}` at or after offset in contents,
// skipping whitespace, commas and comments, and -1 when anything else comes first.
func closingBraceAfter(contents string, offset int) int {
	for i := offset; i < len(contents); i++ {
		switch {
		case contents[i] == '}':
			return i
		case strings.IndexByte(" \t\r\n,", contents[i]) >= 0:
		case strings.HasPrefix(contents[i:], "//"):
			n := strings.IndexByte(contents[i:], '\n')
			if n < 0 {
				return -1
			}
			i += n
		case strings.HasPrefix(contents[i:], "/*"):
			n := strings.Index(contents[i+2:], "*/")
			if n < 0 {
				return -1
			}
			i += n + 3
		default:
			return -1
		}
	}
	return -1
}

// parenthesizedEnd returns the offset just past the parentheses that wrap span in contents,
// or span's end when none do. Only whitespace may separate span from them.
func parenthesizedEnd(contents string, span ast.Span) int {
	start, end := span.Start.Offset, span.End.Offset
	for {
		before := skipSpaceBefore(contents, start)
		after := skipSpaceAfter(contents, end)
		if before == 0 || after == len(contents) || contents[before-1] != '(' || contents[after] != ')' {
			return end
		}
		start, end = before-1, after+1
	}
}

// lineIndent returns the run of spaces and tabs that starts the line holding offset.
func lineIndent(contents string, offset int) string {
	start := strings.LastIndexByte(contents[:offset], '\n') + 1
	end := start
	for end < len(contents) && (contents[end] == ' ' || contents[end] == '\t') {
		end++
	}
	return contents[start:end]
}

// indentUnit returns the text contents indents one level with: a tab when some line starts
// with one, and otherwise four spaces.
func indentUnit(contents string) string {
	if strings.HasPrefix(contents, "\t") || strings.Contains(contents, "\n\t") {
		return "\t"
	}
	return "    "
}

// witnessPattern returns a pattern that matches every value of t, the type of a value no arm
// names, written as the user would write it in file.
//
//   - An enum variant is an extractor pattern binding each field under the name the variant
//     declares it with, or the bare variant when it has no fields.
//   - A class instance is an instance pattern with no fields.
//   - A literal, `null` and `undefined` are written as themselves.
//   - An object binds each of its keys, and a tuple matches each element with `_`.
//   - Any other type is an identifier annotated with that type.
func witnessPattern(file checkedFile, t soltype.Type) string {
	switch t := t.(type) {
	case *soltype.ClassType:
		name, variant := classSpelling(file, t)
		if !t.Variant {
			return name + "{}"
		}
		if variant == nil || len(variant.Params) == 0 {
			return name
		}
		fields := make([]string, len(variant.Params))
		for i, p := range variant.Params {
			fields[i] = "_"
			if ident, ok := p.Pattern.(*ast.IdentPat); ok {
				fields[i] = ident.Name
			}
		}
		return name + "(" + strings.Join(fields, ", ") + ")"
	case *soltype.LitType, *soltype.NullType, *soltype.UndefinedType:
		return soltype.Print(t)
	case *soltype.ObjectType:
		if pattern, ok := objectPattern(t); ok {
			return pattern
		}
	case *soltype.TupleType:
		if pattern, ok := tuplePattern(t); ok {
			return pattern
		}
	}
	return "value: " + soltype.Print(t)
}

// objectPattern returns an object pattern binding each property of t under its own name.
// It returns false when t holds a member other than a property, or a key that is not an
// identifier, which a shorthand field cannot bind.
func objectPattern(t *soltype.ObjectType) (string, bool) {
	fields := make([]string, 0, len(t.Elems))
	for _, elem := range t.Elems {
		prop, ok := elem.(*soltype.PropertyElem)
		if !ok {
			return "", false
		}
		if !isIdentifier(prop.Name) {
			return "", false
		}
		fields = append(fields, prop.Name)
	}
	return "{" + strings.Join(fields, ", ") + "}", true
}

// tuplePattern returns a tuple pattern matching each element of t with `_`, and a trailing
// rest for an inexact tuple. It returns false when t holds a spread element, whose length
// the pattern cannot state.
func tuplePattern(t *soltype.TupleType) (string, bool) {
	elems := make([]string, 0, len(t.Elems)+1)
	for _, elem := range t.Elems {
		if _, spread := elem.(*soltype.RestSpreadType); spread {
			return "", false
		}
		elems = append(elems, "_")
	}
	if t.Inexact {
		elems = append(elems, "..._")
	}
	return "[" + strings.Join(elems, ", ") + "]", true
}

// classSpelling returns the shortest name that refers to the class ct from file's top level,
// and the declaration of ct when ct is an enum variant. A variant's declaration is nil when
// neither the scope nor file holds it.
//
// It looks for ct in two places:
//
//   - The type keys the scope chain binds. A class in a lib/ namespace is keyed by its
//     dotted name, as `shapes.Box` is, and a file in that namespace writes it without the
//     namespace prefix.
//   - The namespaces the scope chain binds, searched through their nested namespaces. An
//     enum binds a namespace holding its variants, and an import binds the package's
//     namespace, as `import "std:error"` binds `error`.
//
// When neither holds ct, it falls back to the name the checker prints for ct.
func classSpelling(file checkedFile, ct *soltype.ClassType) (string, *ast.EnumVariant) {
	best := ""
	var variant *ast.EnumVariant
	consider := func(spelling string, v *ast.EnumVariant) {
		dots, bestDots := strings.Count(spelling, "."), strings.Count(best, ".")
		if best == "" || dots < bestDots || dots == bestDots && spelling < best {
			best, variant = spelling, v
		}
	}
	// A builtin scope binds classes such as `Error` under the bare names the fallback below
	// prints, so the search stops short of them.
	for cur := file.scope; cur != nil && !cur.IsBuiltin(); cur = cur.Parent() {
		for _, key := range cur.TypeNames() {
			// A nearer scope's binding hides this level's, so the key is read from the top.
			if b, ok := file.scope.GetType(key); ok && namesClass(b.Type, ct) {
				if spelling, ok := keySpelling(file, key); ok {
					consider(spelling, nil)
				}
			}
		}
		for _, name := range cur.NamespaceNames() {
			if ns, ok := file.scope.GetNamespace(name); ok && namespaceReadsAs(file, ns, name) {
				findInNamespace(ns, name, ct, namespaceSearchDepth, consider)
			}
		}
	}
	if best == "" {
		best = soltype.Print(&soltype.ClassType{Name: ct.Name, Final: ct.Final, Variant: ct.Variant})
	}
	return best, variant
}

// namespaceSearchDepth bounds how many levels of nested namespaces classSpelling searches
// below a namespace the scope binds.
const namespaceSearchDepth = 3

// findInNamespace calls consider with each name under which ns, or a namespace nested in it
// at most depth levels down, binds the class ct as a type. spelling is the name ns is
// reached by. For a binding that is an enum variant, consider also receives the variant's
// declaration, or nil when the binding names none.
func findInNamespace(ns *solver.Namespace, spelling string, ct *soltype.ClassType, depth int, consider func(string, *ast.EnumVariant)) {
	for _, name := range slices.Sorted(maps.Keys(ns.Types)) {
		if !namesClass(ns.Types[name].Type, ct) {
			continue
		}
		var variant *ast.EnumVariant
		if b, ok := ns.Values[name]; ok && len(b.Sources) > 0 {
			if prov, ok := b.Sources[0].(*ast.NodeProvenance); ok {
				variant, _ = prov.Node.(*ast.EnumVariant)
			}
		}
		consider(spelling+"."+name, variant)
	}
	if depth == 0 {
		return
	}
	for _, name := range slices.Sorted(maps.Keys(ns.Nested)) {
		findInNamespace(ns.Nested[name], spelling+"."+name, ct, depth-1, consider)
	}
}

// namespaceReadsAs reports whether file writes the namespace ns under name. An import
// binds a package's namespace under the name the file reads it by, as `import "std:error"`
// binds `error`. A namespace of this package reads as name only from the namespace that
// declares it. The module scope also binds an enum declared in `geo` under the bare name
// `Shape`, which a file outside `geo` writes as `geo.Shape`.
func namespaceReadsAs(file checkedFile, ns *solver.Namespace, name string) bool {
	if strings.Contains(ns.Name, ":") {
		return true
	}
	return ns.Name == name || file.namespace != "" && ns.Name == file.namespace+"."+name
}

// namesClass reports whether t is an instance of the class ct, at any type arguments.
func namesClass(t soltype.Type, ct *soltype.ClassType) bool {
	other, ok := t.(*soltype.ClassType)
	return ok && other.Name == ct.Name
}

// keySpelling returns the name that reads the type key from file. A file resolves a type
// name against its own namespace first, so it writes a key in that namespace without the
// namespace prefix, unless the bare name is itself a key. It returns false when the name
// would read a different key, as a key outside the namespace does when the namespace holds
// a key of the same name.
func keySpelling(file checkedFile, key string) (string, bool) {
	if file.namespace == "" {
		return key, true
	}
	short, inNamespace := strings.CutPrefix(key, file.namespace+".")
	if !inNamespace {
		_, shadowed := file.scope.GetType(file.namespace + "." + key)
		return key, !shadowed
	}
	if _, shadowed := file.scope.GetType(short); shadowed {
		return key, true
	}
	return short, true
}
