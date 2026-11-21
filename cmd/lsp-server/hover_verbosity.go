package main

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
)

// hoverParams is the params of `textDocument/hover` with Escalier's verbosity extension.
// The LSP has no verbosity field on a hover request, so a client that does not know the
// extension sends no level and gets level 0.
type hoverParams struct {
	protocol.HoverParams
	// VerbosityLevel is how many layers of aliases the hover expands. Each layer also
	// evaluates indexed access, `keyof`, conditional and mapped types. A negative level
	// reads as 0.
	VerbosityLevel int `json:"verbosityLevel,omitempty"`
}

// verboseHover is a hover with Escalier's verbosity extension. The level fields let a
// client offer an expand or collapse action, and a client that does not know the
// extension reads it as a plain hover.
type verboseHover struct {
	Contents protocol.MarkupContent `json:"contents"`
	// VerbosityLevel is the level the hover was rendered at. It is lower than the level
	// asked for when expanding stops changing the type before that level.
	VerbosityLevel int `json:"verbosityLevel,omitempty"`
	// CanIncreaseVerbosity reports whether the next level renders differently.
	CanIncreaseVerbosity bool `json:"canIncreaseVerbosity,omitempty"`
	// CanDecreaseVerbosity reports whether VerbosityLevel is above 0.
	CanDecreaseVerbosity bool `json:"canDecreaseVerbosity,omitempty"`
}

// newVerboseHover returns a hover that shows code as an Escalier code block, with no
// level to expand to.
func newVerboseHover(code string) *verboseHover {
	return &verboseHover{Contents: codeHover(code).Contents.(protocol.MarkupContent)}
}
