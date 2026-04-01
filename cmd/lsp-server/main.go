package main

import (
	"fmt"
	"net/url"
	"os"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	glsp_server "github.com/tliron/glsp/server"

	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/compiler"
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

	// Unified package check output — contains ASTs, scopes, and errors
	// for all lib/ and bin/ files. Updated by validate().
	checkOutput *compiler.CheckOutput

	// Tracks the last validated document version per URI so the completion
	// handler can detect when the cache is stale.
	validatedVersion map[protocol.DocumentUri]protocol.Integer

	// packageGen is incremented whenever any .esc file changes.
	// packageValidatedGen records the packageGen at which checkOutput was built.
	// The completion handler uses these to detect when the cache is stale
	// due to changes in sibling files.
	packageGen          int64
	packageValidatedGen int64

	// libGen is incremented only when a lib/ file changes. Used to decide
	// whether the cached lib output can be reused for bin/-only changes.
	libGen          int64
	libValidatedGen int64

	// Cached absolute paths to .esc files under lib/ and bin/, refreshed at
	// startup and on workspace file create/rename/delete notifications.
	libFilesCache map[string]struct{}
	binFilesCache map[string]struct{}

	// Cached prelude/global scope and its completion items.
	// Computed lazily on first completion request; never changes after that.
	preludeScope       *checker.Scope
	preludeCompletions []protocol.CompletionItem

	mu sync.RWMutex
	// validated is broadcast after validate() updates the checkOutput.
	// The completion handler waits on this when the cached version is
	// behind the document version.
	validated *sync.Cond
	rootURI   string // workspace root URI (from InitializeParams)
}

func NewServer() *Server {
	// nolint: exhaustruct
	s := Server{
		documents:        map[protocol.DocumentUri]protocol.TextDocumentItem{},
		validatedVersion: map[protocol.DocumentUri]protocol.Integer{},
		libFilesCache:    map[string]struct{}{},
		binFilesCache:    map[string]struct{}{},
	}
	s.validated = sync.NewCond(s.mu.RLocker())
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
		TextDocumentCompletion:     s.textDocumentCompletion,
		CompletionItemResolve:      s.completionItemResolve,
		TextDocumentCodeAction:     s.textDocumentCodeAction,

		// Workspace
		WorkspaceExecuteCommand: s.workspaceExecuteCommand,
		WorkspaceDidCreateFiles: s.workspaceDidCreateFiles,
		WorkspaceDidRenameFiles: s.workspaceDidRenameFiles,
		WorkspaceDidDeleteFiles: s.workspaceDidDeleteFiles,
	}

	return &s
}

func (s *Server) Handle(context *glsp.Context) (r any, validMethod bool, validParams bool, err error) {
	return s.handler.Handle(context)
}

// uriToPath converts a file:// URI to a filesystem path, decoding any
// percent-encoded characters (e.g. %20 for spaces).
func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	path, err := url.PathUnescape(u.Path)
	if err != nil {
		return u.Path
	}
	return path
}

// pathToURI converts a filesystem path to a file:// URI, properly encoding
// any characters that are not valid in a URI path (e.g. spaces as %20).
func pathToURI(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	return u.String()
}

func (s *Server) initialize(context *glsp.Context, params *protocol.InitializeParams) (any, error) {
	// TODO: store the client capabilities so that we can use them to customize
	// repsonses.
	// x := params.Capabilities.TextDocument.CodeAction.IsPreferredSupport

	if params.RootURI != nil {
		s.rootURI = string(*params.RootURI)
	}

	capabilities := s.handler.CreateServerCapabilities()
	capabilities.TextDocumentSync = protocol.TextDocumentSyncKindFull
	resolveProvider := true
	capabilities.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{"."},
		ResolveProvider:   &resolveProvider,
	}
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
	escFileFilters := []protocol.FileOperationFilter{
		{Pattern: protocol.FileOperationPattern{Glob: "lib/*.esc"}},
		{Pattern: protocol.FileOperationPattern{Glob: "lib/**/*.esc"}},
		{Pattern: protocol.FileOperationPattern{Glob: "bin/*.esc"}},
		{Pattern: protocol.FileOperationPattern{Glob: "bin/**/*.esc"}},
	}
	capabilities.Workspace = &protocol.ServerCapabilitiesWorkspace{
		FileOperations: &protocol.ServerCapabilitiesWorkspaceFileOperations{
			DidCreate: &protocol.FileOperationRegistrationOptions{Filters: escFileFilters},
			DidRename: &protocol.FileOperationRegistrationOptions{Filters: escFileFilters},
			DidDelete: &protocol.FileOperationRegistrationOptions{Filters: escFileFilters},
		},
	}

	if err := s.refreshLibFilesCache(); err != nil {
		fmt.Fprintf(os.Stderr, "initialize: failed to cache lib files: %s\n", err)
	}
	if err := s.refreshBinFilesCache(); err != nil {
		fmt.Fprintf(os.Stderr, "initialize: failed to cache bin files: %s\n", err)
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
