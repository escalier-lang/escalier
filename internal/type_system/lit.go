package type_system

import (
	"math/big"
	"strconv"
)

//sumtype:decl
type Lit interface {
	isLiteral()
	Equal(Lit) bool
	String() string
}

func (*BoolLit) isLiteral()      {}
func (*NumLit) isLiteral()       {}
func (*StrLit) isLiteral()       {}
func (*RegexLit) isLiteral()     {}
func (*BigIntLit) isLiteral()    {}
func (*NullLit) isLiteral()      {}
func (*UndefinedLit) isLiteral() {}

type BoolLit struct{ Value bool }

func (l *BoolLit) Equal(other Lit) bool {
	if other, ok := other.(*BoolLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *BoolLit) String() string {
	if l.Value {
		return "true"
	}
	return "false"
}

type NumLit struct{ Value float64 }

func (l *NumLit) Equal(other Lit) bool {
	if other, ok := other.(*NumLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *NumLit) String() string {
	return strconv.FormatFloat(l.Value, 'f', -1, 32)
}

type StrLit struct{ Value string }

func (l *StrLit) Equal(other Lit) bool {
	if other, ok := other.(*StrLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *StrLit) String() string {
	return strconv.Quote(l.Value)
}

type RegexLit struct{ Value string }

func (l *RegexLit) Equal(other Lit) bool {
	if other, ok := other.(*RegexLit); ok {
		return l.Value == other.Value
	}
	return false
}
func (l *RegexLit) String() string {
	return "/" + l.Value + "/"
}

type BigIntLit struct{ Value big.Int }

func (l *BigIntLit) Equal(other Lit) bool {
	if other, ok := other.(*BigIntLit); ok {
		return l.Value.Cmp(&other.Value) == 0
	}
	return false
}
func (l *BigIntLit) String() string {
	return l.Value.String()
}

type NullLit struct{}

func (l *NullLit) Equal(other Lit) bool {
	if _, ok := other.(*NullLit); ok {
		return true
	}
	return false
}
func (l *NullLit) String() string {
	return "null"
}

type UndefinedLit struct{}

func (l *UndefinedLit) Equal(other Lit) bool {
	if _, ok := other.(*UndefinedLit); ok {
		return true
	}
	return false
}
func (l *UndefinedLit) String() string {
	return "undefined"
}
