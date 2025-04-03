package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	"github.com/escalier-lang/escalier/internal/parser"
)

const lsName = "escalier"

var (
	version string = "0.0.1"
	handler protocol.Handler
)

func main() {
	fmt.Fprintf(os.Stderr, "Hello, from lsp-server\n")

	//nolint:exhaustruct
	handler = protocol.Handler{
		Initialize:              initialize,
		Initialized:             initialized,
		Shutdown:                shutdown,
		SetTrace:                setTrace,
		TextDocumentDidOpen:     textDocumentDidOpen,
		TextDocumentDidChange:   textDocumentDidChange,
		TextDocumentDeclaration: textDocumentDeclaration,
		TextDocumentDefinition:  textDocumentDefinition,
	}

	server := server.NewServer(&handler, lsName, false)

	err := server.RunStdio()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func initialize(context *glsp.Context, params *protocol.InitializeParams) (any, error) {
	capabilities := handler.CreateServerCapabilities()
	capabilities.TextDocumentSync = protocol.TextDocumentSyncKindFull
	capabilities.DeclarationProvider = true
	capabilities.DefinitionProvider = true

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    lsName,
			Version: &version,
		},
	}, nil
}

func initialized(context *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

func shutdown(context *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func setTrace(context *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, parser.Source{
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

func textDocumentDidOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	validate(context, params.TextDocument.URI, params.TextDocument.Text)
	return nil
}

func textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	for _, _change := range params.ContentChanges {
		change := _change.(protocol.TextDocumentContentChangeEventWhole)
		validate(context, params.TextDocument.URI, change.Text)
	}
	return nil
}

func textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDefinition - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/definition not implemented yet")
	return nil, err
}
