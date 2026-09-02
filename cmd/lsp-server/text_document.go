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
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// spanToRange converts a span into the range an LSP client expects. A span
// holds byte offsets, so lineMap must be a map over the file the span indexes
// into. LSP numbers lines and characters from 0 and counts a character as a
// UTF-16 code unit, where Escalier numbers lines from 1.
func spanToRange(lineMap *ast.LineMap, span ast.Span) protocol.Range {
	startLine, startColumn := lineMap.Position(span.Start.Offset, ast.UTF16Columns)
	endLine, endColumn := lineMap.Position(span.End.Offset, ast.UTF16Columns)
	return protocol.Range{
		Start: protocol.Position{
			Line:      protocol.UInteger(startLine - 1),
			Character: protocol.UInteger(startColumn - 1),
		},
		End: protocol.Position{
			Line:      protocol.UInteger(endLine - 1),
			Character: protocol.UInteger(endColumn - 1),
		},
	}
}

// posToLoc converts a position an LSP client sent into a byte offset in the
// file it refers to, which is the file lineMap covers.
func posToLoc(lineMap *ast.LineMap, pos protocol.Position) ast.Location {
	// LSP numbers from 0 and Escalier from 1.
	line := int(pos.Line) + 1
	column := int(pos.Character) + 1
	return ast.Location{Offset: lineMap.Offset(line, column, ast.UTF16Columns)}
}

// lineMapForURI returns a map over the text of a document. An open document's
// text changes with every edit, so its map is built per request rather than
// kept. A request can also name a file the editor has not opened, and the text
// the last check parsed stands in for it.
func (s *Server) lineMapForURI(uri protocol.DocumentUri) *ast.LineMap {
	s.mu.RLock()
	doc, opened := s.documents[uri]
	s.mu.RUnlock()
	if opened {
		return ast.NewLineMap(doc.Text)
	}
	return s.checkedLineMap(s.sourceIDForURI(uri))
}

// checkedLineMap returns a map over the file a SourceID names in the last
// check's output. The lookup runs under the read lock, because validateBinScript
// replaces an entry in that map while requests are being served. Building the
// map happens after the lock is released, which is safe because Source.LineMap
// builds it once however many goroutines ask at the same time.
func (s *Server) checkedLineMap(sourceID int) *ast.LineMap {
	s.mu.RLock()
	src := checkedSource(s.checkOutput, sourceID)
	s.mu.RUnlock()
	return lineMapFor(src)
}

// checkedSource returns the file a SourceID names in a check output, or nil
// when the output holds none for that id.
func checkedSource(co *compiler.CheckOutput, sourceID int) *ast.Source {
	if co == nil {
		return nil
	}
	return co.SourceByID(sourceID)
}

// lineMapFor returns a map over a source, or over the empty string when the
// source is unknown.
func lineMapFor(src *ast.Source) *ast.LineMap {
	if src == nil {
		return ast.NewLineMap("")
	}
	return src.LineMap()
}

func (*Server) textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	loc := posToLoc(s.lineMapForURI(params.TextDocument.URI), params.Position)
	sourceID := s.sourceIDForURI(params.TextDocument.URI)
	s.mu.RLock()
	var node ast.Node
	co := s.checkOutput
	if co != nil {
		node = findNodeAtLocation(co, sourceID, loc)
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
		declURI := params.TextDocument.URI
		if co != nil && co.Module != nil {
			if srcPath := co.Module.GetSourcePath(span.SourceID); srcPath != "" {
				declURI = protocol.DocumentUri(pathToURI(filepath.Join(rootPath, srcPath)))
			}
		}
		loc := protocol.Location{
			URI:   declURI,
			Range: spanToRange(s.checkedLineMap(span.SourceID), span),
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

	loc := posToLoc(server.lineMapForURI(params.TextDocument.URI), params.Position)
	// The client's position is already a line and column, so report it directly
	// rather than converting the offset back. It is 0-based, where a person
	// counts from 1.
	value := fmt.Sprintf(
		"textDocumentHover - loc = line:%d, column:%d\n",
		params.Position.Line+1,
		params.Position.Character+1,
	)

	sourceID := server.sourceIDForURI(params.TextDocument.URI)
	server.mu.RLock()
	var hoverNode ast.Node
	if co := server.checkOutput; co != nil {
		hoverNode = findNodeAtLocation(co, sourceID, loc)
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

	s.mu.Lock()
	s.libFilesCache = set.FromSlice(files)
	s.mu.Unlock()

	return nil
}

// cachedLibFilesSnapshot returns a stable snapshot of cached lib file paths.
func (s *Server) cachedLibFilesSnapshot() []string {
	s.mu.RLock()
	files := s.libFilesCache.ToSlice()
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
	// We can incrementally re-check just this one bin/ script (instead of
	// doing a full package check) when all three conditions hold:
	//   1. The file is a bin/ script (lib/ changes always need a full check).
	//   2. A prior full check has run (checkOutput != nil), so we have a
	//      cached lib namespace to type-check the script against.
	//   3. No lib/ files have changed since that full check
	//      (libGen == libValidatedGen), so the cached namespace is still valid.
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
	// The new AST's spans index the text just parsed, so the source has to move
	// with them. Keeping the one the last full check stored would convert the
	// new offsets against stale text.
	if co.Sources == nil {
		co.Sources = map[int]*ast.Source{}
	}
	co.Sources[triggerSourceID] = src
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
	server.publishDiagnosticsForScript(lspContext, uri, version, triggerSourceID, src.LineMap(), result.ParseErrors, result.TypeErrors)
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
	// Also update validatedVersion for all other open Escalier documents.
	// This iterates server.documents rather than sources because all open
	// lib/ and bin/ .esc files are included in sources (collectSources uses
	// the editor buffer for open files). If a non-package file somehow gets
	// stamped, the only effect is that isCacheStale returns false for it,
	// which is harmless — there is nothing to wait for since it won't be
	// re-validated through the full-package path.
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
			Range:    spanToRange(lineMapFor(checkedSource(&output, err.Span.SourceID)), err.Span),
			Severity: &severity,
			Source:   &source,
			Message:  err.Message,
		})
	}
	for _, err := range output.TypeErrors {
		span := err.Span()
		diagsBySourceID[span.SourceID] = append(diagsBySourceID[span.SourceID], protocol.Diagnostic{
			Range:    spanToRange(lineMapFor(checkedSource(&output, span.SourceID)), span),
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
	version protocol.Integer,
	sourceID int,
	lineMap *ast.LineMap,
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
				Range:    spanToRange(lineMap, err.Span),
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
				Range:    spanToRange(lineMap, span),
				Severity: &severity,
				Source:   &source,
				Message:  err.Message(),
			})
		}
	}

	diagVersion := protocol.UInteger(version)

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
