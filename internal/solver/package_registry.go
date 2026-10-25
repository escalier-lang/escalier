package solver

import (
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// package_registry.go holds the packages one inference run has loaded, so a
// second module's declarations survive past the walk that inferred them and a
// third module importing the same package reads the first result.
//
// A package is addressed by its full URI, the string an `import` writes. Two
// packages that declare the same name stay distinct because every definition
// they register is keyed under that URI, so `std:prelude`'s `Array` and a user's
// `Array` name two entries in one nominal registry.

// ModuleSource resolves a package URI to the module to infer for it and the
// path that module was read from. The path is carried for diagnostics; nothing
// keys on it.
//
// An error comes back to the importing declaration as a diagnostic. A source
// that cannot resolve a URI is an ordinary outcome, not a failure of the run.
type ModuleSource func(uri string) (module *ast.Module, path string, err error)

// packageEntry is one package's state in the registry. ns is nil exactly while
// loading is true, which is the window a cyclic import lands in.
type packageEntry struct {
	ns      *Namespace
	path    string
	loading bool
}

// PackageRegistry maps a package URI to the exported surface inferred for it.
//
// It is not a scope. A package reaches an importer only through the binding
// that importer's file scope makes for it, so nothing resolves a name here by
// lexical lookup.
type PackageRegistry struct {
	packages map[string]*packageEntry
	// librarySources holds the SourceID of every file a loaded package was read from, and
	// libraryKeyPrefixes the prefix packageKeyPrefix builds for each loaded package's URI.
	// markLibrary fills both.
	librarySources     set.Set[int]
	libraryKeyPrefixes set.Set[string]
}

// NewPackageRegistry returns an empty registry.
func NewPackageRegistry() *PackageRegistry {
	return &PackageRegistry{
		packages:           map[string]*packageEntry{},
		librarySources:     set.NewSet[int](),
		libraryKeyPrefixes: set.NewSet[string](),
	}
}

// Lookup returns the exported surface of a loaded package.
//
// Three outcomes, and a caller has to tell them apart. A loaded package returns
// its namespace and true. A package being loaded further up the same call chain
// returns nil and true, which is the cycle sentinel: the caller binds nothing
// and moves on rather than recursing. An unknown URI returns nil and false,
// which is the caller's signal to load it.
func (r *PackageRegistry) Lookup(uri string) (*Namespace, bool) {
	entry, ok := r.packages[uri]
	if !ok {
		return nil, false
	}
	if entry.loading {
		return nil, true
	}
	return entry.ns, true
}

// Loading reports whether uri is being loaded further up the current call
// chain.
func (r *PackageRegistry) Loading(uri string) bool {
	entry, ok := r.packages[uri]
	return ok && entry.loading
}

// Path returns the path the package at uri was read from, or "" for a URI the
// registry does not hold.
func (r *PackageRegistry) Path(uri string) string {
	if entry, ok := r.packages[uri]; ok {
		return entry.path
	}
	return ""
}

// URIs returns every URI the registry holds, loading ones included, in
// unspecified order.
func (r *PackageRegistry) URIs() []string {
	uris := make([]string, 0, len(r.packages))
	for uri := range r.packages {
		uris = append(uris, uri)
	}
	return uris
}

// markLoading opens the in-progress window for uri. Every lookup between here
// and publish returns the cycle sentinel.
func (r *PackageRegistry) markLoading(uri, path string) {
	r.packages[uri] = &packageEntry{path: path, loading: true}
}

// publish closes the in-progress window, making ns the package's surface.
func (r *PackageRegistry) publish(uri string, ns *Namespace) {
	entry, ok := r.packages[uri]
	if !ok {
		r.packages[uri] = &packageEntry{ns: ns}
		return
	}
	entry.ns = ns
	entry.loading = false
}

// markLibrary records module as a loaded package's declarations, inferred with keyURI as the
// URI qualifyDecl prefixes their keys with. module's SourceIDs must not be shared with any
// file of the program being checked, or isLibrarySource answers true for that file too.
func (r *PackageRegistry) markLibrary(keyURI string, module *ast.Module) {
	for id := range module.Sources {
		r.librarySources.Add(id)
	}
	r.libraryKeyPrefixes.Add(packageKeyPrefix(keyURI))
}

// isLibrarySource reports whether the file sourceID names was read for a loaded package.
func (r *PackageRegistry) isLibrarySource(sourceID int) bool {
	return r.librarySources.Contains(sourceID)
}

// isLibraryKey reports whether key is one qualifyDecl built for a loaded package's
// declaration. Such a key is a package prefix, a `.`, and the qualified name. The prefix holds
// no `.`, since packageKeyPrefix escapes every `.` in the URI.
func (r *PackageRegistry) isLibraryKey(key string) bool {
	prefix, _, found := strings.Cut(key, ".")
	return found && r.libraryKeyPrefixes.Contains(prefix)
}
