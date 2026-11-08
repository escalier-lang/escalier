package webidl

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources map[string]string
		want    map[string]Definition
	}{
		{
			name: "exposed list, transferable and no constructor",
			sources: map[string]string{"a.idl": `
				[Exposed=(Window,Worker), Serializable, Transferable]
				interface ImageBitmap {
				  readonly attribute unsigned long width;
				  undefined close();
				};`},
			want: map[string]Definition{
				"ImageBitmap": {Kind: KindInterface, Exposed: []string{"Window", "Worker"}, Transferable: true},
			},
		},
		{
			name: "single exposed name, inheritance and a constructor",
			sources: map[string]string{"a.idl": `
				[Exposed=Window]
				interface CustomEvent : Event {
				  constructor(DOMString type, optional CustomEventInit eventInitDict = {});
				};`},
			want: map[string]Definition{
				"CustomEvent": {Kind: KindInterface, Exposed: []string{"Window"}, Constructible: true},
			},
		},
		{
			name: "exposed everywhere",
			sources: map[string]string{"a.idl": `
				[Exposed=*]
				namespace console {
				  undefined log(any... data);
				};`},
			want: map[string]Definition{
				"console": {Kind: KindNamespace, Exposed: []string{"*"}},
			},
		},
		{
			name: "global scope",
			sources: map[string]string{"a.idl": `
				[Global=(Worker,DedicatedWorker),Exposed=DedicatedWorker]
				interface DedicatedWorkerGlobalScope : WorkerGlobalScope {
				  undefined close();
				};`},
			want: map[string]Definition{
				"DedicatedWorkerGlobalScope": {
					Kind:    KindInterface,
					Exposed: []string{"DedicatedWorker"},
					Global:  []string{"Worker", "DedicatedWorker"},
				},
			},
		},
		{
			name: "constructor as an argument name is not a constructor",
			sources: map[string]string{"a.idl": `
				[Exposed=Window]
				interface CustomElementRegistry {
				  undefined define(DOMString name, CustomElementConstructor constructor);
				};`},
			want: map[string]Definition{
				"CustomElementRegistry": {Kind: KindInterface, Exposed: []string{"Window"}},
			},
		},
		{
			name: "legacy factory function does not make the interface constructible",
			sources: map[string]string{"a.idl": `
				[Exposed=Window,
				 LegacyFactoryFunction=Image(optional unsigned long width, optional unsigned long height)]
				interface HTMLImageElement : HTMLElement {
				  [HTMLConstructor] constructor();
				};
				[Exposed=Window, LegacyFactoryFunction=Audio(optional DOMString src)]
				interface HTMLAudioElement : HTMLMediaElement {};`},
			want: map[string]Definition{
				"HTMLImageElement": {Kind: KindInterface, Exposed: []string{"Window"}, Constructible: true},
				"HTMLAudioElement": {Kind: KindInterface, Exposed: []string{"Window"}},
			},
		},
		{
			name: "an argument list after an extended attribute name and a trailing line comment",
			sources: map[string]string{"a.idl": `
				[Exposed=Window, Constructor(DOMString type)]
				interface Legacy {};
				// A trailing comment with no newline`},
			want: map[string]Definition{
				"Legacy": {Kind: KindInterface, Exposed: []string{"Window"}},
			},
		},
		{
			name: "a legacy namespace qualifies the name",
			sources: map[string]string{"a.idl": `
				[LegacyNamespace=WebAssembly, Exposed=*]
				interface Module {
				  constructor(BufferSource bytes);
				};`},
			want: map[string]Definition{
				"WebAssembly.Module": {Kind: KindInterface, Exposed: []string{"*"}, Constructible: true},
			},
		},
		{
			name: "a partial of a legacy namespace interface merges into its qualified key",
			sources: map[string]string{
				"a.idl": `
					partial interface Module {
					  constructor(BufferSource bytes);
					};`,
				"b.idl": `
					[LegacyNamespace=WebAssembly, Exposed=*]
					interface Module {};`,
			},
			want: map[string]Definition{
				"WebAssembly.Module": {Kind: KindInterface, Exposed: []string{"*"}, Constructible: true},
			},
		},
		{
			name: "callback interfaces with and without an interface object",
			sources: map[string]string{"a.idl": `
				callback interface EventListener {
				  undefined handleEvent(Event event);
				};
				[Exposed=Window]
				callback interface NodeFilter {
				  const unsigned short FILTER_ACCEPT = 1;
				  unsigned short acceptNode(Node node);
				};`},
			want: map[string]Definition{
				"EventListener": {Kind: KindCallbackInterface},
				"NodeFilter":    {Kind: KindCallbackInterface, Exposed: []string{"Window"}},
			},
		},
		{
			name: "partials add a constructor and transferable but not their exposure",
			sources: map[string]string{
				"a.idl": `
					[Exposed=(Window,DedicatedWorker), Transferable]
					partial interface MediaStreamTrack {
					  undefined f();
					};
					partial interface Thing {
					  constructor();
					};`,
				"b.idl": `
					[Exposed=Window]
					interface MediaStreamTrack : EventTarget {};
					[Exposed=Window]
					interface Thing {};`,
			},
			want: map[string]Definition{
				"MediaStreamTrack": {Kind: KindInterface, Exposed: []string{"Window"}, Transferable: true},
				"Thing":            {Kind: KindInterface, Exposed: []string{"Window"}, Constructible: true},
			},
		},
		{
			name: "mixins, dictionaries, enums, typedefs, callbacks and includes are skipped",
			sources: map[string]string{"a.idl": `
				// A line comment naming interface Fake {};
				/* A block comment naming interface Fake {}; */
				[Exposed=Window]
				interface mixin GlobalEventHandlers {
				  attribute EventHandler onabort;
				};
				dictionary EventInit {
				  boolean bubbles = false;
				  sequence<DOMString> names = [];
				  Record defaults = {};
				};
				enum ReadyState { "closed", "open", "ended" };
				typedef (Int8Array or Uint8Array) ArrayBufferView;
				callback Function = any (any... arguments);
				Window includes GlobalEventHandlers;
				[Exposed=Window, Global=Window]
				interface Window : EventTarget {};`},
			want: map[string]Definition{
				"Window": {Kind: KindInterface, Exposed: []string{"Window"}, Global: []string{"Window"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Scan(tt.sources)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestScanErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources map[string]string
		want    string
	}{
		{
			name: "duplicate definition",
			sources: map[string]string{
				"a.idl": `[Exposed=Window] interface Node {};`,
				"b.idl": `[Exposed=Window] interface Node {};`,
			},
			want: "b.idl: Node is also defined in a.idl",
		},
		{
			name: "one identifier under two legacy namespaces",
			sources: map[string]string{
				"a.idl": `[LegacyNamespace=WebAssembly, Exposed=*] interface Module {};`,
				"b.idl": `[LegacyNamespace=Other, Exposed=*] interface Module {};`,
			},
			want: "b.idl: Module is defined as both WebAssembly.Module in a.idl and Other.Module",
		},
		{
			name:    "partial without a definition",
			sources: map[string]string{"a.idl": `partial interface Missing { constructor(); };`},
			want:    "a.idl: partial Missing extends no definition",
		},
		{
			name: "first of several partials without a definition",
			sources: map[string]string{
				"a.idl": `partial interface Second {};`,
				"b.idl": `partial interface First {};`,
			},
			want: "a.idl: partial Second extends no definition",
		},
		{
			name: "partial of a different kind",
			sources: map[string]string{
				"a.idl": `partial namespace Node { undefined f(); };`,
				"b.idl": `[Exposed=Window] interface Node {};`,
			},
			want: "a.idl: partial namespace Node extends interface Node in b.idl",
		},
		{
			name:    "unterminated body",
			sources: map[string]string{"a.idl": `[Exposed=Window] interface Node { undefined f();`},
			want:    "a.idl: Node: unterminated body",
		},
		{
			name:    "unterminated block comment",
			sources: map[string]string{"a.idl": `/* never closed`},
			want:    "a.idl: unterminated comment",
		},
		{
			name:    "unterminated string",
			sources: map[string]string{"a.idl": `enum E { "a };`},
			want:    "a.idl: unterminated string",
		},
		{
			name:    "a definition name that is not an identifier",
			sources: map[string]string{"a.idl": `interface { };`},
			want:    `a.idl: expected a definition name, found "{"`,
		},
		{
			name:    "an interface with no body",
			sources: map[string]string{"a.idl": `interface Node : Base`},
			want:    "a.idl: Node: expected a body",
		},
		{
			name:    "a body with no closing semicolon",
			sources: map[string]string{"a.idl": `interface Node {} interface Other {};`},
			want:    `a.idl: Node: expected ; after the body, found "interface"`,
		},
		{
			name:    "an extended attribute name that is not an identifier",
			sources: map[string]string{"a.idl": `[=Window] interface Node {};`},
			want:    `a.idl: expected an extended attribute name, found "="`,
		},
		{
			name:    "an unterminated extended attribute value list",
			sources: map[string]string{"a.idl": `[Exposed=(Window`},
			want:    "a.idl: unterminated value list for Exposed",
		},
		{
			name:    "an unbalanced argument list after an extended attribute name",
			sources: map[string]string{"a.idl": `[Constructor(long x interface Node {};`},
			want:    "a.idl: unbalanced brackets",
		},
		{
			name:    "an unbalanced argument list after an extended attribute value",
			sources: map[string]string{"a.idl": `[LegacyFactoryFunction=Image(long x interface Node {};`},
			want:    "a.idl: unbalanced brackets",
		},
		{
			name:    "an unterminated mixin",
			sources: map[string]string{"a.idl": `interface mixin M { undefined f();`},
			want:    "a.idl: unbalanced brackets",
		},
		{
			name:    "an unterminated callback function",
			sources: map[string]string{"a.idl": `callback F = any (any x`},
			want:    "a.idl: unbalanced brackets",
		},
		{
			name:    "an unterminated typedef",
			sources: map[string]string{"a.idl": `typedef long Count`},
			want:    "a.idl: unterminated definition",
		},
		{
			name:    "malformed extended attributes",
			sources: map[string]string{"a.idl": `[Exposed=Window; interface Node {};`},
			want:    `a.idl: expected , or ] in extended attributes, found ";"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Scan(tt.sources)
			require.EqualError(t, err, tt.want)
		})
	}
}

func TestScanDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.idl"), []byte(`[Exposed=Window] interface Node {};`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte(`interface Ignored {};`), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.idl"), 0o755))

	got, err := ScanDir(dir)
	require.NoError(t, err)
	require.Equal(t, map[string]Definition{"Node": {Kind: KindInterface, Exposed: []string{"Window"}}}, got)

	_, err = ScanDir(filepath.Join(dir, "missing"))
	require.Error(t, err)
}

func TestPackageVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest string
		want     string
		wantErr  string
	}{
		{name: "a version", manifest: `{"version": "1.2.3"}`, want: "1.2.3"},
		{name: "no version", manifest: `{}`, wantErr: "%s has no version"},
		{name: "malformed JSON", manifest: `{`, wantErr: "%s: unexpected end of JSON input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "package.json")
			require.NoError(t, os.WriteFile(path, []byte(tt.manifest), 0o644))
			got, err := PackageVersion(dir)
			if tt.wantErr != "" {
				require.EqualError(t, err, fmt.Sprintf(tt.wantErr, path))
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := PackageVersion(t.TempDir())
	require.Error(t, err)
}

func TestGenerateErrors(t *testing.T) {
	t.Parallel()

	_, err := Generate(t.TempDir())
	require.Error(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"version": "1.0.0"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.idl"), []byte(`/* never closed`), 0o644))
	_, err = Generate(dir)
	require.EqualError(t, err, "a.idl: unterminated comment")
}

func TestKindNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind    Kind
		name    string
		keyword string
	}{
		{KindInterface, "KindInterface", "interface"},
		{KindCallbackInterface, "KindCallbackInterface", "callback interface"},
		{KindNamespace, "KindNamespace", "namespace"},
		{Kind(7), "Kind(7)", "interface"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.name, tt.kind.String())
		require.Equal(t, tt.keyword, tt.kind.keyword())
	}
}
