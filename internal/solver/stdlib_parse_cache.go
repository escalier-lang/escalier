package solver

import (
	"os"
	"sync"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
)

// stdlib_parse_cache.go keeps the parsed form of each pseudo-package file, so a
// second run against the same stdlib directory does not re-read and re-parse it.
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
// each run holds its own. `TestACachedModuleServesIndependentRuns` pins that.

// parsedModuleCache holds one parsed module per file, keyed by path and checked
// against the file's size and modification time.
//
// The stamp is what makes the cache safe in a process that outlives an edit. A
// generated tree does not change under a compiler run, but a language server
// runs for hours and a contributor regenerating the tree underneath it would
// otherwise keep getting the parse from before. Stating the file costs
// microseconds against a parse that costs milliseconds.
type parsedModuleCache struct {
	mu      sync.Mutex
	entries map[string]parsedModuleEntry
}

// parsedModuleEntry is one cached parse and the file stamp it was read at.
type parsedModuleEntry struct {
	module  *ast.Module
	size    int64
	modTime time.Time
}

func newParsedModuleCache() *parsedModuleCache {
	return &parsedModuleCache{entries: map[string]parsedModuleEntry{}}
}

// stampOf reads the size and modification time the cache compares against. A
// file it cannot stat has no stamp, and every load of it parses.
func stampOf(path string) (int64, time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, time.Time{}, false
	}
	return info.Size(), info.ModTime(), true
}

// get returns the module cached for path, parsing it with parse on a miss.
//
// The parse runs outside the lock only in the sense that a second caller for the
// same path waits for the first. Holding the lock across the parse is what keeps
// one file from being parsed twice concurrently, which is the case worth
// avoiding: the whole point is to parse each file once.
func (c *parsedModuleCache) get(path string, parse func() (*ast.Module, error)) (*ast.Module, error) {
	size, modTime, stamped := stampOf(path)

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, held := c.entries[path]; held && stamped &&
		entry.size == size && entry.modTime.Equal(modTime) {
		return entry.module, nil
	}
	module, err := parse()
	if err != nil {
		// A failure is not cached. The next load retries rather than inheriting a
		// read error from a file that may since have appeared.
		return nil, err
	}
	if stamped {
		c.entries[path] = parsedModuleEntry{module: module, size: size, modTime: modTime}
	}
	return module, nil
}

// stdlibParses is the process-wide cache every StdlibSource reads.
var stdlibParses = newParsedModuleCache()
