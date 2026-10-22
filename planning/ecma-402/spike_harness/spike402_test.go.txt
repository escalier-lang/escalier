package ecma262

import (
	"os"
	"sort"
	"strings"
	"testing"
)

// TestSpike402 reports what the analysis derives for the ECMA-402 surface in a
// merged graph. It is a spike probe, not a check: set ESC_SPIKE_CFG to the
// cfg.json the ECMA-402 spike wrote and read the output.
func TestSpike402(t *testing.T) {
	path := os.Getenv("ESC_SPIKE_CFG")
	if path == "" {
		t.Skip("set ESC_SPIKE_CFG")
	}
	cfg, err := LoadCFG(path)
	if err != nil {
		t.Fatal(err)
	}
	facts := analyze(cfg)

	locale := []string{
		"String.prototype.localeCompare",
		"String.prototype.toLocaleLowerCase",
		"String.prototype.toLocaleUpperCase",
		"Number.prototype.toLocaleString",
		"BigInt.prototype.toLocaleString",
		"Array.prototype.toLocaleString",
		"Date.prototype.toLocaleString",
		"Date.prototype.toLocaleDateString",
		"Date.prototype.toLocaleTimeString",
	}
	anonymous := map[string]bool{
		"Collator Compare Functions": true,
		"DateTime Format Functions":  true,
		"Number Format Functions":    true,
	}
	inScope := func(name string) bool {
		bare := strings.TrimPrefix(strings.TrimPrefix(name, "get "), "set ")
		if strings.HasPrefix(bare, "Intl") || anonymous[bare] {
			return true
		}
		for _, n := range locale {
			if bare == n {
				return true
			}
		}
		return false
	}

	var names []string
	for name := range facts.Methods {
		if inScope(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	t.Logf("%d ECMA-402 builtins in the graph", len(names))
	for _, name := range names {
		t.Logf("%s %s", name, facts.Methods[name])
	}

	base := len(facts.Methods) - len(names)
	for _, axis := range []Axis{AxisReceiver, AxisReturns, AxisThrows, AxisRejects} {
		var open, baseOpen []string
		for _, name := range facts.Unclassified(axis) {
			if inScope(name) {
				open = append(open, name)
			} else {
				baseOpen = append(baseOpen, name)
			}
		}
		t.Logf("unclassified %s — ECMA-402: %d of %d (%.0f%%), ECMA-262: %d of %d (%.0f%%)",
			axis,
			len(open), len(names), 100*float64(len(open))/float64(len(names)),
			len(baseOpen), base, 100*float64(len(baseOpen))/float64(base))
		t.Logf("    ECMA-402 open on %s: %s", axis, strings.Join(open, ", "))
	}
}
