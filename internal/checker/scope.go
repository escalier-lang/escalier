package checker

import (
	. "github.com/escalier-lang/escalier/internal/type_system"
)

type Scope struct {
	Parent    *Scope // optional, parent is nil for the root scope
	Namespace *Namespace
}

func NewScope(parent *Scope) *Scope {
	return &Scope{
		Parent: parent,
		Namespace: &Namespace{
			Values:     make(map[string]*Binding),
			Types:      make(map[string]*TypeAlias),
			Namespaces: make(map[string]*Namespace),
		},
	}
}

func (s *Scope) getValue(name string) *Binding {
	if v, ok := s.Namespace.Values[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getValue(name)
	}
	return nil
}

func (s *Scope) setValueInNamespace(ns *Namespace, name string, binding *Binding) {
	if _, ok := ns.Values[name]; ok {
		panic("value already exists")
	}
	ns.Values[name] = binding
}

func (s *Scope) setValue(name string, binding *Binding) {
	if _, ok := s.Namespace.Values[name]; ok {
		panic("value already exists")
	}
	s.Namespace.Values[name] = binding
}

func (s *Scope) getNamespace(name string) *Namespace {
	if v, ok := s.Namespace.Namespaces[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getNamespace(name)
	}
	return nil
}

func (s *Scope) setNamespace(name string, namespace *Namespace) {
	if _, ok := s.Namespace.Namespaces[name]; ok {
		panic("namespace already exists")
	}
	s.Namespace.Namespaces[name] = namespace
}

func (s *Scope) getTypeAlias(name string) *TypeAlias {
	if v, ok := s.Namespace.Types[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getTypeAlias(name)
	}
	return nil
}

func (s *Scope) setTypeAliasInNamespace(ns *Namespace, name string, alias *TypeAlias) {
	if _, ok := ns.Types[name]; ok {
		panic("type alias already exists")
	}
	ns.Types[name] = alias
}

func (s *Scope) setTypeAlias(name string, alias *TypeAlias) {
	if _, ok := s.Namespace.Types[name]; ok {
		panic("type alias already exists")
	}
	s.Namespace.Types[name] = alias
}
