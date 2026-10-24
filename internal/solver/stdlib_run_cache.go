package solver

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
)

// stdlib_run_cache.go keeps one checker per standard library tree with the prelude
// and the ambient packages already loaded. A run against that tree starts from a
// deep copy of it instead of inferring the packages again.
//
// The copy is what keeps runs apart. Constrain mutates a type variable's bounds in
// place, and loading a package registers its classes and aliases on the Context, so a
// run that shared the cached checker would write into every other run's state. Each
// run gets its own variables, registry, and side tables, and the cached checker is
// only ever read.

// ambientKey names one cached checker.
type ambientKey struct {
	dir string
	// closure is the sorted URIs of the packages the run loads together, joined
	// with commas. Two runs whose modules import different packages load
	// different groups, and those infer differently.
	closure string
	// digest is a hash over the contents of every file in closure, so an edit to
	// the tree misses the cache rather than reading a stale load.
	digest         [sha256.Size]byte
	noAmbientNames bool
}

// ambientEntry holds one cached checker. once guards the load, so concurrent runs
// against one tree load it once.
type ambientEntry struct {
	once    sync.Once
	checker *checker
}

var ambientCheckers = struct {
	mu      sync.Mutex
	entries map[ambientKey]*ambientEntry
}{entries: map[ambientKey]*ambientEntry{}}

// ambientChecker returns a checker for one run against the tree in dir, with the
// prelude and the ambient packages loaded and bound. groups are the run's groups
// from BuildPackageClosure. ok is false when a file in the closure cannot be read,
// and the caller then starts from newRunChecker.
func ambientChecker(dir string, groups PackageGroups, opts Options) (c *checker, ok bool) {
	key, ok := ambientKeyOf(dir, groups, opts)
	if !ok {
		return nil, false
	}

	ambientCheckers.mu.Lock()
	entry, held := ambientCheckers.entries[key]
	if !held {
		entry = &ambientEntry{}
		ambientCheckers.entries[key] = entry
	}
	ambientCheckers.mu.Unlock()

	entry.once.Do(func() {
		entry.checker = loadAmbientChecker(dir, groups, opts)
	})
	return entry.checker.fork(StdlibSource(dir), StdlibGroupSource(dir), groups), true
}

// ambientKeyOf returns the cache key for a run against dir that loads groups.
func ambientKeyOf(dir string, groups PackageGroups, opts Options) (ambientKey, bool) {
	uris := make([]string, 0, len(groups))
	for uri := range groups {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	hash := sha256.New()
	for _, uri := range uris {
		path, err := resolveStdlibPath(dir, uri)
		if err != nil {
			return ambientKey{}, false
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return ambientKey{}, false
		}
		// Each file is written with its URI and length ahead of it, so moving
		// bytes from the end of one file to the start of the next changes the hash.
		hash.Write(binary.AppendUvarint(nil, uint64(len(uri))))
		hash.Write([]byte(uri))
		hash.Write(binary.AppendUvarint(nil, uint64(len(contents))))
		hash.Write(contents)
	}

	key := ambientKey{
		dir:            dir,
		closure:        strings.Join(uris, ","),
		noAmbientNames: opts.NoAmbientNames,
	}
	copy(key.digest[:], hash.Sum(nil))
	return key, true
}

// loadAmbientChecker returns a checker with the prelude and the ambient packages of
// dir loaded and bound, ready for fork. It holds nothing that refers to the run
// that built it.
func loadAmbientChecker(dir string, groups PackageGroups, opts Options) *checker {
	c := newRunChecker(StdlibSource(dir), StdlibGroupSource(dir), groups, opts)
	c.ambientScope()
	c.bindAmbientExports()

	// fork copies the checker field by field and cannot copy a func, so every
	// field holding one is cleared here and fork sets it again on the copy.
	c.ctx.fusionRecorder = nil
	c.source, c.groupSource, c.groups = nil, nil, nil
	// Both hold the signature phase's per-declaration state, which carries funcs.
	// They are read only while the declaration they key is inferred, and every
	// declaration in the loaded packages is finished.
	c.classShells, c.builtClassSigs = nil, nil
	return c
}

// fork returns a deep copy of c that runs with source, groupSource, and groups. AST
// nodes are shared with c, since inference records what it learns about a node in
// Info rather than on the node.
func (c *checker) fork(source ModuleSource, groupSource GroupSource, groups PackageGroups) *checker {
	f := copyPtr(newGraphCopier(isASTPointer), c)
	f.ctx.fusionRecorder = f.recordFusionEdge
	f.source, f.groupSource, f.groups = source, groupSource, groups
	return f
}

// astPackage is the import path of internal/ast.
var astPackage = reflect.TypeFor[ast.Span]().PkgPath()

// isASTPointer reports whether t points at a type declared in internal/ast.
func isASTPointer(t reflect.Type) bool {
	return t.Elem().PkgPath() == astPackage
}
