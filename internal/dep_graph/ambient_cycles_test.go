package dep_graph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A cycle whose value bindings are all ambient constructs nothing at run time,
// so there is no initialization order for it to get wrong.
//
// Only an `extends` clause puts a class's VALUE binding in a cycle, because
// building the subclass needs the superclass built first. A member's type
// annotation produces a type edge, and a type-only cycle is already allowed. So
// this rule is what separates `declare class A extends B` from the same pair
// without the modifier.
func TestAmbientCyclesAreNotProblematic(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		src         string
		problematic bool
	}{
		// Neither class is built, so neither has to exist before the other.
		"TwoAmbientClassesExtendingEachOther": {
			src: `
				declare class A extends B {}
				declare class B extends A {}
			`,
			problematic: false,
		},
		// The `declare` modifier is the test rather than the absence of an
		// initializer, because a class carries no initializer either way. Here
		// each class needs the other built before it can extend it.
		"TwoClassesExtendingEachOther": {
			src: `
				class A extends B {}
				class B extends A {}
			`,
			problematic: true,
		},
		// One member is built, so the cycle still has an order to get wrong.
		// Every value binding has to be ambient, not merely one of them.
		"OneAmbientAndOnePlainExtendingEachOther": {
			src: `
				declare class A extends B {}
				class B extends A {}
			`,
			problematic: true,
		},
		// A member's type annotation reaches the other class's TYPE binding
		// alone, so this pair forms a type-only cycle and never reaches the rule
		// above. Recorded because it is the shape the generated tree forms most
		// of its cycles in.
		"AmbientClassesReferringByMemberType": {
			src: `
				declare class A { b: B }
				declare class B { a: A }
			`,
			problematic: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cycles := BuildDepGraph(parseModule(test.src)).FindCycles()
			if test.problematic {
				require.NotEmpty(t, cycles, "expected a problematic cycle")
				return
			}
			require.Empty(t, cycles, "expected no problematic cycle")
		})
	}
}
