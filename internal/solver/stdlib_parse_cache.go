package solver

import (
	"strconv"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_parse_cache.go keeps the parsed form of each pseudo-package file, so a
// second load of the same source does not re-parse it.
//
// Every inference run loads `std:prelude`, thousands of times over one run of
// the solver's suite, and parsing is about half the cost of a load. The other
// half is inference. A run through InferModuleAgainstStdlib or
// InferScriptAgainstStdlib reads the inferred packages from the cache in
// stdlib_run_cache.go. Any other run infers them itself.
//
// Sharing the parsed module is safe because nothing that runs over it writes to
// an AST node. Inferred types go in the per-run Info side table, rename-pass
// VarIDs go in the per-run liveness.VarIDs table, and resolved namespaces go in
// DepGraph.IdentNamespaces. A dependency graph is built once per cached module
// and shared too, since inference only reads it.

// parsedModuleCache holds one parsed module per distinct source, keyed by the
// file's basename and contents, which are the two inputs the parse reads.
//
// The basename is in the key because the entry records it: a parsed module
// carries an ast.Source holding the name the parse was given, and GetSourcePath
// reads it back. Keying on contents alone would let two packages that happen to
// share a body resolve to one module under whichever basename parsed first.
//
// The directory is out of the key and no modification time is consulted.
// Keying on the path and comparing a size-and-mtime stamp is the obvious
// alternative, and three things decide against it.
//
//  1. Two directories holding the same file share one parse. Each test seeding
//     a stdlib tree writes the same prelude to a fresh temporary directory, so
//     a path key pins an identical module per test and serves no hit between
//     them. This is where most of what the cache saves comes from.
//  2. Nothing stale survives an edit, because changed content is a different
//     key. A stamp can miss a write landing at the same size within the
//     filesystem's timestamp granularity, and a language server runs for hours
//     and outlives a contributor regenerating the tree underneath it.
//  3. The cache is bounded by how many distinct sources a process reads rather
//     than how many directories it visits.
//
// Reading the file is not saved, since the contents are what the key is built
// from. That costs microseconds against a parse that costs milliseconds.
//
// The key holds the two strings rather than a digest of them. Go hashes the
// fields and compares them for equality on a hash match, so two distinct
// sources can never resolve to one entry, where a digest would have to be
// trusted without that comparison. It is also far cheaper. A SHA-256 over the
// megabyte of `web/dom.esc` costs milliseconds on every lookup, hit or miss.
//
// The key holds no memory of its own. The entry's module carries an ast.Source
// recording the same name and contents.
type parsedModuleCache struct {
	mu      sync.Mutex
	entries map[parseKey]parsedModuleEntry
	// groups holds one merged module per distinct set of members. A group parse
	// produces one module from many sources, which the per-source entries above
	// cannot be composed into: the merge is what places each member's
	// declarations under its own namespace, and only the parser does it.
	groups map[groupKey]parsedModuleEntry
	// nextSourceID is handed out once per distinct source, so every package in
	// a process parses under an id of its own and keeps it across runs. A span
	// carries its source id into provenance and into every diagnostic built from
	// it, and two packages sharing one would make a "declared here" from either
	// read as the other's.
	nextSourceID int
	// modules holds every module an entry in entries or groups holds.
	modules set.Set[*ast.Module]
	// graphs holds one dependency graph per cached module and the group it loads as.
	graphs map[depGraphKey]*dep_graph.DepGraph
}

// depGraphKey identifies a dependency graph by the module it is built over and by
// the members of the group the module loads as. group is empty for a module that
// loads as a single package.
type depGraphKey struct {
	module *ast.Module
	group  string
}

// parseKey identifies a source by the two inputs the parse reads.
type parseKey struct {
	name     string
	contents string
}

// groupKey identifies a merged parse by its name and by every member's synthetic
// path and contents, in order.
type groupKey struct {
	name     string
	paths    string
	contents string
}

// joinParts builds one string from parts that no other list of parts can build,
// by writing each part's length before it. A separator alone would not do: with
// one, ["a", "b"] and ["a<sep>b"] join to the same thing, and nothing rules a
// separator out of a source file.
func joinParts(parts []string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
	}
	return b.String()
}

// parsedModuleEntry is one parsed module and the source id it parsed under.
type parsedModuleEntry struct {
	module   *ast.Module
	sourceID int
}

func newParsedModuleCache(firstSourceID int) *parsedModuleCache {
	return &parsedModuleCache{
		entries:      map[parseKey]parsedModuleEntry{},
		groups:       map[groupKey]parsedModuleEntry{},
		modules:      set.NewSet[*ast.Module](),
		graphs:       map[depGraphKey]*dep_graph.DepGraph{},
		nextSourceID: firstSourceID,
	}
}

// get returns the module parsed from the file called name holding contents,
// calling parse with a fresh source id on a miss.
//
// The lock is held across the parse, so a second caller for the same contents
// waits rather than parsing it again. That serializes parses of DIFFERENT
// sources too, which one small mutex costs and one process's stdlib can afford.
func (c *parsedModuleCache) get(
	name, contents string,
	parse func(sourceID int) (*ast.Module, error),
) (*ast.Module, error) {
	key := parseKey{name: name, contents: contents}
	return parseOnce(c, c.entries, key, 1, parse)
}

// getGroup returns the module parsed from the members called paths holding
// contents, calling parse with the first of len(paths) consecutive source ids on
// a miss.
//
// A group is cached as one module rather than as its members, since the merge is
// what puts each member's declarations under its own namespace. Parsing the
// members separately and merging afterwards would mean doing the parser's job
// again.
func (c *parsedModuleCache) getGroup(
	paths, contents []string,
	parse func(firstSourceID int) (*ast.Module, error),
) (*ast.Module, error) {
	return c.getNamedGroup("", paths, contents, parse)
}

// getNamedGroup is getGroup for a parse whose module depends on name as well as
// on its members. Two calls with the same members and different names are cached
// apart.
func (c *parsedModuleCache) getNamedGroup(
	name string,
	paths, contents []string,
	parse func(firstSourceID int) (*ast.Module, error),
) (*ast.Module, error) {
	key := groupKey{name: name, paths: joinParts(paths), contents: joinParts(contents)}
	return parseOnce(c, c.groups, key, len(paths), parse)
}

// parseOnce returns the module entries holds under key, calling parse with the
// next free source id on a miss. A successful parse consumes ids consecutive
// source ids, starting at the one parse was given, and its module joins
// c.modules. A failure is not cached and consumes no ids, so the next load of the
// same key retries.
//
// parseOnce holds c.mu for the whole call, parse included.
func parseOnce[K comparable](
	c *parsedModuleCache,
	entries map[K]parsedModuleEntry,
	key K,
	ids int,
	parse func(firstSourceID int) (*ast.Module, error),
) (*ast.Module, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, held := entries[key]; held {
		return entry.module, nil
	}

	firstSourceID := c.nextSourceID
	module, err := parse(firstSourceID)
	if err != nil {
		return nil, err
	}
	c.nextSourceID += ids
	entries[key] = parsedModuleEntry{module: module, sourceID: firstSourceID}
	c.modules.Add(module)
	return module, nil
}

// depGraph returns the dependency graph build produces for module loaded as the
// group with members group, sorted. group is empty for a module that loads as a
// single package.
//
// The graph is built once per module and group when the cache holds module, and
// every later call returns that same graph. A caller must only read it. A module
// the cache does not hold gets a fresh graph on every call, so the graphs held
// grow only with the parsed modules.
//
// For a cached module, build runs with the lock held, the way a parse does, so a
// second caller for the same module waits rather than building it again.
func (c *parsedModuleCache) depGraph(
	module *ast.Module,
	group []string,
	build func() *dep_graph.DepGraph,
) *dep_graph.DepGraph {
	c.mu.Lock()
	if !c.modules.Contains(module) {
		c.mu.Unlock()
		return build()
	}
	defer c.mu.Unlock()
	key := depGraphKey{module: module, group: joinParts(group)}
	if g, held := c.graphs[key]; held {
		return g
	}
	g := build()
	c.graphs[key] = g
	return g
}

// stdlibParses is the process-wide cache every StdlibSource reads. Its ids start
// above any an entry module assigns, which are handed out from zero per module.
var stdlibParses = newParsedModuleCache(stdlibSourceIDBase)
