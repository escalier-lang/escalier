package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
		for _, _change := range params.ContentChanges {
			change := _change.(protocol.TextDocumentContentChangeEventWhole)
			s.validate(context, params.TextDocument.URI, change.Text, params.TextDocument.Version)
		}
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
		return nil, err
	}
	return files, nil
}

// getSourceIDForURI finds the SourceID for a given URI in the cached module.
func (s *Server) getSourceIDForURI(uri protocol.DocumentUri) (int, bool) {
	if s.moduleCache == nil {
		return 0, false
	}
	rel := s.relPath(uri)
	for _, file := range s.moduleCache.Files {
		if file.Path == rel {
			return file.SourceID, true
		}
	}
	return 0, false
}

func (server *Server) validateModule(lspContext *glsp.Context, uri protocol.DocumentUri, version protocol.Integer) {
	rootPath := uriToPath(server.rootURI)

	// Find all .esc files in lib/
	libFiles, err := server.findLibFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "validateModule: error finding lib files: %s\n", err)
		return
	}

	// Build sources: use in-memory content for open files, read from disk for others.
	var sources []*ast.Source
	server.mu.RLock()
	for i, absPath := range libFiles {
		rel, _ := filepath.Rel(rootPath, absPath)
		fileURI := protocol.DocumentUri("file://" + absPath)
		var contents string
		if doc, ok := server.documents[fileURI]; ok {
			contents = doc.Text
		} else {
			data, err := os.ReadFile(absPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "validateModule: error reading %s: %s\n", absPath, err)
				continue
			}
			contents = string(data)
		}
		sources = append(sources, &ast.Source{
			ID:       i,
			Path:     rel,
			Contents: contents,
		})
	}
	server.mu.RUnlock()

	if len(sources) == 0 {
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

	// Store module cache and file scopes.
	server.mu.Lock()
	currentDoc := server.documents[uri]
	if currentDoc.Version != version {
		server.mu.Unlock()
		return
	}
	server.moduleCache = module
	server.moduleScopeCache = inferCtx.Scope
	server.fileScopeCache = c.FileScopes
	server.mu.Unlock()

	// Publish diagnostics for all open files in the module.
	for _, file := range module.Files {
		fileURI := protocol.DocumentUri("file://" + filepath.Join(rootPath, file.Path))

		var fileDiags []protocol.Diagnostic
		for _, err := range parseErrors {
			if err.Span.SourceID == file.SourceID {
				severity := protocol.DiagnosticSeverityError
				source := "escalier"
				fileDiags = append(fileDiags, protocol.Diagnostic{
					Range:    spanToRange(err.Span),
					Severity: &severity,
					Source:   &source,
					Message:  err.Message,
				})
			}
		}
		for _, err := range typeErrors {
			span := err.Span()
			if span.SourceID == file.SourceID {
				severity := protocol.DiagnosticSeverityError
				source := "escalier"
				fileDiags = append(fileDiags, protocol.Diagnostic{
					Range:    spanToRange(span),
					Severity: &severity,
					Code:     &protocol.IntegerOrString{Value: "ERR_CODE"},
					Source:   &source,
					Message:  err.Message(),
				})
			}
		}

		diagVersion := protocol.UInteger(version)
		go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         fileURI,
			Diagnostics: fileDiags,
			Version:     &diagVersion,
		})
	}
}

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string, version protocol.Integer) {
	// Route module files to module-level validation.
	if server.isModuleFile(uri) {
		server.validateModule(lspContext, uri, version)
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

	// INVARIANT: validate() must never be called while holding mu.
	// Check that the document hasn't been updated since we started.
	server.mu.Lock()
	currentDoc := server.documents[uri]
	if currentDoc.Version != version {
		server.mu.Unlock()
		return
	}
	server.astCache[uri] = script
	server.scopeCache[uri] = scriptScope
	server.mu.Unlock()

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
