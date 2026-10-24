package solver

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
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
	// digest is a hash over the contents of the prelude and of every file in
	// closure, so an edit to the tree misses the cache rather than reading a stale
	// load. The prelude loads alone, outside any group, so closure does not name it.
	digest [sha256.Size]byte
	// noAmbientNames and checkJsDecorators are the Options fields that change what
	// loading the packages binds or reports.
	noAmbientNames    bool
	checkJsDecorators bool
}

// ambientEntry holds one cached checker. once guards the load, so concurrent runs
// against one tree load it once.
type ambientEntry struct {
	once    sync.Once
	checker *checker
}

// maxAmbientCheckers is how many cached checkers ambientCheckers keeps. A process
// checking one tree needs one per distinct closure its modules import. The bound
// stops a test suite that seeds a fresh tree for each test from keeping a checker
// for every one of them.
const maxAmbientCheckers = 8

// ambientCheckers holds the cached checkers. order lists their keys oldest first,
// and the oldest is dropped when a new key would exceed maxAmbientCheckers.
var ambientCheckers = struct {
	mu      sync.Mutex
	entries map[ambientKey]*ambientEntry
	order   []ambientKey
}{entries: map[ambientKey]*ambientEntry{}}

// ambientChecker returns a checker for one run against the tree in dir, with the
// prelude and the ambient packages loaded and bound. The run loads every other
// package through source. groups are the run's groups from BuildPackageClosure. ok is false when a file in the closure cannot be read or
// an earlier load of the same tree panicked, and the caller then starts from
// newRunChecker.
func ambientChecker(dir string, source ModuleSource, groups PackageGroups, opts Options) (c *checker, ok bool) {
	key, ok := ambientKeyOf(dir, groups, opts)
	if !ok {
		return nil, false
	}

	ambientCheckers.mu.Lock()
	entry, held := ambientCheckers.entries[key]
	if !held {
		entry = &ambientEntry{}
		ambientCheckers.entries[key] = entry
		ambientCheckers.order = append(ambientCheckers.order, key)
		if len(ambientCheckers.order) > maxAmbientCheckers {
			delete(ambientCheckers.entries, ambientCheckers.order[0])
			ambientCheckers.order = ambientCheckers.order[1:]
		}
	}
	ambientCheckers.mu.Unlock()

	entry.once.Do(func() {
		entry.checker = loadAmbientChecker(dir, groups, opts)
	})
	// A load that panicked leaves no checker, and once does not run it again.
	if entry.checker == nil {
		return nil, false
	}
	run := entry.checker.fork(source, StdlibGroupSource(dir), groups)
	run.dependencies = opts.Dependencies
	return run, true
}

// ambientKeyOf returns the cache key for a run against dir that loads groups.
func ambientKeyOf(dir string, groups PackageGroups, opts Options) (ambientKey, bool) {
	uris := make([]string, 0, len(groups))
	for uri := range groups {
		uris = append(uris, uri)
	}
	sort.Strings(uris)

	sum := sha256.New()
	// A tree the prelude cannot be read from loads without one, so it hashes as
	// empty rather than turning the cache off.
	prelude, _ := readStdlibFile(dir, preludeURI)
	writeHashedFile(sum, preludeURI, prelude)
	for _, uri := range uris {
		contents, err := readStdlibFile(dir, uri)
		if err != nil {
			return ambientKey{}, false
		}
		writeHashedFile(sum, uri, contents)
	}

	key := ambientKey{
		dir:               dir,
		closure:           strings.Join(uris, ","),
		noAmbientNames:    opts.NoAmbientNames,
		checkJsDecorators: opts.CheckJsDecorators,
	}
	copy(key.digest[:], sum.Sum(nil))
	return key, true
}

// readStdlibFile returns the contents of the file uri names in dir.
func readStdlibFile(dir, uri string) ([]byte, error) {
	path, err := resolveStdlibPath(dir, uri)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// writeHashedFile writes uri and contents to digest, each behind its length, so
// moving bytes from the end of one file to the start of the next changes the hash.
func writeHashedFile(digest hash.Hash, uri string, contents []byte) {
	digest.Write(binary.AppendUvarint(nil, uint64(len(uri))))
	digest.Write([]byte(uri))
	digest.Write(binary.AppendUvarint(nil, uint64(len(contents))))
	digest.Write(contents)
}

// loadAmbientChecker returns a checker with the prelude and the ambient packages of
// dir loaded and bound, ready for fork. It holds nothing that refers to the run
// that built it.
func loadAmbientChecker(dir string, groups PackageGroups, opts Options) *checker {
	// The packages loaded here are pseudo-packages, which never reach node_modules
	// or the project's dependency list, so the load ignores both.
	c := newRunChecker(StdlibSource(dir), StdlibGroupSource(dir), groups, Options{
		NoAmbientNames:    opts.NoAmbientNames,
		CheckJsDecorators: opts.CheckJsDecorators,
	})
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
	// Every Info read is keyed by a node of the module being inferred, or by a node
	// of the user's own source in codegen and the language server. A run never reads
	// the entries the load recorded for stdlib nodes, so the cache keeps none and
	// fork has none to copy.
	c.info = NewInfo()
	c.info.ctx = c.ctx
	if frozenVarChecks {
		c.ctx.frozenVars, c.ctx.frozenLts = reachableVars(c)
	}
	return c
}

// reachableVars returns every type variable and every lifetime variable reachable
// from c.
func reachableVars(c *checker) (set.Set[*soltype.TypeVarType], set.Set[*soltype.LifetimeVar]) {
	// A copy visits every object reachable from c, and the copier's table of the
	// objects it copied names each one.
	g := newGraphCopier(isASTPointer)
	copyPtr(g, c)
	typeVar := reflect.TypeFor[soltype.TypeVarType]()
	lifetimeVar := reflect.TypeFor[soltype.LifetimeVar]()
	vars := set.NewSet[*soltype.TypeVarType]()
	lts := set.NewSet[*soltype.LifetimeVar]()
	for key := range g.pointers {
		switch key.plan.typ {
		case typeVar:
			vars.Add((*soltype.TypeVarType)(key.addr))
		case lifetimeVar:
			lts.Add((*soltype.LifetimeVar)(key.addr))
		}
	}
	return vars, lts
}

// fork returns a deep copy of c that runs with source, groupSource, and groups. A
// pointer to an AST node is not copied, so the copy reads the same parsed modules c
// does.
func (c *checker) fork(source ModuleSource, groupSource GroupSource, groups PackageGroups) *checker {
	// The parsed modules are already shared by every run through the parse cache,
	// so copying them here would only cost time.
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
