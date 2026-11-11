package main

import (
	"regexp"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/solver"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// signatureActionKind is the kind of the code action that writes a function's inferred
// signature.
const signatureActionKind = protocol.CodeActionKindRefactorRewrite

// signatureTarget is a function or method whose signature the code action can write.
type signatureTarget struct {
	// name is the declared name, which a binder list is written after.
	name ast.Node
	sig  *ast.FuncSig
	// receiver is a method's receiver, or nil.
	receiver *ast.MethodReceiver
	// signature is what the checker inferred for the function.
	signature solver.DeclSignature
}

// signatureActionAt returns the code action that writes the inferred signature of the
// function or method whose signature contains pos in the document uri, and false when the
// last check offers none there. It offers none while the document has changed since the last
// check, since the edits are offsets into the text that check read.
func (s *Server) signatureActionAt(uri protocol.DocumentUri, pos protocol.Position) (protocol.CodeAction, bool) {
	sourceID := s.sourceIDForURI(uri)
	// validateBinScript replaces a script's entries in the check output under mu, so the
	// entries for this file are read together under it.
	var file checkedFile
	s.mu.RLock()
	if co := s.checkOutput; co != nil && !s.isCacheStale(uri) {
		file = checkedFile{
			id:     sourceID,
			src:    co.SourceByID(sourceID),
			info:   co.InfoFor(sourceID),
			script: co.Scripts[sourceID],
			module: co.Module,
		}
	}
	s.mu.RUnlock()
	if file.src == nil || file.info == nil {
		return protocol.CodeAction{}, false
	}
	loc := posToLoc(file.src.LineMap(), pos)
	s.solverMu.RLock()
	defer s.solverMu.RUnlock()
	return signatureAction(file, uri, loc)
}

// checkedFile is one file of a check output with the records the check made for it.
type checkedFile struct {
	id   int
	src  *ast.Source
	info *solver.Info
	// script is the file's AST when it is a bin/ script, and nil when it is a lib/ file.
	script *ast.Script
	// module is the lib/ module, which holds a lib/ file's declarations.
	module *ast.Module
}

// signatureAction returns the code action that writes the inferred signature of the
// function or method whose signature contains loc, and false when there is no such
// function or its signature is already written in full. loc is in file, and uri is
// file's document.
func signatureAction(file checkedFile, uri protocol.DocumentUri, loc ast.Location) (protocol.CodeAction, bool) {
	target, ok := findSignatureTarget(file, loc)
	if !ok {
		return protocol.CodeAction{}, false
	}
	edits, ok := signatureEdits(target, file.src)
	if !ok || len(edits) == 0 {
		return protocol.CodeAction{}, false
	}
	return protocol.CodeAction{
		Title: "Write the inferred signature",
		Kind:  addr(signatureActionKind),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{uri: edits},
		},
	}, true
}

// findSignatureTarget returns the function declared at the top level of file, or the
// method of a class declared there, whose signature contains loc.
func findSignatureTarget(file checkedFile, loc ast.Location) (signatureTarget, bool) {
	sourceID, info := file.id, file.info
	var decls []ast.Decl
	if file.script != nil {
		for _, stmt := range file.script.Stmts {
			if ds, ok := stmt.(*ast.DeclStmt); ok {
				decls = append(decls, ds.Decl)
			}
		}
	} else if file.module != nil {
		for _, ns := range file.module.Namespaces.Values() {
			for _, d := range ns.Decls {
				if d.Span().SourceID == sourceID {
					decls = append(decls, d)
				}
			}
		}
	}
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			span := ast.Span{Start: d.Span().Start, End: d.Body.Span.Start, SourceID: sourceID}
			if !span.Contains(loc) {
				continue
			}
			signature, ok := info.FuncDeclSignature(d)
			return signatureTarget{name: d.Name, sig: &d.FuncSig, signature: signature}, ok
		case *ast.ClassDecl:
			for _, elem := range d.Body {
				m, ok := elem.(*ast.MethodElem)
				if !ok || m.Fn.Body == nil {
					continue
				}
				span := ast.Span{Start: m.Span().Start, End: m.Fn.Body.Span.Start, SourceID: sourceID}
				if !span.Contains(loc) {
					continue
				}
				signature, ok := info.MethodSignature(d, m)
				return signatureTarget{
					name: m.Name, sig: &m.Fn.FuncSig, receiver: m.Receiver, signature: signature,
				}, ok
			}
		}
	}
	return signatureTarget{}, false
}

// signatureEdits returns the edits that write target's inferred signature into src. A
// part the source leaves out is written. A part the source writes is kept as written,
// unless it needs a lifetime the edits add to the binder list, in which case it is
// rewritten. ok is false when the inferred signature cannot be written into the source:
// for a function type that carries its own lifetime, for a parameter list whose length
// differs from the inferred one, and for a binder list that needs rewriting while the
// source writes a `where` clause or declares a lifetime that elision assigns.
func signatureEdits(target signatureTarget, src *ast.Source) ([]protocol.TextEdit, bool) {
	ft := target.signature.Type
	sig := target.sig
	if ft.Lt != nil || len(ft.Params) != len(sig.Params) {
		return nil, false
	}
	rendered := target.signature.PrintWritten()
	lineMap := src.LineMap()
	contents := src.Contents
	var edits []protocol.TextEdit
	replace := func(start, end int, text string) {
		span := ast.Span{Start: ast.Location{Offset: start}, End: ast.Location{Offset: end}}
		edits = append(edits, protocol.TextEdit{Range: spanToRange(lineMap, span), NewText: text})
	}
	insert := func(offset int, text string) { replace(offset, offset, text) }
	replaceSpan := func(span ast.Span, text string) { replace(span.Start.Offset, span.End.Offset, text) }

	// The binder list is rewritten when the inferred signature binds a lifetime the source
	// does not declare, or more parameters than the source writes, and every part that names
	// one of the added lifetimes is rewritten with it.
	declared := set.NewSet[string]()
	for _, lp := range sig.LifetimeParams {
		declared.Add("'" + lp.Name)
	}
	renderedNames := set.NewSet[string]()
	var added []*regexp.Regexp
	for _, b := range rendered.Binders {
		name, _, _ := strings.Cut(b, ":")
		renderedNames.Add(name)
		if strings.HasPrefix(name, "'") && !declared.Contains(name) {
			added = append(added, regexp.MustCompile(regexp.QuoteMeta(name)+`\b`))
		}
	}
	if len(added) > 0 || len(rendered.Binders) > len(sig.TypeParams)+len(sig.LifetimeParams) {
		// A rewritten list would repeat a `where` clause's bounds, and would drop a lifetime
		// the source declares that elision assigns, which the parts kept as written still
		// name.
		if writesWhereClause(sig) {
			return nil, false
		}
		for _, name := range declared.ToSlice() {
			if !renderedNames.Contains(name) {
				return nil, false
			}
		}
		binders := strings.Join(rendered.Binders, ", ")
		if span, ok := binderSpan(sig); ok {
			replaceSpan(span, binders)
		} else {
			insert(target.name.Span().End.Offset, "<"+binders+">")
		}
	}
	namesAdded := func(text string) bool {
		for _, re := range added {
			if re.MatchString(text) {
				return true
			}
		}
		return false
	}

	if target.receiver != nil && namesAdded(rendered.Receiver) {
		replaceSpan(target.receiver.Span_, rendered.Receiver)
	}
	for i, p := range sig.Params {
		switch {
		case p.TypeAnn == nil:
			end := p.Pattern.Span().End.Offset
			if p.Optional {
				var ok bool
				if end, ok = skipPast(contents, end, '?', ""); !ok {
					return nil, false
				}
			}
			insert(end, ": "+rendered.Params[i])
		case namesAdded(rendered.Params[i]):
			replaceSpan(p.TypeAnn.Span(), rendered.Params[i])
		}
	}
	throws := ""
	if sig.Throws == nil && rendered.Throws != "" {
		throws = " throws " + rendered.Throws
	}
	switch {
	case sig.Return == nil:
		end, ok := paramListEnd(target, contents)
		if !ok {
			return nil, false
		}
		insert(end, " -> "+rendered.Ret+throws)
	case namesAdded(rendered.Ret):
		replaceSpan(sig.Return.Span(), rendered.Ret+throws)
	case throws != "":
		insert(sig.Return.Span().End.Offset, throws)
	}
	if sig.Throws != nil && namesAdded(rendered.Throws) {
		replaceSpan(sig.Throws.Span(), rendered.Throws)
	}
	return edits, true
}

// writesWhereClause reports whether sig writes a `where` clause.
func writesWhereClause(sig *ast.FuncSig) bool {
	for _, tp := range sig.TypeParams {
		if tp.LowerBound != nil || tp.UpperBoundInWhere {
			return true
		}
	}
	return false
}

// binderSpan returns the span from the first binder sig writes between `<` and `>` to the
// end of the last one, and false when sig writes none.
func binderSpan(sig *ast.FuncSig) (ast.Span, bool) {
	var spans []ast.Span
	for _, lp := range sig.LifetimeParams {
		spans = append(spans, lp.Span())
	}
	for _, tp := range sig.TypeParams {
		spans = append(spans, tp.Span())
	}
	if len(spans) == 0 {
		return ast.Span{}, false
	}
	span := spans[0]
	for _, s := range spans[1:] {
		if s.Start.Offset < span.Start.Offset {
			span.Start = s.Start
		}
		if s.End.Offset > span.End.Offset {
			span.End = s.End
		}
	}
	return span, true
}

// paramListEnd returns the offset just past the `)` that closes target's parameter list
// in contents, and false when the text there is not one. The scan starts after the last
// parameter, after the receiver, or inside the `(` that follows the name and its binder
// list.
func paramListEnd(target signatureTarget, contents string) (int, bool) {
	sig := target.sig
	var from int
	switch {
	case len(sig.Params) > 0:
		last := sig.Params[len(sig.Params)-1]
		from = last.Pattern.Span().End.Offset
		if last.TypeAnn != nil {
			from = last.TypeAnn.Span().End.Offset
		}
	case target.receiver != nil:
		from = target.receiver.Span_.End.Offset
	default:
		from = target.name.Span().End.Offset
		if span, ok := binderSpan(sig); ok {
			var closed bool
			// A binder list may end in a comma.
			if from, closed = skipPast(contents, span.End.Offset, '>', ","); !closed {
				return 0, false
			}
		}
		var opened bool
		if from, opened = skipPast(contents, from, '(', ""); !opened {
			return 0, false
		}
	}
	// A parameter list may end in a comma, and an inexact one in `...`. An optional
	// parameter written with no type ends in `?`.
	return skipPast(contents, from, ')', "?,.")
}

// skipPast returns the offset just past the byte want, reading contents from offset from
// and skipping whitespace, comments, and any byte in marks before it. It returns false when
// another byte comes first.
func skipPast(contents string, from int, want byte, marks string) (int, bool) {
	for i := from; i < len(contents); {
		switch {
		case contents[i] == want:
			return i + 1, true
		case strings.IndexByte(" \t\r\n", contents[i]) >= 0, strings.IndexByte(marks, contents[i]) >= 0:
			i++
		case strings.HasPrefix(contents[i:], "//"):
			n := strings.IndexByte(contents[i:], '\n')
			if n < 0 {
				return 0, false
			}
			i += n
		case strings.HasPrefix(contents[i:], "/*"):
			n := strings.Index(contents[i+2:], "*/")
			if n < 0 {
				return 0, false
			}
			i += n + 4
		default:
			return 0, false
		}
	}
	return 0, false
}
