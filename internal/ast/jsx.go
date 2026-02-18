package ast

type JSXOpening struct {
	Name      QualIdent // nil for fragments
	Attrs     []JSXAttrElem
	SelfClose bool
	span      Span
}

func NewJSXOpening(name QualIdent, attrs []JSXAttrElem, selfClose bool, span Span) *JSXOpening {
	return &JSXOpening{Name: name, Attrs: attrs, SelfClose: selfClose, span: span}
}
func (n *JSXOpening) Span() Span { return n.span }

type JSXClosing struct {
	Name QualIdent // nil for fragments
	span Span
}

func NewJSXClosing(name QualIdent, span Span) *JSXClosing {
	return &JSXClosing{Name: name, span: span}
}
func (n *JSXClosing) Span() Span { return n.span }

// JSXAttrElem is an interface for JSX attribute elements.
// Both regular attributes and spread attributes implement this interface.
type JSXAttrElem interface {
	isJSXAttrElem()
	Span() Span
}

type JSXAttr struct {
	Name  string
	Value *JSXAttrValue
	span  Span
}

func (*JSXAttr) isJSXAttrElem() {}

func NewJSXAttr(name string, value *JSXAttrValue, span Span) *JSXAttr {
	return &JSXAttr{Name: name, Value: value, span: span}
}
func (n *JSXAttr) Span() Span { return n.span }

// JSXSpreadAttr represents a spread attribute in JSX: {...props}
type JSXSpreadAttr struct {
	Expr Expr
	span Span
}

func (*JSXSpreadAttr) isJSXAttrElem() {}

func NewJSXSpreadAttr(expr Expr, span Span) *JSXSpreadAttr {
	return &JSXSpreadAttr{Expr: expr, span: span}
}
func (n *JSXSpreadAttr) Span() Span { return n.span }

type JSXAttrValue interface{ isJSXAttrValue() }

func (*JSXString) isJSXAttrValue()        {}
func (*JSXExprContainer) isJSXAttrValue() {}
func (*JSXElementExpr) isJSXAttrValue()   {}
func (*JSXFragmentExpr) isJSXAttrValue()  {}

type JSXChild interface{ isJSXChild() }

func (*JSXText) isJSXChild()          {}
func (*JSXExprContainer) isJSXChild() {}
func (*JSXElementExpr) isJSXChild()   {}
func (*JSXFragmentExpr) isJSXChild()  {}

type JSXText struct {
	Value string
	span  Span
}

func NewJSXText(value string, span Span) *JSXText {
	return &JSXText{Value: value, span: span}
}
func (n *JSXText) Span() Span { return n.span }

type JSXExprContainer struct {
	Expr Expr
	span Span
}

func NewJSXExprContainer(expr Expr, span Span) *JSXExprContainer {
	return &JSXExprContainer{Expr: expr, span: span}
}
func (n *JSXExprContainer) Span() Span { return n.span }

type JSXString struct {
	Value string
	span  Span
}

func NewJSXString(value string, span Span) *JSXString {
	return &JSXString{Value: value, span: span}
}
func (n *JSXString) Span() Span { return n.span }
