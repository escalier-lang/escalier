package solver

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/graphcopy"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// stdlib_run_cache.go keeps one checker per standard library tree with the prelude
// and the ambient packages already loaded. A run against that tree starts from a fork
// of it instead of inferring the packages again.
//
// A fork shares every type, definition, scope, and namespace the cached checker holds,
// and has its own copy of each container a run adds to. Sharing is safe because no run
// writes to anything the load created:
//
//   - A run constrains copies of a stdlib type's variables, never the variables
//     themselves. checkThawed enforces this when frozenVarChecks is set.
//   - settleLoadedDefinitions computes before caching every fact about a loaded class
//     or alias that a run would otherwise compute and store on it.
//   - A run adds its own entries to its own copy of each registry and side table.
//
// The cached checker is therefore only ever read, and runs against one tree can run
// concurrently.

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

// ambientChecker returns a checker for one run against the standard library in dir,
// with the prelude and the ambient packages loaded and bound. dir is the root of the
// stdlib tree, the directory holding the `std/`, `web/`, and `node/` pseudo-package
// files, so one dir serves every package checked against that stdlib.
//
// The run loads every other package through source. groups are the run's groups from
// BuildPackageClosure. ok is false when a file in the closure cannot be read or an
// earlier load of the same tree panicked, and the caller then starts from
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
	prelude, err := stdlibFileDigest(dir, preludeURI)
	if err != nil {
		prelude = sha256.Sum256(nil)
	}
	writeHashedFile(sum, preludeURI, prelude)
	for _, uri := range uris {
		digest, err := stdlibFileDigest(dir, uri)
		if err != nil {
			return ambientKey{}, false
		}
		writeHashedFile(sum, uri, digest)
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

// stdlibFileDigest returns the SHA-256 of the file uri names in dir.
func stdlibFileDigest(dir, uri string) ([sha256.Size]byte, error) {
	path, err := resolveStdlibPath(dir, uri)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return fileDigests.digest(path)
}

// writeHashedFile writes uri and the digest of its file to sum, uri behind its
// length, so no two lists of files hash to the same key.
func writeHashedFile(sum hash.Hash, uri string, digest [sha256.Size]byte) {
	sum.Write(binary.AppendUvarint(nil, uint64(len(uri))))
	sum.Write([]byte(uri))
	sum.Write(digest[:])
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
	// A run reads a loaded class's variance and an alias's uniform knot through the
	// cached definitions. Settling both now means no run writes to those definitions.
	c.settleLoadedDefinitions()

	// The copy below cannot copy a func, so every field holding one is cleared here.
	// fork sets each one again on a run's checker.
	c.ctx.fusionRecorder = nil
	c.source, c.groupSource, c.groups = nil, nil, nil
	// Both hold the signature phase's per-declaration state, which carries funcs.
	// They are read only while the declaration they key is inferred, and every
	// declaration in the loaded packages is finished.
	c.classShells, c.builtClassSigs = nil, nil
	// Every Info read is keyed by a node of the module being inferred, or by a node
	// of the user's own source in codegen and the language server. A run never reads
	// the entries the load recorded for stdlib nodes, so the cache keeps none.
	c.info = NewInfo()
	c.info.ctx = c.ctx
	// These are keyed by the node or function being inferred, and only while it is, so
	// a run never reads the loaded packages' entries either.
	c.ltMarks, c.memberFuncNames, c.methodSelfParams = nil, nil, nil
	c.closureCaptureInfo, c.moduleCaptures = nil, nil
	// The copy allocates every slice with no spare capacity. An append by a run to a
	// slice the cached checker holds then allocates, rather than writing into an array
	// that every run sharing the checker can see.
	g := graphcopy.New(isASTPointer)
	c = graphcopy.Copy(g, c)
	// Every run's fork reads through this table, and forks run concurrently.
	c.prov.freeze()
	if frozenVarChecks {
		// The copier's table of what it copied names every variable the cached checker
		// reaches.
		c.ctx.frozenVars = set.FromSlice(graphcopy.Copies[soltype.TypeVarType](g))
		c.ctx.frozenLts = set.FromSlice(graphcopy.Copies[soltype.LifetimeVar](g))
		c.ctx.frozenClasses = set.FromSlice(slices.Collect(maps.Values(c.ctx.classes)))
		c.ctx.frozenAliases = set.FromSlice(slices.Collect(maps.Values(c.ctx.aliases)))
	}
	return c
}

// settleLoadedDefinitions computes every fact about the classes and aliases registered so
// far that would otherwise be computed the first time something reads it:
//
//  1. Each provisional class is measured again until no class settles. A class still
//     provisional after that has its Invariant vectors stored as final.
//  2. Each alias's uniform knot is probed and memoized.
//
// A second call does nothing. The type variables and unique symbols it mints come from
// the library counters. The μ-binders a knot mints continue the run's own count, since
// two knots composed into one type need distinct binders.
func (c *checker) settleLoadedDefinitions() {
	if c.definitionsSettled {
		return
	}
	c.definitionsSettled = true
	classNames := slices.Sorted(maps.Keys(c.ctx.classes))
	aliasNames := slices.Sorted(maps.Keys(c.ctx.aliases))
	c.withLibCounters(func() {
		// settleVariance skips a class tried at the current count, and a try that waited
		// on a class measured later bumped nothing. Each is tried once more regardless.
		for _, name := range classNames {
			if def := c.ctx.classes[name]; def.varianceProvisional {
				def.varianceTriedAt = -1
			}
		}
		for {
			settled := c.ctx.measuredClasses
			for _, name := range classNames {
				c.ctx.settleVariance(c.ctx.classes[name])
			}
			if c.ctx.measuredClasses == settled {
				break
			}
		}
		for _, name := range classNames {
			// Every class these wait on is registered and measured by now, so no later
			// measurement settles them. Invariant is sound for every parameter. Storing it
			// as final keeps settleVarianceGroup from pulling the class into the group of
			// a class declared after this point.
			if def := c.ctx.classes[name]; def.varianceProvisional {
				def.varianceProvisional = false
				def.varianceWaitsOn = nil
			}
		}
		for _, name := range aliasNames {
			c.ctx.uniformKnotOf(name)
		}
	})
}

// astPackage is the import path of internal/ast.
var astPackage = reflect.TypeFor[ast.Span]().PkgPath()

// isASTPointer reports whether t points at a type declared in internal/ast.
func isASTPointer(t reflect.Type) bool {
	return t.Elem().PkgPath() == astPackage
}

// fork returns a checker that runs with source, groupSource, and groups over what c
// loaded. The two share every type, definition, scope, and namespace c holds, and the
// fork gets its own copy of every container a run adds to. The fork's Prov is a layer
// over c's, so it reads every origin c recorded and writes only its own.
//
// c must not be written to while it is shared. Two forks of c can run concurrently, and
// neither reaches the other's state.
func (c *checker) fork(source ModuleSource, groupSource GroupSource, groups PackageGroups) *checker {
	f := *c
	f.ctx = c.ctx.fork()
	f.ctx.fusionRecorder = f.recordFusionEdge
	f.info = NewInfo()
	f.info.ctx = f.ctx
	f.source, f.groupSource, f.groups = source, groupSource, groups

	f.prov = c.prov.child()
	f.varIDs = c.varIDs.Clone()
	f.errs = slices.Clip(c.errs)
	f.memberFuncNames = maps.Clone(c.memberFuncNames)
	f.closureCaptureInfo = clipValues(c.closureCaptureInfo)
	f.moduleCaptures = clipValues(c.moduleCaptures)
	f.openVars = maps.Clone(c.openVars)
	f.preludeNames = slices.Clip(c.preludeNames)
	f.namedLifetimes = maps.Clone(c.namedLifetimes)
	f.ltMarks = maps.Clone(c.ltMarks)
	f.methodSelfParams = maps.Clone(c.methodSelfParams)
	f.declLifetimes = maps.Clone(c.declLifetimes)
	f.componentClasses = maps.Clone(c.componentClasses)
	f.componentClassOf = maps.Clone(c.componentClassOf)
	f.memberNamespaces = maps.Clone(c.memberNamespaces)
	f.nsIndex = maps.Clone(c.nsIndex)
	f.symbolOwners = maps.Clone(c.symbolOwners)
	f.weakBindings = slices.Clip(c.weakBindings)
	f.weakReaders = slices.Clip(c.weakReaders)
	f.weakLocals = slices.Clip(c.weakLocals)
	f.weakBindingVars = set.NewSet[*soltype.TypeVarType]()
	f.fileScopes = maps.Clone(c.fileScopes)
	f.activeGroup = maps.Clone(c.activeGroup)
	f.moduleDeclared = declaredNames{values: maps.Clone(c.moduleDeclared.values), types: maps.Clone(c.moduleDeclared.types)}
	f.loadStack = slices.Clip(c.loadStack)
	f.pendingRecvReads = slices.Clip(c.pendingRecvReads)
	f.pendingMutations = slices.Clip(c.pendingMutations)
	f.deferredArgBounds = slices.Clip(c.deferredArgBounds)
	f.pendingReturns = slices.Clip(c.pendingReturns)
	f.pendingOverrides = slices.Clip(c.pendingOverrides)
	f.pendingImplements = slices.Clip(c.pendingImplements)
	f.provisionalVariance = slices.Clip(c.provisionalVariance)

	// Whether each held diagnostic is reported is the run's own.
	f.heldLoadErrs = make(map[string]*heldLoadErrors, len(c.heldLoadErrs))
	f.heldLoadOrder = make([]*heldLoadErrors, len(c.heldLoadOrder))
	copies := make(map[*heldLoadErrors]*heldLoadErrors, len(c.heldLoadOrder))
	for i, held := range c.heldLoadOrder {
		copied := *held
		f.heldLoadOrder[i] = &copied
		copies[held] = &copied
	}
	for uri, held := range c.heldLoadErrs {
		f.heldLoadErrs[uri] = copies[held]
	}

	// A run that binds the globals scope itself, as one with Options.NoAmbientNames does
	// when it loads an npm package, fills a scope of its own.
	if !c.globalsBound {
		f.globals = nil
	}
	return &f
}

// fork returns a Context that shares every definition and type c holds and has its
// own copy of every container a run adds to. c's counters carry over, and the fork has
// no probe open.
func (c *Context) fork() *Context {
	f := *c
	f.probe = nil
	f.paramVars = maps.Clone(c.paramVars)
	f.readRaises = clipValues(c.readRaises)
	f.pendingRigidLts = slices.Clip(c.pendingRigidLts)
	f.packages = &PackageRegistry{packages: maps.Clone(c.packages.packages)}
	f.ltProxyOrigin = maps.Clone(c.ltProxyOrigin)
	f.methodLifetimes = maps.Clone(c.methodLifetimes)
	f.classes = maps.Clone(c.classes)
	f.aliases = maps.Clone(c.aliases)
	f.aliasInterns = maps.Clone(c.aliasInterns)
	f.uniformKnots = maps.Clone(c.uniformKnots)
	f.unfoldingAliases = maps.Clone(c.unfoldingAliases)
	f.buildingAliases = maps.Clone(c.buildingAliases)
	f.borrowedReads = clipValues(c.borrowedReads)
	f.slotModes = maps.Clone(c.slotModes)
	f.unionCommits = maps.Clone(c.unionCommits)
	f.holeVars = maps.Clone(c.holeVars)
	return &f
}

// clipValues returns a copy of m whose slice values have no spare capacity, so an append
// to one of them allocates rather than writing into the array m's value shares.
func clipValues[K comparable, V any](m map[K][]V) map[K][]V {
	if m == nil {
		return nil
	}
	out := make(map[K][]V, len(m))
	for k, v := range m {
		out[k] = slices.Clip(v)
	}
	return out
}
