package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glsp_server "github.com/tliron/glsp/server"

	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/parser"
)

const lsName = "escalier"

var (
	version string = "0.0.1"
	// handler protocol.Handler
)

func main() {
	fmt.Fprintf(os.Stderr, "Hello, from lsp-server\n")

	server := glsp_server.NewServer(NewServer(), lsName, false)

	err := server.RunStdio()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

type Server struct {
	handler   protocol.Handler
	documents map[protocol.DocumentUri]protocol.TextDocumentItem
}

func NewServer() *Server {
	// nolint: exhaustruct
	s := Server{
		documents: map[protocol.DocumentUri]protocol.TextDocumentItem{},
	}
	// nolint: exhaustruct
	s.handler = protocol.Handler{
		Initialize:  s.initialize,
		Initialized: s.initialized,
		Shutdown:    s.shutdown,
		SetTrace:    s.setTrace,

		// TextDocument
		TextDocumentDidOpen:     s.textDocumentDidOpen,
		TextDocumentDidChange:   s.textDocumentDidChange,
		TextDocumentDeclaration: s.textDocumentDeclaration,
		TextDocumentDefinition:  s.textDocumentDefinition,
		TextDocumentCodeAction:  s.textDocumentCodeAction,

		// Workspace
		WorkspaceExecuteCommand: s.workspaceExecuteCommand,
	}

	return &s
}

func (s *Server) Handle(context *glsp.Context) (r any, validMethod bool, validParams bool, err error) {
	return s.handler.Handle(context)
}

func (s *Server) initialize(context *glsp.Context, params *protocol.InitializeParams) (any, error) {
	// TODO: store the client capabilities so that we can use them to customize
	// repsonses.
	// x := params.Capabilities.TextDocument.CodeAction.IsPreferredSupport

	capabilities := s.handler.CreateServerCapabilities()
	capabilities.TextDocumentSync = protocol.TextDocumentSyncKindFull
	capabilities.DeclarationProvider = true
	capabilities.DefinitionProvider = true
	capabilities.CodeActionProvider = protocol.CodeActionOptions{
		WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
			WorkDoneProgress: nil,
		},
		CodeActionKinds: []protocol.CodeActionKind{
			"compile",
		},
		ResolveProvider: nil,
	}
	capabilities.ExecuteCommandProvider = &protocol.ExecuteCommandOptions{
		WorkDoneProgressOptions: protocol.WorkDoneProgressOptions{
			WorkDoneProgress: nil,
		},
		Commands: []string{
			"compile",
		},
	}

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    lsName,
			Version: &version,
		},
	}, nil
}

func (*Server) initialized(context *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func (*Server) shutdown(context *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (*Server) setTrace(context *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func (*Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, parser.Source{
		Path:     uri,
		Contents: contents,
	})
	p.ParseModule()

	diagnotics := []protocol.Diagnostic{}
	for _, err := range p.Errors {
		severity := protocol.DiagnosticSeverityError
		source := "escalier"
		diagnotics = append(diagnotics, protocol.Diagnostic{
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      protocol.UInteger(err.Span.Start.Line - 1),
					Character: protocol.UInteger(err.Span.Start.Column - 1),
				},
				End: protocol.Position{
					Line:      protocol.UInteger(err.Span.End.Line - 1),
					Character: protocol.UInteger(err.Span.End.Column - 1),
				},
			},
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

	go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnotics,
		Version:     nil,
	})
}

func (s *Server) textDocumentDidOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.documents[params.TextDocument.URI] = params.TextDocument
	if params.TextDocument.LanguageID == "escalier" {
		s.validate(context, params.TextDocument.URI, params.TextDocument.Text)
	}
	return nil
}

func (s *Server) textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	doc := s.documents[params.TextDocument.URI]

	for _, change := range params.ContentChanges {
		switch change := change.(type) {
		case protocol.TextDocumentContentChangeEvent:
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
		for _, _change := range params.ContentChanges {
			change := _change.(protocol.TextDocumentContentChangeEventWhole)
			s.validate(context, params.TextDocument.URI, change.Text)
		}
	}
	return nil
}

func (*Server) textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func (*Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDefinition - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/definition not implemented yet")
	return nil, err
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
		return nil, fmt.Errorf("document not found: %s", uri)
	}

	if doc.LanguageID != "escalier" {
		return nil, fmt.Errorf("unsupported language: %s", doc.LanguageID)
	}

	source := parser.Source{
		Path:     uri,
		Contents: doc.Text,
	}

	output := compiler.Compile(source)

	if len(output.Errors) > 0 {
		errorsJSON, err := json.Marshal(output.Errors)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal compilation errors: %v", err)
		}
		return nil, errors.New(string(errorsJSON))
	}

	// TODO: include errors in the response
	response := protocol.TextDocumentItem{
		URI:        strings.TrimSuffix(uri, ".esc") + ".js",
		LanguageID: "javascript",
		Version:    0,
		Text:       output.JS,
	}

	return response, nil
}
