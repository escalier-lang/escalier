package parser

import "github.com/escalier-lang/escalier/internal/ast"

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type Token interface {
	isToken()
	Span() ast.Span
}

// literals
func (*TNumber) isToken()     {}
func (*TString) isToken()     {}
func (*TQuasi) isToken()      {}
func (*TIdentifier) isToken() {}

type TNumber struct {
	Value float64
	span  ast.Span
}

func NewNumber(value float64, span ast.Span) *TNumber { return &TNumber{Value: value, span: span} }
func (t *TNumber) Span() ast.Span                     { return t.span }

type TString struct {
	Value string
	span  ast.Span
}

func NewString(value string, span ast.Span) *TString { return &TString{Value: value, span: span} }
func (t *TString) Span() ast.Span                    { return t.span }

type TQuasi struct {
	Value string
	span  ast.Span
}

func NewQuasi(value string, span ast.Span) *TQuasi { return &TQuasi{Value: value, span: span} }
func (t *TQuasi) Span() ast.Span                   { return t.span }

type TIdentifier struct {
	Value string
	span  ast.Span
}

func NewIdentifier(value string, span ast.Span) *TIdentifier {
	return &TIdentifier{Value: value, span: span}
}
func (t *TIdentifier) Span() ast.Span { return t.span }

// keywords
func (*TFn) isToken()      {}
func (*TVar) isToken()     {}
func (*TVal) isToken()     {}
func (*TReturn) isToken()  {}
func (*TImport) isToken()  {}
func (*TExport) isToken()  {}
func (*TDeclare) isToken() {}

type TFn struct{ span ast.Span }

func NewFn(span ast.Span) Token { return &TFn{span: span} }
func (t *TFn) Span() ast.Span   { return t.span }

type TVar struct{ span ast.Span }

func NewVar(span ast.Span) Token { return &TVar{span: span} }
func (t *TVar) Span() ast.Span   { return t.span }

type TVal struct{ span ast.Span }

func NewVal(span ast.Span) Token { return &TVal{span: span} }
func (t *TVal) Span() ast.Span   { return t.span }

type TReturn struct{ span ast.Span }

func NewReturn(span ast.Span) Token { return &TReturn{span: span} }
func (t *TReturn) Span() ast.Span   { return t.span }

type TImport struct{ span ast.Span }

func NewImport(span ast.Span) Token { return &TImport{span: span} }
func (t *TImport) Span() ast.Span   { return t.span }

type TExport struct{ span ast.Span }

func NewExport(span ast.Span) Token { return &TExport{span: span} }
func (t *TExport) Span() ast.Span   { return t.span }

type TDeclare struct{ span ast.Span }

func NewDeclare(span ast.Span) Token { return &TDeclare{span: span} }
func (t *TDeclare) Span() ast.Span   { return t.span }

// operators
func (*TPlus) isToken()     {}
func (*TMinus) isToken()    {}
func (*TAsterisk) isToken() {}
func (*TSlash) isToken()    {}
func (*TEquals) isToken()   {}
func (*TDot) isToken()      {}
func (*TComma) isToken()    {}
func (*TBackTick) isToken() {}

type TPlus struct{ span ast.Span }

func NewPlus(span ast.Span) *TPlus { return &TPlus{span: span} }
func (t *TPlus) Span() ast.Span    { return t.span }

type TMinus struct{ span ast.Span }

func NewMinus(span ast.Span) *TMinus { return &TMinus{span: span} }
func (t *TMinus) Span() ast.Span     { return t.span }

type TAsterisk struct{ span ast.Span }

func NewAsterisk(span ast.Span) *TAsterisk { return &TAsterisk{span: span} }
func (t *TAsterisk) Span() ast.Span        { return t.span }

type TSlash struct{ span ast.Span }

func NewSlash(span ast.Span) *TSlash { return &TSlash{span: span} }
func (t *TSlash) Span() ast.Span     { return t.span }

type TEquals struct{ span ast.Span }

func NewEquals(span ast.Span) *TEquals { return &TEquals{span: span} }
func (t *TEquals) Span() ast.Span      { return t.span }

type TDot struct{ span ast.Span }

func NewDot(span ast.Span) *TDot { return &TDot{span: span} }
func (t *TDot) Span() ast.Span   { return t.span }

type TComma struct{ span ast.Span }

func NewComma(span ast.Span) *TComma { return &TComma{span: span} }
func (t *TComma) Span() ast.Span     { return t.span }

type TBackTick struct{ span ast.Span }

func NewBackTick(span ast.Span) *TBackTick { return &TBackTick{span: span} }
func (t *TBackTick) Span() ast.Span        { return t.span }

// optional chaining
func (*TQuestionOpenParen) isToken()   {}
func (*TQuestionDot) isToken()         {}
func (*TQuestionOpenBracket) isToken() {}

type TQuestionOpenParen struct{ span ast.Span }

func NewQuestionOpenParen(span ast.Span) *TQuestionOpenParen { return &TQuestionOpenParen{span: span} }
func (t *TQuestionOpenParen) Span() ast.Span                 { return t.span }

type TQuestionDot struct{ span ast.Span }

func NewQuestionDot(span ast.Span) *TQuestionDot { return &TQuestionDot{span: span} }
func (t *TQuestionDot) Span() ast.Span           { return t.span }

type TQuestionOpenBracket struct{ span ast.Span }

func NewQuestionOpenBracket(span ast.Span) *TQuestionOpenBracket {
	return &TQuestionOpenBracket{span: span}
}
func (t *TQuestionOpenBracket) Span() ast.Span { return t.span }

// grouping
func (*TOpenParen) isToken()    {}
func (*TCloseParen) isToken()   {}
func (*TOpenBrace) isToken()    {}
func (*TCloseBrace) isToken()   {}
func (*TOpenBracket) isToken()  {}
func (*TCloseBracket) isToken() {}

type TOpenParen struct{ span ast.Span }

func NewOpenParen(span ast.Span) *TOpenParen { return &TOpenParen{span: span} }
func (t *TOpenParen) Span() ast.Span         { return t.span }

type TCloseParen struct{ span ast.Span }

func NewCloseParen(span ast.Span) *TCloseParen { return &TCloseParen{span: span} }
func (t *TCloseParen) Span() ast.Span          { return t.span }

type TOpenBrace struct{ span ast.Span }

func NewOpenBrace(span ast.Span) *TOpenBrace { return &TOpenBrace{span: span} }
func (t *TOpenBrace) Span() ast.Span         { return t.span }

type TCloseBrace struct{ span ast.Span }

func NewCloseBrace(span ast.Span) *TCloseBrace { return &TCloseBrace{span: span} }
func (t *TCloseBrace) Span() ast.Span          { return t.span }

type TOpenBracket struct{ span ast.Span }

func NewOpenBracket(span ast.Span) *TOpenBracket { return &TOpenBracket{span: span} }
func (t *TOpenBracket) Span() ast.Span           { return t.span }

type TCloseBracket struct{ span ast.Span }

func NewCloseBracket(span ast.Span) *TCloseBracket { return &TCloseBracket{span: span} }
func (t *TCloseBracket) Span() ast.Span            { return t.span }

func (*TEndOfFile) isToken() {}
func (*TInvalid) isToken()   {}

type TEndOfFile struct{ span ast.Span }
type TInvalid struct{ span ast.Span }

func NewEndOfFile(span ast.Span) *TEndOfFile { return &TEndOfFile{span: span} }
func (t *TEndOfFile) Span() ast.Span         { return t.span }

func NewInvalid(span ast.Span) *TInvalid { return &TInvalid{span: span} }
func (t *TInvalid) Span() ast.Span       { return t.span }
