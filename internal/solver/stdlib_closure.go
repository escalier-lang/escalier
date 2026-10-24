package solver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// stdlib_closure.go works out which pseudo-packages a module's imports reach,
// so they can be read before any of them is inferred.
//
// Loading one package at a time cannot resolve a cycle: whichever goes first
// reaches a name the other has not declared yet. Reading the whole closure
// first and inferring it as one module lets the dep graph's own ordering
// resolve every reference, however the packages refer to each other.
//
// Nothing about a cycle needs refusing. A pseudo-package declares and runs
// nothing, so it has no initialization order to get wrong, and the dep graph
// already allows a cycle between declarations that build nothing.
//
// The closure is what a module's imports reach, not the whole directory. A
// program importing `web:fetch` reads that package and what it names, and a
// browser package it never mentions is neither read nor inferred.

// closureSchemes are the schemes a pseudo-package import may name. `node:` is
// reserved and populated by nothing, so it contributes no packages and no edges.
var closureSchemes = []string{"std", "web"}

// PackageGroups maps each pseudo-package a module's imports reach to the whole
// set they load with, sorted. Every reachable URI maps to the same set.
type PackageGroups map[string][]string

// GroupOf returns the set a URI loads with, and false for a URI the module's
// imports do not reach.
func (g PackageGroups) GroupOf(uri string) ([]string, bool) {
	group, ok := g[uri]
	return group, ok
}

// BuildPackageClosure scans dir for pseudo-packages, reads each one's import
// header, and returns everything the roots reach.
//
// A root naming a package the directory does not hold contributes nothing. The
// load reports that URI with a span, which is where the reader can act on it.
func BuildPackageClosure(dir string, roots []string) (PackageGroups, error) {
	edges, err := buildPackageGraph(dir)
	if err != nil {
		return nil, err
	}
	closure := reachable(edges, roots)
	groups := PackageGroups{}
	for _, uri := range closure {
		groups[uri] = closure
	}
	return groups, nil
}

// reachable returns every URI the roots reach through edges, sorted, roots
// included.
func reachable(edges map[string][]string, roots []string) []string {
	seen := set.NewSet[string]()
	stack := make([]string, 0, len(roots))
	for _, root := range roots {
		if _, held := edges[root]; held {
			stack = append(stack, root)
		}
	}
	for len(stack) > 0 {
		uri := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen.Contains(uri) {
			continue
		}
		seen.Add(uri)
		for _, next := range edges[uri] {
			if _, held := edges[next]; held && !seen.Contains(next) {
				stack = append(stack, next)
			}
		}
	}
	out := seen.ToSlice()
	sort.Strings(out)
	return out
}

// buildPackageGraph returns the URIs each `.esc` file under dir's scheme
// subdirectories imports.
//
// Every run asks for the same tree's graph, and reading the tree costs a few
// megabytes of I/O per run. So the graph is cached per directory and reused
// while each file's size and modification time match what the cache recorded.
func buildPackageGraph(dir string) (map[string][]string, error) {
	stamps, err := scanPackageFiles(dir)
	if err != nil {
		return nil, err
	}
	if edges, held := packageGraphs.get(dir, stamps); held {
		return edges, nil
	}

	edges := make(map[string][]string, len(stamps))
	for _, stamp := range stamps {
		imports, err := readPackageImports(stamp.path)
		if err != nil {
			return nil, err
		}
		edges[stamp.uri] = imports
	}
	packageGraphs.put(dir, stamps, edges)
	return edges, nil
}

// packageFileStamp identifies one pseudo-package file by what a stat reports.
type packageFileStamp struct {
	uri     string
	path    string
	size    int64
	modTime time.Time
}

// scanPackageFiles stats every `.esc` file under dir's scheme subdirectories,
// in scheme order and then by file name.
func scanPackageFiles(dir string) ([]packageFileStamp, error) {
	var stamps []packageFileStamp
	for _, scheme := range closureSchemes {
		schemeDir := filepath.Join(dir, scheme)
		entries, err := os.ReadDir(schemeDir)
		if err != nil {
			// A scheme with no directory contributes no packages, which is what a
			// tree holding only `std/` looks like.
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot scan %s: %w", schemeDir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".esc") {
				continue
			}
			path := filepath.Join(schemeDir, entry.Name())
			// os.Stat rather than entry.Info, so an edit to the target of a
			// symlinked file changes the stamp.
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("cannot stat %s: %w", path, err)
			}
			stamps = append(stamps, packageFileStamp{
				uri:     scheme + ":" + strings.TrimSuffix(entry.Name(), ".esc"),
				path:    path,
				size:    info.Size(),
				modTime: info.ModTime(),
			})
		}
	}
	return stamps, nil
}

// packageGraphCache holds one import graph per stdlib directory, with the stamps
// of the files it was read from.
//
// A stamp misses an edit that keeps the file's size and lands within the
// filesystem's timestamp granularity of the previous write. put guards against
// that by refusing a graph read from any file modified within racyWindow of the
// read, so a later edit can only produce a modification time the cache has not
// recorded.
type packageGraphCache struct {
	mu      sync.Mutex
	entries map[string]packageGraphEntry
}

// packageGraphEntry is one directory's graph and the stamps it was read under.
type packageGraphEntry struct {
	stamps []packageFileStamp
	edges  map[string][]string
}

// racyWindow is the coarsest timestamp granularity a filesystem the stdlib may
// sit on records. FAT records two seconds.
const racyWindow = 2 * time.Second

var packageGraphs = &packageGraphCache{entries: map[string]packageGraphEntry{}}

// get returns the graph cached for dir when it was read under exactly stamps.
// The caller must not mutate the returned map or its slices.
func (c *packageGraphCache) get(dir string, stamps []packageFileStamp) (map[string][]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, held := c.entries[dir]
	if !held || !slices.EqualFunc(entry.stamps, stamps, sameStamp) {
		return nil, false
	}
	return entry.edges, true
}

// put records edges as dir's graph under stamps. It records nothing when a file
// was modified within racyWindow of now, and drops whatever dir held before.
func (c *packageGraphCache) put(dir string, stamps []packageFileStamp, edges map[string][]string) {
	cutoff := time.Now().Add(-racyWindow)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, stamp := range stamps {
		if !stamp.modTime.Before(cutoff) {
			delete(c.entries, dir)
			return
		}
	}
	c.entries[dir] = packageGraphEntry{stamps: stamps, edges: edges}
}

// sameStamp reports whether a and b describe the same file at the same state.
func sameStamp(a, b packageFileStamp) bool {
	return a.uri == b.uri && a.path == b.path && a.size == b.size && a.modTime.Equal(b.modTime)
}

// readPackageImports returns the pseudo-package URIs one file imports.
//
// The parse goes through the shared cache, under the same basename and contents
// StdlibSource parses with. The closure is built from the same files the load
// then reads, so each is parsed once and both steps use that one result.
//
// A parse error is ignored, since the same file is parsed again when it loads
// and the error is reported there with a span. Truncating the import list puts
// the file in a smaller closure than it belongs to, which changes the shape of
// the failure rather than hiding it.
func readPackageImports(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	base := filepath.Base(path)
	module, err := stdlibParses.get(base, string(contents), func(sourceID int) (*ast.Module, error) {
		parsed, parseErrs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
			ID: sourceID, Path: base, Contents: string(contents),
		}})
		if len(parseErrs) > 0 {
			return nil, fmt.Errorf("parse errors in %s", path)
		}
		return parsed, nil
	})
	if err != nil {
		return nil, nil
	}

	var imports []string
	for _, file := range module.Files {
		for _, stmt := range file.Imports {
			scheme, _, ok := splitScheme(stmt.PackageName)
			if ok && slices.Contains(closureSchemes, scheme) {
				imports = append(imports, stmt.PackageName)
			}
		}
	}
	return imports, nil
}
