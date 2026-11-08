package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/webidl"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// webIDLTrios declares two trios the way `lib.dom.d.ts` does. TypeScript gives
// both a bare `new()`, and WebIDL gives only MessageChannel a constructor.
const webIDLTrios = `
interface MessagePort {
    close(): void;
}
declare var MessagePort: {
    prototype: MessagePort;
    new(): MessagePort;
};
interface MessageChannel {
    readonly port1: MessagePort;
}
declare var MessageChannel: {
    prototype: MessageChannel;
    new(): MessageChannel;
};
`

// convertWithWebIDL converts input the way ConvertBuckets converts a `web:*`
// package, consulting defs for which interfaces WebIDL lets a program construct.
func convertWithWebIDL(t *testing.T, input string, defs map[string]webidl.Definition) *StandaloneModule {
	t.Helper()
	source := &ast.Source{Path: "test.d.ts", Contents: input, ID: 0}
	dtsModule, errs := dts_parser.NewDtsParser(source).ParseModule()
	require.Empty(t, errs, "dts parse errors")
	mod, err := convertStandaloneModule(dtsModule, nil, nil, nil, defs)
	require.NoError(t, err)
	return mod
}

// TestStandalone_WebIDLMakesAnInventedConstructorPrivate asserts that an
// interface WebIDL declares without a `constructor()` converts with a private
// constructor in place of TypeScript's, and that one WebIDL declares a
// constructor on keeps TypeScript's.
func TestStandalone_WebIDLMakesAnInventedConstructorPrivate(t *testing.T) {
	t.Parallel()

	mod := convertWithWebIDL(t, webIDLTrios, map[string]webidl.Definition{
		"MessagePort":    {Kind: webidl.KindInterface},
		"MessageChannel": {Kind: webidl.KindInterface, Constructible: true},
	})
	snaps.MatchInlineSnapshot(t, printNamedClass(t, mod, "MessagePort"), snaps.Inline(`@js("MessagePort")
export declare class MessagePort {
    close(&mut self) -> undefined,
    static prototype: MessagePort,
    private constructor(&mut self)
}`))
	snaps.MatchInlineSnapshot(t, printNamedClass(t, mod, "MessageChannel"), snaps.Inline(`@js("MessageChannel")
export declare class MessageChannel {
    readonly port1: MessagePort,
    static prototype: MessageChannel,
    constructor(&mut self)
}`))
}

// TestStandalone_NoWebIDLKeepsEveryConstructor asserts that a package converted
// without the WebIDL table keeps each constructor the `.d.ts` declares. A
// third-party package's `MessagePort` is not the platform's.
func TestStandalone_NoWebIDLKeepsEveryConstructor(t *testing.T) {
	t.Parallel()

	mod := convertWithWebIDL(t, webIDLTrios, nil)
	snaps.MatchInlineSnapshot(t, printNamedClass(t, mod, "MessagePort"), snaps.Inline(`@js("MessagePort")
export declare class MessagePort {
    close(&mut self) -> undefined,
    static prototype: MessagePort,
    constructor(&mut self)
}`))
}
