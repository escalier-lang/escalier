package solver

import (
	"strconv"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
)

// stdlib_parse_cache.go keeps the parsed form of each pseudo-package file, so a
// second load of the same source does not re-parse it.
//
// Every inference run loads `std:prelude`, thousands of times over one run of
// the solver's suite, and parsing is about half the cost of a load. The other
// half is inference, which stays per run: the prelude's declarations mint type
// variables and register definitions on the run's Context, both mutable state a
// second run must not share. #1567 covers that half.
//
// Sharing the parsed module is safe because the solver never writes to an AST
// node, recording inferred types in the per-run Info side table instead.

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
}

// parseKey identifies a source by the two inputs the parse reads.
type parseKey struct {
	name     string
	contents string
}

// groupKey identifies a merged parse by every member's synthetic path and
// contents, in order.
type groupKey struct {
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

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, held := c.entries[key]; held {
		return entry.module, nil
	}

	sourceID := c.nextSourceID
	module, err := parse(sourceID)
	if err != nil {
		// A failure is not cached, and the id it would have used is not consumed.
		// The next load of the same source retries.
		return nil, err
	}
	c.nextSourceID++
	c.entries[key] = parsedModuleEntry{module: module, sourceID: sourceID}
	return module, nil
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
	key := groupKey{paths: joinParts(paths), contents: joinParts(contents)}

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, held := c.groups[key]; held {
		return entry.module, nil
	}

	firstSourceID := c.nextSourceID
	module, err := parse(firstSourceID)
	if err != nil {
		// Not cached and no id consumed, so the next load of the same members
		// retries. The single-source path answers a failure the same way.
		return nil, err
	}
	c.nextSourceID += len(paths)
	c.groups[key] = parsedModuleEntry{module: module, sourceID: firstSourceID}
	return module, nil
}

// stdlibParses is the process-wide cache every StdlibSource reads. Its ids start
// above any an entry module assigns, which are handed out from zero per module.
var stdlibParses = newParsedModuleCache(stdlibSourceIDBase)
