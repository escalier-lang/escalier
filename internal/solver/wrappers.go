package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// wrappers.go settles the class a primitive reads its members through.
//
// A primitive is not an object, so `(1.5).toFixed(2)` has no member to read off
// `number` itself. JavaScript boxes the receiver into its wrapper object for the
// read, and TypeScript types the read against the wrapper's interface. The solver
// does the same with the class the standard library declares for each primitive.
// A literal type boxes to the wrapper of its primitive, so `1.5` reads through
// `Number` the way `number` does, and a `unique symbol` reads through `Symbol`.

// primitiveWrapper names the class one primitive reads its members through and
// the package that declares it.
type primitiveWrapper struct {
	prim soltype.Prim
	uri  string
	name string
}

// primitiveWrappers lists every primitive's wrapper class. Each name is looked up
// on its package's exported surface, so the lookup is by qualified name and does
// not depend on what a file imports or on any ambient scope.
var primitiveWrappers = []primitiveWrapper{
	{soltype.NumPrim, "std:number", "Number"},
	{soltype.StrPrim, "std:string", "String"},
	{soltype.BoolPrim, "std:boolean", "Boolean"},
	{soltype.BigIntPrim, "std:bigint", "BigInt"},
	{soltype.SymPrim, preludeURI, "Symbol"},
}

// wrapperPackageURIs returns the packages declaring a wrapper class that the
// prelude does not. A stdlib run grows a package closure from them and loads it as
// a group of its own, ahead of the closure the module's imports reach.
func wrapperPackageURIs() []string {
	uris := make([]string, 0, len(primitiveWrappers))
	for _, w := range primitiveWrappers {
		if w.uri != preludeURI {
			uris = append(uris, w.uri)
		}
	}
	return uris
}

// resolveWrapperClasses records the class each primitive reads its members
// through, loading every package that declares one.
//
// It runs once at the start of a run, beside resolvePreludeClasses and for the
// same reason. The rule that boxes a primitive runs inside constraint solving, and
// a package loaded from inside a speculation trial would have the bounds its
// inference recorded truncated by a discard.
//
// A primitive whose wrapper the tree does not declare, or declares as something
// other than a non-generic class, gets no entry and reads no members. That is a
// narrower standard library rather than a broken one, so nothing is reported.
func (c *checker) resolveWrapperClasses() {
	c.ctx.wrapperClasses = map[soltype.Prim]*soltype.ClassType{}
	for _, w := range primitiveWrappers {
		b, found := c.wrapperBinding(w)
		if !found {
			continue
		}
		cls, isClass := b.Type.(*soltype.ClassType)
		if !isClass {
			continue
		}
		def, registered := c.ctx.classDef(cls.Name)
		if !registered || len(def.TypeParams) != 0 {
			continue
		}
		c.ctx.wrapperClasses[w.prim] = cls
	}
}

// wrapperBinding returns the type binding w's package exports under w's name.
func (c *checker) wrapperBinding(w primitiveWrapper) (TypeBinding, bool) {
	if w.uri == preludeURI {
		return c.prelude.OwnType(w.name)
	}
	ns := c.loadStdlibPackage(w.uri)
	if ns == nil {
		return TypeBinding{}, false
	}
	b, found := ns.Types[w.name]
	return b, found
}

// loadStdlibPackage loads the package at uri on behalf of the run rather than of
// any file in it, and returns its exported surface, or nil when it did not load.
//
// The package draws its variables and unique symbols from the counters the prelude
// left off at, so the program's own numbering is the same whether or not a wrapper
// loaded.
//
// What the package reports is dropped. No file asked for it, so a diagnostic it
// raises is about the standard library rather than about the program. A file that
// imports the package later finds it loaded and reports nothing for it either,
// which is what a second import of any package reports.
func (c *checker) loadStdlibPackage(uri string) *Namespace {
	var ns *Namespace
	c.withStdlibCounters(func() { ns, _ = c.loadPackage(uri, ast.Span{}) })
	return ns
}

// primitiveMember resolves a read of a method, getter, or setter off a primitive
// receiver through the instance of its wrapper class, answering as projectedMember
// answers for that instance. ok is false for any other receiver and for a member
// projectedMember declines, such as a field.
func (c *checker) primitiveMember(lvl int, blame ast.Node, name string, recv, carrier soltype.Type) (pathResult, bool) {
	box, ok := c.wrapperCarrier(carrier)
	if !ok {
		return pathResult{}, false
	}
	return c.projectedMember(lvl, blame, name, recv, box)
}

// wrapperCarrier returns an instance of the standard-library class a primitive receiver
// reads its methods from, such as `Number` for `5` or `number`, and `String` for `"a"`.
// ok is false when the receiver is not a primitive.
//
// A receiver can also be a type variable inference has not resolved yet. The types that
// flow into such a variable are its lower bounds. The variable gets a class only when
// every lower bound is a primitive and they all read from the same class. A variable
// with no lower bounds, or with one that is not a primitive, gets no class.
//
// soleLowerBound, by contrast, skips any lower bound its check rejects. Skipping would be
// wrong here. A variable that holds either `5` or a generator would then read `next`
// from `Number` alone and miss the generator's own `next`.
func (c *checker) wrapperCarrier(carrier soltype.Type) (*soltype.ClassType, bool) {
	if wrapper, ok := c.ctx.wrapperInstance(c.memberCarrier(carrier)); ok {
		return wrapper, true
	}
	v, isVar := carrier.(*soltype.TypeVarType)
	if !isVar {
		return nil, false
	}
	var found *soltype.ClassType
	for _, lb := range v.LowerBounds {
		if lb == soltype.Type(v) {
			continue
		}
		wrapper, ok := c.ctx.wrapperInstance(c.memberCarrier(lb))
		if !ok || (found != nil && wrapper.Name != found.Name) {
			return nil, false
		}
		found = wrapper
	}
	return found, found != nil
}

// wrapperInstance returns an instance of the class t reads its members through when
// t is a primitive, a literal, or a `unique symbol`, and false for any other type or
// for a primitive the run's standard library declares no wrapper for.
//
// The instance is a fresh copy on every call, so a caller may record provenance
// against it.
func (c *Context) wrapperInstance(t soltype.Type) (*soltype.ClassType, bool) {
	var prim soltype.Prim
	switch t := t.(type) {
	case *soltype.PrimType:
		prim = t.Prim
	case *soltype.LitType:
		prim = primOf(t.Lit)
	case *soltype.UniqueSymbolType:
		prim = soltype.SymPrim
	default:
		return nil, false
	}
	cls, found := c.wrapperClasses[prim]
	if !found {
		return nil, false
	}
	instance := *cls
	return &instance, true
}
