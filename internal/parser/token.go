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

func (*TNumber) isToken()     {}
func (*TString) isToken()     {}
func (*TIdentifier) isToken() {}
func (*TFn) isToken()         {}
func (*TVar) isToken()        {}
func (*TVal) isToken()        {}
func (*TPlus) isToken()       {}
func (*TMinus) isToken()      {}
func (*TAsterisk) isToken()   {}
func (*TSlash) isToken()      {}
func (*TEquals) isToken()     {}

// grouping
func (*TOpenParen) isToken()    {}
func (*TCloseParen) isToken()   {}
func (*TOpenBrace) isToken()    {}
func (*TCloseBrace) isToken()   {}
func (*TOpenBracket) isToken()  {}
func (*TCloseBracket) isToken() {}

// optional chaining
func (*TQuestionOpenParen) isToken()   {}
func (*TQuestionDot) isToken()         {}
func (*TQuestionOpenBracket) isToken() {}

func (*TDot) isToken()   {}
func (*TComma) isToken() {}

func (*TEOF) isToken()     {}
func (*TInvalid) isToken() {}

type TNumber struct {
	Value float64
}

type TString struct {
	Value string
}

type TIdentifier struct {
	Value string
}

type TFn struct{}
type TVar struct{}
type TVal struct{}
type TPlus struct{}
type TMinus struct{}
type TAsterisk struct{}
type TSlash struct{}
type TEquals struct{}
type TOpenParen struct{}
type TCloseParen struct{}
type TOpenBrace struct{}
type TCloseBrace struct{}
type TOpenBracket struct{}
type TCloseBracket struct{}
type TDot struct{}
type TQuestionOpenParen struct{}
type TQuestionDot struct{}
type TQuestionOpenBracket struct{}
type TComma struct{}

type TEOF struct{}
type TInvalid struct{}
