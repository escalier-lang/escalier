package ast

type JSXOpening struct {
	Name      string
	Attrs     []JSXAttr
	SelfClose bool
	span      Span
}

func NewJSXOpening(name string, attrs []JSXAttr, selfClose bool, span Span) *JSXOpening {
	return &JSXOpening{Name: name, Attrs: attrs, SelfClose: selfClose, span: span}
}
func (n *JSXOpening) Span() Span { return n.span }

type JSXClosing struct {
	Name string
	span Span
}

func NewJSXClosing(name string, span Span) *JSXClosing {
	return &JSXClosing{Name: name, span: span}
}
func (n *JSXClosing) Span() Span { return n.span }

type JSXAttr struct {
	Name  string
	Value *JSXAttrValue
	span  Span
}

func NewJSXAttr(name string, value *JSXAttrValue, span Span) *JSXAttr {
	return &JSXAttr{Name: name, Value: value, span: span}
}
func (n *JSXAttr) Span() Span { return n.span }

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
