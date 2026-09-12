package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// prelude_classes.go settles which classes the checker's own rules mean by `Array`
// and `Promise`, and reads their type arguments back.
//
// A rule such as "a rest parameter absorbs its arguments into an array", or
// "awaiting a value yields what its promise carries", has to reach the class
// whether or not the file under inference imports one. The prelude package declares
// both, and preludeScope binds that package's exports once per run, so each name is
// read off that scope rather than through the scope chain a file writes into.

const (
	arrayClassName   = "Array"
	promiseClassName = "Promise"
)

// resolvePreludeClasses records the qualified name of each class the rules single
// out, leaving one empty when the run's tree declares no such class.
//
// The rules that read them run deep inside constraint solving, where a lookup that
// could load a package would be unsafe. Settling the names once at the start of the
// run keeps those rules to a string comparison, and they answer the same inside a
// speculation trial as outside one.
func (c *checker) resolvePreludeClasses() {
	c.ctx.arrayClass = c.preludeClassName(arrayClassName)
	c.ctx.promiseClass = c.preludeClassName(promiseClassName)
}

// preludeClassName returns the qualified name the prelude package declares name as a
// class under, and "" when it declares no such class.
//
// The lookup reads the prelude scope's own bindings rather than walking to its
// parent, so it answers what the prelude package declared rather than what an
// enclosing scope seeded under the same name.
func (c *checker) preludeClassName(name string) string {
	b, found := c.preludeScope().ownType(name)
	if !found {
		return ""
	}
	cls, isClass := b.Type.(*soltype.ClassType)
	if !isClass {
		return ""
	}
	return cls.Name
}

// arrayElem returns the element type of t when t is an instance of the `Array`
// the run settled on, and false otherwise. It lives on Context rather than on the
// checker because the subtyping core does, and that is where the rest-parameter
// rules read it.
func (c *Context) arrayElem(t soltype.Type) (soltype.Type, bool) {
	args, ok := c.instanceArgs(t, c.arrayClass, 1)
	if !ok {
		return nil, false
	}
	return args[0], true
}

// promiseParts returns what an instance of the `Promise` the run settled on resolves
// to and what it may reject with, and false when t is not one.
//
// A promise that cannot reject carries `never` in the rejection slot rather than
// nothing, so a caller compares one canonical value and never a nil.
func (c *Context) promiseParts(t soltype.Type) (inner, errT soltype.Type, ok bool) {
	args, held := c.instanceArgs(t, c.promiseClass, 2)
	if !held {
		return nil, nil, false
	}
	return args[0], args[1], true
}

// promiseOf returns an instance of the `Promise` the run settled on, and false when
// the run resolved none to instantiate. A nil errT stands for the `never` a promise
// that cannot reject carries, so a caller with no rejection to name passes nothing.
func (c *Context) promiseOf(inner, errT soltype.Type) (soltype.Type, bool) {
	if c.promiseClass == "" {
		return nil, false
	}
	if errT == nil {
		errT = &soltype.NeverType{}
	}
	return &soltype.ClassType{
		Name:     c.promiseClass,
		TypeArgs: []soltype.Type{inner, errT},
		// The rejection slot is what a promise leaves empty, so the printer renders
		// `Promise<T>` for one that cannot reject. See printedArgs.
		Defaults: []soltype.Type{nil, &soltype.NeverType{}},
	}, true
}

// instanceArgs returns t's type arguments when t is an instance of the class named
// className carrying want of them, and false otherwise. An empty className is a run
// that resolved no such class, where nothing in play is one.
func (c *Context) instanceArgs(t soltype.Type, className string, want int) ([]soltype.Type, bool) {
	cls, isClass := t.(*soltype.ClassType)
	if !isClass || className == "" || cls.Name != className {
		return nil, false
	}
	if len(cls.TypeArgs) != want {
		return nil, false
	}
	return cls.TypeArgs, true
}
