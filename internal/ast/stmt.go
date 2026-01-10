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
	Specifiers []*ImportSpecifier
	ModulePath string
	span       Span
}

func NewImportStmt(specifiers []*ImportSpecifier, modulePath string, span Span) *ImportStmt {
	return &ImportStmt{Specifiers: specifiers, ModulePath: modulePath, span: span}
}
func (*ImportStmt) isStmt()      {}
func (s *ImportStmt) Span() Span { return s.span }
func (s *ImportStmt) Accept(v Visitor) {
	if v.EnterStmt(s) {
		// Import statements don't have nested expressions to visit
	}
	v.ExitStmt(s)
}
