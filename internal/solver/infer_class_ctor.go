package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// inferConstructor produces a class's constructor as a FuncType returning the
// instance. With an explicit constructor it walks the constructor body so field
// assignments refine the instance fields, then builds a callable signature from the
// value parameters. With none it synthesizes one from the instance fields, unless the
// class extends a superclass — a subclass must declare its own constructor to call
// `super`, so a missing one is reported and a field-based constructor is synthesized
// for recovery.
func (c *checker) inferConstructor(scope *Scope, lvl int, decl *ast.ClassDecl, self *soltype.ClassType, body *soltype.ObjectType, ctors []*ast.ConstructorElem) soltype.Type {
	if len(ctors) == 0 {
		if decl.Extends != nil {
			c.report(&SubclassConstructorRequiredError{Decl: decl})
		}
		return c.synthesizeConstructor(self, body)
	}
	return c.walkConstructorBody(scope, lvl, self, body, ctors[0])
}

// synthesizeConstructor builds the implicit constructor of a class with no explicit
// one: a function taking one parameter per required instance field, in declaration
// order, and returning the instance. An optional field is omitted, matching its
// omission from the required set.
func (c *checker) synthesizeConstructor(self *soltype.ClassType, body *soltype.ObjectType) soltype.Type {
	var params []*soltype.FuncParam
	for _, e := range body.Elems {
		prop, ok := e.(*soltype.PropertyElem)
		if !ok || prop.Optional {
			continue
		}
		params = append(params, &soltype.FuncParam{
			Pattern: &soltype.IdentPat{Name: prop.Name},
			Type:    prop.Type,
		})
	}
	return &soltype.FuncType{Params: params, Ret: self}
}

// walkConstructorBody walks an explicit constructor's body with `self` bound to the
// owned-mutable instance body, so `self.x = value` refines field x through the record
// write machinery, and returns a callable signature: the constructor's value
// parameters — the params after the leading `mut self` — returning the instance.
func (c *checker) walkConstructorBody(scope *Scope, lvl int, self *soltype.ClassType, body *soltype.ObjectType, ctor *ast.ConstructorElem) soltype.Type {
	// The parser materializes `mut self` as Fn.Params[0]; the callable signature is
	// the params after it.
	valueParams := ctor.Fn.Params
	if ctor.Receiver != nil && len(valueParams) > 0 {
		valueParams = valueParams[1:]
	}
	bodySig := ctor.Fn.FuncSig
	bodySig.Params = valueParams

	ctorScope := scope.Child()
	// A constructor's `self` is always owned-mutable so its body can assign fields,
	// regardless of the class's default mutability.
	c.bindSelf(ctorScope, &ast.MethodReceiver{Mut: true}, body)

	ft := c.inferFunc(ctorScope, lvl, bodySig, ctor.Fn.Body, ctor)
	// A constructor returns a fresh instance, not the void its statement body falls off
	// to, so override the inferred return with the instance type.
	return &soltype.FuncType{Params: ft.Params, Ret: self, Inexact: ft.Inexact}
}
