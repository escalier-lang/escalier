package main

import (
	"encoding/json"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/solver"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// borrowFixData is the `data` field of a diagnostic whose fix writes a borrow before an
// expression. The client sends it back unchanged in a code action request, and
// borrowFixAction builds the edit from it.
type borrowFixData struct {
	// Borrow is the operator to write, `&` or `&mut`.
	Borrow string `json:"borrow"`
	// Range is the range of the expression the borrow is written before. It covers the
	// parentheses around an operand at either edge, which the diagnostic's range can leave
	// out.
	Range protocol.Range `json:"range"`
	// Parenthesize says the expression is wrapped in parentheses as the borrow is written.
	Parenthesize bool `json:"parenthesize,omitempty"`
	// Key is the property name to write before the borrow when the expression is the key of
	// a shorthand property, so that `{f}` becomes `{f: &f}`.
	Key string `json:"key,omitempty"`
}

// borrowFixDataFor returns the data for the diagnostic published for err, and false when err
// has no fix that writes a borrow. src is the file err's span indexes into.
func borrowFixDataFor(err compiler.Diagnostic, src *ast.Source) (borrowFixData, bool) {
	missing, ok := err.(solver.MissingBorrowError)
	if !ok || src == nil {
		return borrowFixData{}, false
	}
	expr := missing.Expr()
	// The parser leaves the parentheses around an operand at either edge out of a span. The
	// span of `(h).f` starts at `h`, and a borrow written there would borrow `h` alone.
	span, ok := groupedSpan(expr.Span(), src.Contents)
	if !ok {
		return borrowFixData{}, false
	}
	return borrowFixData{
		Borrow: missing.Borrow(),
		Range:  spanToRange(src.LineMap(), span),
		// Every expression borrowOperandNeedsParens does not name is wrapped. That keeps the fix
		// correct without weighing each operator's precedence against the prefix `&`.
		Parenthesize: borrowOperandNeedsParens(expr),
		Key:          missing.ShorthandKey(),
	}, true
}

// groupedSpan returns span widened over the parentheses that its text opens or closes
// without matching. A span covering `h).f` in `g((h).f)` widens to `(h).f`, and one covering
// `a + (b` in `g(a + (b))` widens to `a + (b)`. Only whitespace may separate span from the
// parentheses it widens over. It returns false when contents does not hold them.
//
// The scan skips string and template literals and comments. A regular expression literal
// or JSX text that holds an unmatched parenthesis is miscounted.
func groupedSpan(span ast.Span, contents string) (ast.Span, bool) {
	start, end := span.Start.Offset, span.End.Offset
	if start < 0 || end > len(contents) || start > end {
		return span, false
	}
	unclosed, unopened := unmatchedParens(contents[start:end])
	for ; unopened > 0; unopened-- {
		start = skipSpaceBefore(contents, start)
		if start == 0 || contents[start-1] != '(' {
			return span, false
		}
		start--
	}
	for ; unclosed > 0; unclosed-- {
		end = skipSpaceAfter(contents, end)
		if end == len(contents) || contents[end] != ')' {
			return span, false
		}
		end++
	}
	span.Start.Offset, span.End.Offset = start, end
	return span, true
}

// unmatchedParens returns how many `(` in text no `)` after it closes, and how many `)` in
// text close a `(` that comes before text.
func unmatchedParens(text string) (unclosed, unopened int) {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '(':
			unclosed++
		case ')':
			if unclosed > 0 {
				unclosed--
			} else {
				unopened++
			}
		case '"', '`':
			i = skipQuoted(text, i)
		case '/':
			if strings.HasPrefix(text[i:], "//") {
				if n := strings.IndexByte(text[i:], '\n'); n >= 0 {
					i += n
				} else {
					i = len(text)
				}
			} else if strings.HasPrefix(text[i:], "/*") {
				if n := strings.Index(text[i+2:], "*/"); n >= 0 {
					i += n + 3
				} else {
					i = len(text)
				}
			}
		}
	}
	return unclosed, unopened
}

// skipQuoted returns the index of the quote that closes the literal opening at text[i], or
// the last index of text when nothing closes it.
func skipQuoted(text string, i int) int {
	quote := text[i]
	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			j++
		case quote:
			return j
		}
	}
	return len(text) - 1
}

// skipSpaceBefore returns the offset of the first byte in the run of whitespace that ends at
// offset i in contents.
func skipSpaceBefore(contents string, i int) int {
	for i > 0 && strings.IndexByte(" \t\r\n", contents[i-1]) >= 0 {
		i--
	}
	return i
}

// skipSpaceAfter returns the offset just past the run of whitespace that starts at offset i
// in contents.
func skipSpaceAfter(contents string, i int) int {
	for i < len(contents) && strings.IndexByte(" \t\r\n", contents[i]) >= 0 {
		i++
	}
	return i
}

// borrowOperandNeedsParens reports whether e is wrapped in parentheses as the operand of a
// prefix `&` or `&mut`. It returns false for an identifier, a field access, an index, and a
// call, whose postfix `.`, `[]`, and `()` bind tighter than the prefix, so `&a.b` borrows
// `a.b`. It returns true for every other expression.
func borrowOperandNeedsParens(e ast.Expr) bool {
	switch e.(type) {
	case *ast.IdentExpr, *ast.MemberExpr, *ast.IndexExpr, *ast.CallExpr:
		return false
	default:
		return true
	}
}

// borrowFixAction returns the quick fix for diag that writes the borrow its data names, and
// false when diag carries no borrowFixData. uri is the document diag belongs to.
func borrowFixAction(uri protocol.DocumentUri, diag protocol.Diagnostic) (protocol.CodeAction, bool) {
	if diag.Data == nil {
		return protocol.CodeAction{}, false
	}
	// A client sends the data back as decoded JSON, so it is read through a JSON round trip
	// instead of a type assertion.
	raw, err := json.Marshal(diag.Data)
	if err != nil {
		return protocol.CodeAction{}, false
	}
	var data borrowFixData
	if err := json.Unmarshal(raw, &data); err != nil {
		return protocol.CodeAction{}, false
	}
	var prefix string
	switch data.Borrow {
	case "&":
		prefix = "&"
	case "&mut":
		prefix = "&mut "
	default:
		return protocol.CodeAction{}, false
	}
	if data.Key != "" {
		prefix = data.Key + ": " + prefix
	}
	start, end := data.Range.Start, data.Range.End
	if data.Parenthesize {
		prefix += "("
	}
	edits := []protocol.TextEdit{{Range: protocol.Range{Start: start, End: start}, NewText: prefix}}
	if data.Parenthesize {
		edits = append(edits, protocol.TextEdit{Range: protocol.Range{Start: end, End: end}, NewText: ")"})
	}
	return protocol.CodeAction{
		Title:       "Write the borrow " + data.Borrow,
		Kind:        addr(protocol.CodeActionKindQuickFix),
		Diagnostics: []protocol.Diagnostic{diag},
		IsPreferred: addr(true),
		Edit: &protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentUri][]protocol.TextEdit{uri: edits},
		},
	}, true
}
