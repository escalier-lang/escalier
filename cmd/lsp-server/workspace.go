package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

	doc, ok := s.documents[uri]
	if !ok {
		fmt.Fprintf(os.Stderr, "document not found: %s\n", uri)
		return nil, fmt.Errorf("document not found: %s", uri)
	}

	if doc.LanguageID != "escalier" {
		return nil, fmt.Errorf("unsupported language: %s", doc.LanguageID)
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
	buildDir := filepath.Join(rootPath, "build")
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

	// Walk the filesystem to discover all lib/ and bin/ source files.
	libFiles, err := s.findLibFiles()
	if err != nil {
		return nil, err
	}

	binFiles, err := s.findBinFiles()
	if err != nil {
		return nil, err
	}

	allFiles := append(libFiles, binFiles...)

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
		rel, _ := filepath.Rel(rootPath, absPath)
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
			ID:       stableSourceID(rel),
			Path:     rel,
			Contents: contents,
		})
	}

	return sources, nil
}

// isLibEscFileURI returns true if uri refers to a .esc file inside the lib/ directory.
func (s *Server) isLibEscFileURI(uri string) bool {
	return strings.HasSuffix(uri, ".esc") && s.isModuleFile(protocol.DocumentUri(uri))
}

func (s *Server) workspaceDidCreateFiles(context *glsp.Context, params *protocol.CreateFilesParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range params.Files {
		if s.isLibEscFileURI(file.URI) {
			s.libFilesCache[uriToPath(file.URI)] = struct{}{}
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
	}
	return nil
}
