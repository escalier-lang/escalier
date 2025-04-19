package type_system

import "math/big"

//sumtype:decl
type Lit interface {
	isLiteral()
	Equal(Lit) bool
}

func (*BoolLit) isLiteral()      {}
func (*NumLit) isLiteral()       {}
func (*StrLit) isLiteral()       {}
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

type NumLit struct{ Value float64 }

func (l *NumLit) Equal(other Lit) bool {
	if other, ok := other.(*NumLit); ok {
		return l.Value == other.Value
	}
	return false
}

type StrLit struct{ Value string }

func (l *StrLit) Equal(other Lit) bool {
	if other, ok := other.(*StrLit); ok {
		return l.Value == other.Value
	}
	return false
}

type BigIntLit struct{ Value big.Int }

func (l *BigIntLit) Equal(other Lit) bool {
	if other, ok := other.(*BigIntLit); ok {
		return l.Value.Cmp(&other.Value) == 0
	}
	return false
}

type NullLit struct{}

func (l *NullLit) Equal(other Lit) bool {
	if _, ok := other.(*NullLit); ok {
		return true
	}
	return false
}

type UndefinedLit struct{}

func (l *UndefinedLit) Equal(other Lit) bool {
	if _, ok := other.(*UndefinedLit); ok {
		return true
	}
	return false
}
