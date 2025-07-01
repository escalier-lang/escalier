package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

	source := &ast.Source{
		Path:     uri,
		Contents: doc.Text,
	}

	output := compiler.Compile(source)

	if len(output.ParseErrors) > 0 {
		errorsJSON, err := json.Marshal(output.ParseErrors)
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
