package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func spanToRange(span ast.Span) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{
			Line:      protocol.UInteger(span.Start.Line),
			Character: protocol.UInteger(span.Start.Column),
		},
		End: protocol.Position{
			Line:      protocol.UInteger(span.End.Line),
			Character: protocol.UInteger(span.End.Column),
		},
	}
}

func (*Server) textDocumentDeclaration(context *glsp.Context, params *protocol.DeclarationParams) (any, error) {
	fmt.Fprintf(os.Stderr, "textDocumentDeclaration - uri = %s\n", params.TextDocument.URI)
	err := fmt.Errorf("textDocument/declaration not implemented yet")
	return nil, err
}

func (s *Server) textDocumentDefinition(context *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	loc := ast.Location{
		Line:   int(params.Position.Line),
		Column: int(params.Position.Character),
	}
	script := s.astCache[params.TextDocument.URI]
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
		loc := protocol.Location{
			URI:   params.TextDocument.URI,
			Range: spanToRange(node.Source.Span()),
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

func (server *Server) textDocumentHover(context *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	fmt.Fprintf(os.Stderr, "textDocumentHover - uri = %s\n", params.TextDocument.URI)
	fmt.Fprintf(os.Stderr, "textDocumentHover - position = line:%d, column:%d\n", params.Position.Line, params.Position.Character)

	value := fmt.Sprintf(
		"textDocumentHover - position = line:%d, column:%d\n",
		params.Position.Line,
		params.Position.Character,
	)

	script := server.astCache[params.TextDocument.URI]
	if script != nil {
		loc := ast.Location{
			Line:   int(params.Position.Line),
			Column: int(params.Position.Character),
		}
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

func (server *Server) validate(lspContext *glsp.Context, uri protocol.DocumentUri, contents string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, parser.Source{
		Path:     uri,
		Contents: contents,
	})
	script, parseErrors := p.ParseScript()
	server.astCache[uri] = script

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Filename:   uri,
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	_, typeErrors := c.InferScript(inferCtx, script)

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

	go lspContext.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnotics,
		Version:     nil,
	})
}
