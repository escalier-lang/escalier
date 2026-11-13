// Package sigedit writes the signature the checker inferred for a function into the source
// that declares it. The language server's code action and the tools that migrate source to
// written signatures both read it.
package sigedit

import (
	"regexp"
	"slices"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/solver"
)

// Target is a function or method whose inferred signature can be written.
type Target struct {
	// Name is the declared name, which a binder list is written after.
	Name ast.Node
	Sig  *ast.FuncSig
	// Receiver is a method's receiver, or nil.
	Receiver *ast.MethodReceiver
	// SignatureSpan runs from the start of the declaration to the start of its body.
	SignatureSpan ast.Span
	// Signature is what the checker inferred for the function.
	Signature solver.DeclSignature
}

// Edit replaces the bytes from Start to End of a file's text with Text. Start equals End
// for an insertion.
type Edit struct {
	Start, End int
	Text       string
}

// FileDecls returns the declarations of the file sourceID names. When script is not nil the
// file is that script, and its top-level declarations are returned. Otherwise the file is
// a lib/ file of module, and the declarations of module written in it are returned.
func FileDecls(script *ast.Script, module *ast.Module, sourceID int) []ast.Decl {
	var decls []ast.Decl
	if script != nil {
		for _, stmt := range script.Stmts {
			if ds, ok := stmt.(*ast.DeclStmt); ok {
				decls = append(decls, ds.Decl)
			}
		}
		return decls
	}
	if module == nil {
		return nil
	}
	for _, ns := range module.Namespaces.Values() {
		for _, d := range ns.Decls {
			if d.Span().SourceID == sourceID {
				decls = append(decls, d)
			}
		}
	}
	return decls
}

// Targets returns each function decls declares with a body, and each method with a body of
// a class decls declares, for which info records a signature. sourceID is the file decls
// are declared in.
func Targets(info *solver.Info, decls []ast.Decl, sourceID int) []Target {
	var targets []Target
	for _, d := range decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Body == nil {
				continue
			}
			signature, ok := info.FuncDeclSignature(d)
			if !ok {
				continue
			}
			span := ast.Span{Start: d.Span().Start, End: d.Body.Span.Start, SourceID: sourceID}
			targets = append(targets, Target{Name: d.Name, Sig: &d.FuncSig, SignatureSpan: span, Signature: signature})
		case *ast.ClassDecl:
			for _, elem := range d.Body {
				m, ok := elem.(*ast.MethodElem)
				if !ok || m.Fn.Body == nil {
					continue
				}
				signature, ok := info.MethodSignature(d, m)
				if !ok {
					continue
				}
				span := ast.Span{Start: m.Span().Start, End: m.Fn.Body.Span.Start, SourceID: sourceID}
				targets = append(targets, Target{
					Name: m.Name, Sig: &m.Fn.FuncSig, Receiver: m.Receiver, SignatureSpan: span, Signature: signature,
				})
			}
		}
	}
	return targets
}

// Apply returns contents with edits applied. The edits must not overlap.
func Apply(contents string, edits []Edit) string {
	// Applying the last edit first keeps the offsets of the earlier ones valid. Two
	// insertions at one offset keep their order in edits, since the one listed later is
	// applied first and the earlier one then lands in front of it.
	order := make([]int, len(edits))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		if edits[i].Start != edits[j].Start {
			return edits[j].Start - edits[i].Start
		}
		return j - i
	})
	for _, i := range order {
		e := edits[i]
		contents = contents[:e.Start] + e.Text + contents[e.End:]
	}
	return contents
}

// Edits returns the edits that write target's inferred signature into contents, the text of
// the file target is declared in. A part the source leaves out is written. A part the source
// writes is kept as written, unless it needs a lifetime the edits add to the binder list, in
// which case it is rewritten. ok is false when the inferred signature cannot be written into
// the source:
// for a function type that carries its own lifetime, for a parameter list whose length
// differs from the inferred one, and for a binder list that needs rewriting while the
// source writes a `where` clause or declares a lifetime that elision assigns.
func (target Target) Edits(contents string) ([]Edit, bool) {
	ft := target.Signature.Type
	sig := target.Sig
	if ft.Lt != nil || len(ft.Params) != len(sig.Params) {
		return nil, false
	}
	rendered := target.Signature.PrintWritten()
	var edits []Edit
	replace := func(start, end int, text string) {
		edits = append(edits, Edit{Start: start, End: end, Text: text})
	}
	insert := func(offset int, text string) { replace(offset, offset, text) }
	replaceSpan := func(span ast.Span, text string) { replace(span.Start.Offset, span.End.Offset, text) }

	// The binder list is rewritten when the inferred signature binds a lifetime the source
	// does not declare, or a type parameter the source does not write while it leaves a part
	// out, and every part that names one of the added lifetimes is rewritten with it.
	declared := set.NewSet[string]()
	for _, lp := range sig.LifetimeParams {
		declared.Add("'" + lp.Name)
	}
	writtenTypeParams := set.NewSet[string]()
	for _, tp := range sig.TypeParams {
		writtenTypeParams.Add(tp.Name)
	}
	// A type parameter the source does not write can appear only in a part the source
	// leaves out, so a signature that writes every parameter type and its return type gets
	// no binder added for one.
	inferred := sig.Return == nil
	for _, p := range sig.Params {
		inferred = inferred || p.TypeAnn == nil
	}
	renderedNames := set.NewSet[string]()
	var added []*regexp.Regexp
	addsTypeParam := false
	for _, b := range rendered.Binders {
		name, _, _ := strings.Cut(b, ":")
		name = strings.TrimSpace(name)
		renderedNames.Add(name)
		switch {
		case strings.HasPrefix(name, "'"):
			if !declared.Contains(name) {
				added = append(added, regexp.MustCompile(regexp.QuoteMeta(name)+`\b`))
			}
		case inferred && !writtenTypeParams.Contains(name):
			addsTypeParam = true
		}
	}
	if len(added) > 0 || addsTypeParam {
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
			insert(target.Name.Span().End.Offset, "<"+binders+">")
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

	if target.Receiver != nil && namesAdded(rendered.Receiver) {
		replaceSpan(target.Receiver.Span_, rendered.Receiver)
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
	// A function with no `throws` clause raises nothing, so its inferred signature has no
	// throws type and no clause is ever written.
	switch {
	case sig.Return == nil:
		end, ok := paramListEnd(target, contents)
		if !ok {
			return nil, false
		}
		insert(end, " -> "+rendered.Ret)
	case namesAdded(rendered.Ret):
		replaceSpan(sig.Return.Span(), rendered.Ret)
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
func paramListEnd(target Target, contents string) (int, bool) {
	sig := target.Sig
	var from int
	switch {
	case len(sig.Params) > 0:
		last := sig.Params[len(sig.Params)-1]
		from = last.Pattern.Span().End.Offset
		if last.TypeAnn != nil {
			from = last.TypeAnn.Span().End.Offset
		}
	case target.Receiver != nil:
		from = target.Receiver.Span_.End.Offset
	default:
		from = target.Name.Span().End.Offset
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
