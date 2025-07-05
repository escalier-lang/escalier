package checker

import (
	"github.com/moznion/go-optional"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/type_system"
)

// We want to model both `let x = 5` as well as `fn (x: number) => x`
type Binding struct {
	Source  optional.Option[ast.BindingSource]
	Type    Type
	Mutable bool
}

type Scope struct {
	Parent *Scope // optional, parent is nil for the root scope
	Values map[string]Binding
	Types  map[string]TypeAlias
}

func NewScope(parent *Scope) *Scope {
	return &Scope{
		Parent: parent,
		Values: map[string]Binding{},
		Types:  map[string]TypeAlias{},
	}
}

func (s *Scope) getValue(name string) optional.Option[Binding] {
	if v, ok := s.Values[name]; ok {
		return optional.Some(v)
	}
	if s.Parent != nil {
		return s.Parent.getValue(name)
	}
	return optional.None[Binding]()
}

func (s *Scope) setValue(name string, binding Binding) {
	if _, ok := s.Values[name]; ok {
		panic("value already exists")
	}
	s.Values[name] = binding
}

func (s *Scope) getTypeAlias(name string) optional.Option[TypeAlias] {
	if v, ok := s.Types[name]; ok {
		return optional.Some(v)
	}
	if s.Parent != nil {
		return s.Parent.getTypeAlias(name)
	}
	return optional.None[TypeAlias]()
}

func (s *Scope) setTypeAlias(name string, alias TypeAlias) {
	if _, ok := s.Types[name]; ok {
		panic("type alias already exists")
	}
	s.Types[name] = alias
}
