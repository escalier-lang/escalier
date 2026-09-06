package ast

import "strings"

//sumtype:decl
type Stmt interface {
	isStmt()
	Node
}

type ExprStmt struct {
	Expr Expr
	span Span
	commentSlots
}

func NewExprStmt(expr Expr, span Span) *ExprStmt {
	return &ExprStmt{Expr: expr, span: span, commentSlots: commentSlots{}}
}
func (*ExprStmt) isStmt()      {}
func (s *ExprStmt) Span() Span { return s.span }
func (s *ExprStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		s.Expr.Accept(v)
	}
	v.ExitStmt(s)
}

type DeclStmt struct {
	Decl Decl
	span Span
	commentSlots
}

func NewDeclStmt(decl Decl, span Span) *DeclStmt {
	return &DeclStmt{Decl: decl, span: span, commentSlots: commentSlots{}}
}
func (*DeclStmt) isStmt()      {}
func (s *DeclStmt) Span() Span { return s.span }
func (s *DeclStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		s.Decl.Accept(v)
	}
	v.ExitStmt(s)
}

type ReturnStmt struct {
	Expr Expr // optional
	span Span
	commentSlots
}

func NewReturnStmt(expr Expr, span Span) *ReturnStmt {
	return &ReturnStmt{Expr: expr, span: span, commentSlots: commentSlots{}}
}
func (*ReturnStmt) isStmt()      {}
func (s *ReturnStmt) Span() Span { return s.span }
func (s *ReturnStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		if s.Expr != nil {
			s.Expr.Accept(v)
		}
	}
	v.ExitStmt(s)
}

// ImportSpecifier represents a single import specifier
// For named imports: { foo, bar as baz }
// For namespace imports: * as ns
// ImportStmt is `import "uri"` with an optional `as name`, the one import form
// Escalier has. It binds the package as a namespace and members are reached
// through it. There is no named specifier: a member is never bound directly.
type ImportStmt struct {
	PackageName string   // module specifier without the `?flag` suffix, e.g. "lodash", "std:math"
	Alias       string   // the `as name` binding, or "" to derive one from PackageName
	Flags       []string // `?flag1&flag2` suffix parsed into a list, preserving order; nil if none
	span        Span
	commentSlots
}

func NewImportStmt(packageName, alias string, flags []string, span Span) *ImportStmt {
	return &ImportStmt{PackageName: packageName, Alias: alias, Flags: flags, span: span, commentSlots: commentSlots{}}
}

// LocalName returns the name this import binds the package under: the alias
// when one is written, and otherwise a name derived from the specifier.
func (s *ImportStmt) LocalName() string {
	if s.Alias != "" {
		return s.Alias
	}
	return DeriveImportName(s.PackageName)
}

// DeriveImportName turns a module specifier into the name a bare import binds
// it under: the last path segment, with any scheme dropped, mapped to an
// identifier. `std:math` gives `math` and `lodash/fp` gives `fp`.
//
// An npm package name is not an identifier in general, so every character an
// identifier cannot hold becomes an underscore and a leading digit gains one:
// `fast-deep-equal` gives `fast_deep_equal` and `package-1` gives `package_1`.
// Writing `as name` is how an author picks something else.
func DeriveImportName(specifier string) string {
	name := specifier
	if _, pkg, ok := strings.Cut(name, ":"); ok {
		name = pkg
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return ""
	}

	var b strings.Builder
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
			b.WriteByte(c)
		case c >= '0' && c <= '9':
			// A leading digit would not lex as an identifier, so the derived
			// name opens with an underscore instead.
			if b.Len() == 0 {
				b.WriteByte('_')
			}
			b.WriteByte(c)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}
func (*ImportStmt) isStmt()      {}
func (s *ImportStmt) Span() Span { return s.span }
func (s *ImportStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		// Import statements don't have nested expressions to visit
	}
	v.ExitStmt(s)
}

type ErrorStmt struct {
	span Span
	commentSlots
}

func NewErrorStmt(span Span) *ErrorStmt {
	return &ErrorStmt{span: span, commentSlots: commentSlots{}}
}
func (*ErrorStmt) isStmt()      {}
func (s *ErrorStmt) Span() Span { return s.span }
func (s *ErrorStmt) Accept(v Visitor) {
	v.EnterStmt(s)
	v.ExitStmt(s)
}

type ForInStmt struct {
	Pattern  Pat   // Loop variable pattern (supports destructuring)
	Iterable Expr  // Expression being iterated
	Body     Block // Loop body
	IsAwait  bool  // true for `for await...in`
	span     Span
	commentSlots
}

func NewForInStmt(pattern Pat, iterable Expr, body Block, isAwait bool, span Span) *ForInStmt {
	return &ForInStmt{
		Pattern:      pattern,
		Iterable:     iterable,
		Body:         body,
		IsAwait:      isAwait,
		span:         span,
		commentSlots: commentSlots{},
	}
}

func (*ForInStmt) isStmt()      {}
func (s *ForInStmt) Span() Span { return s.span }
func (s *ForInStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		s.Pattern.Accept(v)
		s.Iterable.Accept(v)
		s.Body.Accept(v)
	}
	v.ExitStmt(s)
}
