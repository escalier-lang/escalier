package ecma262

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

// committed.go derives the fact set of the pinned spec revision, which is what
// the converter and the checker prelude classify receivers from. Appendix B of
// planning/ecma-262/implementation_plan.md keeps facts.json derived rather than
// committed, so the pinned answer is the analysis run over the committed graph
// rather than a file anyone edits.

// committedCFG is the control-flow graph tools/spec-extract serializes, which
// the analysis runs over. It is embedded because the compiler classifies
// receivers from it wherever it runs, which is a user's project directory
// rather than this repository. See tools/spec-extract/README.md for how a spec
// bump regenerates it.
//
//go:embed cfg.json
var committedCFG []byte

var (
	committedOnce  sync.Once
	committedFacts *Facts
	committedErr   error
)

// CommittedFacts is the published fact set for the committed graph. Analyzing
// the graph takes long enough to be worth doing once, so every call after the
// first returns the same pointer.
//
// It fails when the analysis leaves a determination unanswered, naming the
// methods that need a curated entry. That is a committed graph and a committed
// curated layer disagreeing about what is covered, which a build has to stop
// on rather than paper over: §7 auto-applies the receiver, so an unanswered
// method would silently become the `&mut self` default.
func CommittedFacts() (*Facts, error) {
	committedOnce.Do(func() {
		cfg, err := ParseCFG(committedCFG)
		if err != nil {
			committedErr = err
			return
		}
		facts, err := NewFacts(cfg)
		if err != nil {
			committedErr = err
			return
		}
		if holes := facts.Incomplete(); len(holes) > 0 {
			committedErr = fmt.Errorf("the committed graph leaves determinations unanswered:\n  %s",
				strings.Join(holes, "\n  "))
			return
		}
		committedFacts = facts
	})
	return committedFacts, committedErr
}
