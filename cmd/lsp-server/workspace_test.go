package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/escalier-lang/escalier/internal/set"
)

// newTestServer creates a Server wired to a temporary workspace root
// containing a lib/ directory with the given relative .esc file paths.
func newTestServer(t *testing.T, libFiles []string) (*Server, string) {
	t.Helper()

	root := t.TempDir()
	for _, rel := range libFiles {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("val x: number = 1\n"), 0o644))
	}

	s := &Server{
		documents:        map[protocol.DocumentUri]protocol.TextDocumentItem{},
		validatedVersion: map[protocol.DocumentUri]protocol.Integer{},
		libFilesCache:    set.NewSet[string](),
		binFilesCache:    set.NewSet[string](),
		rootURI:          pathToURI(root),
	}
	return s, root
}

// libCacheKeys returns a copy of the server's lib file cache.
func libCacheKeys(s *Server) set.Set[string] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return set.FromSlice(s.libFilesCache.ToSlice())
}

// --- refreshLibFilesCache ---

// writeLibFile replaces the workspace's single lib source with src. compilePackage
// reads the cached file list rather than the filesystem, so the cache is refreshed
// here to match what was just written.
func writeLibFile(t *testing.T, s *Server, root, src string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "lib", "index.esc"), []byte(src), 0o644))
	require.NoError(t, s.refreshLibFilesCache())
}

// buildDirFiles returns the paths under the workspace's build/ directory,
// relative to that directory, so a test can name what a compile emitted.
func buildDirFiles(t *testing.T, root string) []string {
	t.Helper()
	buildDir := filepath.Join(root, "build")
	var found []string
	err := filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(buildDir, path)
		if err != nil {
			return err
		}
		// ToSlash so the paths read the same however the platform spells a separator,
		// since the expectations below are written with `/`.
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	sort.Strings(found)
	return found
}

// A type error stops the write, and the artifacts the last clean compile
// produced stay on disk rather than being replaced by output lowered from the
// rejected tree.
func TestCompilePackageKeepsArtifactsWhenTheSourceDoesNotCheck(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/index.esc"})
	writeLibFile(t, s, root, "export val n: number = 1\n")

	_, err := s.compilePackage()
	require.NoError(t, err)
	clean := buildDirFiles(t, root)
	require.Equal(t, []string{"lib/index.d.ts", "lib/index.js", "lib/index.js.map"}, clean)

	dts, err := os.ReadFile(filepath.Join(root, "build", "lib", "index.d.ts"))
	require.NoError(t, err)

	writeLibFile(t, s, root, `export val n: number = "not a number"`+"\n")

	_, err = s.compilePackage()
	require.Error(t, err)
	require.Equal(t, `"not a number" cannot be assigned to number`, err.Error())

	require.Equal(t, clean, buildDirFiles(t, root))
	after, err := os.ReadFile(filepath.Join(root, "build", "lib", "index.d.ts"))
	require.NoError(t, err)
	require.Equal(t, string(dts), string(after))
}

// A parse error stops the write at the same point, so the same artifacts survive.
func TestCompilePackageKeepsArtifactsWhenTheSourceDoesNotParse(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/index.esc"})
	writeLibFile(t, s, root, "export val n: number = 1\n")

	_, err := s.compilePackage()
	require.NoError(t, err)
	clean := buildDirFiles(t, root)
	require.Equal(t, []string{"lib/index.d.ts", "lib/index.js", "lib/index.js.map"}, clean)

	writeLibFile(t, s, root, "export val broken =\n")

	_, err = s.compilePackage()
	require.Error(t, err)
	// The error is the marshalled parse errors, whose spans carry a SourceID derived
	// from the file's path under a per-run temp directory. Decoding and asserting the
	// message keeps the whole diagnostic pinned without pinning that id.
	var parseErrs []struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(err.Error()), &parseErrs))
	require.Len(t, parseErrs, 1)
	require.Equal(t, "Expected an expression", parseErrs[0].Message)

	require.Equal(t, clean, buildDirFiles(t, root))
}

func TestRefreshLibFilesCache_PopulatesFromDisk(t *testing.T) {
	s, root := newTestServer(t, []string{
		"lib/a.esc",
		"lib/sub/b.esc",
	})

	require.NoError(t, s.refreshLibFilesCache())

	cache := libCacheKeys(s)
	assert.Contains(t, cache, filepath.Join(root, "lib", "a.esc"))
	assert.Contains(t, cache, filepath.Join(root, "lib", "sub", "b.esc"))
	assert.Len(t, cache, 2)
}

func TestRefreshLibFilesCache_IgnoresNonEscFiles(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	// add a non-.esc file manually
	require.NoError(t, os.WriteFile(filepath.Join(root, "lib", "readme.md"), []byte("hi"), 0o644))

	require.NoError(t, s.refreshLibFilesCache())

	cache := libCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.Contains(t, cache, filepath.Join(root, "lib", "a.esc"))
}

func TestRefreshLibFilesCache_EmptyWhenLibDirAbsent(t *testing.T) {
	// No lib files created, so no lib/ directory exists.
	s, _ := newTestServer(t, nil)

	require.NoError(t, s.refreshLibFilesCache())

	assert.Empty(t, libCacheKeys(s))
}

func TestRefreshLibFilesCache_UpdatesOnChange(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 1)

	// Add a second file and refresh again.
	newFile := filepath.Join(root, "lib", "b.esc")
	require.NoError(t, os.WriteFile(newFile, []byte("val y: number = 2\n"), 0o644))
	require.NoError(t, s.refreshLibFilesCache())

	cache := libCacheKeys(s)
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, newFile)
}

// --- cachedLibFilesSnapshot ---

func TestCachedLibFilesSnapshot_ReturnsSortedPaths(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/c.esc", "lib/a.esc", "lib/b.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	snap := s.cachedLibFilesSnapshot()
	require.Len(t, snap, 3)
	assert.Equal(t, filepath.Join(root, "lib", "a.esc"), snap[0])
	assert.Equal(t, filepath.Join(root, "lib", "b.esc"), snap[1])
	assert.Equal(t, filepath.Join(root, "lib", "c.esc"), snap[2])
}

func TestCachedLibFilesSnapshot_EmptyWhenCacheEmpty(t *testing.T) {
	s, _ := newTestServer(t, nil)
	assert.Empty(t, s.cachedLibFilesSnapshot())
}

// --- workspaceDidCreateFiles ---

func TestWorkspaceDidCreateFiles_RefreshesCacheForLibFile(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 1)

	// Write a new file on disk then send the LSP notification.
	newFile := filepath.Join(root, "lib", "b.esc")
	require.NoError(t, os.WriteFile(newFile, []byte("val y: number = 2\n"), 0o644))

	err := s.workspaceDidCreateFiles(
		&glsp.Context{},
		&protocol.CreateFilesParams{
			Files: []protocol.FileCreate{{URI: pathToURI(newFile)}},
		},
	)
	require.NoError(t, err)

	cache := libCacheKeys(s)
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, newFile)
}

func TestWorkspaceDidCreateFiles_IgnoresNonLibFile(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	// Notification for a file outside lib/ — cache should stay unchanged.
	outsideFile := filepath.Join(root, "other", "x.esc")
	require.NoError(t, os.MkdirAll(filepath.Dir(outsideFile), 0o755))
	require.NoError(t, os.WriteFile(outsideFile, []byte(""), 0o644))

	before := libCacheKeys(s)
	err := s.workspaceDidCreateFiles(
		&glsp.Context{},
		&protocol.CreateFilesParams{
			Files: []protocol.FileCreate{{URI: pathToURI(outsideFile)}},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, before, libCacheKeys(s))
}

// --- workspaceDidRenameFiles ---

func TestWorkspaceDidRenameFiles_RefreshesOnOldURIMatch(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 1)

	// Rename a.esc → b.esc on disk, then notify.
	oldFile := filepath.Join(root, "lib", "a.esc")
	newFile := filepath.Join(root, "lib", "b.esc")
	require.NoError(t, os.Rename(oldFile, newFile))

	err := s.workspaceDidRenameFiles(
		&glsp.Context{},
		&protocol.RenameFilesParams{
			Files: []protocol.FileRename{{OldURI: pathToURI(oldFile), NewURI: pathToURI(newFile)}},
		},
	)
	require.NoError(t, err)

	cache := libCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.Contains(t, cache, newFile)
	assert.NotContains(t, cache, oldFile)
}

func TestWorkspaceDidRenameFiles_RefreshesOnNewURIMatch(t *testing.T) {
	// old path is outside lib/, new path is inside lib/
	s, root := newTestServer(t, nil)
	require.NoError(t, s.refreshLibFilesCache())

	newFile := filepath.Join(root, "lib", "moved.esc")
	require.NoError(t, os.MkdirAll(filepath.Dir(newFile), 0o755))
	require.NoError(t, os.WriteFile(newFile, []byte("val z: number = 3\n"), 0o644))

	outsideFile := filepath.Join(root, "scratch", "moved.esc")

	err := s.workspaceDidRenameFiles(
		&glsp.Context{},
		&protocol.RenameFilesParams{
			Files: []protocol.FileRename{{OldURI: pathToURI(outsideFile), NewURI: pathToURI(newFile)}},
		},
	)
	require.NoError(t, err)

	assert.Contains(t, libCacheKeys(s), newFile)
}

func TestWorkspaceDidRenameFiles_IgnoresNonLibFiles(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	outsideOld := filepath.Join(root, "scratch", "x.esc")
	outsideNew := filepath.Join(root, "scratch", "y.esc")

	before := libCacheKeys(s)
	err := s.workspaceDidRenameFiles(
		&glsp.Context{},
		&protocol.RenameFilesParams{
			Files: []protocol.FileRename{{OldURI: pathToURI(outsideOld), NewURI: pathToURI(outsideNew)}},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, before, libCacheKeys(s))
}

// --- workspaceDidDeleteFiles ---

func TestWorkspaceDidDeleteFiles_RefreshesCacheForLibFile(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc", "lib/b.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 2)

	// Delete one file on disk then send the LSP notification.
	deletedFile := filepath.Join(root, "lib", "a.esc")
	require.NoError(t, os.Remove(deletedFile))

	err := s.workspaceDidDeleteFiles(
		&glsp.Context{},
		&protocol.DeleteFilesParams{
			Files: []protocol.FileDelete{{URI: pathToURI(deletedFile)}},
		},
	)
	require.NoError(t, err)

	cache := libCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.NotContains(t, cache, deletedFile)
}

func TestWorkspaceDidDeleteFiles_IgnoresNonLibFile(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	outsideFile := filepath.Join(root, "other", "x.esc")

	before := libCacheKeys(s)
	err := s.workspaceDidDeleteFiles(
		&glsp.Context{},
		&protocol.DeleteFilesParams{
			Files: []protocol.FileDelete{{URI: pathToURI(outsideFile)}},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, before, libCacheKeys(s))
}

// --- workspaceDidChangeWatchedFiles ---

func TestDidChangeWatchedFiles_CreatedAddsToLibCache(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 1)

	newFile := filepath.Join(root, "lib", "b.esc")
	require.NoError(t, os.WriteFile(newFile, []byte("val y: number = 2\n"), 0o644))

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(newFile)), Type: protocol.FileChangeTypeCreated},
			},
		},
	)
	require.NoError(t, err)

	cache := libCacheKeys(s)
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, newFile)
}

func TestDidChangeWatchedFiles_DeletedRemovesFromLibCache(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc", "lib/b.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	assert.Len(t, libCacheKeys(s), 2)

	deletedFile := filepath.Join(root, "lib", "a.esc")
	require.NoError(t, os.Remove(deletedFile))

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(deletedFile)), Type: protocol.FileChangeTypeDeleted},
			},
		},
	)
	require.NoError(t, err)

	cache := libCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.NotContains(t, cache, deletedFile)
}

func TestDidChangeWatchedFiles_ChangedDoesNotAffectCache(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	existingFile := filepath.Join(root, "lib", "a.esc")
	before := libCacheKeys(s)

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(existingFile)), Type: protocol.FileChangeTypeChanged},
			},
		},
	)
	require.NoError(t, err)

	assert.Equal(t, before, libCacheKeys(s))
}

func TestDidChangeWatchedFiles_CreatedAddsToBinCache(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{"bin/main.esc"})
	require.NoError(t, s.refreshBinFilesCache())
	assert.Len(t, binCacheKeys(s), 1)

	newFile := filepath.Join(root, "bin", "other.esc")
	require.NoError(t, os.WriteFile(newFile, []byte("val y = 2\n"), 0o644))

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(newFile)), Type: protocol.FileChangeTypeCreated},
			},
		},
	)
	require.NoError(t, err)

	cache := binCacheKeys(s)
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, newFile)
}

func TestDidChangeWatchedFiles_DeletedRemovesFromBinCache(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{"bin/main.esc", "bin/other.esc"})
	require.NoError(t, s.refreshBinFilesCache())
	assert.Len(t, binCacheKeys(s), 2)

	deletedFile := filepath.Join(root, "bin", "main.esc")
	require.NoError(t, os.Remove(deletedFile))

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(deletedFile)), Type: protocol.FileChangeTypeDeleted},
			},
		},
	)
	require.NoError(t, err)

	cache := binCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.NotContains(t, cache, deletedFile)
}

func TestDidChangeWatchedFiles_IgnoresNonEscFiles(t *testing.T) {
	s, root := newTestServer(t, []string{"lib/a.esc"})
	require.NoError(t, s.refreshLibFilesCache())

	jsFile := filepath.Join(root, "lib", "a.js")
	before := libCacheKeys(s)

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(jsFile)), Type: protocol.FileChangeTypeCreated},
			},
		},
	)
	require.NoError(t, err)

	assert.Equal(t, before, libCacheKeys(s))
}

func TestDidChangeWatchedFiles_MultipleEventsInOneNotification(t *testing.T) {
	s, root := newTestServerWithBin(t, []string{"lib/a.esc"}, []string{"bin/main.esc"})
	require.NoError(t, s.refreshLibFilesCache())
	require.NoError(t, s.refreshBinFilesCache())

	newLibFile := filepath.Join(root, "lib", "b.esc")
	require.NoError(t, os.WriteFile(newLibFile, []byte("val y = 2\n"), 0o644))

	deletedBinFile := filepath.Join(root, "bin", "main.esc")
	require.NoError(t, os.Remove(deletedBinFile))

	err := s.workspaceDidChangeWatchedFiles(
		&glsp.Context{},
		&protocol.DidChangeWatchedFilesParams{
			Changes: []protocol.FileEvent{
				{URI: protocol.DocumentUri(pathToURI(newLibFile)), Type: protocol.FileChangeTypeCreated},
				{URI: protocol.DocumentUri(pathToURI(deletedBinFile)), Type: protocol.FileChangeTypeDeleted},
			},
		},
	)
	require.NoError(t, err)

	assert.Len(t, libCacheKeys(s), 2)
	assert.Contains(t, libCacheKeys(s), newLibFile)
	assert.Empty(t, binCacheKeys(s))
}

// --- bin files cache helpers ---

// binCacheKeys returns a copy of the server's bin file cache.
func binCacheKeys(s *Server) set.Set[string] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return set.FromSlice(s.binFilesCache.ToSlice())
}

// newTestServerWithBin creates a Server with both lib/ and bin/ files.
func newTestServerWithBin(t *testing.T, libFiles, binFiles []string) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	for _, rel := range libFiles {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("val x: number = 1\n"), 0o644))
	}
	for _, rel := range binFiles {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("val x: number = 1\n"), 0o644))
	}
	s := &Server{
		documents:        map[protocol.DocumentUri]protocol.TextDocumentItem{},
		validatedVersion: map[protocol.DocumentUri]protocol.Integer{},
		libFilesCache:    set.NewSet[string](),
		binFilesCache:    set.NewSet[string](),
		rootURI:          pathToURI(root),
	}
	return s, root
}

// --- refreshBinFilesCache ---

func TestRefreshBinFilesCache_PopulatesFromDisk(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{
		"bin/main.esc",
		"bin/sub/other.esc",
	})

	require.NoError(t, s.refreshBinFilesCache())

	cache := binCacheKeys(s)
	assert.Contains(t, cache, filepath.Join(root, "bin", "main.esc"))
	assert.Contains(t, cache, filepath.Join(root, "bin", "sub", "other.esc"))
	assert.Len(t, cache, 2)
}

func TestRefreshBinFilesCache_EmptyWhenBinDirAbsent(t *testing.T) {
	s, _ := newTestServerWithBin(t, nil, nil)

	require.NoError(t, s.refreshBinFilesCache())

	assert.Empty(t, binCacheKeys(s))
}

// --- workspace notifications update bin cache ---

func TestWorkspaceDidCreateFiles_UpdatesBinCache(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{"bin/main.esc"})
	require.NoError(t, s.refreshBinFilesCache())
	assert.Len(t, binCacheKeys(s), 1)

	newFile := filepath.Join(root, "bin", "other.esc")
	require.NoError(t, os.WriteFile(newFile, []byte("val y = 2\n"), 0o644))

	err := s.workspaceDidCreateFiles(
		&glsp.Context{},
		&protocol.CreateFilesParams{
			Files: []protocol.FileCreate{{URI: pathToURI(newFile)}},
		},
	)
	require.NoError(t, err)

	cache := binCacheKeys(s)
	assert.Len(t, cache, 2)
	assert.Contains(t, cache, newFile)
}

func TestWorkspaceDidDeleteFiles_UpdatesBinCache(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{"bin/main.esc", "bin/other.esc"})
	require.NoError(t, s.refreshBinFilesCache())
	assert.Len(t, binCacheKeys(s), 2)

	deletedFile := filepath.Join(root, "bin", "main.esc")
	require.NoError(t, os.Remove(deletedFile))

	err := s.workspaceDidDeleteFiles(
		&glsp.Context{},
		&protocol.DeleteFilesParams{
			Files: []protocol.FileDelete{{URI: pathToURI(deletedFile)}},
		},
	)
	require.NoError(t, err)

	cache := binCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.NotContains(t, cache, deletedFile)
}

func TestWorkspaceDidRenameFiles_UpdatesBinCache(t *testing.T) {
	s, root := newTestServerWithBin(t, nil, []string{"bin/old.esc"})
	require.NoError(t, s.refreshBinFilesCache())

	oldFile := filepath.Join(root, "bin", "old.esc")
	newFile := filepath.Join(root, "bin", "new.esc")
	require.NoError(t, os.Rename(oldFile, newFile))

	err := s.workspaceDidRenameFiles(
		&glsp.Context{},
		&protocol.RenameFilesParams{
			Files: []protocol.FileRename{{OldURI: pathToURI(oldFile), NewURI: pathToURI(newFile)}},
		},
	)
	require.NoError(t, err)

	cache := binCacheKeys(s)
	assert.Len(t, cache, 1)
	assert.Contains(t, cache, newFile)
	assert.NotContains(t, cache, oldFile)
}
