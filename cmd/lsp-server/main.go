package main

import (
	"fmt"
	"os"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glsp_server "github.com/tliron/glsp/server"

	"github.com/escalier-lang/escalier/internal/ast"
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
	astCache  map[protocol.DocumentUri]*ast.Script
}

func NewServer() *Server {
	// nolint: exhaustruct
	s := Server{
		documents: map[protocol.DocumentUri]protocol.TextDocumentItem{},
		astCache:  map[protocol.DocumentUri]*ast.Script{},
	}
	// nolint: exhaustruct
	s.handler = protocol.Handler{
		Initialize:  s.initialize,
		Initialized: s.initialized,
		Shutdown:    s.shutdown,
		SetTrace:    s.setTrace,

		// TextDocument
		TextDocumentDeclaration:    s.textDocumentDeclaration,
		TextDocumentDefinition:     s.textDocumentDefinition,
		TextDocumentTypeDefinition: s.textDocumentTypeDefinition,
		TextDocumentDidOpen:        s.textDocumentDidOpen,
		TextDocumentDidChange:      s.textDocumentDidChange,
		TextDocumentHover:          s.textDocumentHover,
		TextDocumentCodeAction:     s.textDocumentCodeAction,

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
