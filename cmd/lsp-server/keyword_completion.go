package main

import (
	"strings"
	"unicode/utf8"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/lexer_util"
)

// keywordCompletion returns a completion item of kind Keyword for keyword. snippet is
// the text to insert in LSP snippet syntax, such as `throws ${1:_}`, and plain is the
// same text without placeholders. The item carries snippet when the client accepts
// snippets and plain otherwise. An empty snippet inserts the keyword alone.
func keywordCompletion(keyword, snippet, plain string, snippets bool) protocol.CompletionItem {
	kind := protocol.CompletionItemKindKeyword
	item := protocol.CompletionItem{Label: keyword, Kind: &kind}
	if snippet == "" {
		return item
	}
	format := protocol.InsertTextFormatPlainText
	text := plain
	if snippets {
		format = protocol.InsertTextFormatSnippet
		text = snippet
	}
	item.InsertText = &text
	item.InsertTextFormat = &format
	return item
}

// throwsCompletions returns the `throws` keyword item when the cursor at loc follows the
// return type of a function, method, getter, `declare fn` or function type annotation
// that has no `throws` clause. Between the return type and the cursor there may be only
// spaces or tabs and the start of a word, as in `fn f() -> number th|`. The item inserts
// `throws _` with the `_` selected when snippets is true, or `throws` alone when a name
// already follows the cursor on its line, as in `thr| string`.
//
// walk runs a visitor over the file the cursor is in, and text is that file's contents.
// ok is false when the cursor is not at such a position. When ok is true, items holds
// `throws` if the word before the cursor is a prefix of it, and is empty otherwise. It
// is also empty when the cursor sits inside a word, as in `th|rows`.
func throwsCompletions(walk func(ast.Visitor), text string, loc ast.Location, snippets bool) (items []protocol.CompletionItem, ok bool) {
	if loc.Offset < 0 || loc.Offset > len(text) {
		return nil, false
	}
	prefix := wordAtCursor(text, loc)
	// A word typed on the line after a `declare fn` or a type alias starts the next
	// statement. Trimming spaces and tabs but not newlines keeps the match to the
	// return type's own line.
	trimmed := strings.TrimRight(text[:loc.Offset-len(prefix)], " \t")
	finder := &returnTypeEndFinder{end: len(trimmed)}
	walk(finder)
	if !finder.found {
		return nil, false
	}
	rest := text[loc.Offset:]
	// The client replaces only the word before the cursor, so an item accepted inside
	// a word would leave the rest of the word after it.
	if next, _ := utf8.DecodeRuneInString(rest); lexer_util.IsIdentContinue(next) {
		return []protocol.CompletionItem{}, true
	}
	item := keywordCompletion("throws", "throws ${1:_}", "throws _", snippets)
	// A name after the cursor is the clause's type, so the placeholder would stand in
	// front of it.
	if next, _ := utf8.DecodeRuneInString(strings.TrimLeft(rest, " \t")); lexer_util.IsIdentStart(next) {
		item = keywordCompletion("throws", "", "", snippets)
	}
	return filterByPrefix([]protocol.CompletionItem{item}, prefix), true
}

// returnTypeEndFinder looks for a signature with no `throws` clause whose return type
// ends at byte offset end. It descends only into nodes whose span contains end. found
// reports whether it saw one.
type returnTypeEndFinder struct {
	ast.DefaultVisitor
	end   int
	found bool
}

// visits reports whether the walk should descend into a node with span. A node whose
// span doesn't contain end holds no return type that ends there.
func (f *returnTypeEndFinder) visits(span ast.Span) bool {
	return !f.found && span.Start.Offset <= f.end && f.end <= span.End.Offset
}

// check records a match when ret ends at f.end and throws is nil.
func (f *returnTypeEndFinder) check(ret, throws ast.TypeAnn) {
	if ret == nil || throws != nil {
		return
	}
	// The parser places an ErrorTypeAnn where `->` has no type after it, and a clause
	// written there would stand in the missing type's place.
	if _, isErr := ret.(*ast.ErrorTypeAnn); isErr {
		return
	}
	if ret.Span().End.Offset == f.end {
		f.found = true
	}
}

func (f *returnTypeEndFinder) EnterDecl(d ast.Decl) bool {
	if !f.visits(d.Span()) {
		return false
	}
	if fn, ok := d.(*ast.FuncDecl); ok {
		f.check(fn.Return, fn.Throws)
	}
	return true
}

func (f *returnTypeEndFinder) EnterExpr(e ast.Expr) bool {
	if !f.visits(e.Span()) {
		return false
	}
	if fn, ok := e.(*ast.FuncExpr); ok {
		f.check(fn.Return, fn.Throws)
	}
	return true
}

func (f *returnTypeEndFinder) EnterTypeAnn(t ast.TypeAnn) bool {
	if !f.visits(t.Span()) {
		return false
	}
	if fn, ok := t.(*ast.FuncTypeAnn); ok {
		f.check(fn.Return, fn.Throws)
	}
	return true
}

func (f *returnTypeEndFinder) EnterStmt(s ast.Stmt) bool   { return f.visits(s.Span()) }
func (f *returnTypeEndFinder) EnterBlock(b ast.Block) bool { return f.visits(b.Span) }
func (f *returnTypeEndFinder) EnterClassElem(e ast.ClassElem) bool {
	return f.visits(e.Span())
}
func (f *returnTypeEndFinder) EnterObjTypeAnnElem(e ast.ObjTypeAnnElem) bool {
	return f.visits(e.Span())
}
