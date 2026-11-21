package main

import (
	"slices"
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

// expressionKeywords start an expression, so they fit any position that takes one.
var expressionKeywords = []string{
	"async", "do", "false", "fn", "gen", "if", "match", "null", "throw", "true", "try",
	"undefined",
}

// statementKeywords start a statement but not an expression. A statement start offers
// them beside expressionKeywords.
var statementKeywords = []string{"class", "enum", "for", "interface", "type", "val", "var"}

// libDeclKeywords start a declaration at the top level of a `lib/` file. A module
// holds only declarations there, so a statement such as `if` or `for` doesn't fit.
var libDeclKeywords = []string{
	"async", "class", "declare", "enum", "export", "fn", "gen", "import", "interface",
	"type", "val", "var",
}

// classBodyKeywords are the modifiers a class member can start with, and
// `constructor`, which names a member rather than a binding.
var classBodyKeywords = []string{
	"async", "constructor", "gen", "get", "private", "readonly", "set", "static",
}

// typeKeywords name a type or start a type annotation.
var typeKeywords = []string{
	"any", "bigint", "boolean", "false", "function", "keyof", "never", "null", "number",
	"string", "symbol", "true", "typeof", "undefined", "unique", "unknown", "void",
}

// typeKeywordCompletions returns an item for each of typeKeywords.
func typeKeywordCompletions() []protocol.CompletionItem {
	return keywordItems(typeKeywords)
}

// keywordItems returns a plain Keyword item for each of keywords.
func keywordItems(keywords []string) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(keywords))
	for _, keyword := range keywords {
		items = append(items, keywordCompletion(keyword, "", "", false))
	}
	return items
}

// keywordSite classifies the cursor's position by the keywords that fit there.
type keywordSite int

const (
	// noKeywordSite takes no keyword, as in a member name after `.` or a string.
	noKeywordSite keywordSite = iota
	// expressionSite takes an expression, as in `val x = tr|`.
	expressionSite
	// statementSite starts a statement inside a block, as in a function body.
	statementSite
	// topLevelSite starts a statement at the top level of a file.
	topLevelSite
	// classBodySite starts a class member.
	classBodySite
)

// positionalKeywords returns the keyword items that fit the cursor at loc, unfiltered
// by the word before it. node is the deepest node at loc and ancestors are its
// ancestors, outermost first. text is the file's contents, comments are its comments,
// and walk runs a visitor over the file. isModule is true for a `lib/` file.
//
// exclusive is true when no name in scope can be written at loc, so the caller lists
// the keywords alone. That holds in a class body, where a member's name is new.
func positionalKeywords(
	node ast.Node,
	ancestors []ast.Node,
	text string,
	loc ast.Location,
	comments []*ast.Comment,
	walk func(ast.Visitor),
	isModule bool,
) (items []protocol.CompletionItem, exclusive bool) {
	if loc.Offset < 0 || loc.Offset > len(text) || inComment(comments, loc) {
		return nil, false
	}
	prefix := wordAtCursor(text, loc)
	wordStart := loc.Offset - len(prefix)

	var keywords []string
	site := keywordSiteAt(node, ancestors, text, loc, wordStart)
	switch site {
	case noKeywordSite:
	case classBodySite:
		return keywordItems(classBodyKeywords), true
	case topLevelSite:
		if isModule {
			keywords = append(keywords, libDeclKeywords...)
		} else {
			keywords = append(keywords, statementKeywords...)
			keywords = append(keywords, expressionKeywords...)
			keywords = append(keywords, "declare", "import")
		}
	case statementSite:
		keywords = append(keywords, statementKeywords...)
		keywords = append(keywords, expressionKeywords...)
	case expressionSite:
		keywords = append(keywords, expressionKeywords...)
	}
	if site == statementSite || site == expressionSite {
		if fn := enclosingFunc(node, ancestors, loc); fn != nil {
			if site == statementSite {
				keywords = append(keywords, "return")
			}
			if fn.Async {
				keywords = append(keywords, "await")
			}
			if fn.Gen {
				keywords = append(keywords, "yield")
			}
		}
	}

	// A half-typed `el` or `ca` after a block parses as a statement of its own, so the
	// `if` or `try` it continues is found from the block's end instead.
	// Only a `}` can end the block, so the walk is skipped for any other text.
	before := strings.TrimRight(text[:wordStart], " \t\r\n")
	if strings.HasSuffix(before, "}") {
		finder := &openBlockFinder{end: len(before)}
		walk(finder)
		if finder.ifWithoutElse {
			keywords = append(keywords, "else")
		}
		if finder.tryWithoutCatch {
			keywords = append(keywords, "catch")
		}
	}
	return keywordItems(keywords), false
}

// keywordSiteAt returns where the word that starts at byte offset wordStart sits. node
// and ancestors are as positionalKeywords takes them.
func keywordSiteAt(node ast.Node, ancestors []ast.Node, text string, loc ast.Location, wordStart int) keywordSite {
	// A statement or class member starts its own line, so a word with other text in
	// front of it on its line continues something else.
	lineStart := strings.LastIndexByte(text[:wordStart], '\n') + 1
	startsLine := strings.TrimSpace(text[lineStart:wordStart]) == ""

	parent := parentOf(ancestors)
	switch n := node.(type) {
	case nil:
		// Only the top level of a file has no node around the cursor.
		if startsLine {
			return topLevelSite
		}
		return noKeywordSite
	case *ast.IdentExpr:
		switch p := parent.(type) {
		case *ast.MemberExpr:
			return noKeywordSite
		case *ast.ClassDecl:
			// The walk records no node for a class member, so a name in a field's
			// initializer also has the class as its parent. A modifier starts its
			// member, and a word anywhere else in one is inside an expression.
			if member := classMemberAt(p, loc); member != nil && member.Span().Start.Offset != wordStart {
				return expressionSite
			}
			if body := classBodyStart(p, text); startsLine && body >= 0 && body < wordStart {
				return classBodySite
			}
			return noKeywordSite
		case *ast.PropertyExpr:
			// The name of a property is a key, and only its value is an expression.
			if p.Value == n {
				return expressionSite
			}
			return noKeywordSite
		case *ast.ExprStmt:
			if p.Expr == n && startsLine {
				// A script's top-level statement has its statement as its only ancestor.
				if len(ancestors) == 1 {
					return topLevelSite
				}
				return statementSite
			}
			return expressionSite
		default:
			return expressionSite
		}
	case *ast.ClassDecl:
		if body := classBodyStart(n, text); startsLine && body >= 0 && body < wordStart {
			return classBodySite
		}
		return noKeywordSite
	default:
		if startsLine && inStatementList(node, loc) {
			return statementSite
		}
		return noKeywordSite
	}
}

// classBodyStart returns the byte offset of the `{` that opens class's body in text,
// or -1 when the class has none. The body's `{` is the first one after the class's
// name, type parameters, `extends` clause and `implements` clause. Each of those can
// hold a `{` of an object type, as in `class C extends Base<{a: number}> {`.
func classBodyStart(class *ast.ClassDecl, text string) int {
	headerEnd := class.Span().Start.Offset
	extend := func(span ast.Span) { headerEnd = max(headerEnd, span.End.Offset) }
	if class.Name != nil {
		extend(class.Name.Span())
	}
	for _, param := range class.TypeParams {
		extend(param.Span())
	}
	if class.Extends != nil {
		extend(class.Extends.Span())
	}
	for _, iface := range class.Implements {
		extend(iface.Span())
	}
	if headerEnd > len(text) {
		return -1
	}
	brace := strings.IndexByte(text[headerEnd:], '{')
	if brace < 0 {
		return -1
	}
	return headerEnd + brace
}

// classMemberAt returns the member of class whose span contains loc, or nil.
func classMemberAt(class *ast.ClassDecl, loc ast.Location) ast.ClassElem {
	for _, member := range class.Body {
		if member.Span().Contains(loc) {
			return member
		}
	}
	return nil
}

// inStatementList reports whether loc sits between the braces of a block that node
// holds directly, such as a function's body or an `if`'s branch.
func inStatementList(node ast.Node, loc ast.Location) bool {
	var blocks []*ast.Block
	switch n := node.(type) {
	case *ast.FuncDecl:
		blocks = append(blocks, n.Body)
	case *ast.FuncExpr:
		blocks = append(blocks, n.Body)
	case *ast.IfElseExpr:
		blocks = append(blocks, &n.Cons)
		if n.Alt != nil {
			blocks = append(blocks, n.Alt.Block)
		}
	case *ast.ForInStmt:
		blocks = append(blocks, &n.Body)
	case *ast.DoExpr:
		blocks = append(blocks, &n.Body)
	case *ast.TryCatchExpr:
		blocks = append(blocks, &n.Try)
		for _, c := range n.Catch {
			blocks = append(blocks, c.Body.Block)
		}
	case *ast.MatchExpr:
		for _, c := range n.Cases {
			blocks = append(blocks, c.Body.Block)
		}
	}
	for _, block := range blocks {
		if block != nil && block.Span.Start.Offset < loc.Offset && loc.Offset < block.Span.End.Offset {
			return true
		}
	}
	return false
}

// enclosingFunc returns the signature of the innermost function whose body holds loc,
// or nil at the top level. node and ancestors are as positionalKeywords takes them.
func enclosingFunc(node ast.Node, ancestors []ast.Node, loc ast.Location) *ast.FuncSig {
	chain := append(slices.Clone(ancestors), node)
	for i := len(chain) - 1; i >= 0; i-- {
		switch n := chain[i].(type) {
		case *ast.FuncDecl:
			if n.Body != nil && n.Body.Span.Contains(loc) {
				return &n.FuncSig
			}
		case *ast.FuncExpr:
			if n.Body != nil && n.Body.Span.Contains(loc) {
				return &n.FuncSig
			}
		}
	}
	return nil
}

// inComment reports whether loc sits inside one of comments. A line comment runs to
// the end of its line, so the cursor at its end is inside it.
func inComment(comments []*ast.Comment, loc ast.Location) bool {
	for _, c := range comments {
		span := c.Span()
		if span.Start.Offset < loc.Offset && loc.Offset < span.End.Offset {
			return true
		}
		if c.Kind == ast.LineCommentKind && loc.Offset == span.End.Offset {
			return true
		}
	}
	return false
}

// openBlockFinder looks for an `if` with no `else` and a `try` with no `catch` whose
// block ends at byte offset end. It descends only into nodes whose span contains end.
type openBlockFinder struct {
	ast.DefaultVisitor
	end             int
	ifWithoutElse   bool
	tryWithoutCatch bool
}

func (f *openBlockFinder) visits(span ast.Span) bool {
	return span.Start.Offset <= f.end && f.end <= span.End.Offset
}

func (f *openBlockFinder) EnterExpr(e ast.Expr) bool {
	if !f.visits(e.Span()) {
		return false
	}
	switch n := e.(type) {
	case *ast.IfElseExpr:
		if n.Alt == nil && n.Cons.Span.End.Offset == f.end {
			f.ifWithoutElse = true
		}
	case *ast.TryCatchExpr:
		if len(n.Catch) == 0 && n.Try.Span.End.Offset == f.end {
			f.tryWithoutCatch = true
		}
	}
	return true
}

func (f *openBlockFinder) EnterDecl(d ast.Decl) bool     { return f.visits(d.Span()) }
func (f *openBlockFinder) EnterStmt(s ast.Stmt) bool     { return f.visits(s.Span()) }
func (f *openBlockFinder) EnterBlock(b ast.Block) bool   { return f.visits(b.Span) }
func (f *openBlockFinder) EnterTypeAnn(ast.TypeAnn) bool { return false }
func (f *openBlockFinder) EnterClassElem(e ast.ClassElem) bool {
	return f.visits(e.Span())
}
