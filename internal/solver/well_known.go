package solver

import (
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
