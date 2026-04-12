package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// AccessMode indicates whether a member access is a read (rvalue) or write (lvalue).
type AccessMode int

const (
	AccessRead  AccessMode = iota // reading a property (getters apply)
	AccessWrite                   // writing to a property (setters apply)
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

// MemberAccessKeyProvenance wraps a MemberAccessKey as a provenance.Provenance
// so it can be stored on PropertyElem.Provenance to record which property access
// or index access triggered inference of the property.
type MemberAccessKeyProvenance struct {
	Key MemberAccessKey
}

func (*MemberAccessKeyProvenance) IsProvenance() {}
