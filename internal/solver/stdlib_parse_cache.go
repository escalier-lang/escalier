package solver

import (
	"crypto/sha256"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
)

// stdlib_parse_cache.go keeps the parsed form of each pseudo-package file, so a
// second load of the same source does not re-parse it.
//
// Every inference run loads `std:prelude`, since the checker's own rules name
// the types it declares. One run of the solver's own suite loads it thousands of
// times, and parsing is about half the cost of a load. The rest is inference,
// which stays per run: the prelude's declarations mint type variables and
// register class and alias definitions on the run's Context, and both are
// mutable state a second run must not share. #1567 covers sharing that half.
//
// Sharing the parsed module is safe because the solver never writes to an AST
// node. It records inferred types in the Info side table, keyed by node, and
// each run holds its own. TestACachedModuleServesIndependentRuns pins that.

// parsedModuleCache holds one parsed module per distinct source.
//
// The key is a hash of the file's basename and its contents, which are the two
// inputs the parse reads. The directory is deliberately not in it, and that
// decides three things at once.
//
//  1. A file that changed is a different key, so no stamp is compared and no
//     stale parse can survive an edit. A language server runs for hours and
//     outlives a contributor regenerating the tree underneath it.
//  2. Two directories holding the same file share one parse. Each test seeding
//     a stdlib tree writes the same prelude to a fresh temporary directory, so
//     keying on the full path would pin thousands of identical ASTs and serve
//     no hit between them.
//  3. The cache is bounded by how many distinct package sources a process
//     reads, rather than by how many directories it visits.
//
// The basename is in the key because the entry records it. A parsed module
// carries an ast.Source per source id, holding the basename the parse was given,
// and GetSourcePath reads it back. Keying on contents alone would let two
// packages that happen to share a body resolve to one module under whichever
// basename was parsed first.
//
// Reading the file is not saved, since the contents are what the key is computed
// from. That costs microseconds against a parse that costs milliseconds.
type parsedModuleCache struct {
	mu      sync.Mutex
	entries map[[sha256.Size]byte]parsedModuleEntry
	// nextSourceID is handed out once per distinct source, so every package in
	// a process parses under an id of its own and keeps it across runs. A span
	// carries its source id into provenance and into every diagnostic built from
	// it, and two packages sharing one would make a "declared here" from either
	// read as the other's.
	nextSourceID int
}

// parsedModuleEntry is one parsed module and the source id it parsed under.
type parsedModuleEntry struct {
	module   *ast.Module
	sourceID int
}

func newParsedModuleCache(firstSourceID int) *parsedModuleCache {
	return &parsedModuleCache{
		entries:      map[[sha256.Size]byte]parsedModuleEntry{},
		nextSourceID: firstSourceID,
	}
}

// get returns the module parsed from the file called name holding contents,
// calling parse with a fresh source id on a miss.
//
// The lock is held across the parse, so a second caller for the same contents
// waits rather than parsing it again. That serializes parses of DIFFERENT
// sources too, which is the cost of keeping the whole thing one small mutex. A
// load is milliseconds and the contention window is one process's stdlib, so the
// simpler structure is worth more than the parallelism.
func (c *parsedModuleCache) get(
	name, contents string,
	parse func(sourceID int) (*ast.Module, error),
) (*ast.Module, error) {
	// A NUL separates the two parts. A basename cannot hold one, so the first NUL
	// always ends the name and no two pairs hash the same bytes.
	key := sha256.Sum256([]byte(name + "\x00" + contents))

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

// stdlibParses is the process-wide cache every StdlibSource reads. Its ids start
// above any an entry module assigns, which are handed out from zero per module.
var stdlibParses = newParsedModuleCache(stdlibSourceIDBase)
