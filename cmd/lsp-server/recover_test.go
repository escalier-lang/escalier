package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// recordedNotifications returns a context whose notifications are sent to the returned
// channels: published diagnostics to the first and `window/logMessage` messages to the
// second.
func recordedNotifications() (*glsp.Context, <-chan *protocol.PublishDiagnosticsParams, <-chan string) {
	published := make(chan *protocol.PublishDiagnosticsParams, 64)
	logged := make(chan string, 64)
	ctx := &glsp.Context{
		Notify: func(method string, params any) {
			switch method {
			case protocol.ServerTextDocumentPublishDiagnostics:
				published <- params.(*protocol.PublishDiagnosticsParams)
			case protocol.ServerWindowLogMessage:
				logged <- params.(*protocol.LogMessageParams).Message
			}
		},
	}
	return ctx, published, logged
}

// awaitLogged returns the next message sent through `window/logMessage`.
func awaitLogged(t *testing.T, logged <-chan string) string {
	t.Helper()
	select {
	case msg := <-logged:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("no message logged")
		return ""
	}
}

// requireLocksFree asserts that neither of the server's locks is held.
func requireLocksFree(t *testing.T, s *Server) {
	t.Helper()
	require.True(t, s.mu.TryLock(), "mu is still held")
	s.mu.Unlock()
	require.True(t, s.solverMu.TryLock(), "solverMu is still held")
	s.solverMu.Unlock()
}

// changeFile replaces the text of the open document uri with contents at version and
// checks it, as a didChange notification does but without starting a goroutine.
func changeFile(s *Server, ctx *glsp.Context, uri protocol.DocumentUri, version protocol.Integer, contents string) {
	s.mu.Lock()
	doc := s.documents[uri]
	doc.Version = version
	doc.Text = contents
	s.documents[uri] = doc
	s.packageGen++
	if s.isModuleFile(uri) {
		s.libGen++
	}
	s.mu.Unlock()
	s.validate(ctx, uri, contents, version)
}

func TestHandleAnswersAPanickingRequestWithAnError(t *testing.T) {
	tests := []struct {
		name   string
		method protocol.Method
		// panicIn replaces the handler for method with one that panics.
		panicIn func(s *Server)
		// params returns the request's params for the document uri.
		params func(uri protocol.DocumentUri) any
		// diagnosticAt is where the crash diagnostic goes, as "line:col-line:col".
		diagnosticAt string
	}{
		{
			name:   "Hover",
			method: protocol.MethodTextDocumentHover,
			panicIn: func(s *Server) {
				s.handler.TextDocumentHover = func(*glsp.Context, *protocol.HoverParams) (*protocol.Hover, error) {
					panic("boom")
				}
			},
			params: func(uri protocol.DocumentUri) any {
				return protocol.HoverParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{
						TextDocument: protocol.TextDocumentIdentifier{URI: uri},
						Position:     protocol.Position{Line: 0, Character: 4},
					},
				}
			},
			diagnosticAt: "0:4-0:4",
		},
		{
			// A code action carries a range rather than a position, and the crash is
			// reported at the range's start.
			name:   "CodeAction",
			method: protocol.MethodTextDocumentCodeAction,
			panicIn: func(s *Server) {
				s.handler.TextDocumentCodeAction = func(*glsp.Context, *protocol.CodeActionParams) (any, error) {
					panic("boom")
				}
			},
			params: func(uri protocol.DocumentUri) any {
				return protocol.CodeActionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri},
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 6},
						End:   protocol.Position{Line: 0, Character: 9},
					},
				}
			},
			diagnosticAt: "0:6-0:6",
		},
		{
			// `compile` names its document in its arguments and carries no position.
			name:   "Compile",
			method: protocol.MethodWorkspaceExecuteCommand,
			panicIn: func(s *Server) {
				s.handler.WorkspaceExecuteCommand = func(*glsp.Context, *protocol.ExecuteCommandParams) (any, error) {
					panic("boom")
				}
			},
			params: func(uri protocol.DocumentUri) any {
				return protocol.ExecuteCommandParams{Command: "compile", Arguments: []any{uri}}
			},
			diagnosticAt: "0:0-0:0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			src := `val x: number = "a"`
			s := integrationServer(t, map[string]string{"bin/main.esc": src})
			ctx, published, logged := recordedNotifications()
			uri := openFileIn(t, s, ctx, "bin/main.esc", src)
			require.Equal(t, []string{`0:16-0:19 cannot constrain "a" <: number`},
				awaitDiagnostics(t, published, uri, 1))

			// Handle refuses requests until the client has sent `initialize`.
			s.handler.SetInitialized(true)
			test.panicIn(s)
			params, err := json.Marshal(test.params(uri))
			require.NoError(t, err)
			reqCtx := &glsp.Context{Method: string(test.method), Params: params, Notify: ctx.Notify}
			result, validMethod, validParams, err := s.Handle(reqCtx)

			msg := "the Escalier language server crashed while handling " + string(test.method) + ": boom"
			require.Nil(t, result)
			require.True(t, validMethod)
			require.True(t, validParams)
			require.EqualError(t, err, msg)
			require.Equal(t, msg, awaitLogged(t, logged))
			// The crash is reported beside the error the check found.
			require.Equal(t, []string{
				`0:16-0:19 cannot constrain "a" <: number`,
				test.diagnosticAt + " " + msg + ". See the console for the stack trace.",
			}, awaitDiagnostics(t, published, uri, 1))
			requireLocksFree(t, s)
		})
	}
}

// TestHandleServesTheRequestAfterAPanic asserts that a request a handler panicked on
// leaves the server answering the next one.
func TestHandleServesTheRequestAfterAPanic(t *testing.T) {
	src := "val x: number = 1"
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	uri := openFile(t, s, "bin/main.esc", src)
	s.handler.SetInitialized(true)
	params, err := json.Marshal(protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 0, Character: 4},
		},
	})
	require.NoError(t, err)
	reqCtx := &glsp.Context{Method: string(protocol.MethodTextDocumentHover), Params: params}

	s.handler.TextDocumentHover = func(*glsp.Context, *protocol.HoverParams) (*protocol.Hover, error) {
		panic("boom")
	}
	_, _, _, err = s.Handle(reqCtx)
	require.EqualError(t, err, "the Escalier language server crashed while handling textDocument/hover: boom")

	s.handler.TextDocumentHover = s.textDocumentHover
	result, _, _, err := s.Handle(reqCtx)
	require.NoError(t, err)
	require.Equal(t, codeHover("x: number"), result)
}

func TestValidateRecoversFromAPanickingCheck(t *testing.T) {
	tests := []struct {
		name string
		// file is the file the test opens and then changes.
		file string
		// panicOnOpen is true when the check of the opened text panics. When false, the
		// opened text checks cleanly and the change at version 2 panics. A bin/ file
		// changed alone takes the re-check that holds solverMu around the checker.
		panicOnOpen bool
	}{
		{name: "FullCheck", file: "lib/index.esc", panicOnOpen: true},
		{name: "ScriptCheck", file: "bin/main.esc", panicOnOpen: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clean := "val x: number = 1"
			crashing := "// " + panicMarker + "\nval x: number = 1"
			opened := clean
			if test.panicOnOpen {
				opened = crashing
			}
			s := integrationServer(t, map[string]string{test.file: opened})
			s.testHooks = true
			ctx, published, logged := recordedNotifications()
			uri := openFileIn(t, s, ctx, test.file, opened)
			version := protocol.Integer(1)
			if !test.panicOnOpen {
				require.Empty(t, awaitDiagnostics(t, published, uri, 1))
				version = 2
				changeFile(s, ctx, uri, version, crashing)
			}

			path := filepath.Join(uriToPath(s.rootURI), test.file)
			msg := "the Escalier language server crashed while checking " + pathToURI(path) +
				": forced by " + panicMarker
			require.Equal(t, msg, awaitLogged(t, logged))
			require.Equal(t, []string{"0:0-0:0 " + msg + ". See the console for the stack trace."},
				awaitDiagnostics(t, published, uri, protocol.UInteger(version)))
			requireLocksFree(t, s)
			s.mu.RLock()
			require.True(t, s.needsFullCheck)
			// The cached output describes older text, so a request that needs a fresh
			// check, such as a code action, declines.
			require.True(t, s.isCacheStale(uri))
			s.mu.RUnlock()
			// A request doesn't wait for a check of the crashed version, which isn't coming.
			waited := make(chan struct{})
			go func() {
				s.mu.RLock()
				defer s.mu.RUnlock()
				s.waitForFreshCheck(uri)
				close(waited)
			}()
			select {
			case <-waited:
			case <-time.After(time.Second):
				t.Fatal("waitForFreshCheck is still waiting for the crashed version")
			}

			// The next change takes a full check, which clears needsFullCheck.
			changeFile(s, ctx, uri, version+1, `val y: number = "a"`)
			require.Equal(t, []string{`0:16-0:19 cannot constrain "a" <: number`},
				awaitDiagnostics(t, published, uri, protocol.UInteger(version+1)))
			s.mu.RLock()
			require.False(t, s.needsFullCheck)
			s.mu.RUnlock()
		})
	}
}

// TestRequestPanicLeavesOutStaleDiagnostics asserts that a request that panics while
// the document is ahead of the last check publishes only the crash diagnostic, since the
// check's ranges index older text.
func TestRequestPanicLeavesOutStaleDiagnostics(t *testing.T) {
	src := `val x: number = "a"`
	s := integrationServer(t, map[string]string{"bin/main.esc": src})
	ctx, published, _ := recordedNotifications()
	uri := openFileIn(t, s, ctx, "bin/main.esc", src)
	require.Equal(t, []string{`0:16-0:19 cannot constrain "a" <: number`},
		awaitDiagnostics(t, published, uri, 1))

	// An edit the server has recorded but not yet checked.
	s.mu.Lock()
	doc := s.documents[uri]
	doc.Version = 2
	doc.Text = "\n\n" + src
	s.documents[uri] = doc
	s.packageGen++
	s.mu.Unlock()

	s.handler.SetInitialized(true)
	s.handler.TextDocumentHover = func(*glsp.Context, *protocol.HoverParams) (*protocol.Hover, error) {
		panic("boom")
	}
	params, err := json.Marshal(protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     protocol.Position{Line: 2, Character: 4},
		},
	})
	require.NoError(t, err)
	_, _, _, err = s.Handle(&glsp.Context{Method: string(protocol.MethodTextDocumentHover), Params: params, Notify: ctx.Notify})
	require.Error(t, err)
	require.Equal(t, []string{
		"2:4-2:4 the Escalier language server crashed while handling textDocument/hover: boom. See the console for the stack trace.",
	}, awaitDiagnostics(t, published, uri, 2))
}

// TestEveryLockIsReleasedByDefer asserts that each place the server takes mu or solverMu
// releases it with a deferred unlock on the next line. A panic that unwinds through a
// section with a plain unlock leaves the lock held, and handleRequestPanic and
// handleCheckPanic, which take mu themselves, would then deadlock instead of recovering.
func TestEveryLockIsReleasedByDefer(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	lockCall := regexp.MustCompile(`\b(\w+)\.(mu|solverMu)\.(R?Lock)\(\)$`)
	var undeferred []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			m := lockCall.FindStringSubmatch(strings.TrimSpace(line))
			if m == nil {
				continue
			}
			unlock := "defer " + m[1] + "." + m[2] + "." + strings.Replace(m[3], "Lock", "Unlock", 1) + "()"
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) != unlock {
				undeferred = append(undeferred, fmt.Sprintf("%s:%d: %s", path, i+1, strings.TrimSpace(line)))
			}
		}
	}
	require.Empty(t, undeferred)
}
