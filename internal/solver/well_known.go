package solver

import (
	"github.com/escalier-lang/escalier/internal/soltype"
)

// well_known.go resolves the handful of types the checker's own rules name.
//
// A rule such as "awaiting a value yields what its Promise carries" has to reach
// `Promise` whether or not the file under inference imports it. The prelude
// package declares all of them, and preludeScope binds its exports once per run
// into a scope between the operator table and the module's own declarations. A
// handle is read straight off that scope, so nothing a file imports and nothing
// a module declares can reach it.
//
// The set is fixed and closed. It is not an ambient surface a program can grow,
// and a name outside it is unreachable this way.

// wellKnownName is one of the types the checker's rules name directly.
type wellKnownName string

const (
	wellKnownArray          wellKnownName = "Array"
	wellKnownPromise        wellKnownName = "Promise"
	wellKnownIterable       wellKnownName = "Iterable"
	wellKnownAsyncIterable  wellKnownName = "AsyncIterable"
	wellKnownGenerator      wellKnownName = "Generator"
	wellKnownAsyncGenerator wellKnownName = "AsyncGenerator"
)

// wellKnownNames is the closed set, in the order a report over it reads.
var wellKnownNames = []wellKnownName{
	wellKnownArray, wellKnownPromise, wellKnownIterable,
	wellKnownAsyncIterable, wellKnownGenerator, wellKnownAsyncGenerator,
}

// wellKnownType returns the type the prelude package declares for name, and
// false when the run's tree supplies no prelude or that prelude declares no such
// name.
//
// The lookup reads the prelude scope's own bindings rather than walking to its
// parent. The parent seeds an opaque stub under several of these names for a run
// with no prelude at all, and a stub is not a handle a rule can do anything with.
func (c *checker) wellKnownType(name wellKnownName) (soltype.Type, bool) {
	b, found := c.preludeScope().ownType(string(name))
	if !found {
		return nil, false
	}
	return b.Type, true
}

// resolveArrayClass records the qualified class name the prelude's `Array` binds
// to, leaving it empty when the run supplies no `Array`.
//
// The rules that single an array out run deep inside constraint solving, where a
// lookup that could load a package would be unsafe. Settling the name once at
// the start of the run keeps those rules to a string comparison, and they answer
// the same inside a speculation trial as outside one.
func (c *checker) resolveArrayClass() {
	t, ok := c.wellKnownType(wellKnownArray)
	if !ok {
		return
	}
	if cls, isClass := t.(*soltype.ClassType); isClass {
		c.ctx.arrayClass = cls.Name
	}
}

// arrayElem returns the element type of t when t is an instance of the well-known
// `Array`, and false otherwise. It lives on Context rather than on the checker
// because the subtyping core does, and that is where the rest-parameter rules read
// it.
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

// arrayOf returns an instance of the well-known `Array` over elem, and false when
// the run resolved no `Array` to instantiate.
func (c *Context) arrayOf(elem soltype.Type) (soltype.Type, bool) {
	if c.arrayClass == "" {
		return nil, false
	}
	return &soltype.ClassType{Name: c.arrayClass, TypeArgs: []soltype.Type{elem}}, true
}
