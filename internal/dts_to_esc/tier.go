package dts_to_esc

import (
	"fmt"
	"sort"

	"github.com/escalier-lang/escalier/internal/set"
)

// tier.go assigns each pseudo-package to a runtime tier and orders the
// tiers, so an import edge can be checked against the runtimes its two
// ends are available on.
//
// A file that imports the portable tier and nothing above it is
// checkable against Node, Deno, Bun and Workers, which is the line the
// `node:*` scheme already draws from the other side. Grouping by
// specification does not draw that line. `AbortSignal` is specified in
// the DOM and implemented everywhere, so a package that takes one has to
// reach a name filed under the browser. `web:core` holds those names
// instead, which is what keeps the portable tier off the DOM.

// Tier is the set of runtimes a package's declarations are available
// on. Fewer runtimes carry a higher tier, so the values order by how
// much a package assumes about where it runs.
type Tier int

const (
	// TierCore is available on every runtime that implements the WinterCG
	// minimum common API: the event model, `AbortSignal`, `DOMException`,
	// `BufferSource`, and the text encoder and decoder.
	TierCore Tier = iota
	// TierPortable is available on every non-browser runtime as well as the
	// browser. Fetch, URL, streams, files, crypto, performance and
	// WebSockets are here.
	//
	// The promise is per package, not per declaration. Every WinterCG runtime
	// ships the package. A few legacy or browser-only members inside one are
	// still absent off the browser: Node 22 defines neither `FileReader` nor
	// `PerformanceTiming`. #1586 tracks tiering a declaration rather than a
	// package.
	TierPortable
	// TierBrowser is available in a browser alone. The DOM and everything
	// that names a document, an element, or a browser-only device.
	TierBrowser
	// TierLanguage is available wherever ECMAScript is, so it sits below core
	// and names nothing above itself. Almost every `std:*` package is here;
	// stdTiers records the ones that are not.
	TierLanguage Tier = -1
)

// String names the tier as a diagnostic writes it.
func (t Tier) String() string {
	switch t {
	case TierLanguage:
		return "language"
	case TierCore:
		return "core"
	case TierPortable:
		return "portable"
	case TierBrowser:
		return "browser"
	}
	return fmt.Sprintf("Tier(%d)", int(t))
}

// The scheme and the tier answer different questions. The scheme says how a
// package is named and grouped; the tier says which runtimes carry it. They
// agree for almost every package, and stdTiers records where they do not.

// stdTiers assigns a tier to each `std:*` package whose contents are not
// available wherever ECMAScript is. A `std:*` package absent from this map is
// TierLanguage.
var stdTiers = map[string]Tier{
	// WebAssembly is a host global rather than an ECMAScript one, and
	// `instantiateStreaming` takes a fetch `Response`. Every WinterCG runtime
	// ships it, so it is portable rather than browser-only.
	"std:wasm": TierPortable,
}

// webTiers assigns a tier to each `web:*` package.
//
// A package is portable when every runtime in the WinterCG set ships it,
// not when its specification is silent about the browser. `web:websocket`
// is portable because Node, Deno, Bun and Workers all implement it;
// `web:webgl` is not, because it takes an `HTMLCanvasElement` to draw on.
var webTiers = map[string]Tier{
	"web:core": TierCore,

	"web:fetch":       TierPortable,
	"web:url":         TierPortable,
	"web:streams":     TierPortable,
	"web:file":        TierPortable,
	"web:crypto":      TierPortable,
	"web:performance": TierPortable,
	"web:websocket":   TierPortable,
	"web:encoding":    TierPortable,
	"web:compression": TierPortable,

	"web:dom":            TierBrowser,
	"web:workers":        TierBrowser,
	"web:webgl":          TierBrowser,
	"web:web_audio":      TierBrowser,
	"web:web_rtc":        TierBrowser,
	"web:web_codecs":     TierBrowser,
	"web:indexeddb":      TierBrowser,
	"web:service_worker": TierBrowser,
	"web:push":           TierBrowser,
	"web:cache":          TierBrowser,
	"web:storage":        TierBrowser,
	"web:webauthn":       TierBrowser,
	"web:payments":       TierBrowser,
}

// TierOf returns the tier a package URI sits in, and false for a URI the
// partition does not hold.
func TierOf(uri string) (Tier, bool) {
	if tier, ok := webTiers[uri]; ok {
		return tier, true
	}
	if tier, ok := stdTiers[uri]; ok {
		return tier, true
	}
	if _, ok := PackageForURI(uri); ok && SchemeOf(uri) == "std" {
		return TierLanguage, true
	}
	return 0, false
}

// PackagesInTier returns the URIs of every package in a tier, sorted.
func PackagesInTier(tier Tier) []string {
	uris := make([]string, 0, len(webTiers)+len(stdTiers))
	for _, uri := range PackageList() {
		if got, ok := TierOf(uri); ok && got == tier {
			uris = append(uris, uri)
		}
	}
	sort.Strings(uris)
	return uris
}

// AcceptedUpwardEdges lists the import edges that go up a tier and are allowed
// to, keyed by the importing package and naming the references that force each.
//
// Every entry is a declaration the pinned `.d.ts` types against a browser type
// that a portable runtime implements differently or not at all. Answering one
// means deciding what the portable form should say, which is a change to the
// declaration rather than to this table, so each is left recorded until that
// decision is made. #1590 carries them.
//
// An edge absent from this table fails `generate`, so a new one is caught at
// the run that introduces it.
var AcceptedUpwardEdges = map[string][]string{
	// Node defines FormData, but the pinned type gives it an HTMLFormElement
	// constructor no portable runtime has. XMLHttpRequestBodyInit is a union
	// that names FormData.
	"web:fetch": {"FormData", "XMLHttpRequestBodyInit"},
	// FileReader fires progress events. Node 22 defines neither it nor
	// ProgressEvent, so both belong on the browser side of the line.
	"web:file": {"ProgressEvent"},
	// EventCounts is the performance-timeline map keyed by DOM event names.
	"web:performance": {"EventCounts"},
	// URL.createObjectURL takes a MediaSource in the browser and a Blob
	// everywhere else.
	"web:url": {"MediaSource"},
	// MessageEvent.source is a WindowProxy or ServiceWorker in the browser and
	// always null off it. BinaryType is the socket's payload mode, a string
	// union filed under the DOM.
	"web:websocket": {"BinaryType", "MessageEvent"},
}

// acceptsUpwardEdge reports whether every reference forcing an edge is one
// AcceptedUpwardEdges records for that importer.
func acceptsUpwardEdge(from string, names []string) bool {
	accepted := set.FromSlice(AcceptedUpwardEdges[from])
	for _, name := range names {
		if !accepted.Contains(name) {
			return false
		}
	}
	return true
}
