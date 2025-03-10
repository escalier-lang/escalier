package main

import (
	"fmt"
	"os"

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
		Initialize:            initialize,
		Initialized:           initialized,
		Shutdown:              shutdown,
		SetTrace:              setTrace,
		TextDocumentDidChange: textDocumentDidChange,
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

func textDocumentDidChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	for _, _change := range params.ContentChanges {
		change := _change.(protocol.TextDocumentContentChangeEventWhole)
		fmt.Fprintf(os.Stderr, "file changed: %s\n", params.TextDocument.URI)
		fmt.Fprintf(os.Stderr, "change.Text: %s\n", change.Text)

		p := parser.NewParser(parser.Source{
			Contents: change.Text,
		})
		p.ParseModule()

		diagnotics := []protocol.Diagnostic{}
		documentUri := params.TextDocument.URI
		for _, err := range p.Errors {
			fmt.Fprintf(os.Stderr, "error: %#v\n", err)
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

		go context.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
			URI:         documentUri,
			Diagnostics: diagnotics,
			Version:     nil,
		})
	}

	return nil
}
