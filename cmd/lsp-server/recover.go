package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// panicReport is a recovered panic with the stack of the goroutine that raised it.
type panicReport struct {
	value any
	stack []byte
}

// message renders the panic for a diagnostic, a log line, or a request's error. task
// names the work that panicked, such as `handling textDocument/hover` or
// `checking file:///lib/index.esc`.
func (p panicReport) message(task string) string {
	return fmt.Sprintf("the Escalier language server crashed while %s: %v", task, p.value)
}

// requestLocation is the part of a request's params that says which document and
// position it is about. A field the request doesn't carry stays nil.
type requestLocation struct {
	TextDocument *protocol.TextDocumentIdentifier `json:"textDocument"`
	Position     *protocol.Position               `json:"position"`
	// Range is where a request such as `textDocument/codeAction` applies.
	Range *protocol.Range `json:"range"`
	// Arguments holds a `workspace/executeCommand` request's arguments. The `compile`
	// command takes one, the URI of the document the user compiled from.
	Arguments []any `json:"arguments"`
}

// documentURI returns the document the request is about, and false when it names none.
func (loc requestLocation) documentURI() (protocol.DocumentUri, bool) {
	if loc.TextDocument != nil {
		return loc.TextDocument.URI, true
	}
	if len(loc.Arguments) == 1 {
		if uri, ok := loc.Arguments[0].(string); ok {
			return protocol.DocumentUri(uri), true
		}
	}
	return "", false
}

// handleRequestPanic reports a panic raised while handling the request or notification
// in context, and returns the error the request answers with. The report goes to stderr
// and `window/logMessage`. When the params name an open document, the document also gets
// a diagnostic. It goes at the request's position, or at the start of its range, or at
// the start of the document when the request carries neither. The diagnostics the last
// check found are published with it when that check read the document's current text.
//
// Every section of the server that holds mu or solverMu releases it with a deferred
// unlock, so a panic unwinding through one leaves both locks free, which the read lock
// here depends on. Request handlers read the server's state without writing it. The
// notifications that write it, such as didOpen and didChange, update the document map and
// the generation counters under mu, and leave nothing half-written for the next request.
func (s *Server) handleRequestPanic(context *glsp.Context, p panicReport) error {
	msg := p.message("handling " + context.Method)
	logPanic(context, msg, p)

	var loc requestLocation
	if len(context.Params) > 0 {
		// A request whose params don't decode names no location, and the panic is
		// still reported through the log and the returned error.
		_ = json.Unmarshal(context.Params, &loc)
	}
	if uri, ok := loc.documentURI(); ok {
		var pos protocol.Position
		if loc.Position != nil {
			pos = *loc.Position
		} else if loc.Range != nil {
			pos = loc.Range.Start
		}
		if doc, opened := s.openDocument(uri); opened {
			s.publishPanicDiagnostic(context, uri, doc.Version, pos, msg, true)
		}
	}
	return errors.New(msg)
}

// handleCheckPanic reports a panic raised while checking uri at version, and leaves the
// server's state ready for the next check.
//
// The state a check writes is put back as follows:
//   - A full check assigns checkOutput only after CheckPackage returns, so a panic
//     leaves the previous output whole.
//   - A bin/ re-check writes into the solver registries it shares with the cached
//     library run, so a panic can leave them half-written. needsFullCheck makes the
//     next check rebuild everything from the sources.
//   - Requests that wait for this version's check would wait until the next edit. The
//     version is recorded in panickedVersion, which ends their wait, and they are woken.
//     It is not recorded as checked, since checkOutput still describes older text.
func (s *Server) handleCheckPanic(context *glsp.Context, uri protocol.DocumentUri, version protocol.Integer, p panicReport) {
	msg := p.message("checking " + string(uri))
	logPanic(context, msg, p)

	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.needsFullCheck = true
		s.panickedVersion[uri] = version
	}()
	s.validated.Broadcast()

	// The diagnostics from the last finished check describe older text, so their ranges
	// may not line up with this version. The crash diagnostic replaces them.
	s.publishPanicDiagnostic(context, uri, version, protocol.Position{}, msg, false)
}

// logPanic writes msg and the panic's stack to stderr, and sends msg to the client as
// an error through `window/logMessage`.
func logPanic(context *glsp.Context, msg string, p panicReport) {
	fmt.Fprintf(os.Stderr, "%s\n%s", msg, p.stack)
	if context.Notify == nil {
		return
	}
	go context.Notify(protocol.ServerWindowLogMessage, &protocol.LogMessageParams{
		Type:    protocol.MessageTypeError,
		Message: msg,
	})
}

// publishPanicDiagnostic publishes an error diagnostic for msg at pos in uri, the document
// at version. When keepChecked is true, the diagnostics the last check found for uri are
// published with it, so a crash in one request doesn't hide the file's errors.
func (s *Server) publishPanicDiagnostic(
	context *glsp.Context,
	uri protocol.DocumentUri,
	version protocol.Integer,
	pos protocol.Position,
	msg string,
	keepChecked bool,
) {
	if context.Notify == nil {
		return
	}
	severity := protocol.DiagnosticSeverityError
	source := "escalier"
	diags := []protocol.Diagnostic{}
	if keepChecked {
		diags = append(diags, s.checkedDiagnostics(uri)...)
	}
	diags = append(diags, protocol.Diagnostic{
		Range:    protocol.Range{Start: pos, End: pos},
		Severity: &severity,
		Source:   &source,
		Message:  msg + ". See the console for the stack trace.",
	})
	diagVersion := protocol.UInteger(version)
	go context.Notify(protocol.ServerTextDocumentPublishDiagnostics, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     &diagVersion,
		Diagnostics: diags,
	})
}

// checkedDiagnostics returns the diagnostics the last check found for uri. It returns
// none when the server holds no check output, or when the output describes older text
// than the document's, whose ranges would not line up with the document.
func (s *Server) checkedDiagnostics(uri protocol.DocumentUri) []protocol.Diagnostic {
	sourceID := s.sourceIDForURI(uri)
	s.mu.RLock()
	defer s.mu.RUnlock()
	co := s.checkOutput
	if co == nil || s.isCacheStale(uri) {
		return nil
	}
	return fileDiagnostics(sourceID, checkedSource(co, sourceID), co.ParseErrors, co.TypeErrors)
}

// Comments that make a check fail on purpose when the server runs with test hooks on.
// The playground's tests put one in a file to exercise crash recovery end to end.
const (
	// panicMarker makes the check panic, which the server recovers from.
	panicMarker = "@escalier-lsp-test-panic"
	// crashMarker makes the check panic on a goroutine with no recover, which ends the
	// process. The client has to notice the exit and start a new server.
	crashMarker = "@escalier-lsp-test-crash"
)

// testHooksEnv is the environment variable that turns the test hooks on when set to "1".
const testHooksEnv = "ESCALIER_LSP_TEST_HOOKS"

// runTestHooks panics, or ends the process, when contents holds panicMarker or
// crashMarker and the server runs with test hooks on. It does nothing otherwise. A
// check calls it at the point where the checker runs.
func (s *Server) runTestHooks(contents string) {
	if !s.testHooks {
		return
	}
	if strings.Contains(contents, crashMarker) {
		done := make(chan struct{})
		go func() {
			panic("forced by " + crashMarker)
		}()
		// The goroutine's panic ends the process before anything closes done.
		<-done
	}
	if strings.Contains(contents, panicMarker) {
		panic("forced by " + panicMarker)
	}
}
