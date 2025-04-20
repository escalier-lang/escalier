package checker

import (
	"github.com/moznion/go-optional"

	. "github.com/escalier-lang/escalier/internal/type_system"
)

type Scope struct {
	Parent optional.Option[*Scope]
	Values map[string]Type
	// TODO: Add 'Scheme' struct to 'ast' package
	// Types  map[string]Scheme
}

func NewScope(parent optional.Option[*Scope]) *Scope {
	return &Scope{
		Parent: parent,
		Values: make(map[string]Type),
	}
}

func (s *Scope) getValue(name string) optional.Option[Type] {
	if v, ok := s.Values[name]; ok {
		return optional.Some(v)
	}
	return optional.FlatMap(s.Parent, func(p *Scope) optional.Option[Type] {
		return p.getValue(name)
	}).Or(optional.None[Type]())
}
