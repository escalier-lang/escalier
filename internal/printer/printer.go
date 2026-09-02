package printer

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
)

// Options contains configuration for the printer
type Options struct {
	Indent string // e.g., "  " or "\t"
	// Compact renders the whole node on one line. Every line break becomes a separator on
	// the same line, and all indentation is dropped. A `do` block holding the two
	// statements `1` and `return 2` prints as `do { 1; return 2 }` rather than spanning
	// four lines. Callers that embed a rendered fragment in a line of their own output
	// need this.
	//
	// Text that carries its own newline, such as a multi-line template literal, is
	// written with the newline escaped as `\n` so the one-line guarantee holds. Compact
	// output is meant to be read, not reparsed.
	Compact bool
}

// DefaultOptions returns default printer options
func DefaultOptions() Options {
	return Options{
		Indent:  "    ", // 4 spaces
		Compact: false,
	}
}

// CompactOptions returns options that render a node on one line.
func CompactOptions() Options {
	opts := DefaultOptions()
	opts.Compact = true
	return opts
}

// Printer handles pretty-printing of AST nodes
type Printer struct {
	writer      io.Writer
	opts        Options
	indentLevel int
	needIndent  bool
	lastChar    byte
	// pendingSep is the compact-mode separator a line break left behind, waiting to be
	// resolved once the next token arrives. Holding it lets a separator before a closing
	// brace soften to a space, so a block ends `return 2 }` rather than `return 2; }`.
	pendingSep string
}

// NewPrinter creates a new printer with the given options
func NewPrinter(writer io.Writer, opts Options) *Printer {
	return &Printer{
		writer:      writer,
		opts:        opts,
		indentLevel: 0,
		needIndent:  true,
		lastChar:    0,
		pendingSep:  "",
	}
}

// compactLineEndings rewrites the line endings source text can carry into their escaped
// spellings. CRLF is listed first so it is rewritten as one unit rather than as two
// separate escapes.
var compactLineEndings = strings.NewReplacer(
	"\r\n", `\r\n`,
	"\n", `\n`,
	"\r", `\r`,
)

// Helper methods for output management

func (p *Printer) writeString(s string) {
	// In compact mode lineBreak never emits a line ending, so any line ending reaching
	// here came from source text such as a template literal's quasi. Escaping it keeps
	// the whole rendering on one line. A carriage return counts, since a lone `\r` ends a
	// line for a terminal and for the tools that read the rendering.
	if p.opts.Compact && strings.ContainsAny(s, "\n\r") {
		s = compactLineEndings.Replace(s)
	}

	if len(s) > 0 && p.pendingSep != "" {
		sep := p.pendingSep
		p.pendingSep = ""
		// A closing delimiter ends the group the separator was meant to divide, so it
		// takes a plain space instead.
		if s[0] == '}' || s[0] == ')' || s[0] == ']' {
			sep = " "
		}
		if p.lastChar != ' ' && p.lastChar != 0 {
			p.write(sep)
		}
	}

	if p.needIndent && len(s) > 0 && s[0] != '\n' {
		p.write(strings.Repeat(p.opts.Indent, p.indentLevel))
		p.needIndent = false
	}

	p.write(s)
}

// write sends a string to the writer without consulting the pending separator or the
// indentation state, and records the last byte for space() and the separator logic.
func (p *Printer) write(s string) {
	if _, err := io.WriteString(p.writer, s); err != nil {
		panic(fmt.Sprintf("Printer write error: %v", err))
	}
	if len(s) > 0 {
		p.lastChar = s[len(s)-1]
	}
}

// newline ends a line between two parts of the same construct, such as the elements of an
// object literal, which carry their own comma. Compact mode separates them with a space.
func (p *Printer) newline() {
	p.lineBreak(" ")
}

// newlineStmt ends a line between two statements, which carry no separator of their own.
// Compact mode divides them with a semicolon, so `1 return 2` prints as `1; return 2`.
func (p *Printer) newlineStmt() {
	p.lineBreak("; ")
}

func (p *Printer) lineBreak(compactSep string) {
	if !p.opts.Compact {
		p.writeString("\n")
		p.needIndent = true
		return
	}
	// A semicolon carries more than a space, so it wins when both land in the same gap.
	if p.pendingSep == "" || compactSep != " " {
		p.pendingSep = compactSep
	}
}

func (p *Printer) space() {
	if p.lastChar != ' ' && p.lastChar != '\n' && p.lastChar != 0 {
		p.writeString(" ")
	}
}

func (p *Printer) indent() {
	p.indentLevel++
}

func (p *Printer) dedent() {
	if p.indentLevel > 0 {
		p.indentLevel--
	}
}

// Print methods for different AST node types

// PrintScript prints a Script node
func (p *Printer) PrintScript(script *ast.Script) error {
	first := true
	for _, stmt := range script.Stmts {
		if _, isErr := stmt.(*ast.ErrorStmt); isErr {
			continue
		}
		if !first {
			p.newlineStmt()
		}
		p.printStmt(stmt)
		first = false
	}
	return nil
}

// PrintModule prints a Module node
func (p *Printer) PrintModule(module *ast.Module) error {
	// Iterate over namespaces
	module.Namespaces.Scan(func(key string, ns *ast.Namespace) bool {
		if key != "" {
			p.writeString("namespace ")
			p.writeString(key)
			p.writeString(" {")
			p.newline()
			p.indent()
		}

		for i, decl := range ns.Decls {
			p.printDecl(decl)
			if i < len(ns.Decls)-1 {
				p.newlineStmt()
			}
		}

		if key != "" {
			p.dedent()
			p.writeString("}")
			p.newline()
		}
		return true
	})
	return nil
}

// Statement printing

func (p *Printer) printStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		p.printExpr(s.Expr)
	case *ast.DeclStmt:
		p.printDecl(s.Decl)
	case *ast.ReturnStmt:
		p.writeString("return")
		if s.Expr != nil {
			p.space()
			p.printExpr(s.Expr)
		}
	case *ast.ImportStmt:
		p.printImportStmt(s)
	case *ast.ErrorStmt:
		// Skip error recovery nodes
	default:
		p.writeString("/* unknown statement */")
	}
}

func (p *Printer) printImportStmt(s *ast.ImportStmt) {
	p.writeString("import")
	if !s.Bare() {
		p.space()
		// Namespace import is a single specifier with Name == "*".
		if len(s.Specifiers) == 1 && s.Specifiers[0].Name == "*" {
			p.writeString("* as ")
			p.writeString(s.Specifiers[0].Alias)
		} else {
			p.writeString("{ ")
			for i, spec := range s.Specifiers {
				if i > 0 {
					p.writeString(", ")
				}
				p.writeString(spec.Name)
				if spec.Alias != "" {
					p.writeString(" as ")
					p.writeString(spec.Alias)
				}
			}
			p.writeString(" }")
		}
		p.writeString(" from")
	}
	// Reassemble the module specifier (URI + `?flag1&flag2` suffix) and
	// emit it as a properly escaped string literal — matches the
	// strconv.Quote treatment used for `*ast.StrLit` elsewhere in this
	// printer, so a PackageName or flag containing quotes/backslashes
	// round-trips correctly.
	var spec strings.Builder
	spec.WriteString(s.PackageName)
	for i, flag := range s.Flags {
		if i == 0 {
			spec.WriteByte('?')
		} else {
			spec.WriteByte('&')
		}
		spec.WriteString(flag)
	}
	p.space()
	p.writeString(strconv.Quote(spec.String()))
}

// Declaration printing

func (p *Printer) printDecl(decl ast.Decl) {
	// Compact mode renders the declaration on one line, where a doc comment
	// would either swallow the rest of the line or need escaping to avoid it.
	if doc := decl.Doc(); doc != "" && !p.opts.Compact {
		p.writeDoc(doc)
	}
	switch d := decl.(type) {
	case *ast.VarDecl:
		p.printVarDecl(d)
	case *ast.FuncDecl:
		p.printFuncDecl(d)
	case *ast.TypeDecl:
		p.printTypeDecl(d)
	case *ast.InterfaceDecl:
		p.printInterfaceDecl(d)
	case *ast.EnumDecl:
		p.printEnumDecl(d)
	case *ast.ClassDecl:
		p.printClassDecl(d)
	default:
		p.writeString("/* unknown declaration */")
	}
}

func (p *Printer) printClassDecl(decl *ast.ClassDecl) {
	p.printDecorators(decl.Decorators)
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}
	if decl.Final() {
		p.writeString("final ")
	}

	p.writeString("class ")
	p.writeString(decl.Name.Name)

	p.printGenericParams(decl.LifetimeParams, decl.TypeParams)

	if decl.Extends != nil {
		p.writeString(" extends ")
		p.printTypeAnn(decl.Extends)
	}

	if len(decl.Implements) > 0 {
		p.writeString(" implements ")
		for i, impl := range decl.Implements {
			if i > 0 {
				p.writeString(", ")
			}
			p.printTypeAnn(impl)
		}
	}

	if len(decl.Body) == 0 {
		p.writeString(" {}")
		return
	}

	p.writeString(" {")
	p.newline()
	p.indent()
	for i, elem := range decl.Body {
		p.printClassElem(elem)
		if i < len(decl.Body)-1 {
			p.writeString(",")
		}
		p.newline()
	}
	p.dedent()
	p.writeString("}")
}

// NormalizeDocLines splits a multi-line JSDoc block comment into
// individual lines with continuation-line indentation normalized: each
// line after the first is left-trimmed and, when it begins with `*`,
// prefixed with a single space so the `*` column aligns with the
// second `*` of the opening `/**`. Callers emit each line via their
// own indent-aware writer (e.g. Printer.writeString + newline) so the
// surrounding indent context is applied correctly.
//
// This matters when a comment is hoisted from a nested context (e.g.
// an interface body) to a new indent level (e.g. a top-level decl via
// singleton flattening). The lexer captures the comment verbatim with
// the source's original column-prefix; without normalization the
// continuation lines retain a stale indent.
func NormalizeDocLines(doc string) []string {
	lines := strings.Split(doc, "\n")
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if strings.HasPrefix(trimmed, "*") {
			lines[i] = " " + trimmed
		} else {
			lines[i] = trimmed
		}
	}
	return lines
}

// writeDoc emits a doc comment respecting the current indent level —
// each continuation line is re-indented via the printer's normal
// per-line indent machinery instead of carrying the source's column
// offset.
func (p *Printer) writeDoc(doc string) {
	for i, line := range NormalizeDocLines(doc) {
		if i > 0 {
			p.newline()
		}
		p.writeString(line)
	}
	p.newline()
}

func (p *Printer) printClassElem(elem ast.ClassElem) {
	if doc := elem.Doc(); doc != "" {
		p.writeDoc(doc)
	}
	switch e := elem.(type) {
	case *ast.FieldElem:
		if e.Static {
			p.writeString("static ")
		}
		if e.Private {
			p.writeString("private ")
		}
		if e.Readonly {
			p.writeString("readonly ")
		}
		p.printObjKey(e.Name)
		if e.Optional {
			p.writeString("?")
		}
		if e.Type != nil {
			p.writeString(": ")
			p.printTypeAnn(e.Type)
		}
		if e.Value != nil {
			p.writeString(" = ")
			p.printExpr(e.Value)
		}
	case *ast.MethodElem:
		if e.Static {
			p.writeString("static ")
		}
		if e.Private {
			p.writeString("private ")
		}
		if e.Fn.Async {
			p.writeString("async ")
		}
		if e.Fn.Gen {
			p.writeString("gen ")
		}
		p.printObjKey(e.Name)
		p.printMethodSig(&e.Fn.FuncSig, e.Receiver)
		if e.Fn.Body != nil {
			p.space()
			p.printBlock(e.Fn.Body)
		}
	case *ast.GetterElem:
		if e.Static {
			p.writeString("static ")
		}
		if e.Private {
			p.writeString("private ")
		}
		p.writeString("get ")
		p.printObjKey(e.Name)
		p.printMethodSig(&e.Fn.FuncSig, e.Receiver)
		if e.Fn.Body != nil {
			p.space()
			p.printBlock(e.Fn.Body)
		}
	case *ast.SetterElem:
		if e.Static {
			p.writeString("static ")
		}
		if e.Private {
			p.writeString("private ")
		}
		p.writeString("set ")
		p.printObjKey(e.Name)
		p.printMethodSig(&e.Fn.FuncSig, e.Receiver)
		if e.Fn.Body != nil {
			p.space()
			p.printBlock(e.Fn.Body)
		}
	case *ast.ConstructorElem:
		if e.Private {
			p.writeString("private ")
		}
		p.writeString("constructor")
		p.printMethodSig(&e.Fn.FuncSig, e.Receiver)
		if e.Fn.Body != nil {
			p.space()
			p.printBlock(e.Fn.Body)
		}
	}
}

// printMethodSig prints a method/getter/setter/constructor signature
// including an optional `self` / `mut self` receiver. It is parallel
// to printFuncSig but injects the receiver as the first parameter
// inside the parentheses so the round-trip preserves it.
func (p *Printer) printMethodSig(sig *ast.FuncSig, recv *ast.MethodReceiver) {
	p.printGenericParams(sig.LifetimeParams, sig.TypeParams)
	p.writeString("(")
	first := true
	params := sig.Params
	if recv != nil {
		if recv.Mut {
			p.writeString("mut self")
		} else {
			p.writeString("self")
		}
		first = false
		// Constructors materialize the receiver as Params[0] so the body
		// checker can read it uniformly. Skip it here so we don't print
		// `self` twice.
		// TODO(#635): once FuncSig carries SelfParam, drop this
		// structural name check and skip the receiver via that field.
		if len(params) > 0 {
			if ip, ok := params[0].Pattern.(*ast.IdentPat); ok && ip.Name == "self" {
				params = params[1:]
			}
		}
	}
	for _, param := range params {
		if !first {
			p.writeString(", ")
		}
		first = false
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
	}
	p.writeString(")")
	p.printReturnAndThrows(sig.Return, sig.Throws)
}

// annMemberReceiver returns the receiver text a member annotation prints, or "" for none. The
// parser stores it on the elem rather than in Fn.Params, and a lifetime on it is not rendered,
// matching printMethodSig. fallback covers a member that wrote no receiver: an accessor passes
// `self` or `mut self`, which is what the `.d.ts` converter's output relies on, and a method
// passes "".
func annMemberReceiver(recv *ast.MethodReceiver, fallback string) string {
	if recv == nil {
		return fallback
	}
	if recv.Mut {
		return "mut self"
	}
	return "self"
}

// printAnnMemberParams emits the parenthesized parameter list of a member annotation, leading
// with recv when it is non-empty. It is the member-annotation counterpart of printMethodSig,
// which takes the FuncSig a class member carries rather than the FuncTypeAnn an annotation does.
func (p *Printer) printAnnMemberParams(recv string, params []*ast.Param) {
	p.writeString("(")
	first := true
	if recv != "" {
		p.writeString(recv)
		first = false
	}
	for _, param := range params {
		if !first {
			p.writeString(", ")
		}
		first = false
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
	}
	p.writeString(")")
}

// printReturnAndThrows emits ` -> R` followed by any `throws T` clause, so every signature
// form renders the pair alike. A nil ret emits no arrow, and neither a nil nor a `never`
// throws emits a clause. `-> R` is greedy, so a function-typed return is parenthesized
// once a clause follows it — `fn () -> (fn () -> number) throws string` — or the clause
// re-reads as the inner function's. soltype's printFuncBody does the same.
func (p *Printer) printReturnAndThrows(ret ast.TypeAnn, throws ast.TypeAnn) {
	clause := throws
	if _, isNever := throws.(*ast.NeverTypeAnn); isNever {
		clause = nil
	}
	if ret != nil {
		p.writeString(" -> ")
		_, retIsFunc := ret.(*ast.FuncTypeAnn)
		if retIsFunc && clause != nil {
			p.writeString("(")
			p.printTypeAnn(ret)
			p.writeString(")")
		} else {
			p.printTypeAnn(ret)
		}
	}
	if clause != nil {
		p.writeString(" throws ")
		p.printTypeAnn(clause)
	}
}

// printDecorators emits each decorator on its own line, preserving
// source order. Called by every decoratable decl's print method
// (VarDecl, FuncDecl, TypeDecl, InterfaceDecl, ClassDecl) before any
// modifier keywords. Per planning/builtins/implementation_plan.md
// §3.3, decorators sit above `export` / `declare`.
func (p *Printer) printDecorators(decorators []*ast.Decorator) {
	for _, dec := range decorators {
		p.writeString("@")
		p.writeString(dec.Name.Name)
		if dec.Args != nil {
			p.writeString("(")
			for i, arg := range dec.Args {
				if i > 0 {
					p.writeString(", ")
				}
				p.printExpr(arg)
			}
			p.writeString(")")
		}
		p.newline()
	}
}

func (p *Printer) printVarDecl(decl *ast.VarDecl) {
	p.printDecorators(decl.Decorators)
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	if decl.Kind == ast.ValKind {
		p.writeString("val ")
	} else {
		p.writeString("var ")
	}

	p.printPattern(decl.Pattern)

	if decl.TypeAnn != nil {
		p.writeString(": ")
		p.printTypeAnn(decl.TypeAnn)
	}

	if decl.Init != nil {
		p.writeString(" = ")
		p.printExpr(decl.Init)
	}

	if decl.Else != nil {
		p.writeString(" else ")
		p.printBlock(decl.Else)
	}
}

func (p *Printer) printFuncDecl(decl *ast.FuncDecl) {
	p.printDecorators(decl.Decorators)
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	if decl.Async {
		p.writeString("async ")
	}
	if decl.Gen {
		p.writeString("gen ")
	}
	p.writeString("fn ")
	p.writeString(decl.Name.Name)

	p.printFuncSig(&decl.FuncSig)

	if decl.Body != nil {
		p.space()
		p.printBlock(decl.Body)
	}
}

func (p *Printer) printTypeDecl(decl *ast.TypeDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("type ")
	p.writeString(decl.Name.Name)

	if len(decl.TypeParams) > 0 {
		p.printTypeParams(decl.TypeParams)
	}

	p.writeString(" = ")
	p.printTypeAnn(decl.TypeAnn)
}

func (p *Printer) printInterfaceDecl(decl *ast.InterfaceDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("interface ")
	p.writeString(decl.Name.Name)

	p.printGenericParams(decl.LifetimeParams, decl.TypeParams)

	p.space()
	p.printTypeAnn(decl.TypeAnn)
}

func (p *Printer) printEnumDecl(decl *ast.EnumDecl) {
	if decl.Export() {
		p.writeString("export ")
	}
	if decl.Declare() {
		p.writeString("declare ")
	}

	p.writeString("enum ")
	p.writeString(decl.Name.Name)

	if len(decl.TypeParams) > 0 {
		p.printTypeParams(decl.TypeParams)
	}

	p.writeString(" {")
	p.newline()
	p.indent()

	for i, elem := range decl.Elems {
		switch e := elem.(type) {
		case *ast.EnumVariant:
			p.writeString(e.Name.Name)
			if len(e.Params) > 0 {
				p.writeString("(")
				for j, param := range e.Params {
					p.printPattern(param.Pattern)
					if param.TypeAnn != nil {
						p.writeString(": ")
						p.printTypeAnn(param.TypeAnn)
					}
					if j < len(e.Params)-1 {
						p.writeString(", ")
					}
				}
				p.writeString(")")
			}
		case *ast.EnumSpread:
			p.writeString("...")
			p.writeString(e.Arg.Name)
		}

		if i < len(decl.Elems)-1 {
			p.writeString(",")
			p.newline()
		}
	}

	p.newline()
	p.dedent()
	p.writeString("}")
}

// Expression printing

func (p *Printer) printExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.ErrorExpr:
		// Error expression - don't print anything
	case *ast.BinaryExpr:
		p.printBinaryExpr(e)
	case *ast.UnaryExpr:
		p.printUnaryExpr(e)
	case *ast.BorrowExpr:
		p.printBorrowExpr(e)
	case *ast.LiteralExpr:
		p.printLiteral(e.Lit)
	case *ast.IdentExpr:
		p.writeString(e.Name)
	case *ast.FuncExpr:
		p.printFuncExpr(e)
	case *ast.CallExpr:
		p.printCallExpr(e)
	case *ast.SuperCallExpr:
		p.printSuperCallExpr(e)
	case *ast.IndexExpr:
		p.printIndexExpr(e)
	case *ast.MemberExpr:
		p.printMemberExpr(e)
	case *ast.TupleExpr:
		p.printTupleExpr(e)
	case *ast.ObjectExpr:
		p.printObjectExpr(e)
	case *ast.IfElseExpr:
		p.printIfElseExpr(e)
	case *ast.IfValExpr:
		p.printIfValExpr(e)
	case *ast.MatchExpr:
		p.printMatchExpr(e)
	case *ast.TryCatchExpr:
		p.printTryCatchExpr(e)
	case *ast.DoExpr:
		p.printDoExpr(e)
	case *ast.AwaitExpr:
		p.printAwaitExpr(e)
	case *ast.ThrowExpr:
		p.printThrowExpr(e)
	case *ast.YieldExpr:
		p.printYieldExpr(e)
	case *ast.TemplateLitExpr:
		p.printTemplateLitExpr(e)
	case *ast.TaggedTemplateLitExpr:
		p.printTaggedTemplateLitExpr(e)
	case *ast.JSXElementExpr:
		p.writeString("/* JSX element */")
	case *ast.JSXFragmentExpr:
		p.writeString("/* JSX fragment */")
	case *ast.TypeCastExpr:
		p.printTypeCastExpr(e)
	case *ast.ArraySpreadExpr:
		p.writeString("...")
		p.printExpr(e.Value)
	default:
		p.writeString("/* unknown expression */")
	}
}

func (p *Printer) printBinaryExpr(expr *ast.BinaryExpr) {
	needsParens := p.needsParens(expr, expr.Left)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Left)
	if needsParens {
		p.writeString(")")
	}

	p.space()
	p.writeString(string(expr.Op))
	p.space()

	needsParens = p.needsParens(expr, expr.Right)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Right)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printUnaryExpr(expr *ast.UnaryExpr) {
	switch expr.Op {
	case ast.UnaryPlus:
		p.writeString("+")
	case ast.UnaryMinus:
		p.writeString("-")
	case ast.LogicalNot:
		p.writeString("!")
	}

	needsParens := p.needsParens(expr, expr.Arg)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Arg)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printBorrowExpr(expr *ast.BorrowExpr) {
	p.writeString("&")
	if expr.Mut {
		p.writeString("mut ")
	}
	needsParens := borrowExprArgNeedsParens(expr.Arg)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Arg)
	if needsParens {
		p.writeString(")")
	}
}

// borrowExprArgNeedsParens reports whether the operand of a borrow expression
// must be parenthesized to round-trip. A nested borrow `&&p` would re-lex as the
// AmpersandAmpersand token, so an inner BorrowExpr always needs parens. A
// BinaryExpr would re-parse as `(&a) + b` rather than `&(a + b)`, since the
// prefix `&` binds tighter than infix operators. UnaryExpr and value-producing
// control forms similarly re-associate against the prefix.
func borrowExprArgNeedsParens(e ast.Expr) bool {
	switch e.(type) {
	case *ast.BorrowExpr, *ast.BinaryExpr, *ast.UnaryExpr,
		*ast.IfElseExpr, *ast.IfValExpr, *ast.MatchExpr, *ast.DoExpr:
		return true
	default:
		return false
	}
}

// printReceiver renders a receiver, the expression a call reads its callee from and the
// one an index or a member access reads its object from. A receiver binds tighter than
// every operator, so an operator expression left bare in that position names a different
// expression than the tree holds. `(a + b).c` would print as `a + b.c`, which reads the
// property off `b`, and `(-a).c` would print as `-a.c`, which negates the property.
//
// An argument, an index, and a tuple element are already delimited by their own brackets,
// so those positions stay bare.
func (p *Printer) printReceiver(expr ast.Expr) {
	if !receiverNeedsParens(expr) {
		p.printExpr(expr)
		return
	}
	p.writeString("(")
	p.printExpr(expr)
	p.writeString(")")
}

// receiverNeedsParens reports whether an expression must be parenthesized to survive
// being reparsed in receiver position. Three families qualify.
//
//  1. A prefix form such as `-a`, `&a`, `await a`, or `a: number` binds looser than the
//     `.`, `[`, or `(` that follows it, so the receiver would attach to the operand
//     rather than to the whole form.
//  2. A form ending in a block, such as `if`, `match`, `do`, or a function expression,
//     would let the receiver read as part of that block instead.
//  3. A number literal takes the following `.` as its own decimal point, so `(5).toFixed()`
//     printed bare would lex as the number `5.` followed by the identifier `toFixed`.
func receiverNeedsParens(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.BinaryExpr, *ast.UnaryExpr, *ast.BorrowExpr, *ast.AwaitExpr,
		*ast.ThrowExpr, *ast.YieldExpr, *ast.TypeCastExpr, *ast.ArraySpreadExpr,
		*ast.IfElseExpr, *ast.IfValExpr, *ast.MatchExpr, *ast.TryCatchExpr,
		*ast.DoExpr, *ast.FuncExpr:
		return true
	case *ast.LiteralExpr:
		_, isNum := e.Lit.(*ast.NumLit)
		return isNum
	default:
		return false
	}
}

func (p *Printer) printCallExpr(expr *ast.CallExpr) {
	p.printReceiver(expr.Callee)
	if expr.OptChain {
		p.writeString("?(")
	} else {
		p.writeString("(")
	}

	for i, arg := range expr.Args {
		p.printExpr(arg)
		if i < len(expr.Args)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")
}

func (p *Printer) printIndexExpr(expr *ast.IndexExpr) {
	p.printReceiver(expr.Object)
	if expr.OptChain {
		p.writeString("?[")
	} else {
		p.writeString("[")
	}
	p.printExpr(expr.Index)
	p.writeString("]")
}

func (p *Printer) printMemberExpr(expr *ast.MemberExpr) {
	p.printReceiver(expr.Object)
	if expr.OptChain {
		p.writeString("?.")
	} else {
		p.writeString(".")
	}
	p.writeString(expr.Prop.Name)
}

func (p *Printer) printTupleExpr(expr *ast.TupleExpr) {
	p.writeString("[")
	for i, elem := range expr.Elems {
		p.printExpr(elem)
		if i < len(expr.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("]")
}

func (p *Printer) printObjectExpr(expr *ast.ObjectExpr) {
	if len(expr.Elems) == 0 {
		p.writeString("{}")
		return
	}

	p.writeString("{")
	p.newline()
	p.indent()

	for i, elem := range expr.Elems {
		p.printObjExprElem(elem)
		if i < len(expr.Elems)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printObjExprElem(elem ast.ObjExprElem) {
	switch e := elem.(type) {
	case *ast.CallableExpr:
		p.printFuncExpr(&e.Fn)
	case *ast.ConstructorExpr:
		p.writeString("new")
		p.printFuncExpr(&e.Fn)
	case *ast.PropertyExpr:
		if e.Readonly {
			p.writeString("readonly ")
		}
		p.printObjKey(e.Name)
		if e.Optional {
			p.writeString("?")
		}
		if e.Value != nil {
			p.writeString(": ")
			p.printExpr(e.Value)
		}
	case *ast.ObjSpreadExpr:
		p.writeString("...")
		p.printExpr(e.Value)
	}
}

func (p *Printer) printIfElseExpr(expr *ast.IfElseExpr) {
	p.writeString("if ")
	p.printExpr(expr.Cond)
	p.space()
	p.printBlock(&expr.Cons)

	if expr.Alt != nil {
		p.space()
		p.writeString("else ")
		if expr.Alt.Block != nil {
			p.printBlock(expr.Alt.Block)
		} else if expr.Alt.Expr != nil {
			p.printExpr(expr.Alt.Expr)
		}
	}
}

func (p *Printer) printIfValExpr(expr *ast.IfValExpr) {
	p.writeString("if val ")
	p.printPattern(expr.Pattern)
	p.writeString(" = ")
	p.printExpr(expr.Target)
	p.space()
	p.printBlock(&expr.Cons)

	if expr.Alt != nil {
		p.space()
		p.writeString("else ")
		if expr.Alt.Block != nil {
			p.printBlock(expr.Alt.Block)
		} else if expr.Alt.Expr != nil {
			p.printExpr(expr.Alt.Expr)
		}
	}
}

func (p *Printer) printMatchExpr(expr *ast.MatchExpr) {
	p.writeString("match ")
	p.printExpr(expr.Target)
	p.writeString(" {")
	p.newline()
	p.indent()

	for i, c := range expr.Cases {
		p.printPattern(c.Pattern)
		if c.Guard != nil {
			p.writeString(" if ")
			p.printExpr(c.Guard)
		}
		p.writeString(" => ")
		if c.Body.Block != nil {
			p.printBlock(c.Body.Block)
		} else if c.Body.Expr != nil {
			p.printExpr(c.Body.Expr)
		}
		if i < len(expr.Cases)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printTryCatchExpr(expr *ast.TryCatchExpr) {
	p.writeString("try ")
	p.printBlock(&expr.Try)

	if len(expr.Catch) > 0 {
		p.space()
		p.writeString("catch {")
		p.newline()
		p.indent()

		for i, c := range expr.Catch {
			p.printPattern(c.Pattern)
			if c.Guard != nil {
				p.writeString(" if ")
				p.printExpr(c.Guard)
			}
			p.writeString(" => ")
			if c.Body.Block != nil {
				p.printBlock(c.Body.Block)
			} else if c.Body.Expr != nil {
				p.printExpr(c.Body.Expr)
			}
			if i < len(expr.Catch)-1 {
				p.writeString(",")
			}
			p.newline()
		}

		p.dedent()
		p.writeString("}")
	}
}

func (p *Printer) printDoExpr(expr *ast.DoExpr) {
	p.writeString("do ")
	p.printBlock(&expr.Body)
}

func (p *Printer) printAwaitExpr(expr *ast.AwaitExpr) {
	p.writeString("await ")
	needsParens := p.needsParens(expr, expr.Arg)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Arg)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printThrowExpr(expr *ast.ThrowExpr) {
	p.writeString("throw ")
	p.printExpr(expr.Arg)
}

// printYieldExpr renders `yield`, `yield e`, and the delegating `yield from g`. A bare
// `yield` carries no operand, so it prints the keyword alone with no trailing space. The
// operand is parenthesized on the same rule the `await` printer applies, so a looser
// operand such as a binary expression keeps its grouping.
func (p *Printer) printYieldExpr(expr *ast.YieldExpr) {
	p.writeString("yield")
	if expr.IsDelegate {
		p.writeString(" from")
	}
	if expr.Value == nil {
		return
	}
	p.writeString(" ")
	needsParens := p.needsParens(expr, expr.Value)
	if needsParens {
		p.writeString("(")
	}
	p.printExpr(expr.Value)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printTemplateLitExpr(expr *ast.TemplateLitExpr) {
	p.writeString("`")
	for i, quasi := range expr.Quasis {
		p.writeString(quasi.Value)
		if i < len(expr.Exprs) {
			p.writeString("${")
			p.printExpr(expr.Exprs[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printTaggedTemplateLitExpr(expr *ast.TaggedTemplateLitExpr) {
	p.printReceiver(expr.Tag)
	p.writeString("`")
	for i, quasi := range expr.Quasis {
		p.writeString(quasi.Value)
		if i < len(expr.Exprs) {
			p.writeString("${")
			p.printExpr(expr.Exprs[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printTypeCastExpr(expr *ast.TypeCastExpr) {
	p.printExpr(expr.Expr)
	p.writeString(":")
	// Wrap union/intersection types in parentheses for clarity
	needsParens := needsTypeAnnParens(expr.TypeAnn)
	if needsParens {
		p.writeString("(")
	}
	p.printTypeAnn(expr.TypeAnn)
	if needsParens {
		p.writeString(")")
	}
}

func (p *Printer) printFuncExpr(expr *ast.FuncExpr) {
	if expr.Async {
		p.writeString("async ")
	}
	if expr.Gen {
		p.writeString("gen ")
	}
	p.writeString("fn ")
	p.printFuncSig(&expr.FuncSig)
	p.space()
	p.printBlock(expr.Body)
}

func (p *Printer) printFuncSig(sig *ast.FuncSig) {
	p.printGenericParams(sig.LifetimeParams, sig.TypeParams)

	p.writeString("(")
	for i, param := range sig.Params {
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
		if i < len(sig.Params)-1 {
			p.writeString(", ")
		}
	}
	if sig.Inexact {
		if len(sig.Params) > 0 {
			p.writeString(", ")
		}
		p.writeString("...")
	}
	p.writeString(")")

	p.printReturnAndThrows(sig.Return, sig.Throws)
}

func (p *Printer) printBlock(block *ast.Block) {
	p.writeString("{")
	if len(block.Stmts) > 0 {
		p.newline()
		p.indent()

		for _, stmt := range block.Stmts {
			if _, isErr := stmt.(*ast.ErrorStmt); isErr {
				continue
			}
			p.printStmt(stmt)
			p.newlineStmt()
		}

		p.dedent()
	}
	p.writeString("}")
}

// Pattern printing

func (p *Printer) printPattern(pat ast.Pat) {
	switch pt := pat.(type) {
	case *ast.IdentPat:
		if pt.Mutable {
			p.writeString("mut ")
		}
		p.writeString(pt.Name)
		if pt.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(pt.TypeAnn)
		}
		if pt.Default != nil {
			p.writeString(" = ")
			p.printExpr(pt.Default)
		}
	case *ast.ObjectPat:
		p.printObjectPattern(pt)
	case *ast.TuplePat:
		p.printTuplePattern(pt)
	case *ast.ExtractorPat:
		p.printExtractorPattern(pt)
	case *ast.InstancePat:
		p.printInstancePattern(pt)
	case *ast.RestPat:
		p.writeString("...")
		p.printPattern(pt.Pattern)
	case *ast.LitPat:
		p.printLiteral(pt.Lit)
	case *ast.WildcardPat:
		p.writeString("_")
	default:
		p.writeString("/* unknown pattern */")
	}
}

func (p *Printer) printObjectPattern(pat *ast.ObjectPat) {
	p.writeString("{")
	for i, elem := range pat.Elems {
		switch e := elem.(type) {
		case *ast.ObjKeyValuePat:
			p.writeString(e.Key.Name)
			p.writeString(": ")
			p.printPattern(e.Value)
		case *ast.ObjShorthandPat:
			if e.Mutable {
				p.writeString("mut ")
			}
			p.writeString(e.Key.Name)
			if e.TypeAnn != nil {
				p.writeString(": ")
				p.printTypeAnn(e.TypeAnn)
			}
			if e.Default != nil {
				p.writeString(" = ")
				p.printExpr(e.Default)
			}
		case *ast.ObjRestPat:
			p.writeString("...")
			p.printPattern(e.Pattern)
		}
		if i < len(pat.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("}")
}

func (p *Printer) printTuplePattern(pat *ast.TuplePat) {
	p.writeString("[")
	for i, elem := range pat.Elems {
		p.printPattern(elem)
		if i < len(pat.Elems)-1 {
			p.writeString(", ")
		}
	}
	p.writeString("]")
}

func (p *Printer) printExtractorPattern(pat *ast.ExtractorPat) {
	p.printQualIdent(pat.Name)
	p.writeString("(")
	for i, arg := range pat.Args {
		p.printPattern(arg)
		if i < len(pat.Args)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(")")
}

func (p *Printer) printInstancePattern(pat *ast.InstancePat) {
	p.printQualIdent(pat.ClassName)
	p.space()
	p.printPattern(pat.Object)
}

// Type annotation printing

func (p *Printer) printTypeAnn(typ ast.TypeAnn) {
	switch t := typ.(type) {
	case *ast.LitTypeAnn:
		p.printLiteral(t.Lit)
	case *ast.NumberTypeAnn:
		p.writeString("number")
	case *ast.StringTypeAnn:
		p.writeString("string")
	case *ast.BooleanTypeAnn:
		p.writeString("boolean")
	case *ast.SymbolTypeAnn:
		p.writeString("symbol")
	case *ast.UniqueSymbolTypeAnn:
		p.writeString("unique symbol")
	case *ast.BigintTypeAnn:
		p.writeString("bigint")
	case *ast.AnyTypeAnn:
		p.writeString("any")
	case *ast.UnknownTypeAnn:
		p.writeString("unknown")
	case *ast.NeverTypeAnn:
		p.writeString("never")
	case *ast.ObjectTypeAnn:
		p.printObjectTypeAnn(t)
	case *ast.TupleTypeAnn:
		p.printTupleTypeAnn(t)
	case *ast.UnionTypeAnn:
		p.printUnionTypeAnn(t)
	case *ast.IntersectionTypeAnn:
		p.printIntersectionTypeAnn(t)
	case *ast.TypeRefTypeAnn:
		p.printTypeRefTypeAnn(t)
	case *ast.FuncTypeAnn:
		p.printFuncTypeAnn(t)
	case *ast.KeyOfTypeAnn:
		p.writeString("keyof ")
		p.printPrefixTypeAnnOperand(t.Type)
	case *ast.NegationTypeAnn:
		p.writeString("~")
		p.printPrefixTypeAnnOperand(t.Type)
	case *ast.TypeOfTypeAnn:
		p.writeString("typeof ")
		p.printQualIdent(t.Value)
	case *ast.IndexTypeAnn:
		// An indexed access reads from a target that binds as tightly as a type
		// reference, so anything carrying an operator has to be wrapped. `(keyof A)[B]`
		// printed bare would reparse as `keyof (A[B])`.
		p.printTypeAnnAt(t.Target, precTypePrimary)
		p.writeString("[")
		p.printTypeAnn(t.Index)
		p.writeString("]")
	case *ast.CondTypeAnn:
		p.writeString("if ")
		p.printTypeAnn(t.Check)
		p.writeString(" : ")
		p.printTypeAnn(t.Extends)
		p.writeString(" { ")
		p.printTypeAnn(t.Then)
		p.writeString(" } else { ")
		p.printTypeAnn(t.Else)
		p.writeString(" }")
	case *ast.InferTypeAnn:
		p.writeString("infer ")
		p.writeString(t.Name)
	case *ast.WildcardTypeAnn:
		p.writeString("_")
	case *ast.TemplateLitTypeAnn:
		p.printTemplateLitTypeAnn(t)
	case *ast.IntrinsicTypeAnn:
		p.writeString("intrinsic")
	case *ast.ImportTypeAnn:
		p.writeString("import(\"")
		p.writeString(t.Source)
		p.writeString("\")")
		if t.Qualifier != nil {
			p.writeString(".")
			p.printQualIdent(t.Qualifier)
		}
		if len(t.TypeArgs) > 0 {
			p.writeString("<")
			for i, arg := range t.TypeArgs {
				p.printTypeAnn(arg)
				if i < len(t.TypeArgs)-1 {
					p.writeString(", ")
				}
			}
			p.writeString(">")
		}
	case *ast.MatchTypeAnn:
		p.printMatchTypeAnn(t)
	case *ast.MutableTypeAnn:
		p.writeString("mut ")
		p.printPrefixTypeAnnOperand(t.Target)
	case *ast.RefTypeAnn:
		p.printRefTypeAnn(t)
	case *ast.ErrorTypeAnn:
		// Skip error recovery nodes
	case *ast.RestSpreadTypeAnn:
		p.writeString("...")
		p.printTypeAnn(t.Value)
	default:
		p.writeString("/* unknown type */")
	}
}

func (p *Printer) printObjectTypeAnn(typ *ast.ObjectTypeAnn) {
	if len(typ.Elems) == 0 {
		if typ.Inexact {
			p.writeString("{...}")
		} else {
			p.writeString("{}")
		}
		return
	}

	p.writeString("{")
	p.newline()
	p.indent()

	for i, elem := range typ.Elems {
		p.printObjTypeAnnElem(elem)
		if i < len(typ.Elems)-1 || typ.Inexact {
			p.writeString(",")
		}
		p.newline()
	}
	if typ.Inexact {
		p.writeString("...")
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

func (p *Printer) printObjTypeAnnElem(elem ast.ObjTypeAnnElem) {
	if doc := elem.Doc(); doc != "" {
		p.writeDoc(doc)
	}
	switch e := elem.(type) {
	case *ast.CallableTypeAnn:
		p.printFuncTypeAnn(e.Fn)
	case *ast.ConstructorTypeAnn:
		p.writeString("new")
		p.printFuncTypeAnnTail(e.Fn)
	case *ast.MethodTypeAnn:
		p.printObjKey(e.Name)
		p.printGenericParams(e.Fn.LifetimeParams, e.Fn.TypeParams)
		p.printAnnMemberParams(annMemberReceiver(e.Receiver, ""), e.Fn.Params)
		p.printReturnAndThrows(e.Fn.Return, e.Fn.Throws)
	case *ast.GetterTypeAnn:
		p.writeString("get ")
		p.printObjKey(e.Name)
		p.printAnnMemberParams(annMemberReceiver(e.Receiver, "self"), nil)
		p.printReturnAndThrows(e.Fn.Return, e.Fn.Throws)
	case *ast.SetterTypeAnn:
		p.writeString("set ")
		p.printObjKey(e.Name)
		p.printAnnMemberParams(annMemberReceiver(e.Receiver, "mut self"), e.Fn.Params)
		p.printReturnAndThrows(e.Fn.Return, e.Fn.Throws)
	case *ast.PropertyTypeAnn:
		if e.Readonly {
			p.writeString("readonly ")
		}
		p.printObjKey(e.Name)
		if e.Optional {
			p.writeString("?")
		}
		p.writeString(": ")
		p.printTypeAnn(e.Value)
	case *ast.MappedTypeAnn:
		// Print readonly modifier if present
		if e.ReadOnly != nil {
			if *e.ReadOnly == ast.MMAdd {
				p.writeString("readonly ")
			} else if *e.ReadOnly == ast.MMRemove {
				p.writeString("-readonly ")
			}
		}
		// Print [name], or [key: constraint] for the shorthand. This echoes the spelling the
		// source used rather than normalizing to one, the way a formatter preserves the input.
		p.writeString("[")
		switch {
		case e.Shorthand:
			p.writeString(e.TypeParam.Name)
			p.writeString(": ")
			p.printTypeAnn(e.TypeParam.Constraint)
		case e.Name != nil:
			p.printTypeAnn(e.Name)
		default:
			p.writeString(e.TypeParam.Name)
		}
		p.writeString("]")
		// Print optional modifier. The shorthand spells the adding form `?`, the long form `+?`.
		if e.Optional != nil {
			if *e.Optional == ast.MMAdd {
				if e.Shorthand {
					p.writeString("?")
				} else {
					p.writeString("+?")
				}
			} else if *e.Optional == ast.MMRemove {
				p.writeString("-?")
			}
		}
		// Print : value
		p.writeString(": ")
		p.printTypeAnn(e.Value)
		// The shorthand already wrote the key and its constraint inside the brackets
		if !e.Shorthand {
			p.writeString(" for ")
			p.writeString(e.TypeParam.Name)
			p.writeString(" in ")
			p.printTypeAnn(e.TypeParam.Constraint)
		}
		// Print if clause if present
		if e.Check != nil && e.Extends != nil {
			p.writeString(" if ")
			p.printTypeAnn(e.Check)
			p.writeString(" : ")
			p.printTypeAnn(e.Extends)
		}
	case *ast.RestSpreadTypeAnn:
		p.writeString("...")
		p.printTypeAnn(e.Value)
	}
}

func (p *Printer) printTupleTypeAnn(typ *ast.TupleTypeAnn) {
	p.writeString("[")
	for i, elem := range typ.Elems {
		p.printTypeAnn(elem)
		if i < len(typ.Elems)-1 {
			p.writeString(", ")
		}
	}
	if typ.Inexact {
		if len(typ.Elems) > 0 {
			p.writeString(", ")
		}
		p.writeString("...")
	}
	p.writeString("]")
}

// Binding power of an Escalier type operator. A larger number binds tighter, so
// `A | B & C` groups as `A | (B & C)`. The scale ranks only the operators whose grouping
// the surface syntax can change. A conditional type and a match type end in a closing
// brace, so neither can be pulled apart and both rank as primary.
const (
	// precTypeOpenEnded covers the two forms that have no closing delimiter of their own,
	// the function type `fn (a: A) -> B` and the rest spread `...A`. Each reaches as far
	// right as the syntax allows, which is why `fn () -> A | B` reads as
	// `fn () -> (A | B)` and either form inside a union or an intersection has to be
	// wrapped.
	precTypeOpenEnded    = 1
	precTypeUnion        = 2
	precTypeIntersection = 3
	// precTypePrefix covers `keyof A`, `mut A`, `&A`, `infer A`, and `~A`. Each takes the
	// whole annotation that follows it.
	precTypePrefix = 4
	// precTypePrimary is for an annotation that nothing can regroup, such as a type
	// reference, an object type, a tuple, or an indexed access.
	precTypePrimary = 5
)

// typeAnnPrecedence returns the binding power of a type annotation's top-level operator.
func typeAnnPrecedence(t ast.TypeAnn) int {
	switch t.(type) {
	case *ast.FuncTypeAnn, *ast.RestSpreadTypeAnn:
		return precTypeOpenEnded
	case *ast.UnionTypeAnn:
		return precTypeUnion
	case *ast.IntersectionTypeAnn:
		return precTypeIntersection
	case *ast.KeyOfTypeAnn, *ast.MutableTypeAnn, *ast.RefTypeAnn, *ast.InferTypeAnn, *ast.NegationTypeAnn:
		return precTypePrefix
	default:
		return precTypePrimary
	}
}

// printTypeAnnAt prints a type annotation, parenthesizing it when its own operator binds
// looser than minPrec. Callers pass the binding power the surrounding position demands.
// An intersection member demands precTypeIntersection, so `(number | string) & boolean`
// keeps its parentheses rather than reprinting as `number | string & boolean`, which
// reparses as `number | (string & boolean)`.
func (p *Printer) printTypeAnnAt(t ast.TypeAnn, minPrec int) {
	if typeAnnPrecedence(t) >= minPrec {
		p.printTypeAnn(t)
		return
	}
	p.writeString("(")
	p.printTypeAnn(t)
	p.writeString(")")
}

func (p *Printer) printUnionTypeAnn(typ *ast.UnionTypeAnn) {
	for i, t := range typ.Types {
		if i > 0 {
			p.writeString(" | ")
		}
		p.printTypeAnnAt(t, precTypeUnion)
	}
}

func (p *Printer) printIntersectionTypeAnn(typ *ast.IntersectionTypeAnn) {
	for i, t := range typ.Types {
		if i > 0 {
			p.writeString(" & ")
		}
		p.printTypeAnnAt(t, precTypeIntersection)
	}
}

func (p *Printer) printTypeRefTypeAnn(typ *ast.TypeRefTypeAnn) {
	p.printQualIdent(typ.Name)
	if len(typ.TypeArgs) > 0 {
		p.writeString("<")
		for i, arg := range typ.TypeArgs {
			p.printTypeAnn(arg)
			if i < len(typ.TypeArgs)-1 {
				p.writeString(", ")
			}
		}
		p.writeString(">")
	}
}

// printRefTypeAnn renders a prefix borrow: `&`, `&mut`, `&'a`, `&'a mut`.
// The `&` binds tighter than union and intersection, so a union or
// intersection inner is parenthesized to keep `&(A | B)` distinct from the
// `(&A) | B` that an unparenthesized `&A | B` denotes.
func (p *Printer) printRefTypeAnn(typ *ast.RefTypeAnn) {
	p.writeString("&")
	if typ.Lifetime != nil {
		p.printLifetimeAnn(typ.Lifetime)
		p.writeString(" ")
	}
	if typ.Mut {
		p.writeString("mut ")
	}
	needsParens := borrowInnerNeedsParens(typ.Inner)
	if needsParens {
		p.writeString("(")
	}
	p.printTypeAnn(typ.Inner)
	if needsParens {
		p.writeString(")")
	}
}

// borrowInnerNeedsParens reports whether the inner of a prefix borrow must be
// parenthesized to round-trip. A borrow prints as `& [lifetime] [mut] Inner`,
// so an Inner whose own leading token would re-associate into the borrow needs
// wrapping. A `mut A` inner would otherwise print as `&mut A` and re-parse as a
// mutable borrow of `A`. A nested borrow `&A` would print as `&&A`, which the
// lexer reads as a single `&&` token. Union and intersection bind looser than
// the prefix `&`, the same reason needsTypeAnnParens already gives.
func borrowInnerNeedsParens(typ ast.TypeAnn) bool {
	switch typ.(type) {
	case *ast.MutableTypeAnn, *ast.RefTypeAnn:
		return true
	default:
		return needsTypeAnnParens(typ)
	}
}

// needsTypeAnnParens reports whether a type annotation must be parenthesized
// when it appears as the operand of a prefix such as a borrow `&`, a `mut`, a
// `keyof`, or a type cast. A prefix takes the whole annotation that follows it,
// so only the two infix operators can be pulled apart by what surrounds the
// prefix. `keyof A | B` reads as `(keyof A) | B`, which is why a union operand
// needs wrapping, while `keyof fn () -> A` already reads the way the tree holds.
func needsTypeAnnParens(typ ast.TypeAnn) bool {
	switch typ.(type) {
	case *ast.UnionTypeAnn, *ast.IntersectionTypeAnn:
		return true
	default:
		return false
	}
}

// printPrefixTypeAnnOperand prints the operand of a `keyof` or a `mut` on the rule
// needsTypeAnnParens states.
func (p *Printer) printPrefixTypeAnnOperand(t ast.TypeAnn) {
	if !needsTypeAnnParens(t) {
		p.printTypeAnn(t)
		return
	}
	p.writeString("(")
	p.printTypeAnn(t)
	p.writeString(")")
}

// printLifetimeAnn renders a lifetime annotation node, the `'a` in `'a Point`.
func (p *Printer) printLifetimeAnn(lt ast.LifetimeAnnNode) {
	switch lt := lt.(type) {
	case *ast.LifetimeAnn:
		p.writeString("'")
		p.writeString(lt.Name)
	}
}

func (p *Printer) printFuncTypeAnn(typ *ast.FuncTypeAnn) {
	p.writeString("fn")
	p.printFuncTypeAnnTail(typ)
}

// printFuncTypeAnnTail renders a function signature after its leading keyword: the generic
// parameters, the parameter list, the return, and any `throws` clause. The `fn` annotation
// and an object type's `new (…) -> T` member share it, mirroring the parser's split.
func (p *Printer) printFuncTypeAnnTail(typ *ast.FuncTypeAnn) {
	p.printGenericParams(typ.LifetimeParams, typ.TypeParams)
	p.writeString(" (")
	for i, param := range typ.Params {
		p.printPattern(param.Pattern)
		if param.Optional {
			p.writeString("?")
		}
		if param.TypeAnn != nil {
			p.writeString(": ")
			p.printTypeAnn(param.TypeAnn)
		}
		if i < len(typ.Params)-1 {
			p.writeString(", ")
		}
	}
	if typ.Inexact {
		if len(typ.Params) > 0 {
			p.writeString(", ")
		}
		p.writeString("...")
	}
	p.writeString(")")

	p.printReturnAndThrows(typ.Return, typ.Throws)
}

func (p *Printer) printTemplateLitTypeAnn(typ *ast.TemplateLitTypeAnn) {
	p.writeString("`")
	for i, quasi := range typ.Quasis {
		p.writeString(quasi.Value)
		if i < len(typ.TypeAnns) {
			p.writeString("${")
			p.printTypeAnn(typ.TypeAnns[i])
			p.writeString("}")
		}
	}
	p.writeString("`")
}

func (p *Printer) printMatchTypeAnn(typ *ast.MatchTypeAnn) {
	p.writeString("match ")
	p.printTypeAnn(typ.Target)
	p.writeString(" {")
	p.newline()
	p.indent()

	for i, c := range typ.Cases {
		p.printTypeAnn(c.Extends)
		p.writeString(" => ")
		p.printTypeAnn(c.Cons)
		if i < len(typ.Cases)-1 {
			p.writeString(",")
		}
		p.newline()
	}

	p.dedent()
	p.writeString("}")
}

// Literal printing

func (p *Printer) printLiteral(lit ast.Lit) {
	switch l := lit.(type) {
	case *ast.BoolLit:
		if l.Value {
			p.writeString("true")
		} else {
			p.writeString("false")
		}
	case *ast.NumLit:
		p.writeString(formatNumber(l.Value))
	case *ast.StrLit:
		p.writeString(strconv.Quote(l.Value))
	case *ast.RegexLit:
		p.writeString(l.Value)
	case *ast.BigIntLit:
		p.writeString(l.Value.String())
		p.writeString("n")
	case *ast.NullLit:
		p.writeString("null")
	case *ast.UndefinedLit:
		p.writeString("undefined")
	default:
		p.writeString("/* unknown literal */")
	}
}

// Helper methods

// printVarianceModifier writes a type parameter's `in`/`out`/`in out` modifier
// followed by a space, or nothing when the parameter carries no modifier.
func (p *Printer) printVarianceModifier(v ast.VarianceModifier) {
	switch v {
	case ast.VarianceOut:
		p.writeString("out ")
	case ast.VarianceIn:
		p.writeString("in ")
	case ast.VarianceInOut:
		p.writeString("in out ")
	}
}

func (p *Printer) printTypeParams(params []*ast.TypeParam) {
	p.writeString("<")
	for i, param := range params {
		p.printVarianceModifier(param.Variance)
		p.writeString(param.Name)
		if param.Constraint != nil {
			p.writeString(": ")
			p.printTypeAnn(param.Constraint)
		}
		if param.Default != nil {
			p.writeString(" = ")
			p.printTypeAnn(param.Default)
		}
		if i < len(params)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(">")
}

// printGenericParams renders the combined lifetime and type quantifier list
// `<'a, 'b: 'a, T>`, lifetime binders first then type binders. It writes
// nothing when both lists are empty. A lifetime binder's outlives bounds are
// joined with ` & `, so 'a bounded by 'b and 'c renders as `'a: 'b & 'c`.
func (p *Printer) printGenericParams(lifetimeParams []*ast.LifetimeParam, typeParams []*ast.TypeParam) {
	if len(lifetimeParams) == 0 {
		if len(typeParams) > 0 {
			p.printTypeParams(typeParams)
		}
		return
	}
	p.writeString("<")
	for i, lp := range lifetimeParams {
		p.writeString("'")
		p.writeString(lp.Name)
		for j, bound := range lp.Bounds {
			if j == 0 {
				p.writeString(": ")
			} else {
				p.writeString(" & ")
			}
			p.writeString("'")
			p.writeString(bound.Name)
		}
		if i < len(lifetimeParams)-1 || len(typeParams) > 0 {
			p.writeString(", ")
		}
	}
	for i, tp := range typeParams {
		p.printVarianceModifier(tp.Variance)
		p.writeString(tp.Name)
		if tp.Constraint != nil {
			p.writeString(": ")
			p.printTypeAnn(tp.Constraint)
		}
		if tp.Default != nil {
			p.writeString(" = ")
			p.printTypeAnn(tp.Default)
		}
		if i < len(typeParams)-1 {
			p.writeString(", ")
		}
	}
	p.writeString(">")
}

func (p *Printer) printQualIdent(qi ast.QualIdent) {
	switch q := qi.(type) {
	case *ast.Ident:
		p.writeString(q.Name)
	case *ast.Member:
		p.printQualIdent(q.Left)
		p.writeString(".")
		p.writeString(q.Right.Name)
	}
}

func (p *Printer) printObjKey(key ast.ObjKey) {
	switch k := key.(type) {
	case *ast.IdentExpr:
		p.writeString(k.Name)
	case *ast.StrLit:
		p.writeString(strconv.Quote(k.Value))
	case *ast.NumLit:
		p.writeString(formatNumber(k.Value))
	case *ast.ComputedKey:
		p.writeString("[")
		p.printExpr(k.Expr)
		p.writeString("]")
	}
}

func (p *Printer) needsParens(parent ast.Expr, child ast.Expr) bool {
	// Check if child needs parens based on its relationship to parent
	switch childExpr := child.(type) {
	case *ast.BinaryExpr:
		// If parent is also a binary expression, compare precedence
		if parentBinary, ok := parent.(*ast.BinaryExpr); ok {
			parentPrec := ast.Precedence[parentBinary.Op]
			childPrec := ast.Precedence[childExpr.Op]

			// Need parens if child has lower precedence than parent
			if childPrec < parentPrec {
				return true
			}

			// For equal precedence, check associativity and position
			if childPrec == parentPrec {
				// If child is on the right side
				if parentBinary.Right == child {
					// For non-associative operators (-, /), always need parens on right
					// e.g., a - (b - c) != a - b - c
					if parentBinary.Op == ast.Minus || parentBinary.Op == ast.Divide {
						return true
					}
					// For different operators at same precedence, need parens
					// e.g., a + (b - c) vs a + b - c (though parser may handle this)
					if parentBinary.Op != childExpr.Op {
						return true
					}
					// For same associative operator (like +), parens on right are optional
					// but if they're in the AST, we should preserve them for clarity
					// However, a + (b + c) is equivalent to a + b + c
					// The parser already groups them, so if they're separate nodes, keep separate
				}
				// If child is on the left side with same precedence, generally no parens needed
				// because of left-associativity: (a + b) + c == a + b + c
			}

			return false
		}
		// If parent is unary, binary expressions need parens
		if _, ok := parent.(*ast.UnaryExpr); ok {
			return true
		}
		// Otherwise no parens needed
		return false

	case *ast.UnaryExpr:
		// Unary expressions need parens if parent is also unary
		if _, ok := parent.(*ast.UnaryExpr); ok {
			return true
		}
		return false

	case *ast.IfElseExpr, *ast.MatchExpr:
		// These always need parens when used as operands
		return true

	default:
		return false
	}
}

func formatNumber(f float64) string {
	// Format the number, removing unnecessary trailing zeros
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return s
}

// Public API

// Print prints an AST node to a string
func Print(node ast.Node, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)

	switch n := node.(type) {
	case *ast.Script:
		if err := printer.PrintScript(n); err != nil {
			return "", err
		}
	case ast.Expr:
		printer.printExpr(n)
	case ast.Stmt:
		printer.printStmt(n)
	case ast.Decl:
		printer.printDecl(n)
	case ast.Pat:
		printer.printPattern(n)
	case ast.TypeAnn:
		printer.printTypeAnn(n)
	default:
		return "", fmt.Errorf("unsupported node type: %T", node)
	}

	return builder.String(), nil
}

// PrintBlock prints a block, braces included. A block carries no Span method, so it is not
// an ast.Node and Print cannot reach it.
func PrintBlock(block *ast.Block, opts Options) (string, error) {
	if block == nil {
		return "", fmt.Errorf("cannot print a nil block")
	}
	var builder strings.Builder
	NewPrinter(&builder, opts).printBlock(block)
	return builder.String(), nil
}

// PrintClassElem prints one class member — a field, method, getter,
// setter, or constructor — without the enclosing class body. Print
// dispatches over the six node families a whole declaration is built
// from and a member is not one of them, so it cannot reach a ClassElem.
// The output carries no leading indent and no trailing separator. The
// caller places both.
func PrintClassElem(elem ast.ClassElem, opts Options) (string, error) {
	if isNilMember(elem) {
		return "", fmt.Errorf("cannot print a nil class member")
	}
	var builder strings.Builder
	NewPrinter(&builder, opts).printClassElem(elem)
	return builder.String(), nil
}

// PrintObjTypeAnnElem prints one member of an object type annotation or
// interface body, without the enclosing braces. An ObjTypeAnnElem is
// not an ast.Node at all — it declares no Span — so this is the only
// way to print one on its own. Same no-indent, no-separator contract as
// PrintClassElem.
func PrintObjTypeAnnElem(elem ast.ObjTypeAnnElem, opts Options) (string, error) {
	if isNilMember(elem) {
		return "", fmt.Errorf("cannot print a nil object type member")
	}
	var builder strings.Builder
	NewPrinter(&builder, opts).printObjTypeAnnElem(elem)
	return builder.String(), nil
}

// isNilMember reports whether a member interface holds nothing to
// print. Every ClassElem and ObjTypeAnnElem variant is a pointer type,
// so an interface can carry a typed nil: `elem == nil` reports false
// for it and the first method call then panics. A caller handing over
// an absent member gets the same error either way.
func isNilMember(elem any) bool {
	if elem == nil {
		return true
	}
	value := reflect.ValueOf(elem)
	return value.Kind() == reflect.Ptr && value.IsNil()
}

// PrintToWriter prints an AST node to an io.Writer
func PrintToWriter(node ast.Node, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)

	switch n := node.(type) {
	case *ast.Script:
		return printer.PrintScript(n)
	case ast.Expr:
		printer.printExpr(n)
		return nil
	case ast.Stmt:
		printer.printStmt(n)
		return nil
	case ast.Decl:
		printer.printDecl(n)
		return nil
	case ast.Pat:
		printer.printPattern(n)
		return nil
	case ast.TypeAnn:
		printer.printTypeAnn(n)
		return nil
	default:
		return fmt.Errorf("unsupported node type: %T", node)
	}
}

// PrintScript prints a Script to a string
func PrintScript(script *ast.Script, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)
	if err := printer.PrintScript(script); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// PrintScriptToWriter prints a Script to an io.Writer
func PrintScriptToWriter(script *ast.Script, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)
	return printer.PrintScript(script)
}

// PrintModule prints a Module to a string
func PrintModule(module *ast.Module, opts Options) (string, error) {
	var builder strings.Builder
	printer := NewPrinter(&builder, opts)
	if err := printer.PrintModule(module); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// PrintModuleToWriter prints a Module to an io.Writer
func PrintModuleToWriter(module *ast.Module, writer io.Writer, opts Options) error {
	printer := NewPrinter(writer, opts)
	return printer.PrintModule(module)
}

// printSuperCallExpr prints `super(<args>)`, the call that runs the superclass constructor.
func (p *Printer) printSuperCallExpr(e *ast.SuperCallExpr) {
	p.writeString("super(")
	for i, arg := range e.Args {
		if i > 0 {
			p.writeString(", ")
		}
		p.printExpr(arg)
	}
	p.writeString(")")
}
