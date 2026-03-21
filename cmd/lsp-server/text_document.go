package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
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
	s.mu.RLock()
	script := s.astCache[params.TextDocument.URI]
	s.mu.RUnlock()
	if script == nil {
		return nil, fmt.Errorf("textDocument/definition: script not found")
	}
	node := findNodeInScript(script, loc)

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

	server.mu.RLock()
	script := server.astCache[params.TextDocument.URI]
	server.mu.RUnlock()
	if script != nil {
		node := findNodeInScript(script, loc)

		if node != nil {
			switch node := node.(type) {
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
// successful validation. Must be called while holding mu.RLock().
func (s *Server) isCacheStale(uri protocol.DocumentUri) bool {
	doc, ok := s.documents[uri]
	if !ok {
		return false
	}
	validated, ok := s.validatedVersion[uri]
	if !ok {
		return true
	}
	return doc.Version != validated
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

// relPath returns the path of a URI relative to the workspace root.
func (s *Server) relPath(uri protocol.DocumentUri) string {
	rootPath := uriToPath(s.rootURI)
	filePath := uriToPath(string(uri))
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return filePath
	}
	return rel
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

// getSourceIDForModule finds the SourceID for a relative path in a module.
func getSourceIDForModule(module *ast.Module, relPath string) (int, bool) {
	for _, file := range module.Files {
		if file.Path == relPath {
			return file.SourceID, true
		}
	}
	return 0, false
}

func (server *Server) validateModule(lspContext *glsp.Context, uri protocol.DocumentUri, version protocol.Integer) {
	fmt.Fprintf(os.Stderr, "validateModule")

	// Check staleness before doing expensive work.
	server.mu.RLock()
	currentDoc := server.documents[uri]
	server.mu.RUnlock()
	if currentDoc.Version != version {
		return
	}

	rootPath := uriToPath(server.rootURI)

	// Use cached lib file paths maintained at startup and by workspace events.
	libFiles := server.cachedLibFilesSnapshot()

	// Build sources: use in-memory content for open files, read from disk for others.
	// Source IDs are derived from the relative path hash so they remain stable
	// regardless of file discovery order or files being added/removed.
	//
	// First, snapshot in-memory documents under the lock, then do disk I/O
	// outside the lock to avoid blocking other operations. We also capture each
	// open file's version so we can verify nothing changed after validation.
	type docSnapshot struct {
		Text    string
		Version protocol.Integer
	}
	openDocs := make(map[protocol.DocumentUri]docSnapshot)
	server.mu.RLock()
	for _, absPath := range libFiles {
		fileURI := protocol.DocumentUri(pathToURI(absPath))
		if doc, ok := server.documents[fileURI]; ok {
			openDocs[fileURI] = docSnapshot{Text: doc.Text, Version: doc.Version}
		}
	}
	server.mu.RUnlock()

	var sources []*ast.Source
	for _, absPath := range libFiles {
		rel, _ := filepath.Rel(rootPath, absPath)
		fileURI := protocol.DocumentUri(pathToURI(absPath))
		var contents string
		if snap, ok := openDocs[fileURI]; ok {
			contents = snap.Text
		} else {
			data, err := os.ReadFile(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "validateModule: error reading %s: %s\n", absPath, err)
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

	if len(sources) == 0 {
		server.mu.Lock()
		currentDoc := server.documents[uri]
		if currentDoc.Version == version {
			server.moduleCache = nil
			server.moduleScopeCache = nil
			server.fileScopeCache = map[int]*checker.Scope{}
		}
		server.mu.Unlock()
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	module, parseErrors := parser.ParseLibFiles(ctx, sources)

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	typeErrors := c.InferModule(inferCtx, module)

	// Store module cache and file scopes. Verify that every open file we
	// snapshotted still has the same version — if any changed during
	// validation, the results are stale and must be discarded.
	server.mu.Lock()
	stale := false
	for snapURI, snap := range openDocs {
		if doc, ok := server.documents[snapURI]; ok && doc.Version != snap.Version {
			stale = true
			break
		}
	}
	if stale {
		server.mu.Unlock()
		return
	}
	server.moduleCache = module
	server.moduleScopeCache = inferCtx.Scope
	server.fileScopeCache = c.FileScopes
	for snapURI, snap := range openDocs {
		server.validatedVersion[snapURI] = snap.Version
	}
	server.mu.Unlock()
	server.validated.Broadcast()

	// Pre-index errors by SourceID so we can look them up per-file in O(1)
	// instead of scanning all errors for each file.
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	diagsBySourceID := make(map[int][]protocol.Diagnostic)
	for _, err := range parseErrors {
		diagsBySourceID[err.Span.SourceID] = append(diagsBySourceID[err.Span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(err.Span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message,
		})
	}
	for _, err := range typeErrors {
		span := err.Span()
		diagsBySourceID[span.SourceID] = append(diagsBySourceID[span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message(),
		})
	}

	// Publish diagnostics for all files in the module.
	// Use each file's own document version (not the triggering file's version)
	// so that sibling files get correct Version fields.
	server.mu.RLock()
	for _, file := range module.Files {
		fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, file.Path)))
		fileDiags := diagsBySourceID[file.SourceID]

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
	server.mu.RUnlock()
}

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string, version protocol.Integer) {
	// Route module files to module-level validation.
	if server.isModuleFile(uri) {
		server.validateModule(lspContext, uri, version)
		return
	}

	fmt.Fprintf(os.Stderr, "validate (script)")

	// Check staleness before doing expensive work.
	server.mu.RLock()
	currentDoc := server.documents[uri]
	server.mu.RUnlock()
	if currentDoc.Version != version {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, &ast.Source{
		Path:     uri,
		Contents: contents,
	})
	script, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Scope:      checker.Prelude(c),
		IsAsync:    false,
		IsPatMatch: false,
	}
	scriptScope, typeErrors := c.InferScript(inferCtx, script)

	// Check again after expensive work in case a new version arrived.
	server.mu.Lock()
	currentDoc = server.documents[uri]
	if currentDoc.Version != version {
		server.mu.Unlock()
		return
	}
	server.astCache[uri] = script
	server.scopeCache[uri] = scriptScope
	server.validatedVersion[uri] = version
	server.mu.Unlock()
	server.validated.Broadcast()

	diagnotics := []protocol.Diagnostic{}
	for _, err := range parseErrors {
		severity := protocol.DiagnosticSeverityError
		source := "escalier"
		diagnotics = append(diagnotics, protocol.Diagnostic{
			Range:              spanToRange(err.Span),
			Severity:           &severity,
			Code:               nil,
			CodeDescription:    nil,
			Source:             &source,
			Message:            err.Message,
			Tags:               nil,
			RelatedInformation: nil,
			Data:               nil,
		})
	}

	for _, err := range typeErrors {
		severity := protocol.DiagnosticSeverityError
		source := "escalier"
		span := err.Span()
		diagnotics = append(diagnotics, protocol.Diagnostic{
			Range:              spanToRange(span),
			Severity:           &severity,
			Code:               &protocol.IntegerOrString{Value: "ERR_CODE"},
			CodeDescription:    nil,
			Source:             &source,
			Message:            err.Message(),
			Tags:               nil,
			RelatedInformation: nil,
			Data:               nil,
		})
	}

	diagVersion := protocol.UInteger(version)
	go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnotics,
		Version:     &diagVersion,
	})
}
