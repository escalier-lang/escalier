package main

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
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
	file, diags, ok := s.checkedFileAt(uri)
	if !ok {
		return protocol.CodeAction{}, false
	}
	loc := posToLoc(file.src.LineMap(), pos)
	s.solverMu.RLock()
	defer s.solverMu.RUnlock()
	return signatureAction(file, diags, uri, loc)
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
	// scope is the scope the file's top level was checked in, or nil when the check output
	// holds none for it.
	scope *solver.Scope
	// namespace is the dotted namespace a lib/ file's directory places it in, and "" for a
	// bin/ script and for a lib/ file at the root.
	namespace string
}

// signatureAction returns the code action that writes the inferred signature of the
// function or method whose signature contains loc, and false when there is no such
// function, its signature is already written in full, or the signature would write a type
// naming `never` while the function has another diagnostic. loc is in file, diags are the
// check's diagnostics for file, and uri is file's document.
func signatureAction(file checkedFile, diags []compiler.Diagnostic, uri protocol.DocumentUri, loc ast.Location) (protocol.CodeAction, bool) {
	decls := sigedit.FileDecls(file.script, file.module, file.id)
	for _, target := range sigedit.Targets(file.info, decls, file.id) {
		if !target.SignatureSpan.Contains(loc) {
			continue
		}
		edits, ok := target.Edits(file.src.Contents)
		if !ok || len(edits) == 0 {
			return protocol.CodeAction{}, false
		}
		// An error inside the function can leave a type the checker infers as `never`, which
		// describes the error rather than what the author means. Hover still shows the type.
		if sigedit.WritesNeverWhileDiagnosed(target, edits, diags) {
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
