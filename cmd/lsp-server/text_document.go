package main

import (
	"fmt"
	"hash/fnv"
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

// LSP uses 0-based line and column indices, while Escalier uses 1-based.
func spanToRange(span ast.Span) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{
			Line:      protocol.UInteger(span.Start.Line - 1),
			Character: protocol.UInteger(span.Start.Column - 1),
		},
		End: protocol.Position{
			Line:      protocol.UInteger(span.End.Line - 1),
			Character: protocol.UInteger(span.End.Column - 1),
		},
	}
}

func posToLoc(pos protocol.Position) ast.Location {
	return ast.Location{
		Line:   int(pos.Line) + 1,      // Convert to 1-based index
		Column: int(pos.Character) + 1, // Convert to 1-based index
	}
}

func (*Server) textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	loc := posToLoc(params.Position)
	sourceID := s.sourceIDForURI(params.TextDocument.URI)
	s.mu.RLock()
	var node ast.Node
	if co := s.checkOutput; co != nil {
		if script, ok := co.Scripts[sourceID]; ok {
			node = findNodeInScript(script, loc)
		} else if co.Module != nil {
			node, _ = findNodeWithAncestorsInFile(co.Module, sourceID, loc)
		}
	}
	s.mu.RUnlock()
	if node == nil {
		return nil, fmt.Errorf("textDocument/definition: node not found")
	}

	if node == nil {
		return nil, fmt.Errorf("textDocument/definition: node not found")
	}

	switch node := node.(type) {
	case *ast.IdentExpr:
		if node.Source == nil {
			return nil, fmt.Errorf("textDocument/definition: node.Decl is nil")
		}
		var span ast.Span
		switch provenance := node.Source.(type) {
		case *ast.NodeProvenance:
			span = provenance.Node.Span()
		default:
			panic(fmt.Sprintf("textDocument/definition: unexpected provenance type %T", node.Source))
		}
		loc := protocol.Location{
			URI:   params.TextDocument.URI,
			Range: spanToRange(span),
		}

		return loc, nil
	default:
		return nil, fmt.Errorf("textDocument/definition: node is not an IdentExpr")
	}
}

func (s *Server) textDocumentTypeDefinition(context *glsp.Context, params *protocol.TypeDefinitionParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentTypeDefinition - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/typeDefinition not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDidOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.mu.Lock()
	s.documents[params.TextDocument.URI] = params.TextDocument
	s.packageGen++
	s.mu.Unlock()
	if params.TextDocument.LanguageID == "escalier" {
		s.validate(context, params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
	}
	return nil
}

func (s *Server) textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	s.mu.Lock()
	doc := s.documents[params.TextDocument.URI]

	for _, change := range params.ContentChanges {
		switch change := change.(type) {
		case protocol.TextDocumentContentChangeEvent:
			s.mu.Unlock()
			return fmt.Errorf("incremental changes not supported")
		case protocol.TextDocumentContentChangeEventWhole:
			s.documents[params.TextDocument.URI] = protocol.TextDocumentItem{
				URI:        params.TextDocument.URI,
				LanguageID: doc.LanguageID,
				Version:    params.TextDocument.Version,
				Text:       change.Text,
			}
		}
	}
	s.packageGen++
	s.mu.Unlock()

	if doc.LanguageID == "escalier" {
		// Use only the last content change since we're in full-sync mode
		// and only the final state matters.
		lastChange := params.ContentChanges[len(params.ContentChanges)-1].(protocol.TextDocumentContentChangeEventWhole)
		go s.validate(context, params.TextDocument.URI, lastChange.Text, params.TextDocument.Version)
	}
	return nil
}

func (server *Server) textDocumentHover(context *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	fmt.Fprintf(os.Stderr, "textDocumentHover - uri = %s\n", params.TextDocument.URI)

	loc := posToLoc(params.Position)
	value := fmt.Sprintf(
		"textDocumentHover - loc = line:%d, column:%d\n",
		loc.Line,
		loc.Column,
	)

	sourceID := server.sourceIDForURI(params.TextDocument.URI)
	server.mu.RLock()
	var hoverNode ast.Node
	if co := server.checkOutput; co != nil {
		if script, ok := co.Scripts[sourceID]; ok {
			hoverNode = findNodeInScript(script, loc)
		} else if co.Module != nil {
			hoverNode, _ = findNodeWithAncestorsInFile(co.Module, sourceID, loc)
		}
	}
	server.mu.RUnlock()
	if hoverNode != nil {
		switch node := hoverNode.(type) {
		case ast.Expr:
			if node.InferredType() != nil {
				value = "`" + node.InferredType().String() + "`"
			}
		case ast.Pat:
			if node.InferredType() != nil {
				value = "`" + node.InferredType().String() + "`"
			}
		default:
			// do nothing
		}
	}

	hover := protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: value,
		},
		Range: nil,
	}
	return &hover, nil
}

func addr[T any](x T) *T {
	return &x
}

func (*Server) textDocumentCodeAction(context *glsp.Context, params *protocol.CodeActionParams) (any, error) {
	compileAction := protocol.CodeAction{
		Title:       "Compile",
		Kind:        addr("compile"),
		Diagnostics: []protocol.Diagnostic{},
		IsPreferred: nil,
		Disabled:    nil,
		Edit:        nil, // Require the client to make a workspace/executeCommand request
		Command: &protocol.Command{
			Title:     "Compile",
			Command:   "compile",
			Arguments: []any{},
		},
		Data: nil,
	}

	codeActions := []protocol.CodeAction{compileAction}

	return codeActions, nil
}

// isCacheStale returns true if the document has been updated since the last
// successful validation, or if any sibling file has changed since the last
// package-level validation.
// Must be called while holding mu.RLock().
func (s *Server) isCacheStale(uri protocol.DocumentUri) bool {
	doc, ok := s.documents[uri]
	if !ok {
		return false
	}
	validated, ok := s.validatedVersion[uri]
	if !ok {
		return true
	}
	if doc.Version != validated {
		return true
	}
	if s.packageGen != s.packageValidatedGen {
		return true
	}
	return false
}

// isModuleFile checks if a URI corresponds to a file under the lib/ directory.
func (s *Server) isModuleFile(uri protocol.DocumentUri) bool {
	if s.rootURI == "" {
		return false
	}
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(string(uri))
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return false
	}
	return strings.HasPrefix(rel, "lib/") || strings.HasPrefix(rel, "lib\\")
}

// findLibFiles discovers all .esc files in the lib/ directory under the workspace root.
func (s *Server) findLibFiles() ([]string, error) {
	rootPath := uriToPath(s.rootURI)
	libDir := filepath.Join(rootPath, "lib")

	var files []string
	err := filepath.WalkDir(libDir, func(path string, d fs.DirEntry, err error) error {
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

// refreshLibFilesCache scans lib/ and stores absolute .esc file paths in memory.
func (s *Server) refreshLibFilesCache() error {
	files, err := s.findLibFiles()
	if err != nil {
		return err
	}

	cache := make(map[string]struct{}, len(files))
	for _, file := range files {
		cache[file] = struct{}{}
	}

	s.mu.Lock()
	s.libFilesCache = cache
	s.mu.Unlock()

	return nil
}

// cachedLibFilesSnapshot returns a stable snapshot of cached lib file paths.
func (s *Server) cachedLibFilesSnapshot() []string {
	s.mu.RLock()
	files := make([]string, 0, len(s.libFilesCache))
	for file := range s.libFilesCache {
		files = append(files, file)
	}
	s.mu.RUnlock()

	sort.Strings(files)
	return files
}

// stableSourceID returns a deterministic integer ID for a relative file path.
// This ensures IDs remain stable across re-parses regardless of file discovery
// order or files being added/removed.
func stableSourceID(relPath string) int {
	h := fnv.New32a()
	h.Write([]byte(relPath))
	return int(h.Sum32())
}

// sourceIDForURI computes the stable source ID for a document URI.
func (s *Server) sourceIDForURI(uri protocol.DocumentUri) int {
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(string(uri))
	rel, _ := filepath.Rel(rootPath, filePath)
	return stableSourceID(rel)
}

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string, version protocol.Integer) {
	// Check staleness before doing expensive work.
	server.mu.RLock()
	currentDoc := server.documents[uri]
	server.mu.RUnlock()
	if currentDoc.Version != version {
		server.validated.Broadcast()
		return
	}

	rootPath := uriToPath(server.rootURI)

	// Collect all source files (lib/ + bin/) with in-memory content for open docs.
	sources, err := server.collectSources(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: failed to collect sources: %v\n", err)
		server.validated.Broadcast()
		return
	}

	// Override the current file's content with the latest from the editor,
	// since collectSources reads from s.documents which may have been
	// updated to a newer version than the one we're validating.
	triggerSourceID := server.sourceIDForURI(uri)
	for _, src := range sources {
		if src.ID == triggerSourceID {
			src.Contents = contents
			break
		}
	}

	// Run package-level type checking (no codegen).
	output := compiler.CheckPackage(sources)

	// Verify that no document versions changed during validation.
	server.mu.Lock()
	currentDoc = server.documents[uri]
	if currentDoc.Version != version {
		server.mu.Unlock()
		server.validated.Broadcast()
		return
	}
	server.checkOutput = &output
	server.packageValidatedGen = server.packageGen
	server.validatedVersion[uri] = version
	// Also update validatedVersion for all other open documents that were
	// included in this check.
	for docURI, doc := range server.documents {
		if docURI != uri && doc.LanguageID == "escalier" {
			server.validatedVersion[docURI] = doc.Version
		}
	}
	server.mu.Unlock()
	server.validated.Broadcast()

	// Publish diagnostics for all files.
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	diagsBySourceID := make(map[int][]protocol.Diagnostic)
	for _, err := range output.ParseErrors {
		diagsBySourceID[err.Span.SourceID] = append(diagsBySourceID[err.Span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(err.Span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message,
		})
	}
	for _, err := range output.TypeErrors {
		span := err.Span()
		diagsBySourceID[span.SourceID] = append(diagsBySourceID[span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message(),
		})
	}

	// Publish diagnostics for all files. Guard against nil context
	// (e.g., in tests without an LSP connection).
	if lspContext.Notify == nil {
		return
	}
	server.mu.RLock()
	if output.Module != nil {
		for _, file := range output.Module.Files {
			fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, file.Path)))
			fileDiags := emptyIfNil(diagsBySourceID[file.SourceID])
			params := &protocol.PublishDiagnosticsParams{
				URI:         fileURI,
				Diagnostics: fileDiags,
			}
			if doc, ok := server.documents[fileURI]; ok {
				v := protocol.UInteger(doc.Version)
				params.Version = &v
			}
			go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
		}
	}
	// Publish diagnostics for bin/ files.
	for srcID := range output.Scripts {
		// Find the source path for this script.
		for _, src := range sources {
			if src.ID == srcID {
				fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, src.Path)))
				fileDiags := emptyIfNil(diagsBySourceID[srcID])
				params := &protocol.PublishDiagnosticsParams{
					URI:         fileURI,
					Diagnostics: fileDiags,
				}
				if doc, ok := server.documents[fileURI]; ok {
					v := protocol.UInteger(doc.Version)
					params.Version = &v
				}
				go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
				break
			}
		}
	}
	server.mu.RUnlock()
}

// emptyIfNil returns an empty slice if the input is nil, ensuring JSON
// serialization produces [] instead of null.
func emptyIfNil(diags []protocol.Diagnostic) []protocol.Diagnostic {
	if diags == nil {
		return []protocol.Diagnostic{}
	}
	return diags
}
