package webidl

import (
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
			name:    "partial without a definition",
			sources: map[string]string{"a.idl": `partial interface Missing { constructor(); };`},
			want:    "a.idl: partial Missing extends no definition",
		},
		{
			name:    "unterminated body",
			sources: map[string]string{"a.idl": `[Exposed=Window] interface Node { undefined f();`},
			want:    "a.idl: Node: unterminated body",
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
