package main

import (
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// checkStartedMethod and checkFinishedMethod are the notifications that bracket each
// check of a document version. Every checkStartedMethod is followed by a
// checkFinishedMethod with the same params, whether the check publishes diagnostics,
// finds the version stale, or panics. The playground times checks with them.
const (
	checkStartedMethod  = "escalier/checkStarted"
	checkFinishedMethod = "escalier/checkFinished"
)

// checkEventParams is the params of a checkStartedMethod or checkFinishedMethod
// notification. It names the document version whose change started the check.
type checkEventParams struct {
	URI     protocol.DocumentUri `json:"uri"`
	Version protocol.Integer     `json:"version"`
}

// notifyCheck sends method with uri and version. It sends synchronously, so the client
// receives a check's start before its finish.
func notifyCheck(context *glsp.Context, method string, uri protocol.DocumentUri, version protocol.Integer) {
	if context.Notify == nil {
		return
	}
	context.Notify(method, &checkEventParams{URI: uri, Version: version})
}
