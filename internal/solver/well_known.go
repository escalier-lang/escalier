package solver

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// well_known.go resolves the handful of types the checker's own rules name.
//
// A rule such as "awaiting a value yields what its Promise carries" has to reach
// `Promise` whether or not the file under inference imports it. A handle is
// therefore independent of lexical scope: it is read from the package that
// declares the type, not from the scope chain.
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

// wellKnownOwner names the pseudo-package declaring each well-known type. The map
// is the closed set: a name absent from it has no handle.
var wellKnownOwner = map[wellKnownName]string{
	wellKnownArray:          "std:array",
	wellKnownPromise:        "std:async",
	wellKnownIterable:       "std:iterator",
	wellKnownAsyncIterable:  "std:async",
	wellKnownGenerator:      "std:iterator",
	wellKnownAsyncGenerator: "std:async",
}

// wellKnownType returns the type declared for name, loading its owning package on
// first use and caching the answer for the rest of the run.
//
// It answers false when the package does not resolve or does not declare the name,
// having reported one diagnostic naming both. A tree missing a package is a
// legible failure rather than a panic, since a partial stdlib is a state the
// converter can leave behind.
func (c *checker) wellKnownType(name wellKnownName) (soltype.Type, bool) {
	if t, cached := c.ctx.wellKnown[name]; cached {
		// A miss is cached as a nil type, so a rule that consults one name many
		// times reports its absence once rather than once per consultation.
		return t, t != nil
	}

	uri, known := wellKnownOwner[name]
	if !known {
		return nil, false
	}

	// A speculation trial journals every bound it appends and truncates them on a
	// discard. Inferring a package under one would publish it to the registry and
	// cache a handle whose bounds the discard then removes, so a trial answers
	// nothing and leaves the load to a consultation outside one.
	if c.ctx.probe != nil {
		return nil, false
	}

	t, ok, retry := c.readWellKnown(uri, name)
	if retry {
		// The package is loading further up this call chain, so it has no surface to
		// read yet. Leave the cache untouched: the load completes, and a later
		// consultation answers from the finished package.
		return nil, false
	}
	if c.ctx.wellKnown == nil {
		c.ctx.wellKnown = map[wellKnownName]soltype.Type{}
	}
	if !ok {
		c.ctx.wellKnown[name] = nil
		return nil, false
	}
	c.ctx.wellKnown[name] = t
	return t, true
}

// readWellKnown loads uri and reads name off the package's exported surface. The
// span it reports against is empty, since a handle is reached by a rule rather
// than by anything the user wrote.
//
// retry is true when the package is already loading further up the call chain,
// which makes the absence temporary rather than a fact about the tree.
func (c *checker) readWellKnown(uri string, name wellKnownName) (t soltype.Type, ok bool, retry bool) {
	// bindFileImports installs the loaded module's file scopes over the caller's and
	// does not put them back, since every other load runs before a walk starts
	// rather than inside one. A handle is consulted by a rule mid-walk, so the
	// caller's scopes are restored here or every later declaration loses its file's
	// imports.
	savedFileScopes := c.fileScopes
	ns, errs := c.loadPackage(uri, ast.Span{})
	c.fileScopes = savedFileScopes

	if ns == nil {
		if isCycleError(errs) {
			return nil, false, true
		}
		c.report(&MissingWellKnownTypeError{
			Name:   string(name),
			URI:    uri,
			Reason: firstMessage(errs),
		})
		return nil, false, false
	}
	// A package that published a surface may still have reported diagnostics of its
	// own. Both import call sites pass those on, so a handle does too.
	for _, err := range errs {
		c.report(err)
	}
	b, found := ns.Types[string(name)]
	if !found {
		c.report(&MissingWellKnownTypeError{
			Name:   string(name),
			URI:    uri,
			Reason: "the package declares no such type",
		})
		return nil, false, false
	}
	return b.Type, true, false
}

// isCycleError reports whether errs is the diagnostic loadPackage raises for a URI
// already being loaded further up the call chain.
func isCycleError(errs []SolverError) bool {
	for _, err := range errs {
		if _, isCycle := err.(*ImportCycleError); isCycle {
			return true
		}
	}
	return false
}

// firstMessage renders the first diagnostic in errs, or a fixed phrase when the
// load failed without producing one.
func firstMessage(errs []SolverError) string {
	if len(errs) == 0 {
		return "the package did not load"
	}
	return errs[0].Message()
}

// MissingWellKnownTypeError reports a type the checker's own rules name that the
// stdlib tree does not supply. It names both the type and the package expected to
// declare it, since the fix is to that tree rather than to the program.
type MissingWellKnownTypeError struct {
	// Name is the well-known type that was not found.
	Name string
	// URI is the package expected to declare it.
	URI string
	// Reason says what went wrong, in the loader's words.
	Reason string
	span   ast.Span
}

func (e *MissingWellKnownTypeError) Message() string {
	return "the standard library does not supply " + e.Name +
		", expected in " + e.URI + ": " + e.Reason
}
func (e *MissingWellKnownTypeError) Span() ast.Span      { return e.span }
func (e *MissingWellKnownTypeError) Related() []ast.Span { return nil }
func (e *MissingWellKnownTypeError) isSolverError()      {}

// resolveArrayClass resolves the well-known `Array` once and records its qualified
// class name on the Context, leaving the name empty when the run supplies no
// `Array`. A second call reads the cached handle rather than loading again.
//
// The rules that single an array out run deep inside constraint solving, where a
// package load would be unsafe: a speculation trial truncates every bound it
// journals, so a load raised under one would publish a package whose bounds the
// discard then removes. Settling the name at the annotation that writes `Array`
// keeps those rules to a string comparison. An annotation resolves while its
// declaration is being bound, which is ahead of any constraint an array can reach.
//
// An annotation resolved under a probe is the exception, since the load declines
// there. `resolveSigArrays` settles the name ahead of the one caller that does
// that, the overload pre-bind in inferComponent.
//
// Diagnostics are dropped. A tree without a stdlib supplies no `Array` and needs
// no report for one it never mentions. A program that does mention `Array` gets
// the ordinary unknown-type diagnostic at the reference instead.
func (c *checker) resolveArrayClass() {
	saved := c.errs
	t, ok := c.wellKnownType(wellKnownArray)
	c.errs = saved
	if !ok {
		return
	}
	if cls, isClass := t.(*soltype.ClassType); isClass {
		c.ctx.arrayClass = cls.Name
	}
}

// resolveSigArrays resolves the well-known `Array` when any annotation in sig
// writes it, so a later resolution of sig runs against a name that is already
// cached. Its caller is the overload pre-bind in inferComponent, which resolves a
// signature under a probe. `resolveArrayClass` declines to load there, and the
// annotation would fall back to a bare var that checks nothing. Nothing is loaded
// for a signature that does not write `Array`, or once the name is settled.
func (c *checker) resolveSigArrays(sig ast.FuncSig) {
	if c.ctx.arrayClass != "" {
		return
	}
	f := &arrayRefFinder{}
	for _, tp := range sig.TypeParams {
		acceptTypeAnn(tp.Constraint, f)
		acceptTypeAnn(tp.Default, f)
	}
	for _, param := range sig.Params {
		acceptTypeAnn(param.TypeAnn, f)
	}
	acceptTypeAnn(sig.Return, f)
	acceptTypeAnn(sig.Throws, f)
	if f.found {
		c.resolveArrayClass()
	}
}

// acceptTypeAnn walks t with v, treating an absent optional annotation as nothing
// to walk.
func acceptTypeAnn(t ast.TypeAnn, v ast.Visitor) {
	if t == nil {
		return
	}
	t.Accept(v)
}

// arrayRefFinder records whether the annotations it walks write `Array` anywhere,
// at any depth.
type arrayRefFinder struct {
	ast.DefaultVisitor
	found bool
}

func (f *arrayRefFinder) EnterTypeAnn(t ast.TypeAnn) bool {
	if ref, isRef := t.(*ast.TypeRefTypeAnn); isRef && namesArray(ref.Name) {
		f.found = true
	}
	return !f.found
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

// namesArray reports whether a written type reference names `Array`, bare or
// qualified by the namespace an import bound it under.
func namesArray(name ast.QualIdent) bool {
	written := ast.QualIdentToString(name)
	return written == "Array" || strings.HasSuffix(written, ".Array")
}
