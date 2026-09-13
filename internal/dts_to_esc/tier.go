package dts_to_esc

import (
	"fmt"
	"sort"
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
