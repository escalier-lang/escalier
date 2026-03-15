package ast

//sumtype:decl
type Stmt interface {
	isStmt()
	Node
}

type ExprStmt struct {
	Expr Expr
	span Span
}

func NewExprStmt(expr Expr, span Span) *ExprStmt {
	return &ExprStmt{Expr: expr, span: span}
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
}

func NewDeclStmt(decl Decl, span Span) *DeclStmt {
	return &DeclStmt{Decl: decl, span: span}
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
}

func NewReturnStmt(expr Expr, span Span) *ReturnStmt {
	return &ReturnStmt{Expr: expr, span: span}
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
type ImportSpecifier struct {
	Name  string // The name being imported (or "*" for namespace imports)
	Alias string // The local name (optional for named imports, required for namespace imports)
	span  Span
}

func NewImportSpecifier(name, alias string, span Span) *ImportSpecifier {
	return &ImportSpecifier{Name: name, Alias: alias, span: span}
}
func (i *ImportSpecifier) Span() Span { return i.span }

type ImportStmt struct {
	Specifiers  []*ImportSpecifier
	PackageName string // e.g., "lodash", "@types/node", "lodash/fp"
	span        Span
}

func NewImportStmt(specifiers []*ImportSpecifier, packageName string, span Span) *ImportStmt {
	return &ImportStmt{Specifiers: specifiers, PackageName: packageName, span: span}
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
}

func NewErrorStmt(span Span) *ErrorStmt {
	return &ErrorStmt{span: span}
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
}

func NewForInStmt(pattern Pat, iterable Expr, body Block, isAwait bool, span Span) *ForInStmt {
	return &ForInStmt{
		Pattern:  pattern,
		Iterable: iterable,
		Body:     body,
		IsAwait:  isAwait,
		span:     span,
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
