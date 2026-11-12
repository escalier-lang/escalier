package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/sigedit"
	"github.com/escalier-lang/escalier/internal/solver"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// signatureActionKind is the kind of the code action that writes a function's inferred
// signature.
const signatureActionKind = protocol.CodeActionKindRefactorRewrite

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
				if d.Span().SourceID == file.id {
					decls = append(decls, d)
				}
			}
		}
	}
	for _, target := range sigedit.Targets(file.info, decls, file.id) {
		if !target.SignatureSpan.Contains(loc) {
			continue
		}
		edits, ok := target.Edits(file.src.Contents)
		if !ok || len(edits) == 0 {
			return protocol.CodeAction{}, false
		}
		lineMap := file.src.LineMap()
		textEdits := make([]protocol.TextEdit, len(edits))
		for i, e := range edits {
			span := ast.Span{Start: ast.Location{Offset: e.Start}, End: ast.Location{Offset: e.End}}
			textEdits[i] = protocol.TextEdit{Range: spanToRange(lineMap, span), NewText: e.Text}
		}
		return protocol.CodeAction{
			Title: "Write the inferred signature",
			Kind:  addr(signatureActionKind),
			Edit: &protocol.WorkspaceEdit{
				Changes: map[protocol.DocumentUri][]protocol.TextEdit{uri: textEdits},
			},
		}, true
	}
	return protocol.CodeAction{}, false
}
