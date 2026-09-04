package dts_to_esc

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// A `web:*` package should be importable in a Web Worker or not, with
// nothing in between. The §6.1 partition splits by API family and says
// nothing about the host a name is available in, so one package can
// hold some names a worker has and some it does not. Importing such a
// package in a worker binds names that do not exist at runtime.
//
// The pinned lib set answers the question on its own. WorkerHostSources
// records how, and workerHasName below applies it. A hand-maintained
// per-package table would drift on every TypeScript bump instead, which
// is the failure the §6.1 unmapped-symbol fail-safe exists to
// prevent.

// HostVerdict says which hosts a package's declarations are available
// in.
type HostVerdict string

const (
	// WorkerCleanHost means every declaration is available in a worker.
	WorkerCleanHost HostVerdict = "worker-clean"

	// WindowOnlyHost means none is.
	WindowOnlyHost HostVerdict = "window-only"

	// MixedHost means some are and some are not, which is the state
	// that needs a split or a move.
	MixedHost HostVerdict = "mixed"
)

// PackageHosts is one package's host availability.
type PackageHosts struct {
	// URI is the package URI.
	URI string

	// Verdict is the package's reading over Total.
	Verdict HostVerdict

	// Total is how many distinct names the package declares.
	Total int

	// WindowOnly names the declarations a worker does not have, sorted.
	// It is empty for a worker-clean package and holds every name of a
	// window-only one.
	WindowOnly []string
}

// AnalyzeHostAvailability reads every routed package and reports which
// hosts its declarations are available in. Every package the partition
// filled gets an entry, sorted by URI.
func AnalyzeHostAvailability(result *PartitionResult) []PackageHosts {
	out := make([]PackageHosts, 0, len(result.Buckets))
	for uri, stmts := range result.Buckets {
		entry := PackageHosts{URI: uri}
		seen := set.NewSet[string]()
		for _, stmt := range stmts {
			name := topLevelName(stmt)
			if name == "" || seen.Contains(name) {
				continue
			}
			seen.Add(name)
			entry.Total++
			if !workerHasName(result.DeclaringSources[name]) {
				entry.WindowOnly = append(entry.WindowOnly, name)
			}
		}
		sort.Strings(entry.WindowOnly)
		switch len(entry.WindowOnly) {
		case 0:
			entry.Verdict = WorkerCleanHost
		case entry.Total:
			entry.Verdict = WindowOnlyHost
		default:
			entry.Verdict = MixedHost
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URI < out[j].URI })
	return out
}

// workerHasName reports whether a worker has a name, given the `.d.ts`
// basenames declaring it. One outside the window host is enough: the
// worker host lib restating a name says a worker has it, and an
// ES-core file declaring one says both hosts do, since each loads that
// file. The Windows Script Host lib is neither host, so it does not
// count.
func workerHasName(sources set.Set[string]) bool {
	for _, file := range sources.ToSlice() {
		if !DOMResidualSources.Contains(file) && file != ScriptHostSource {
			return true
		}
	}
	return false
}

// windowOnlyNameCap is how many window-only names one report line
// carries before the rest become a count. `web:dom` holds hundreds, and
// listing them would bury the packages a reader can act on.
const windowOnlyNameCap = 12

// ReportHostAvailability prints the host availability of every package
// the partition filled: a summary line of the counts by verdict, then
// one line for each package that is not worker-clean. Worker-clean is
// the state that needs nothing, so those packages are left to the
// summary's count.
//
// Each mixed package carries either the reason MixedHostPackages
// records for it or "needs a decision" when it records none. That
// second case is the §6.1 gate on this axis.
func ReportHostAvailability(result *PartitionResult, w io.Writer) error {
	entries := AnalyzeHostAvailability(result)

	// The report is assembled in memory and written once, matching
	// ReportPartition. A strings.Builder write cannot fail, so the body
	// stays free of error plumbing.
	var b strings.Builder

	counts := map[HostVerdict]int{}
	for _, e := range entries {
		counts[e.Verdict]++
	}
	fmt.Fprintf(&b, "  host availability: %d worker-clean, %d window-only, %d mixed\n",
		counts[WorkerCleanHost], counts[WindowOnlyHost], counts[MixedHost])

	for _, e := range entries {
		switch e.Verdict {
		case WorkerCleanHost:
			continue
		case WindowOnlyHost:
			fmt.Fprintf(&b, "    %s: window-only, %d declaration%s\n",
				e.URI, e.Total, plural(e.Total))
		case MixedHost:
			fmt.Fprintf(&b, "    %s: mixed — %d of %d window-only: %s\n",
				e.URI, len(e.WindowOnly), e.Total,
				summarizeNames(e.WindowOnly))
			if reason, ok := MixedHostPackages[e.URI]; ok {
				fmt.Fprintf(&b, "      recorded: %s\n", reason)
			} else {
				b.WriteString("      needs a decision\n")
			}
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// summarizeNames renders a name list, replacing everything past
// windowOnlyNameCap with a count of what it left out.
func summarizeNames(names []string) string {
	if len(names) <= windowOnlyNameCap {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, and %d more",
		strings.Join(names[:windowOnlyNameCap], ", "),
		len(names)-windowOnlyNameCap)
}
