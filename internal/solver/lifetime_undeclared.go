package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
)

// checkLifetimeDeclarations reports the two directions in which a signature's named
// lifetimes and its `<…>` binder list can disagree:
//
//   - A used-but-undeclared lifetime. A `&'x` borrow or a bound right-hand side names
//     `'x` that no binder introduces, so `'x` is a forgotten declaration or a typo.
//   - A declared-but-unused binder. A `<'a>` that no borrow and no bound references.
//
// A use is any named lifetime the signature mentions that is not itself a binder. It is
// the lifetime of a `&'x` borrow and the right-hand side of a bound, since `'a: 'x` uses
// `'x`. The left-hand side of a bound is a binder, not a use. `'static` is the built-in
// bottom of the outlives lattice and is never undeclared, on either side.
//
// A nested `fn(…)` type is its own lifetime-quantifier scope. Its borrows are checked by
// its own resolveFuncTypeAnn pass, so the scan treats a nested function annotation as an
// opaque boundary and neither collects its inner borrows here nor counts them toward this
// signature's binders.
//
// enclosing holds the `<…>` binder names of every enclosing function. A used name in it is
// declared on the chain and resolves to the enclosing lifetime, so it is not undeclared
// even when this signature does not bind it. A name bound nowhere on the chain is the hard
// error.
//
// Recovery is left to namedLifetime, which mints a fresh lifetime for an undeclared name
// so the signature stays well-formed. This scan only reports; it changes no resolution.
func (c *checker) checkLifetimeDeclarations(lifetimeParams []*ast.LifetimeParam, params []*ast.Param, ret, throws ast.TypeAnn, enclosing set.Set[string]) {
	// declared maps each binder name to its first binder node, the node the unused-binder
	// scan blames. declaredOrder is the deduplicated first-appearance order, which drives
	// deterministic suggestions and a single unused report per name even when a name is
	// bound more than once.
	declared := map[string]*ast.LifetimeParam{}
	declaredOrder := []string{}
	for _, p := range lifetimeParams {
		// A 'static left-hand side is not a bindable parameter; the parser rejects it.
		// This guard is defensive against a hand-built AST.
		if p.Name == "static" {
			continue
		}
		// A name bound twice binds nothing new. Report each repeat against the kept first
		// binder and dedup so the undeclared and unused scans see the name once.
		if first, seen := declared[p.Name]; seen {
			c.report(&DuplicateLifetimeParamError{Name: p.Name, Param: p, First: first})
			continue
		}
		declaredOrder = append(declaredOrder, p.Name)
		declared[p.Name] = p
	}

	// Collect every named-lifetime use in the signature's borrows and bound right-hand
	// sides.
	var col lifetimeUseCollector
	for _, p := range params {
		if p.TypeAnn != nil {
			p.TypeAnn.Accept(&col)
		}
	}
	if ret != nil {
		ret.Accept(&col)
	}
	if throws != nil {
		throws.Accept(&col)
	}
	for _, p := range lifetimeParams {
		for _, b := range p.Bounds {
			if b.Name != "static" {
				col.uses = append(col.uses, b)
			}
		}
	}

	// A use with no `<…>` clause at all prompts adding one; a use under a clause suggests
	// the nearest declared name. Both are hard errors — hasClause shapes only the hint.
	hasClause := len(lifetimeParams) > 0
	used := set.NewSet[string]()
	for _, u := range col.uses {
		used.Add(u.Name)
		if _, ok := declared[u.Name]; ok {
			continue
		}
		// A name an enclosing function binds is inherited, not undeclared. namedLifetime
		// resolves it to the enclosing lifetime, so the borrow ties to that region.
		if enclosing.Contains(u.Name) {
			continue
		}
		c.report(&UndeclaredLifetimeError{
			Name:        u.Name,
			Suggestions: nearestLifetimes(u.Name, declaredOrder),
			hasClause:   hasClause,
			span:        u.Span(),
		})
	}

	// The symmetric companion: a binder no use references is dead weight. Iterating
	// declaredOrder reports each unused name once and blames its first binder, even for a
	// name bound more than once.
	for _, name := range declaredOrder {
		if !used.Contains(name) {
			c.report(&UnusedLifetimeParamError{Name: name, Param: declared[name]})
		}
	}
}

// lifetimeUseCollector walks a signature's type annotations and records each `&'x`
// borrow's lifetime as a use, keeping the LifetimeAnn node so the check reads its name
// and blames its span. It descends through nested borrows and object, tuple, and union
// annotations, but stops at a nested function annotation, which owns its own lifetime
// scope.
//
// The set of forms this records must match the forms resolveLifetimeAnn interns through
// namedLifetime, since a use the scan misses is one namedLifetime still mints a fresh
// recovery lifetime for. Today that is the `&'x` borrow alone; `'x` on a referenced type,
// as `Foo<'x>` or `'x Point`, resolves as an unsupported feature and interns nothing. When
// that form gains support, add it here and to addLifetime.
type lifetimeUseCollector struct {
	ast.DefaultVisitor
	uses []*ast.LifetimeAnn
}

func (v *lifetimeUseCollector) EnterTypeAnn(t ast.TypeAnn) bool {
	switch n := t.(type) {
	case *ast.RefTypeAnn:
		v.addLifetime(n.Lifetime)
		return true
	case *ast.FuncTypeAnn:
		// A nested function type is its own quantifier scope. Its inner borrows are not
		// uses of this signature's clause, and its own pass checks them.
		return false
	default:
		return true
	}
}

// addLifetime records a borrow's lifetime as a use. A nil node is a bare `&` inferred
// borrow that carries no name, so it is skipped. `'static` is the outlives bottom and is
// never undeclared, so it is skipped too. A `('a | 'b)` union records each member.
func (v *lifetimeUseCollector) addLifetime(node ast.LifetimeAnnNode) {
	switch n := node.(type) {
	case *ast.LifetimeAnn:
		if n.Name != "static" {
			v.uses = append(v.uses, n)
		}
	case *ast.LifetimeUnionAnn:
		for _, m := range n.Lifetimes {
			if m.Name != "static" {
				v.uses = append(v.uses, m)
			}
		}
	}
}

// nearestLifetimes returns the declared siblings closest to name by Levenshtein edit
// distance, for an undeclared-lifetime typo suggestion. Only siblings within maxSuggest
// edits qualify, and among those only the minimum-distance ones are returned, in the
// siblings' first-appearance order. An empty result means no sibling is close enough to
// suggest. Lifetime names are short, often a single letter, so a small threshold keeps a
// `'c` typo pointing at `'a` or `'b` without suggesting an unrelated `'lifetime`.
func nearestLifetimes(name string, siblings []string) []string {
	const maxSuggest = 2
	best := maxSuggest + 1
	var out []string
	for _, s := range siblings {
		switch d := levenshtein(name, s); {
		case d < best:
			best = d
			out = []string{s}
		case d == best:
			out = append(out, s)
		}
	}
	if best > maxSuggest {
		return nil
	}
	return out
}

// levenshtein returns the edit distance between a and b, the least number of single-
// character insertions, deletions, or substitutions that turns one into the other. It
// runs the standard two-row dynamic program, keeping only the previous and current rows.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}
