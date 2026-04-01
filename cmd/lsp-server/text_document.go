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
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
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
	co := s.checkOutput
	if co != nil {
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

	rootPath := uriToPath(s.rootURI)

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
		// Resolve the declaration's file URI from the span's SourceID.
		defURI := params.TextDocument.URI
		if co != nil && co.Module != nil {
			if srcPath := co.Module.GetSourcePath(span.SourceID); srcPath != "" {
				defURI = protocol.DocumentUri(pathToURI(filepath.Join(rootPath, srcPath)))
			}
		}
		loc := protocol.Location{
			URI:   defURI,
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
	if params.TextDocument.LanguageID == "escalier" {
		s.packageGen++
		if s.isModuleFile(params.TextDocument.URI) {
			s.libGen++
		}
	}
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
	if doc.LanguageID == "escalier" {
		s.packageGen++
		if s.isModuleFile(params.TextDocument.URI) {
			s.libGen++
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
	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sourceIDForURI: filepath.Rel(%s, %s): %s\n", rootPath, filePath, err)
		return stableSourceID(filePath)
	}
	return stableSourceID(filepath.ToSlash(rel))
}

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string, version protocol.Integer) {
	// Check staleness before doing expensive work.
	server.mu.RLock()
	currentDoc := server.documents[uri]
	isBinFile := !server.isModuleFile(uri)
	canIncrCheck := isBinFile && server.checkOutput != nil && server.libGen == server.libValidatedGen
	var cachedLibNS *type_system.Namespace
	if canIncrCheck && server.checkOutput != nil && server.checkOutput.ModuleScope != nil {
		cachedLibNS = server.checkOutput.ModuleScope.Namespace
	}
	snapshotPackageGen := server.packageGen
	snapshotLibGen := server.libGen
	server.mu.RUnlock()
	if currentDoc.Version != version {
		server.validated.Broadcast()
		return
	}

	rootPath := uriToPath(server.rootURI)

	if canIncrCheck {
		// Fast path: only a bin/ file changed and lib/ is unchanged.
		// Re-check just this one script using the cached lib namespace.
		server.validateBinScript(lspContext, uri, contents, version, rootPath, cachedLibNS, snapshotPackageGen, snapshotLibGen)
		return
	}

	// Slow path: full package check.
	server.validateFull(lspContext, uri, contents, version, rootPath, snapshotPackageGen, snapshotLibGen)
}

// validateBinScript re-checks a single bin/ script using a cached lib namespace,
// avoiding re-parsing and re-checking all lib/ files.
func (server *Server) validateBinScript(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	contents string,
	version protocol.Integer,
	rootPath string,
	libNS *type_system.Namespace,
	snapshotPackageGen int64,
	snapshotLibGen int64,
) {
	triggerSourceID := server.sourceIDForURI(uri)
	rel, err := filepath.Rel(rootPath, uriToPath(string(uri)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "validateBinScript: filepath.Rel: %s\n", err)
		server.validated.Broadcast()
		return
	}

	src := &ast.Source{
		ID:       triggerSourceID,
		Path:     filepath.ToSlash(rel),
		Contents: contents,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := compiler.CheckBinScript(ctx, libNS, src)

	// Verify staleness and update caches.
	server.mu.Lock()
	currentDoc := server.documents[uri]
	if currentDoc.Version != version || server.packageGen != snapshotPackageGen || server.libGen != snapshotLibGen {
		server.mu.Unlock()
		server.validated.Broadcast()
		return
	}
	co := server.checkOutput
	// Update only this script's entries in the existing checkOutput.
	co.Scripts[triggerSourceID] = result.Script
	co.ScriptScopes[triggerSourceID] = result.Scope
	// Rebuild errors: keep non-script errors, replace this script's errors.
	co.ParseErrors = filterOutSourceID(co.ParseErrors, triggerSourceID)
	co.ParseErrors = append(co.ParseErrors, result.ParseErrors...)
	co.TypeErrors = filterOutTypeErrors(co.TypeErrors, triggerSourceID)
	co.TypeErrors = append(co.TypeErrors, result.TypeErrors...)
	server.packageValidatedGen = server.packageGen
	server.validatedVersion[uri] = version
	server.mu.Unlock()
	server.validated.Broadcast()

	// Publish diagnostics for just this file.
	server.publishDiagnosticsForScript(lspContext, uri, triggerSourceID, result.ParseErrors, result.TypeErrors)
}

// validateFull performs a full package check (lib/ + bin/).
func (server *Server) validateFull(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	contents string,
	version protocol.Integer,
	rootPath string,
	snapshotPackageGen int64,
	snapshotLibGen int64,
) {
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
	currentDoc := server.documents[uri]
	if currentDoc.Version != version || server.packageGen != snapshotPackageGen || server.libGen != snapshotLibGen {
		server.mu.Unlock()
		server.validated.Broadcast()
		return
	}
	server.checkOutput = &output
	server.packageValidatedGen = server.packageGen
	server.libValidatedGen = server.libGen
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
			if ver, ok := server.validatedVersion[fileURI]; ok {
				v := protocol.UInteger(ver)
				params.Version = &v
			}
			go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
		}
	}
	// Publish diagnostics for bin/ files.
	sourcePathByID := make(map[int]string, len(sources))
	for _, src := range sources {
		sourcePathByID[src.ID] = src.Path
	}
	for srcID := range output.Scripts {
		path, ok := sourcePathByID[srcID]
		if !ok {
			continue
		}
		fileURI := protocol.DocumentUri(pathToURI(filepath.Join(rootPath, path)))
		fileDiags := emptyIfNil(diagsBySourceID[srcID])
		params := &protocol.PublishDiagnosticsParams{
			URI:         fileURI,
			Diagnostics: fileDiags,
		}
		if ver, ok := server.validatedVersion[fileURI]; ok {
			v := protocol.UInteger(ver)
			params.Version = &v
		}
		go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
	}
	server.mu.RUnlock()
}

// publishDiagnosticsForScript publishes diagnostics for a single bin/ script.
func (server *Server) publishDiagnosticsForScript(
	lspContext *glsp.Context,
	uri protocol.DocumentUri,
	sourceID int,
	parseErrors []*parser.Error,
	typeErrors []checker.Error,
) {
	if lspContext.Notify == nil {
		return
	}
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	var diags []protocol.Diagnostic
	for _, err := range parseErrors {
		if err.Span.SourceID == sourceID {
			diags = append(diags, protocol.Diagnostic{
				Range:    spanToRange(err.Span),
				Severity: &severity,
				Source:   &source,
				Message:  err.Message,
			})
		}
	}
	for _, err := range typeErrors {
		span := err.Span()
		if span.SourceID == sourceID {
			diags = append(diags, protocol.Diagnostic{
				Range:    spanToRange(span),
				Severity: &severity,
				Source:   &source,
				Message:  err.Message(),
			})
		}
	}

	server.mu.RLock()
	diagVersion := protocol.UInteger(server.documents[uri].Version)
	server.mu.RUnlock()

	go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: emptyIfNil(diags),
		Version:     &diagVersion,
	})
}

// filterOutSourceID removes parse errors belonging to the given sourceID.
func filterOutSourceID(errs []*parser.Error, sourceID int) []*parser.Error {
	result := make([]*parser.Error, 0, len(errs))
	for _, e := range errs {
		if e.Span.SourceID != sourceID {
			result = append(result, e)
		}
	}
	return result
}

// filterOutTypeErrors removes type errors belonging to the given sourceID.
func filterOutTypeErrors(errs []checker.Error, sourceID int) []checker.Error {
	result := make([]checker.Error, 0, len(errs))
	for _, e := range errs {
		if e.Span().SourceID != sourceID {
			result = append(result, e)
		}
	}
	return result
}

// emptyIfNil returns an empty slice if the input is nil, ensuring JSON
// serialization produces [] instead of null.
func emptyIfNil(diags []protocol.Diagnostic) []protocol.Diagnostic {
	if diags == nil {
		return []protocol.Diagnostic{}
	}
	return diags
}
