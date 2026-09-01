package checker

import (
	"fmt"
	"sync"

	"github.com/escalier-lang/escalier/internal/dts_to_esc"
	"github.com/escalier-lang/escalier/internal/ecma262"
)

// ecma262.go resolves the receiver claims the ECMA-262 analysis publishes for
// the builtins, which rank above the name heuristics wherever this package
// classifies a `.d.ts` method's receiver. See
// planning/ecma-262/implementation_plan.md §7.

var (
	receiverFactsMu     sync.Mutex
	cachedReceiverFacts *dts_to_esc.ReceiverFacts
)

// receiverFacts is the ECMA-262 receiver source for the pinned spec revision,
// indexed for lookup by owner and member name. The claims are derived from the
// committed control-flow graph, so the first call runs the analysis and every
// later one returns the same source.
//
// A graph the analysis cannot read panics, matching how this package treats an
// absent TypeScript lib directory. Neither is an input the prelude can
// classify without. The fact source is what decides the receiver of
// `String.prototype.replace` and of every other builtin the name heuristics
// answer wrongly. Falling back to those heuristics would put a mutating
// receiver on methods that do not mutate, with nothing on stderr to say why.
func receiverFacts() *dts_to_esc.ReceiverFacts {
	receiverFactsMu.Lock()
	defer receiverFactsMu.Unlock()
	if cachedReceiverFacts != nil {
		return cachedReceiverFacts
	}

	facts, err := ecma262.CommittedFacts()
	if err != nil {
		panic(fmt.Sprintf("failed to derive the ECMA-262 facts: %v", err))
	}
	cachedReceiverFacts = dts_to_esc.NewReceiverFacts(facts)
	return cachedReceiverFacts
}
