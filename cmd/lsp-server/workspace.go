package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) workspaceExecuteCommand(context *glsp.Context, params *protocol.ExecuteCommandParams) (any, error) {
	if params.Command != "compile" {
		return nil, fmt.Errorf("unknown command: %s", params.Command)
	}

	if len(params.Arguments) != 1 {
		return nil, fmt.Errorf("invalid arguments: %v", params.Arguments)
	}

	uri, ok := params.Arguments[0].(protocol.DocumentUri)
	if !ok {
		return nil, fmt.Errorf("invalid argument: %v", params.Arguments[0])
	}

	if !strings.HasSuffix(string(uri), ".esc") {
		return nil, fmt.Errorf("unsupported file type: %s", uri)
	}

	s.mu.RLock()
	_, isOpen := s.documents[uri]
	s.mu.RUnlock()
	if !isOpen {
		return nil, fmt.Errorf("document not open: %s", uri)
	}

	rootPath := uriToPath(s.rootURI)

	// Collect all source files (lib/ + bin/) with in-memory content for open docs.
	sources, err := s.collectSources(rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to collect sources: %v", err)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no source files found")
	}

	// Compile the entire package.
	output := compiler.CompilePackage(sources)

	if len(output.ParseErrors) > 0 {
		errorsJSON, err := json.Marshal(output.ParseErrors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal compilation errors: %v", err)
		}
		return nil, errors.New(string(errorsJSON))
	}

	// Write output files to the build/ directory in the virtual filesystem.
	// Remove any stale artifacts from a previous build first.
	buildDir := filepath.Join(rootPath, "build")
	if err := os.RemoveAll(buildDir); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to clean build directory: %v", err)
	}
	for name, module := range output.Modules {
		// name is like "lib/index" or "bin/main"
		jsPath := filepath.Join(buildDir, name+".js")
		mapPath := filepath.Join(buildDir, name+".js.map")

		// Create parent directories
		if err := os.MkdirAll(filepath.Dir(jsPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory: %v", err)
		}

		// Write .js
		if err := os.WriteFile(jsPath, []byte(module.JS), 0644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %v", jsPath, err)
		}

		// Write .js.map (pretty-printed)
		var prettyMap bytes.Buffer
		if err := json.Indent(&prettyMap, []byte(module.SourceMap), "", "  "); err != nil {
			prettyMap.WriteString(module.SourceMap)
		}
		if err := os.WriteFile(mapPath, prettyMap.Bytes(), 0644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %v", mapPath, err)
		}

		// Write .d.ts for lib/ modules only
		if strings.HasPrefix(name, "lib/") && module.DTS != "" {
			dtsPath := filepath.Join(buildDir, name+".d.ts")
			if err := os.WriteFile(dtsPath, []byte(module.DTS), 0644); err != nil {
				return nil, fmt.Errorf("failed to write %s: %v", dtsPath, err)
			}
		}
	}

	return map[string]any{"success": true}, nil
}

// findBinFiles discovers all .esc files in the bin/ directory under the workspace root.
func (s *Server) findBinFiles() ([]string, error) {
	rootPath := uriToPath(s.rootURI)
	binDir := filepath.Join(rootPath, "bin")

	var files []string
	err := filepath.WalkDir(binDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".esc") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	return files, nil
}

// collectSources gathers all lib/ and bin/ .esc source files, using in-memory
// content for documents open in the editor and reading from disk otherwise.
func (s *Server) collectSources(rootPath string) ([]*ast.Source, error) {
	var sources []*ast.Source

	// Use cached file lists instead of walking the filesystem each time.
	libFiles := s.cachedLibFilesSnapshot()
	binFiles := s.cachedBinFilesSnapshot()

	allFiles := make([]string, 0, len(libFiles)+len(binFiles))
	allFiles = append(allFiles, libFiles...)
	allFiles = append(allFiles, binFiles...)

	// Snapshot in-memory documents under the lock.
	s.mu.RLock()
	openDocs := make(map[protocol.DocumentUri]string)
	for _, absPath := range allFiles {
		fileURI := protocol.DocumentUri(pathToURI(absPath))
		if doc, ok := s.documents[fileURI]; ok {
			openDocs[fileURI] = doc.Text
		}
	}
	s.mu.RUnlock()

	for _, absPath := range allFiles {
		rel, err := filepath.Rel(rootPath, absPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "collectSources: filepath.Rel(%s, %s): %s\n", rootPath, absPath, err)
			continue
		}
		norm := filepath.ToSlash(rel)
		fileURI := protocol.DocumentUri(pathToURI(absPath))
		var contents string
		if text, ok := openDocs[fileURI]; ok {
			contents = text
		} else {
			data, err := os.ReadFile(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "collectSources: error reading %s: %s\n", absPath, err)
				continue
			}
			contents = string(data)
		}
		sources = append(sources, &ast.Source{
			ID:       stableSourceID(norm),
			Path:     norm,
			Contents: contents,
		})
	}

	return sources, nil
}

// refreshBinFilesCache scans bin/ and stores absolute .esc file paths in memory.
func (s *Server) refreshBinFilesCache() error {
	files, err := s.findBinFiles()
	if err != nil {
		return err
	}

	cache := make(map[string]struct{}, len(files))
	for _, file := range files {
		cache[file] = struct{}{}
	}

	s.mu.Lock()
	s.binFilesCache = cache
	s.mu.Unlock()

	return nil
}

// cachedBinFilesSnapshot returns a stable snapshot of cached bin file paths.
func (s *Server) cachedBinFilesSnapshot() []string {
	s.mu.RLock()
	files := make([]string, 0, len(s.binFilesCache))
	for file := range s.binFilesCache {
		files = append(files, file)
	}
	s.mu.RUnlock()

	sort.Strings(files)
	return files
}

// isLibEscFileURI returns true if uri refers to a .esc file inside the lib/ directory.
func (s *Server) isLibEscFileURI(uri string) bool {
	return strings.HasSuffix(uri, ".esc") && s.isModuleFile(protocol.DocumentUri(uri))
}

// isBinEscFileURI returns true if uri refers to a .esc file inside the bin/ directory.
func (s *Server) isBinEscFileURI(uri string) bool {
	if !strings.HasSuffix(uri, ".esc") || s.rootURI == "" {
		return false
	}
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(uri)
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(rel, "bin/") || strings.HasPrefix(rel, "bin\\")
}

func (s *Server) workspaceDidCreateFiles(context *glsp.Context, params *protocol.CreateFilesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range params.Files {
		if s.isLibEscFileURI(file.URI) {
			s.libFilesCache[uriToPath(file.URI)] = struct{}{}
		}
		if s.isBinEscFileURI(file.URI) {
			s.binFilesCache[uriToPath(file.URI)] = struct{}{}
		}
	}
	return nil
}

func (s *Server) workspaceDidRenameFiles(context *glsp.Context, params *protocol.RenameFilesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range params.Files {
		if s.isLibEscFileURI(file.OldURI) {
			delete(s.libFilesCache, uriToPath(file.OldURI))
		}
		if s.isLibEscFileURI(file.NewURI) {
			s.libFilesCache[uriToPath(file.NewURI)] = struct{}{}
		}
		if s.isBinEscFileURI(file.OldURI) {
			delete(s.binFilesCache, uriToPath(file.OldURI))
		}
		if s.isBinEscFileURI(file.NewURI) {
			s.binFilesCache[uriToPath(file.NewURI)] = struct{}{}
		}
	}
	return nil
}

func (s *Server) workspaceDidDeleteFiles(context *glsp.Context, params *protocol.DeleteFilesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range params.Files {
		if s.isLibEscFileURI(file.URI) {
			delete(s.libFilesCache, uriToPath(file.URI))
		}
		if s.isBinEscFileURI(file.URI) {
			delete(s.binFilesCache, uriToPath(file.URI))
		}
	}
	return nil
}
