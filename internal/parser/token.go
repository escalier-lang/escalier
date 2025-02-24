package parser

type Token struct {
	Data T
	Span Span
}

// This interface is never called. Its purpose is to encode a variant type in
// Go's type system.
//
//sumtype:decl
type T interface{ isToken() }

// literals
func (*TNumber) isToken()     {}
func (*TString) isToken()     {}
func (*TIdentifier) isToken() {}

type TNumber struct{ Value float64 }
type TString struct{ Value string }
type TIdentifier struct{ Value string }

// keywords
func (*TFn) isToken()      {}
func (*TVar) isToken()     {}
func (*TVal) isToken()     {}
func (*TReturn) isToken()  {}
func (*TImport) isToken()  {}
func (*TExport) isToken()  {}
func (*TDeclare) isToken() {}

type TFn struct{}
type TVar struct{}
type TVal struct{}
type TReturn struct{}
type TImport struct{}
type TExport struct{}
type TDeclare struct{}

// operators
func (*TPlus) isToken()     {}
func (*TMinus) isToken()    {}
func (*TAsterisk) isToken() {}
func (*TSlash) isToken()    {}
func (*TEquals) isToken()   {}
func (*TDot) isToken()      {}
func (*TComma) isToken()    {}

type TPlus struct{}
type TMinus struct{}
type TAsterisk struct{}
type TSlash struct{}
type TEquals struct{}
type TDot struct{}
type TComma struct{}

// optional chaining
func (*TQuestionOpenParen) isToken()   {}
func (*TQuestionDot) isToken()         {}
func (*TQuestionOpenBracket) isToken() {}

type TQuestionOpenParen struct{}
type TQuestionDot struct{}
type TQuestionOpenBracket struct{}

// grouping
func (*TOpenParen) isToken()    {}
func (*TCloseParen) isToken()   {}
func (*TOpenBrace) isToken()    {}
func (*TCloseBrace) isToken()   {}
func (*TOpenBracket) isToken()  {}
func (*TCloseBracket) isToken() {}

type TOpenParen struct{}
type TCloseParen struct{}
type TOpenBrace struct{}
type TCloseBrace struct{}
type TOpenBracket struct{}
type TCloseBracket struct{}

func (*TEndOfFile) isToken() {}
func (*TInvalid) isToken()   {}

type TEndOfFile struct{}
type TInvalid struct{}
