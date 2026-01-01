package checker

import (
	"fmt"
	"os"

	"github.com/escalier-lang/escalier/internal/type_system"
)

type Scope struct {
	Parent    *Scope // optional, parent is nil for the root scope
	Namespace *type_system.Namespace
}

func NewScope() *Scope {
	return &Scope{
		Parent:    nil,
		Namespace: type_system.NewNamespace(),
	}
}

func (s *Scope) WithNewScope() *Scope {
	return &Scope{
		Parent:    s,
		Namespace: type_system.NewNamespace(),
	}
}

func (s *Scope) WithNewScopeAndNamespace(ns *type_system.Namespace) *Scope {
	return &Scope{
		Parent:    s,
		Namespace: ns,
	}
}

func (s *Scope) GetValue(name string) *type_system.Binding {
	if v, ok := s.Namespace.Values[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.GetValue(name)
	}
	return nil
}

func (s *Scope) setValue(name string, binding *type_system.Binding) {
	if _, ok := s.Namespace.Values[name]; ok {
		fmt.Fprintf(os.Stderr, "DEBUG: value %s already exists in current scope\n", name)
		panic("value already exists")
	}
	s.Namespace.Values[name] = binding
}

func (s *Scope) getNamespace(name string) *type_system.Namespace {
	if v, ok := s.Namespace.Namespaces[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getNamespace(name)
	}
	return nil
}

func (s *Scope) setNamespace(name string, namespace *type_system.Namespace) {
	if _, ok := s.Namespace.Namespaces[name]; ok {
		panic("namespace already exists")
	}
	s.Namespace.Namespaces[name] = namespace
}

func (s *Scope) getTypeAlias(name string) *type_system.TypeAlias {
	if v, ok := s.Namespace.Types[name]; ok {
		return v
	}
	if s.Parent != nil {
		return s.Parent.getTypeAlias(name)
	}
	return nil
}

func (s *Scope) SetTypeAlias(name string, alias *type_system.TypeAlias) {
	if _, ok := s.Namespace.Types[name]; ok {
		panic(fmt.Sprintf("type alias already exists: %s", name))
	}
	s.Namespace.Types[name] = alias
}
