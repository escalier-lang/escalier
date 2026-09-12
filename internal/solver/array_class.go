package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// array_class.go settles which class the checker's own rules mean by `Array`,
// and answers whether a type is an instance of it.
//
// A rule such as "a rest parameter absorbs its arguments into an array" has to
// reach `Array` whether or not the file under inference imports one. The prelude
// package declares it, and preludeScope binds that package's exports once per
// run, so the name is read off that scope rather than through the scope chain a
// file writes into.

// arrayClassName is the name the prelude package declares the array class under.
const arrayClassName = "Array"

// resolveArrayClass records the qualified class name the prelude's `Array` binds
// to, leaving it empty when the run's tree supplies none.
//
// The rules that single an array out run deep inside constraint solving, where a
// lookup that could load a package would be unsafe. Settling the name once at the
// start of the run keeps those rules to a string comparison, and they answer the
// same inside a speculation trial as outside one.
//
// The lookup reads the prelude scope's own bindings rather than walking to its
// parent, so it answers what the prelude package declared rather than what an
// enclosing scope seeded under the same name.
func (c *checker) resolveArrayClass() {
	b, found := c.preludeScope().ownType(arrayClassName)
	if !found {
		return
	}
	if cls, isClass := b.Type.(*soltype.ClassType); isClass {
		c.ctx.arrayClass = cls.Name
	}
}

// arrayElem returns the element type of t when t is an instance of the `Array`
// the run settled on, and false otherwise. It lives on Context rather than on the
// checker because the subtyping core does, and that is where the rest-parameter
// rules read it.
func (c *Context) arrayElem(t soltype.Type) (soltype.Type, bool) {
	cls, isClass := t.(*soltype.ClassType)
	if !isClass || c.arrayClass == "" || cls.Name != c.arrayClass {
		return nil, false
	}
	if len(cls.TypeArgs) != 1 {
		return nil, false
	}
	return cls.TypeArgs[0], true
}
