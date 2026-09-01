package dts_to_esc

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// The §7 gate. Every method the converter emits into a `std:*` package that a
// published fact addresses must carry the receiver that fact claims.
//
// The comparison runs over the pinned lib set, which is the input the bootstrap
// subcommand converts. A method no fact addresses is left out: the name tiers
// answer it, and what they answer is not what this measures.
//
// The count is pinned alongside, because a fact tier that stopped firing would
// leave the receivers agreeing with the heuristics on most of these names and
// show up here as a drop rather than as a mismatch. It moves when the pinned
// lib set or the committed graph does.
func TestStdReceiversMatchTheFacts(t *testing.T) {
	t.Parallel()

	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(filepath.Join(libDir, "lib.es5.d.ts")); err != nil {
		t.Skipf("TypeScript lib files not present at %s; run `pnpm install`: %v", libDir, err)
	}
	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)
	result, err := PartitionLib(inputs)
	require.NoError(t, err)

	facts, err := ecma262.CommittedFacts()
	require.NoError(t, err)
	receivers := NewReceiverFacts(facts)
	mods, err := ConvertBuckets(result, receivers)
	require.NoError(t, err)

	compared := 0
	var mismatched []string
	for uri, mod := range mods {
		if !strings.HasPrefix(uri, "std:") {
			continue
		}
		eachEmittedMethod(mod, func(owner string, elem *ast.MethodElem) {
			ref, addressed := ecma262.Normalize(specKey("", owner+".prototype", elem.Name))
			if !addressed {
				return
			}
			mut, claimed := receivers.Instance(ref.Owner, ref.Member)
			if !claimed {
				return
			}
			compared++
			if mut != elem.Receiver.Mut {
				mismatched = append(mismatched, ref.String())
			}
		})
	}

	sort.Strings(mismatched)
	require.Empty(t, mismatched)
	snaps.MatchInlineSnapshot(t, compared, snaps.Inline("int(182)"))
}

// eachEmittedMethod calls visit for every instance method of every class the
// module emits, with the class's dotted runtime path. A static has no receiver
// to compare, and an accessor's polarity is fixed where the class is built.
func eachEmittedMethod(mod *StandaloneModule, visit func(owner string, elem *ast.MethodElem)) {
	mod.Module.Accept(&methodWalk{mod: mod, visit: visit})
}

// methodWalk carries the enclosing class's runtime path from the declaration
// down to the members declared under it, which is what addresses them in the
// fact source. owner is set while the walk is inside a class the module
// records a path for, and empty everywhere else.
type methodWalk struct {
	ast.DefaultVisitor
	mod   *StandaloneModule
	visit func(owner string, elem *ast.MethodElem)
	owner string
}

func (w *methodWalk) EnterDecl(decl ast.Decl) bool {
	if _, ok := decl.(*ast.ClassDecl); !ok {
		return false
	}
	w.owner = w.mod.Paths[decl]
	return w.owner != ""
}

func (w *methodWalk) ExitDecl(ast.Decl) { w.owner = "" }

// EnterClassElem returns false throughout: a method's name and body hold no
// further member to compare, so there is nothing under one worth walking into.
func (w *methodWalk) EnterClassElem(elem ast.ClassElem) bool {
	method, ok := elem.(*ast.MethodElem)
	if ok && !method.Static && method.Receiver != nil {
		w.visit(w.owner, method)
	}
	return false
}

// Where the fact tier sits in the cascade, one case per neighbouring rung. The
// facts below are hand-built rather than derived, so each case names the
// receiver it needs and nothing else.
func TestClassifyFactTier(t *testing.T) {
	t.Parallel()

	facts := NewReceiverFacts(factsOf(map[string]ecma262.ReceiverKind{
		// A borrow against the mutating `push` prefix, and a mutating
		// receiver against the non-mutating `get*` rule. Neither is what the
		// spec says about these two. What is under test is which answer wins,
		// not which answer is right.
		"Array.prototype.push":            ecma262.RecvBorrow,
		"Array.prototype.getItem":         ecma262.RecvMutBorrow,
		"Array.prototype.toString":        ecma262.RecvMutBorrow,
		"RegExp.prototype [ @@match ]":    ecma262.RecvMutBorrow,
		"Array.prototype.freeze":          ecma262.RecvBorrow,
		"Intl.DateTimeFormat.prototype.f": ecma262.RecvBorrow,
	}))

	tests := map[string]struct {
		ctx    ClassifyContext
		mut    bool
		source ResolutionTier
	}{
		// The two name tiers the fact outranks.
		"OverrulesTheNameHeuristic": {
			ctx:    ClassifyContext{Member: makeMethodDecl("push", nil), ClassName: "Array", Facts: facts},
			mut:    false,
			source: TierECMA262Fact,
		},
		"OverrulesTheGetPrefix": {
			ctx:    ClassifyContext{Member: makeMethodDecl("getItem", nil), ClassName: "Array", Facts: facts},
			mut:    true,
			source: TierECMA262Fact,
		},
		// The explicit author signal the fact does not outrank. `toString`
		// is on the well-known non-mutating list, which tier 3 answers
		// before the fact tier is reached.
		"LosesToAnExplicitSignal": {
			ctx:    ClassifyContext{Member: makeMethodDecl("toString", nil), ClassName: "Array", Facts: facts},
			mut:    false,
			source: TierExplicitSignal,
		},
		// A symbol-keyed member reaches no name tier, so a fact is the only
		// thing below tier 3 that can answer it. `Symbol.match` is not on
		// the well-known list `Symbol.iterator` sits on, so it falls past
		// tier 3 to the fact.
		"ReachesASymbolKeyedMember": {
			ctx:    ClassifyContext{Member: makeComputedMethodDecl("Symbol", "match"), ClassName: "RegExp", Facts: facts},
			mut:    true,
			source: TierECMA262Fact,
		},
		// An imported package's `Array` is its own class, so the builtin's
		// facts say nothing about it and the name tiers decide.
		"IgnoresAModulePath": {
			ctx:    ClassifyContext{Member: makeMethodDecl("push", nil), ClassName: "Array", ModulePath: "lodash/fp", Facts: facts},
			mut:    true,
			source: TierNameHeuristic,
		},
		// A class inside a namespace is addressed by its dotted runtime
		// path, so the namespace has to reach the lookup.
		"AddressesANamespacedClass": {
			ctx:    ClassifyContext{Member: makeMethodDecl("f", nil), ClassName: "DateTimeFormat", NamespacePath: "Intl", Facts: facts},
			mut:    false,
			source: TierECMA262Fact,
		},
		// A converter given no graph reaches the name tiers, the same as a
		// method no fact addresses.
		"FallsThroughWithNoFactSource": {
			ctx:    ClassifyContext{Member: makeMethodDecl("push", nil), ClassName: "Array"},
			mut:    true,
			source: TierNameHeuristic,
		},
		// A name no fact addresses. `freeze` is a fact on `Array.prototype`
		// here, and this is `Set.prototype`.
		"FallsThroughOnADifferentOwner": {
			ctx:    ClassifyContext{Member: makeMethodDecl("freeze", nil), ClassName: "Set", Facts: facts},
			mut:    true,
			source: TierDefault,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			result := Classify(test.ctx)
			require.Equal(t, test.mut, result.Mut)
			require.Equal(t, test.source, result.Source)
		})
	}
}

// The shapes ReceiverFacts answers nothing for. Each would otherwise read as a
// claim about a receiver the fact does not make one about.
func TestReceiverFactsAnswersOnlyInstanceReceivers(t *testing.T) {
	t.Parallel()

	facts := NewReceiverFacts(factsOf(map[string]ecma262.ReceiverKind{
		"Array.prototype.push":   ecma262.RecvMutBorrow,
		"Array.isArray":          ecma262.RecvNone,
		"get Map.prototype.size": ecma262.RecvBorrow,
	}))

	tests := map[string]struct {
		facts  *ReceiverFacts
		owner  string
		member ecma262.MemberKey
	}{
		"NoFactSource":     {facts: nil, owner: "Array", member: ecma262.StrMember("push")},
		"NoOwner":          {facts: facts, owner: "", member: ecma262.StrMember("push")},
		"UnknownOwner":     {facts: facts, owner: "Set", member: ecma262.StrMember("push")},
		"UnknownMember":    {facts: facts, owner: "Array", member: ecma262.StrMember("pop")},
		"StaticHasNoSelf":  {facts: facts, owner: "Array", member: ecma262.StrMember("isArray")},
		"AccessorIsKeyed":  {facts: facts, owner: "Map", member: ecma262.StrMember("size")},
		"SymbolIsDistinct": {facts: facts, owner: "Array", member: ecma262.SymMember("push")},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mut, ok := test.facts.Instance(test.owner, test.member)
			require.False(t, ok)
			require.False(t, mut)
		})
	}

	mut, ok := facts.Instance("Array", ecma262.StrMember("push"))
	require.True(t, ok)
	require.True(t, mut)
}
