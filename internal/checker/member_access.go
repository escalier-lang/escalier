package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// MemberAccessKey represents either a property name or an index for accessing object/array elements
type MemberAccessKey interface {
	isMemberAccessKey()
	Span() ast.Span
}

type PropertyKey struct {
	Name     string
	OptChain bool
	span     ast.Span
}

func (pk PropertyKey) isMemberAccessKey() {}
func (pk PropertyKey) Span() ast.Span {
	return pk.span
}

type IndexKey struct {
	Type type_system.Type
	span ast.Span
}

func (ik IndexKey) isMemberAccessKey() {}
func (ik IndexKey) Span() ast.Span {
	return ik.span
}
