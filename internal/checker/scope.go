package checker

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

type Scope struct {
	Parent optional.Option[*Scope]
	Values map[string]ast.Type
	// TODO: Add 'Scheme' struct to 'ast' package
	// Types  map[string]ast.Scheme
}

func NewScope(parent optional.Option[*Scope]) *Scope {
	return &Scope{
		Parent: parent,
		Values: make(map[string]ast.Type),
	}
}

func (s *Scope) getValue(name string) optional.Option[ast.Type] {
	if v, ok := s.Values[name]; ok {
		return optional.Some(v)
	}
	return optional.FlatMap(s.Parent, func(p *Scope) optional.Option[ast.Type] {
		return p.getValue(name)
	}).Or(optional.None[ast.Type]())
}
