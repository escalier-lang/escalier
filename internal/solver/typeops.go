package solver

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// maxExpandDepth caps how many times an alias may expand along one reduction path. The
// active-state guard already stops a regular recursive alias, whose instantiation state repeats.
// This budget is the backstop for an alias whose state never repeats, which checkProductive lets
// through whenever each lap emits structure. `type Deep<T> = {a: Deep<{b: T}>}` is that shape. It is
// productive, so the check accepts it, and its payload grows every lap, so a reduction that walks
// into it never meets a state it has already seen. No finite analytical bound exists for that
// fragment, so the budget stops the walk and the operator over the unexpanded alias stays symbolic.
const maxExpandDepth = 200

// maxExpandKeyChars caps the total cost of alias expansion across a whole reduction, counted in the
// length of the rendered instantiation keys. What it bounds that maxExpandDepth cannot is expansion
// that is wide rather than deep, and that involves no recursion at all:
//
//	type A0<T> = {a: T}
//	type A1<T> = {...A0<T>, ...A0<T>}
//	type A2<T> = {...A1<T>, ...A1<T>}
//	…
//
// Grounding `A40<number>` expands two references per lap over forty laps, so a full walk is 2^41
// expansions. No alias here recurses at all, so checkProductive accepts them all, and the reduction
// path is only forty deep, so maxExpandDepth never binds. maxExpandDepth is
// also restored when a branch finishes, which is right for a sequential walk but leaves each of the
// two sibling references restarting from the full remaining depth.
//
// This budget is monotonic instead. It is never restored, so every sibling in that tree spends one
// shared pool, and a key is never empty, so the pool also caps how many aliases a reduction expands
// in total. The 2^41 walk above stops after roughly ten thousand expansions.
//
// The budget is read before a key is rendered, so an exhausted budget costs nothing further. The
// reference that exhausts it still pays its own render, which overshoots by that key's length.
//
// The value is a backstop, not a derived maximum. The largest spend across the test suite by a
// reduction that is not deliberately divergent is under 500 characters, so it leaves two orders of
// magnitude of headroom.
//
// An exhausted budget leaves the operator over the unexpanded alias symbolic, the same outcome as
// exhausting maxExpandDepth. The reference keeps the arguments it was built with.
const maxExpandKeyChars = 100_000

// maxTemplateLitCombinations caps how many string literals a template literal may reduce to. Its
// cartesian product over interpolated unions grows multiplicatively, so `${A}${B}${C}` over three
// large unions could enumerate an unbounded union. The cap rejects such a template with a
// diagnostic rather than materializing the product.
const maxTemplateLitCombinations = 10_000

// typeEvaluator reduces a residual type-level operator to its value. It reduces `keyof T`, indexed
// access `T[K]`, the conditional `if C : E { … } else { … }`, the mapped type
// `{[K]: V for K in Keys}`, template literals, and the intrinsic string operators such as
// `Uppercase<T>`; later operators join as they land. Only constrain invokes it, to check a
// constraint against a residual. Annotation and display keep the residual symbolic, so a stored type
// prints `keyof {x: number}` or `Point["x"]` the way the source wrote it, never the reduced value.
//
// reduce projects the operand's keys: a ground `keyof {x: number}` yields `"x"`, and an alias or
// class operand expands to the referenced type's keys, the transparent-but-named treatment an
// alias itself gets under constrain. A `keyof T` over a type parameter has no ground key set, so
// it stays the symbolic KeyofType.
//
// An alias reached through an operand is made safe by a four-part termination strategy:
//
//   - checkProductive rejects an alias whose recursion emits no structure and marks its AliasDef
//     NotProductive. Such an alias names no type, so the evaluator declines to expand it at all
//     rather than unfolding a definition already reported as an error. See
//     internal/solver/productivity.go.
//   - active holds the alias instantiations currently being expanded, each keyed by the alias
//     name together with its rendered arguments. When one recurs with the identical key, the
//     evaluator leaves that reference as the unexpanded alias node rather than expanding it again.
//     A recursive alias such as `type List<T> = {head: T, tail?: List<T>}` therefore reduces to a
//     finite type whose recursive position points back to the alias instead of unfolding forever.
//   - depth caps expansions along one path, the backstop for a productive recursion whose
//     instantiation state never repeats, so the active-state guard never fires.
//   - keyChars caps the total cost of expansion across the whole reduction, so sibling branches
//     that each restart from the full depth cannot turn that per-path cap into an exponential
//     walk. See maxExpandKeyChars.
//
// The evaluator mutates no solver state — no bound or variable is touched. It accumulates
// reduction diagnostics on errs, but a fresh evaluator is minted per reduction, so nothing
// leaks across calls. It builds its result unions through newUnion with a nil Context so
// newUnion's subsumption never calls constrain — which reduces residuals through this evaluator
// and would otherwise re-enter it and loop.
type typeEvaluator struct {
	// ctx is the alias environment, used to expand an alias operand and project a class body so a
	// reduction reaches the referenced type's keys. constrain and the test expander both supply a
	// non-nil Context; reduce is never invoked without one.
	ctx    *Context
	active set.Set[string]
	depth  int
	// keyChars is the expansion budget left for the whole reduction, counted in the length of the
	// instantiation keys rendered so far. depth is restored when a branch finishes; keyChars is
	// not, so siblings spend one shared pool. See maxExpandKeyChars.
	keyChars int
	// seen is the enclosing constraint's cycle-detection set, carried in so a conditional's
	// `Check <: Extends` probe shares the caller's alias-cycle guard. A conditional reduces by
	// re-entering constrain to decide its branch, and constrain expands an alias operand and
	// re-reduces the conditional in its body, so a self-referential alias such as
	// `type Bad = if Bad : number { number } else { string }` would recurse without bound if the
	// probe started a fresh set. Reusing the caller's set closes that cycle the same way two
	// structurally-equal instances of a recursive alias close through constrain's seen-set. The
	// value solve seeds it fresh at each constraint site, and the test expander passes an empty set.
	seen *seenPairs
	// errs collects the diagnostics a reduction produces. `keyof` reduction is total and adds
	// none; indexed-access reduction records an UnknownObjectKeyError or a
	// TupleIndexOutOfRangeError when a ground access resolves to no member. constrain reads
	// these after reducing an operator it is checking a constraint against, so a malformed
	// `{x: number}["z"]` surfaces at the constraint site. It records diagnostics, not solver
	// state, so no bound or variable is mutated.
	errs []SolverError
}

func newTypeEvaluator(ctx *Context, seen *seenPairs) *typeEvaluator {
	return &typeEvaluator{
		ctx:      ctx,
		active:   set.NewSet[string](),
		depth:    maxExpandDepth,
		keyChars: maxExpandKeyChars,
		seen:     seen,
	}
}

// reduce reduces one type-level operator node to its value, returning any other type
// unchanged. A node whose operand is not yet ground reduces to the same operator rebuilt
// around the expanded operand, so it stays symbolic and reduces later once the operand
// grounds.
func (e *typeEvaluator) reduce(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.KeyofType:
		return e.reduceKeyof(t.Operand, t.Inexact)
	case *soltype.IndexType:
		return e.reduceIndex(t.Target, t.Index, t.Inexact)
	case *soltype.TypeofType:
		// A `typeof x` query reduces to the value's resolved type. constrain unwraps it directly
		// in its pre-switch, so this arm serves a `typeof` reached through another operator.
		return t.Ty
	case *soltype.CondType:
		return e.reduceCond(t)
	case *soltype.TupleType:
		return e.reduceTuple(t)
	case *soltype.ObjectType:
		return e.reduceObject(t)
	case *soltype.TemplateLitType:
		return e.reduceTemplateLit(t)
	case *soltype.StringIntrinsicType:
		return e.reduceStringIntrinsic(t.Kind, t.Operand)
	case *soltype.ExactnessType:
		return e.reduceExactness(t.Kind, t.Operand)
	default:
		return t
	}
}

// reduceCond reduces `if Check : Extends { Then } else { Else }` by deciding `Check <: Extends`
// and selecting a branch, mirroring the old checker's CondType case
// (internal/checker/expand_type.go), which unifies Check with Extends and returns Then on success
// or Else on failure. The decision runs only when both operands are ground:
//
//   - a ground Check and Extends decide the branch with an assignability probe, and the reduction
//     continues into the selected branch alone, so an error in the unselected branch never surfaces;
//   - a Check or Extends still carrying a type parameter or an unreduced residual keeps the whole
//     conditional symbolic, rebuilt around the reduced Check and Extends, and reduces later once
//     they ground. Then and Else stay unreduced in the symbolic form, since neither is selected yet.
//
// A conditional written over a naked type parameter distributes: a Check that grounds to a union is
// decided one member at a time and the branch results union, so `type Wrap<T> = if T : string { [T] }
// else { boolean }` reduces `Wrap<"a" | 1>` to `["a"] | boolean`. An Extends carrying an `infer U`
// binder routes through reduceCondInfer, which infers a capture for each binder from the same
// subtype check that decides the branch.
func (e *typeEvaluator) reduceCond(t *soltype.CondType) soltype.Type {
	check := e.reduce(t.Check)
	extends := e.reduce(t.Extends)
	// An Extends operand holds its `infer` binders by design, so only the Check is tested for one. A
	// binder reaches the Check position only when an enclosing conditional left its Then branch
	// unsubstituted. That position stands for a type no match has chosen yet, so the Check is not
	// ground and the conditional stays symbolic.
	if !condOperandGround(check) || containsInfer(check) || !condOperandGround(extends) {
		return &soltype.CondType{Check: check, Extends: extends, Then: t.Then, Else: t.Else, Distribute: t.Distribute}
	}
	if union, ok := check.(*soltype.UnionType); ok && t.Distribute {
		return e.distributeCond(t, union)
	}
	if containsInfer(extends) {
		return e.reduceCondInfer(t, check, extends)
	}
	if e.ctx.condExtends(check, extends, e.seen) {
		return e.reduce(t.Then)
	}
	return e.reduce(t.Else)
}

// distributeCond decides a distributive conditional one union member at a time and unions the
// branches each member selects, so `type Wrap<T> = if T : string { [T] } else { boolean }` reduces
// `Wrap<"a" | 1>` to `["a"] | boolean` rather than to the single branch the whole union selects.
//
// Every position that named the distributed type parameter reads it as the member, the Extends
// operand included, so each lap is the conditional the alias would have produced had it been
// instantiated with that member alone. `type X<T> = if T : [T] { "wrap" } else { "no" }` over
// `[string] | string` therefore tests `[string]` against `[[string]]`, which fails, and reduces to
// `"no"`, matching TypeScript. Expanding the alias installed one shared pointer at every occurrence
// of the parameter, so replacing that pointer reaches exactly the positions it stood at, and a
// branch naming it sees the member too — `Wrap<"a" | "b">` reduces to `["a"] | ["b"]`.
//
// The pointer replaced is the Check as stored, and each rebuilt operand is taken from the stored
// conditional rather than from a reduced copy. Reduction may reallocate the nodes it walks, so
// pointer identity holds only against the operands the alias expansion produced. reduceCond reduces
// the rebuilt operands itself.
//
// Each member reduces through a copy of the conditional with Distribute cleared: the member is no
// longer a union, and clearing it states that this pass already applied the rule.
//
// An inexact Check union names only some of its members. The rest sit in a tail of unknown type.
// `unknown : Extends` is undecidable for every Extends other than `unknown` itself, so which branch
// those members select cannot be worked out. The result union keeps the tail, which stands for
// whatever those undecided members contribute. Over `"a" | "b" | ...`, the alias
// `type Wrap<T> = if T : string { "yes" } else { "no" }` therefore reduces to `"yes" | ...`
// (exact-types §7.4.3).
func (e *typeEvaluator) distributeCond(t *soltype.CondType, check *soltype.UnionType) soltype.Type {
	parts := make([]soltype.Type, len(check.Types))
	for i, member := range check.Types {
		parts[i] = e.reduceCond(&soltype.CondType{
			Check:   member,
			Extends: substituteOccurrences(t.Extends, t.Check, member),
			Then:    substituteOccurrences(t.Then, t.Check, member),
			Else:    substituteOccurrences(t.Else, t.Check, member),
		})
	}
	return newUnion(nil, parts, check.Inexact)
}

// substituteOccurrences rewrites every occurrence of the from type inside in to the to type,
// matching on pointer identity. Expanding a generic alias substitutes one shared pointer for each
// occurrence of a type parameter, so identity picks out the positions that parameter stood at
// without touching an equal type the body wrote itself.
func substituteOccurrences(in, from, to soltype.Type) soltype.Type {
	return in.Accept(&occurrenceSubst{from: from, to: to}, soltype.Positive)
}

// occurrenceSubst is the rewriting visitor behind substituteOccurrences.
type occurrenceSubst struct{ from, to soltype.Type }

func (s *occurrenceSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if t == s.from {
		return soltype.EnterResult{Type: s.to, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *occurrenceSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// reduceCondInfer decides a conditional whose Extends operand declares `infer` captures, such as
// `if T : [infer U] { U } else { boolean }`. One subtype check does both jobs:
//
//  1. Each declaration is replaced by a fresh inference variable, turning the pattern into an
//     ordinary type. `[infer U]` becomes `[t7]`.
//  2. `Check <: pattern` runs under a probe. Its result decides the branch, and the type each
//     variable was inferred to along the way is the capture for that declaration.
//  3. On success the captures are substituted into the Then branch, so a reference to a captured
//     name reduces to the type the constraint inferred for it.
//
// Letting constrain infer the captures is what keeps this total over the type set. A structural
// matcher would need an arm per container kind and would silently take the Else branch for a kind it
// had no arm for; constrain already decomposes every kind, so a pattern position it can align, it
// can capture from. Union and intersection patterns work for the same reason.
//
// The probe is discarded, so the variables and every bound recorded against them are gone by the
// time this returns and the evaluator's no-mutation invariant holds. A failed constraint selects
// Else, which covers a shape mismatch and a pattern whose written positions reject the Check alike.
func (e *typeEvaluator) reduceCondInfer(t *soltype.CondType, check, extends soltype.Type) soltype.Type {
	decls := inferDeclIDs(extends)
	vars := make([]*soltype.TypeVarType, len(decls))
	holes := make(map[int]soltype.Type, len(decls))
	for i, id := range decls {
		// Level 0 matches the ground Check, so constrain records against these variables directly
		// rather than extruding. They never outlive the probe, so no generalization sees them.
		vars[i] = e.ctx.freshVar(0)
		holes[id] = vars[i]
	}
	captured, ok := e.ctx.trialCaptures(check, substituteInfer(extends, holes), vars, e.seen.Clone())
	if !ok {
		return e.reduce(t.Else)
	}
	captures := make(map[int]soltype.Type, len(decls))
	for i, id := range decls {
		captures[id] = captured[i]
	}
	return e.reduce(substituteInfer(t.Then, captures))
}

// inferDeclIDs returns the ids of the `infer` declarations t holds, in first-appearance order with
// duplicates collapsed, so a name written at two positions in one pattern yields one id and takes
// one variable. The order is the order trialCaptures reports its results in.
func inferDeclIDs(t soltype.Type) []int {
	f := &inferDeclFinder{seen: set.NewSet[int]()}
	t.Accept(f, soltype.Positive)
	return f.ids
}

// inferDeclFinder is the walking visitor behind inferDeclIDs.
type inferDeclFinder struct {
	seen set.Set[int]
	ids  []int
}

func (f *inferDeclFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if iv, ok := t.(*soltype.InferType); ok && !f.seen.Contains(iv.ID) {
		f.seen.Add(iv.ID)
		f.ids = append(f.ids, iv.ID)
	}
	return soltype.EnterResult{}
}

func (f *inferDeclFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type { return t }

// substituteInfer rewrites every `infer` node whose declaration captures holds a type for to that
// type, covering the clause that declares the name and the branch references that read it alike,
// since both carry the declaration's id. A nested conditional writing the same name declares its own
// id, so its clause and references are left for that conditional's own match to fill — shadowing
// needs no special handling here. A declaration with no capture is left in place, which is how the
// caller detects a match that filled only part of the pattern.
func substituteInfer(t soltype.Type, captures map[int]soltype.Type) soltype.Type {
	if len(captures) == 0 {
		return t
	}
	return t.Accept(&inferSubst{captures: captures}, soltype.Positive)
}

// inferSubst is the rewriting visitor behind substituteInfer. It replaces a captured `infer` node
// with the matched type and skips that node's children, since the replacement is already reduced.
type inferSubst struct{ captures map[int]soltype.Type }

func (s *inferSubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if iv, ok := t.(*soltype.InferType); ok {
		if matched, found := s.captures[iv.ID]; found {
			return soltype.EnterResult{Type: matched, SkipChildren: true}
		}
	}
	return soltype.EnterResult{}
}

func (s *inferSubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// containsInfer reports whether t holds an `infer` node with no capture substituted into it yet. A
// conditional consults it on its Extends operand to route to the capturing reduction, and on a
// pattern to reject a match that left a binder uncaptured.
func containsInfer(t soltype.Type) bool {
	f := &inferFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// inferFinder is the walking visitor behind containsInfer. It flags the first `infer` node it
// reaches and skips that node's children, since one occurrence is enough.
type inferFinder struct{ found bool }

func (f *inferFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if _, ok := t.(*soltype.InferType); ok {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *inferFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type { return t }

// condExtends decides a conditional's `Check <: Extends` test with an assignability probe. The
// trial runs under a discard-only probe, so a speculative match records no bound and leaves no
// solver state behind, preserving the evaluator's no-mutation invariant. It runs constrain over a
// clone of the caller's cycle-detection set, so a recursive alias reached through the probe closes
// through the same seen-set the caller built up, while the clone keeps the probe's own keys out of
// the caller's set. An empty result means the subtype check held, selecting the Then branch.
func (c *Context) condExtends(check, extends soltype.Type, seen *seenPairs) bool {
	return !hasHardError(c.trialUnderProbeSeen(check, extends, seen.Clone()))
}

// condOperandGround reports whether a conditional's Check or Extends operand is ground enough to
// decide the branch. An operand carrying a type variable, a skolem, or an unreduced residual
// operator is abstract, so the `Check <: Extends` probe cannot decide a branch over it and the
// conditional stays symbolic. containsFreeVar catches the variable and skolem cases;
// containsResidualOp catches a residual such as a `keyof T` the reduction left symbolic.
// indexSignatureFor returns the object's index signature that describes the given key, and whether
// one does. An object may carry several over different key sets, so the match is by assignability:
// the first signature whose key set accepts the key wins, using the same probe a conditional's
// branch selection uses. `{[K: string]?: number, [J: number]?: boolean}` therefore reads `"a"`
// through the string signature and `0` through the number one.
//
// A key that no signature accepts reports false, which the caller turns into its own diagnostic.
func (c *Context) indexSignatureFor(obj *soltype.ObjectType, key soltype.Type, seen *seenPairs) (*soltype.MappedElem, bool) {
	for _, sig := range obj.IndexSignatures() {
		if c.condExtends(key, sig.Keys, seen) {
			return sig, true
		}
	}
	return nil, false
}

// strLitKey builds the string-literal type that names a property, so a lookup keyed by a property
// name can be probed against an index signature's key set the same way a written key is.
func strLitKey(name string) soltype.Type {
	return &soltype.LitType{Lit: &soltype.StrLit{Value: name}}
}

func condOperandGround(t soltype.Type) bool {
	return !containsFreeVar(t) && !containsResidualOp(t)
}

// reduceTuple splices each `...P` spread element whose operand grounds to a concrete tuple into
// position, reducing `[...P, x]` to a plain tuple. A tuple with no spread element returns unchanged
// so a plain tuple keeps its pointer. The splice mirrors the M4 literal case in inferTuple:
//
//   - a non-spread element carries through, reduced in case it holds a nested operator;
//   - a spread whose operand grounds to an exact tuple contributes that tuple's elements;
//   - an inexact operand splices only as the last element, where its known prefix extends the
//     result and its open tail makes the result inexact too;
//   - a spread whose operand stays abstract — a type parameter, an alias the guard leaves
//     unexpanded, or an inexact operand in a non-last position — is kept as a `...P` element around
//     the reduced operand, so the tuple stays inert and reduces later once the operand grounds.
func (e *typeEvaluator) reduceTuple(t *soltype.TupleType) soltype.Type {
	if !hasRestSpread(t.Elems) {
		return t
	}
	elems := make([]soltype.Type, 0, len(t.Elems))
	inexact := t.Inexact
	for i, el := range t.Elems {
		rest, ok := el.(*soltype.RestSpreadType)
		if !ok {
			elems = append(elems, e.reduce(el))
			continue
		}
		operand := e.groundOperand(rest.Operand)
		tup, ok := operand.(*soltype.TupleType)
		last := i == len(t.Elems)-1
		if !ok || hasRestSpread(tup.Elems) || (tup.Inexact && !last) {
			// The operand is not a fully-ground tuple, or is inexact in a non-last position: keep
			// the spread residual around the reduced operand rather than splicing.
			elems = append(elems, &soltype.RestSpreadType{Operand: operand})
			continue
		}
		elems = append(elems, tup.Elems...)
		if tup.Inexact {
			inexact = true
		}
	}
	return &soltype.TupleType{Elems: elems, Inexact: inexact}
}

// groundTuple reduces a tuple's `...P` spreads and reports the concrete tuple when none remain. A
// tuple with no spread returns unchanged with ok=true. A tuple whose spread never grounds returns
// ok=false, so `keyof`/indexed access over it stays symbolic rather than projecting the spread
// element as if it were a single position.
func (e *typeEvaluator) groundTuple(t *soltype.TupleType) (*soltype.TupleType, bool) {
	if !hasRestSpread(t.Elems) {
		return t, true
	}
	reduced, ok := e.reduceTuple(t).(*soltype.TupleType)
	if !ok || hasRestSpread(reduced.Elems) {
		return nil, false
	}
	return reduced, true
}

// groundOperand reduces an operator's operand toward a concrete type. It reduces any nested
// operator, then expands a named alias to its body under the shared termination guard, so a spread
// operand `Pair` over `type Pair = [number, string]` grounds to the referenced tuple and a template
// interpolation `Dir` over `type Dir = "a" | "b"` grounds to the referenced union. A mapped type's
// key set grounds the same way, so `for K in Names` over `type Names = "a" | "b"` reaches the union.
// A type parameter, a recurring alias state, an exhausted budget, or an unresolved alias body each
// leaves the operand unexpanded, which keeps the enclosing operator symbolic. The tuple-spread,
// template-literal, string-intrinsic, and mapped-type reductions share it.
func (e *typeEvaluator) groundOperand(operand soltype.Type) soltype.Type {
	reduced := e.reduce(operand)
	alias, ok := reduced.(*soltype.AliasType)
	if !ok {
		return reduced
	}
	return e.expandAliasGuarded(alias, aliasItself, func(body soltype.Type) soltype.Type {
		return e.groundOperand(body)
	})
}

// reduceObject merges a spread-carrying object's elements left to right into a plain ObjectType,
// following Flow's spread semantics. An object with no `...A` spread element returns unchanged, so a
// plain object keeps its pointer. Each element contributes its fields: a property as its own
// one-field group, a `...A` spread as the fields of the object its operand grounds to. A later
// field wins over an earlier key, except that a later optional field unions with the earlier value
// rather than overriding it, see mergeSpreadElem.
//
// Per element it mirrors reduceTuple: a non-spread member has its value types reduced in place, and
// a spread's operand is reduced and expanded toward a concrete object through groundObjectOperand,
// under the termination guard reduceKeyofAlias uses.
//
// Unlike reduceTuple, the merge is all-or-nothing. If any spread operand stays abstract, such as a
// type parameter or a truncated expanding alias, the whole object stays symbolic and carries every
// element in its reduced form, so it merges once the operand grounds. reduceTuple can finalize each
// ground spread independently, because tuple splice is positional concatenation and a later element
// never disturbs an earlier position. An object spread instead overrides earlier keys, so a field is
// safe to finalize only when no un-ground spread follows it. reduceObject keeps the whole object
// symbolic rather than finalizing just that safe suffix: it is simpler and renders the way the
// source wrote it, at the cost of re-reducing from scratch each pass.
func (e *typeEvaluator) reduceObject(t *soltype.ObjectType) soltype.Type {
	if !soltype.HasResidualElem(t.Elems) {
		return t
	}
	reducedElems := make([]soltype.ObjTypeElem, len(t.Elems))
	operandElems := make([][]soltype.ObjTypeElem, 0, len(t.Elems))
	inexact := t.Inexact
	ground := true
	for i, el := range t.Elems {
		switch el := el.(type) {
		case *soltype.SpreadElem:
			operand := e.groundObjectOperand(el.Type)
			reducedElems[i] = &soltype.SpreadElem{Type: operand}
			obj, ok := operand.(*soltype.ObjectType)
			if !ok || soltype.HasResidualElem(obj.Elems) {
				ground = false
				continue
			}
			operandElems = append(operandElems, obj.Elems)
			inexact = inexact || obj.Inexact
		case *soltype.MappedElem:
			// A mapped member contributes the fields it computes as one group, the same shape a
			// spread's operand object contributes, so both merge under one rule in source order.
			reduced, fields, inexactKeys, ok := e.expandMapped(el)
			reducedElems[i] = reduced
			if !ok {
				ground = false
				continue
			}
			operandElems = append(operandElems, fields)
			inexact = inexact || inexactKeys
		default:
			// An ordinary member has its value types reduced, then contributes as a one-field group.
			re := e.reduceElem(el)
			reducedElems[i] = re
			operandElems = append(operandElems, []soltype.ObjTypeElem{re})
		}
	}
	if !ground {
		return &soltype.ObjectType{Elems: reducedElems, Inexact: inexact}
	}
	return &soltype.ObjectType{Elems: mergeSpreadOperands(operandElems), Inexact: inexact}
}

// reduceElem reduces the value types inside one non-spread object member, the object analogue of the
// `e.reduce(el)` reduceTuple runs on each non-spread tuple element. A property, getter, or setter
// carries a value type that may itself be an operator such as `keyof X`, so its type is reduced. A
// method or constructor carries only function types, which reduce leaves untouched, so it passes
// through unchanged.
func (e *typeEvaluator) reduceElem(el soltype.ObjTypeElem) soltype.ObjTypeElem {
	switch el := el.(type) {
	case *soltype.PropertyElem:
		return &soltype.PropertyElem{Name: el.Name, Type: e.reduce(el.Type), Optional: el.Optional, Readonly: el.Readonly}
	case *soltype.GetterElem:
		return &soltype.GetterElem{Name: el.Name, SelfParam: el.SelfParam, Type: e.reduce(el.Type)}
	case *soltype.SetterElem:
		return &soltype.SetterElem{Name: el.Name, SelfParam: el.SelfParam, Param: e.reduce(el.Param)}
	default:
		return el
	}
}

// groundObjectOperand reduces a `...A` spread operand toward the concrete object whose fields it
// contributes, the object analogue of groundOperand. It resolves a `typeof` query to the value's
// type, projects a class instance body, and expands an alias to its body under the termination
// guard reduceKeyofAlias uses, so a recursive alias reached through a spread terminates. Any other
// kind — an object, a type variable, a nested operator — is reduced. It returns the reduced
// operand rather than an ok flag, so a caller keeps that reduced form when the operand does not
// ground, matching how reduceTuple keeps the operand it could not splice.
func (e *typeEvaluator) groundObjectOperand(operand soltype.Type) soltype.Type {
	switch op := operand.(type) {
	case *soltype.TypeofType:
		return e.groundObjectOperand(op.Ty)
	case *soltype.ClassType:
		if obj, ok := e.ctx.projectClassBody(op); ok {
			return obj
		}
		return op
	case *soltype.AliasType:
		return e.expandAliasGuarded(op, aliasItself, func(body soltype.Type) soltype.Type {
			return e.groundObjectOperand(body)
		})
	default:
		return e.reduce(operand)
	}
}

// groundToObject reports the concrete ObjectType a spread operand contributes, or ok=false when the
// operand has no ground object shape — a type variable, a truncated expanding alias, or an object
// that still carries a spread. It is the ok-returning wrapper over groundObjectOperand that keyof
// and indexed access use to decide whether a spread target has grounded.
func (e *typeEvaluator) groundToObject(operand soltype.Type) (*soltype.ObjectType, bool) {
	obj, ok := e.groundObjectOperand(operand).(*soltype.ObjectType)
	if !ok || soltype.HasObjectSpread(obj.Elems) {
		return nil, false
	}
	return obj, true
}

// mergeSpreadOperands folds the field lists of an object spread's operands into one element list,
// preserving first-appearance order. A field whose name is new is appended; one that overlaps an
// earlier key is merged through mergeSpreadElem, so the operands compose left to right. It is
// shared by the type-level reduction and the literal-level inferObject, so the spread merge rule
// lives in one place.
func mergeSpreadOperands(operandElems [][]soltype.ObjTypeElem) []soltype.ObjTypeElem {
	total := 0
	for _, elems := range operandElems {
		total += len(elems)
	}
	out := make([]soltype.ObjTypeElem, 0, total)
	pos := make(map[string]int, total)
	for _, elems := range operandElems {
		for _, elem := range elems {
			name := soltype.ObjElemName(elem)
			if i, seen := pos[name]; seen {
				out[i] = mergeSpreadElem(out[i], elem)
				continue
			}
			pos[name] = len(out)
			out = append(out, elem)
		}
	}
	return out
}

// mergeSpreadElem combines an earlier object member with a later one of the same name under Flow's
// spread rule. A later required field overrides the earlier one, the rightmost-wins default. A
// later optional field instead shows the earlier value through: the merged value unions
// `earlier | later`, and the field stays required unless both are optional. `{...A, ...B}` with
// `A = {k: number}` and `B = {k?: string}` therefore yields `k: number | string`, required. A
// non-property member on either side has no optional flag to key the union off, so the later one
// overrides.
//
// The later operand supplies the merged Readonly flag in both branches, the same rightmost-wins
// source the override branch uses when it returns the later member whole.
func mergeSpreadElem(earlier, later soltype.ObjTypeElem) soltype.ObjTypeElem {
	ep, eok := earlier.(*soltype.PropertyElem)
	lp, lok := later.(*soltype.PropertyElem)
	if !eok || !lok || !lp.Optional {
		return later
	}
	return &soltype.PropertyElem{
		Name:     ep.Name,
		Type:     newUnion(nil, []soltype.Type{ep.Type, lp.Type}, false),
		Optional: ep.Optional && lp.Optional,
		Readonly: lp.Readonly,
	}
}

// expandMapped computes the fields a `[K]: V for K in Keys` member contributes to its object,
// mirroring the old checker's expandMappedElems (internal/checker/expand_type.go). It emits one
// field per key of the Keys union, each built by mappedFields with that key substituted for K:
//
//	{[K]: T[K] for K in keyof T}   with T = {x: number, y: string}
//	⇒ {x: number, y: string}
//
// reduceObject merges the returned fields with the member's siblings in source order, so a mapped
// member composes with ordinary members and with `...A` spreads in one object:
//
//	{id: number, [K]: string for K in Keys}   with Keys = "a" | "b"
//	⇒ {id: number, a: string, b: string}
//
// TypeScript has no such form and writes that as the intersection `{id: number} & {[K in Keys]: string}`.
//
// ok=false leaves the member unexpanded and its object symbolic, carrying the reduced key set so a
// later pass resumes from it. That happens when Keys does not ground to a union of string-literal
// keys, which is what a `keyof` over an object reduces to, or when a key reaches no field
// mappedFields can build. A Keys operand that is a type parameter or an unreduced operator keeps the
// member inert, so `{[K]: T[K] for K in keyof T}` over an abstract T renders the way the source
// wrote it and expands later once T grounds.
//
// An uncountable key set leaves the member unexpanded, and that unexpanded member is the index
// signature. `{[K: string]?: T}` names infinitely many keys, so there is no field list to expand it
// into. constrain and member access read the member where it sits instead. The required form over
// such a key set demands a field at every one of infinitely many keys, which no object has, so it
// reports a RequiredUncountableKeysError.
//
// Two keys that remap to one name merge into a single field whose type is their union, so no key's
// contribution is lost. See mergeMappedField.
//
// inexactKeys reports that the key union was itself inexact, as `keyof {a: number, ...}` is. The
// caller folds it into the object's inexact marker, so the fields for the known keys are listed and
// the object stays inexact for the rest. A filter narrows which of the known keys survive but cannot
// rule out the unlisted ones, so the result stays inexact there too.
func (e *typeEvaluator) expandMapped(t *soltype.MappedElem) (reduced *soltype.MappedElem, fields []soltype.ObjTypeElem, inexactKeys bool, ok bool) {
	keys := e.groundOperand(t.Keys)
	// The unexpanded form keeps the reduced key set, so a later pass resumes from the ground it
	// gained rather than re-expanding the operand from scratch.
	reduced = &soltype.MappedElem{
		Key: t.Key, Keys: keys, Value: t.Value, Name: t.Name, Check: t.Check, Extends: t.Extends,
		Optional: t.Optional, Readonly: t.Readonly,
	}
	if !condOperandGround(keys) {
		return reduced, nil, false, false
	}
	if soltype.UncountableKeys(keys) {
		// An uncountable key set has no keys to enumerate, so the member stays unexpanded and is
		// itself the index signature. The required form over such a key set is uninhabited and is
		// rejected. A rename or filter over one has no enumerable keys to run over, so it stays
		// symbolic with no diagnostic. That gap is #930.
		if soltype.IsIndexSignature(reduced) && reduced.Optional != soltype.ModAdd {
			e.errs = append(e.errs, &RequiredUncountableKeysError{Mapped: reduced})
		}
		return reduced, nil, false, false
	}
	source, homomorphic := e.homomorphicSource(t.Keys)
	members, inexactKeys := mappedKeyMembers(keys)
	pos := make(map[string]int, len(members))
	for _, member := range members {
		built, ok := e.mappedFields(t, member, source, homomorphic)
		if !ok {
			return reduced, nil, false, false
		}
		for _, field := range built {
			if i, dup := pos[field.Name]; dup {
				fields[i] = mergeMappedField(soltype.AsProperty(fields[i]), field)
				continue
			}
			pos[field.Name] = len(fields)
			fields = append(fields, field)
		}
	}
	return reduced, fields, inexactKeys, true
}

// mappedKeyMembers splits a mapped type's reduced Keys operand into the individual keys to emit a
// field for, and reports whether the key set is inexact. A union contributes its members and carries
// its own inexact marker through; `never` is the empty key set, so it contributes none; any other
// type is a single key.
func mappedKeyMembers(keys soltype.Type) ([]soltype.Type, bool) {
	switch keys := keys.(type) {
	case *soltype.UnionType:
		return keys.Types, keys.Inexact
	case *soltype.NeverType:
		return nil, false
	default:
		return []soltype.Type{keys}, false
	}
}

// mergeMappedField combines two fields a key remapping gave the same name. Their value types union,
// so reading the field yields whichever key's value the object actually holds, matching TypeScript.
// A name reachable from an optional or readonly key carries that marker, so the merged field takes
// each marker from either contributor. The union is built through newUnion with a nil Context, the
// evaluator's rule for keeping subsumption from re-entering constrain.
func mergeMappedField(earlier, later *soltype.PropertyElem) *soltype.PropertyElem {
	return &soltype.PropertyElem{
		Name:     earlier.Name,
		Type:     newUnion(nil, []soltype.Type{earlier.Type, later.Type}, false),
		Optional: earlier.Optional || later.Optional,
		Readonly: earlier.Readonly || later.Readonly,
	}
}

// mappedFields builds the fields a mapped type emits for a single key. ok=false means the key
// resolved to nothing a field could be built from, which keeps the whole mapped type symbolic. An
// empty slice with ok=true means the key is deliberately dropped, which the `if C : E` filter and a
// key remapped to `never` both do. A key remapping that yields a union of names emits one field per
// name.
//
// The steps, each reading the original key rather than a remapped one, so the filter and the
// remapping are independent and their order is not observable:
//
//  1. The key must name a field, which mappedKeyName decides. A string literal names one directly
//     and a number literal names the field its digits spell. A primitive or an unreduced operator
//     names none.
//  2. The `if Check : Extends` filter, when the source wrote one, decides `Check <: Extends` with
//     the same assignability probe a conditional's branch selection uses. A key that fails the test
//     is dropped, which is how `Omit` and `Pick` narrow a key set. Both operands are arbitrary type
//     expressions with the key substituted, so a filter may test the value at that key rather than
//     the key itself, as `if T[K] : number` does.
//  3. The key-remapping expression in the brackets, when the source wrote one, reduces to the names
//     the key contributes. `never` drops the key, matching TypeScript's `as` clause.
//  4. The value expression reduces to the field's type. It is normally an indexed access such as
//     `T[K]`, which is why mapped types build on indexed-access reduction.
//
// Each emitted field takes its `readonly` and `?` markers from mappedMarkers.
func (e *typeEvaluator) mappedFields(t *soltype.MappedElem, key soltype.Type, source *soltype.ObjectType, homomorphic bool) ([]*soltype.PropertyElem, bool) {
	name, ok := mappedKeyName(key)
	if !ok {
		return nil, false
	}
	if t.Check != nil && t.Extends != nil {
		check := e.reduce(substituteMappedKey(t.Check, t.Key, key))
		extends := e.reduce(substituteMappedKey(t.Extends, t.Key, key))
		if !condOperandGround(check) || !condOperandGround(extends) {
			return nil, false
		}
		if !e.ctx.condExtends(check, extends, e.seen) {
			return nil, true
		}
	}
	names := []string{name}
	if t.Name != nil {
		if names, ok = e.remappedNames(t, key); !ok {
			return nil, false
		}
	}
	optional, readonly := mappedMarkers(t, name, source, homomorphic)
	value := e.reduce(substituteMappedKey(t.Value, t.Key, key))
	fields := make([]*soltype.PropertyElem, len(names))
	for i, n := range names {
		fields[i] = &soltype.PropertyElem{Name: n, Type: value, Optional: optional, Readonly: readonly}
	}
	return fields, true
}

// remappedNames reduces a mapped type's key-remapping expression for one key and reports the field
// names it yields. A single string literal names one field and `never` names none, dropping the key.
// A union names one field per string-literal member, and drops any `never` member, so a remapping
// written as a conditional over a union of keys contributes each surviving name. ok=false when a
// name position reduces to something that cannot name a field, which keeps the mapped type symbolic.
func (e *typeEvaluator) remappedNames(t *soltype.MappedElem, key soltype.Type) ([]string, bool) {
	remapped := e.groundOperand(substituteMappedKey(t.Name, t.Key, key))
	members, _ := mappedKeyMembers(remapped)
	names := make([]string, 0, len(members))
	for _, member := range members {
		if _, dropped := member.(*soltype.NeverType); dropped {
			continue
		}
		name, ok := mappedKeyName(member)
		if !ok {
			return nil, false
		}
		names = append(names, name)
	}
	return names, true
}

// mappedMarkers reports the `?` and `readonly` markers one emitted field carries. A written modifier
// decides the marker outright: the adding form sets it and the removing form clears it. With no
// modifier written, a homomorphic mapped type inherits the marker from the source member the key
// names, so `{[K]: T[K] for K in keyof T}` is the identity on T and `Pick` keeps an optional
// property optional. Any other mapped type emits the field unmarked, and so does a homomorphic one
// whose source carries no property under that name, such as one mapping over a class's projected
// keys.
func mappedMarkers(t *soltype.MappedElem, key string, source *soltype.ObjectType, homomorphic bool) (optional, readonly bool) {
	optional = t.Optional == soltype.ModAdd
	readonly = t.Readonly == soltype.ModAdd
	if !homomorphic {
		return optional, readonly
	}
	prop, found := source.Prop(key)
	if !found {
		return optional, readonly
	}
	if t.Optional == soltype.ModNone {
		optional = prop.Optional
	}
	if t.Readonly == soltype.ModNone {
		readonly = prop.Readonly
	}
	return optional, readonly
}

// homomorphicSource reports the object a mapped type's emitted fields inherit their `?` and
// `readonly` markers from. A mapped type is homomorphic when its key set is written `keyof T` for
// some T, which is TypeScript's rule and the shape every marker-preserving utility type takes:
// `Partial`, `Readonly`, and `Pick` each map over `keyof T`. The operand grounds to a concrete
// object the same way a spread operand does, so an alias or class named there resolves to the object
// whose members carry the markers.
//
// Any other key set makes the mapped type non-homomorphic. `Record<Ks, V>` maps over a bare key
// union with no source object to read a marker off, so its fields are unmarked, again matching
// TypeScript.
func (e *typeEvaluator) homomorphicSource(keys soltype.Type) (*soltype.ObjectType, bool) {
	keyof, ok := keys.(*soltype.KeyofType)
	if !ok {
		return nil, false
	}
	return e.groundToObject(keyof.Operand)
}

// substituteMappedKey rewrites every reference to one mapped type's key variable to the key being
// emitted, so the value, key-remapping, and filter positions each read that key. It matches on the
// binding id the `for K in …` clause introduced, so a nested mapped type's own key, which draws a
// distinct id, is left for that mapped type's own reduction to fill.
func substituteMappedKey(in soltype.Type, key *soltype.MappedKeyType, to soltype.Type) soltype.Type {
	return in.Accept(&mappedKeySubst{id: key.ID, to: to}, soltype.Positive)
}

// mappedKeySubst is the rewriting visitor behind substituteMappedKey. It replaces a reference to the
// bound key with the key type and skips that node's children, since a key is a leaf.
type mappedKeySubst struct {
	id int
	to soltype.Type
}

func (s *mappedKeySubst) EnterType(t soltype.Type, _ soltype.Polarity) soltype.EnterResult {
	if k, ok := t.(*soltype.MappedKeyType); ok && k.ID == s.id {
		return soltype.EnterResult{Type: s.to, SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (s *mappedKeySubst) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }

// reduceKeyof reduces `keyof operand` to the union of the operand's keys, mirroring the old
// checker's KeyOfType case (internal/checker/expand_type.go):
//
//   - an object projects its property, getter, and setter names as string-literal types;
//   - a tuple yields only its own numeric indices, omitting the inherited "length"; see keyofTuple;
//   - `keyof` distributes over a union or intersection, intersecting each member's keys for a
//     union and unioning them for an intersection;
//   - `keyof` of a primitive, literal, `never`, or `unknown` is `never`, since none has
//     enumerable keys;
//   - `keyof` of a `keyof` is `never` for the same reason, since a key set holds only literals;
//   - an alias expands to its body and a class projects its instance body, and `keyof` reduces
//     over that under the termination guard;
//   - a `typeof` query resolves to the value's type, and `keyof` reduces over that.
//
// A type variable, a skolem, or a named reference the evaluator does not expand keeps the
// operator symbolic, rebuilt around the operand.
func (e *typeEvaluator) reduceKeyof(operand soltype.Type, inexact bool) soltype.Type {
	switch op := operand.(type) {
	case *soltype.KeyofType:
		// `keyof keyof X` is `never` for every X, so the inner operand needs no reduction at all.
		// A key set is a union of string literals for an object and of number literals for a
		// tuple, and the LitType arm below already reduces `keyof` over either to `never`.
		// keyofUnion intersects those, so a key set of more than one member still yields
		// `never`, and `keyof never` is `never` too. The rule holds even when the inner `keyof`
		// stays symbolic over a type parameter, since what `keyof T` projects is a key set
		// whatever T turns out to be.
		//
		// Reducing the inner operand would be wasted work and, over an expanding recursive alias
		// such as `type Grow<T> = keyof Grow<{a: T, b: T}>`, work the evaluator only escapes by
		// spending maxExpandKeyChars. The one cost is that a diagnostic only the inner reduction
		// would raise goes unreported, as in `keyof keyof {a: number}["z"]`, where the unknown key
		// is never reached. The result is `never` either way.
		return &soltype.NeverType{}
	case *soltype.IndexType, *soltype.CondType, *soltype.ExactnessType:
		// The operand is itself an operator — an indexed access, a conditional, or an
		// `Exact`/`Inexact`. Reduce it first, then take keyof its value, so a ground conditional
		// operand selects its branch and `keyof` projects that branch's keys, and a ground
		// `Exact<T>` closes T's key set. If the inner operator stays symbolic because its own
		// operands are not ground, wrap it as `keyof <inner>` rather than re-reducing the same shape
		// forever. A mapped member arrives inside an ObjectType, so the ObjectType arm below covers
		// it: grounding the object emits the member's fields and `keyof` projects their names.
		inner := e.reduce(op)
		if isResidualOp(inner) {
			return &soltype.KeyofType{Operand: inner, Inexact: inexact}
		}
		return e.reduceKeyof(inner, inexact)
	case *soltype.AliasType:
		return e.reduceKeyofAlias(op, inexact)
	case *soltype.TypeofType:
		// `keyof typeof x` resolves the query to the value's type, then projects that type's keys.
		return e.reduceKeyof(op.Ty, inexact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground key set, so `keyof` over it
		// stays symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(op); ok {
			return e.keyofObject(obj)
		}
		return &soltype.KeyofType{Operand: operand, Inexact: inexact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(op)
		if !ok {
			return &soltype.KeyofType{Operand: operand, Inexact: inexact}
		}
		return e.keyofObject(obj)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground index set, so `keyof` over it
		// stays symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(op); ok {
			return e.keyofTuple(tup)
		}
		return &soltype.KeyofType{Operand: operand, Inexact: inexact}
	case *soltype.UnionType:
		// A value of a union type is one of its members, so only a key every member carries can
		// be read from it. Its key sets therefore intersect.
		return e.keyofUnion(op, inexact)
	case *soltype.IntersectionType:
		// An intersection carries every operand's members, so its key sets union.
		return e.keyofIntersection(op.Types, inexact)
	case *soltype.PrimType, *soltype.LitType, *soltype.NeverType, *soltype.UnknownType:
		return &soltype.NeverType{}
	default:
		return &soltype.KeyofType{Operand: operand, Inexact: inexact}
	}
}

// reduceKeyofAlias reduces `keyof Alias` by expanding the alias and reducing `keyof` over its
// body under the termination guard, leaving the alias symbolic when the guard blocks expansion.
func (e *typeEvaluator) reduceKeyofAlias(op *soltype.AliasType, inexact bool) soltype.Type {
	symbolic := func(a *soltype.AliasType) soltype.Type {
		return &soltype.KeyofType{Operand: a, Inexact: inexact}
	}
	return e.expandAliasGuarded(op, symbolic, func(body soltype.Type) soltype.Type {
		return e.reduceKeyof(body, inexact)
	})
}

// expandAliasGuarded expands a named alias to its body and applies cont to the result, under the
// termination guard that makes reduction safe over a recursive alias. The alias stays on the
// active path for the whole reduction of its body, so a member that re-references it, directly
// or through a chain, sees it active and stops. Four conditions leave the alias unexpanded, so the
// operator over it stays symbolic: a definition checkProductive rejected, a recurring instantiation
// state, an exhausted budget, and an unresolved body.
//
// fallback builds that symbolic result around op, the alias reference as the source wrote it. A
// caller whose symbolic form is the bare reference returns the argument unchanged.
//
// Every reduction that expands an alias routes through here, so one guard covers `keyof`, indexed
// access, and the operand-grounding both spread forms use, and the budgets are shared across them.
func (e *typeEvaluator) expandAliasGuarded(op *soltype.AliasType, fallback func(*soltype.AliasType) soltype.Type, cont func(body soltype.Type) soltype.Type) soltype.Type {
	if e.ctx.notProductive(op) {
		// checkProductive rejected this alias at its declaration because a lap of its recursion
		// returns to it emitting nothing, so it names no type to reduce toward. Expanding even one
		// lap would hand the next lap another such state, so decline outright. The reference keeps
		// the arguments the source wrote, which is what a diagnostic names.
		return fallback(op)
	}
	if e.depth <= 0 || e.keyChars <= 0 {
		return fallback(op)
	}
	key := soltype.PrintQualified(op)
	e.keyChars -= len(key)
	if e.active.Contains(key) {
		// A repeated instantiation state is a regular recursion the evaluator represents by
		// pointing back at the alias.
		return fallback(op)
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		// expandAlias yields ErrorType for an unregistered alias, or one whose body a dep-graph
		// sibling has not filled yet. Keep the operator symbolic rather than reducing over `error`.
		return fallback(op)
	}
	e.active.Add(key)
	e.depth--
	result := cont(body)
	e.active.Remove(key)
	e.depth++
	return result
}

// aliasItself is the fallback for a caller whose symbolic form is the bare alias reference, so the
// grounding walks keep the reference unchanged when expandAliasGuarded declines to expand it.
func aliasItself(op *soltype.AliasType) soltype.Type { return op }

// keyofObject projects an object's property, getter, and setter names as string-literal types
// and unions them. An empty projection collapses to `never`, the union identity newUnion returns
// for no members.
//
// An inexact object carries an unknown-keyed tail, so its key set is open: `keyof {a: number, ...}`
// is `"a" | ...`, an inexact union whose members are the known keys and whose tail stands for the
// unlisted ones. keyofObject seeds the union's exactness from the object's, so an exact object
// yields an exact key union and an inexact object an inexact one.
//
// It omits methods, which is correct for a class instance whose methods live on the prototype
// and so are absent from Object.keys, but wrong for a bare object whose methods are own
// enumerable keys. keyofObject cannot tell the two apart from the ObjectType alone, so it
// under-approximates the bare-object case. Issue #916 tracks deciding how keyof should account
// for own vs inherited members.
func (e *typeEvaluator) keyofObject(obj *soltype.ObjectType) soltype.Type {
	keys := make([]soltype.Type, 0, len(obj.Elems))
	for _, elem := range obj.Elems {
		switch elem := elem.(type) {
		case *soltype.PropertyElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.GetterElem:
			keys = append(keys, strLitType(elem.Name))
		case *soltype.SetterElem:
			keys = append(keys, strLitType(elem.Name))
		}
	}
	return newUnion(nil, keys, obj.Inexact)
}

// keyofTuple yields a tuple's own keys: one number-literal type per positional element, the
// indices Object.keys returns. `keyof [number, string]` reduces to `0 | 1`. This deliberately
// deviates from TypeScript, whose keyof of a tuple also includes "length" and the other
// Array.prototype members. Those are inherited rather than own keys, so Escalier omits them.
// TODO: decide how keyof should account for inherited prototype members once interop is designed.
//
// An inexact tuple has unknown trailing positions, so its index set is open the same way an inexact
// object's key set is. `keyof [number, string, ...]` reduces to `0 | 1 | ...`, where the tail stands
// for the indices those unknown positions occupy (exact-types §7.1).
func (e *typeEvaluator) keyofTuple(tup *soltype.TupleType) soltype.Type {
	keys := make([]soltype.Type, 0, len(tup.Elems))
	for i := range tup.Elems {
		keys = append(keys, &soltype.LitType{Lit: &soltype.NumLit{Value: float64(i)}})
	}
	return newUnion(nil, keys, tup.Inexact)
}

// keyofUnion intersects the keys of a union operand's members. A value typed `A | B` is either
// an A or a B, so only a key both carry can be read from it. `keyof ({a: number, shared: string}
// | {b: boolean, shared: string})` reduces to `"shared"`. This is the mirror of the
// IntersectionType arm, which unions its members' keys through keyofIntersection.
//
// An inexact key set is open, so it can carry keys its written members do not name. That makes
// the result inexact whenever the operand union is inexact or any member's key set is. Take
// `keyof ({a: number, shared: string} | {b: boolean, shared: string, ...})`, which reduces to
// `"shared" | ...`. Only "shared" is written on both members, so only "shared" is definitely a
// key, but the second member's open tail may carry "a" too. Intersecting the written keys keeps
// every key the result names one that every member definitely carries, and the trailing `...`
// records that the true key set may be larger.
//
// Some members have no key set to intersect. A type parameter is one, and so is an operator
// whose own operands are not ground. Such a member can only shrink the intersection, never grow
// it, so the fold skips it and keeps going. If the members it can read intersect to nothing, the
// whole intersection is empty whatever the skipped member's keys turn out to be, and the result
// is never. Otherwise the intersection is uncomputable and the whole operator stays symbolic,
// rendering `keyof (T | {a: number})`. Skipping rather than bailing out on the first such member
// keeps the result independent of the order the operand lists its members.
func (e *typeEvaluator) keyofUnion(op *soltype.UnionType, inexact bool) soltype.Type {
	var shared []soltype.Type
	seeded := false
	unreadable := false
	sharedInexact := op.Inexact
	for _, m := range op.Types {
		keys, memberInexact, ok := literalKeys(e.reduceKeyof(m, inexact))
		if !ok {
			unreadable = true
			continue
		}
		if memberInexact {
			sharedInexact = true
		}
		if seeded {
			shared = intersectTypes(shared, keys)
		} else {
			// literalKeys hands back the reduced member's own member slice, and newUnion sorts
			// its input in place, so seed the accumulator with a copy. Every later round
			// allocates a fresh slice in intersectTypes.
			shared = append([]soltype.Type(nil), keys...)
			seeded = true
		}
		if len(shared) == 0 {
			// No later member can put a key back into an empty intersection, so stop here rather
			// than reducing the rest. This holds for an unreadable member too, which is why the
			// answer is never even when one has already been skipped.
			return &soltype.NeverType{}
		}
	}
	if unreadable {
		return &soltype.KeyofType{Operand: op, Inexact: inexact}
	}
	return newUnion(nil, shared, sharedInexact)
}

// literalKeys decomposes a reduced `keyof` result into the literal keys it names and whether its
// key set is inexact. It reports false for a result that names no enumerable key set, such as the
// `keyof T` residual over a type parameter, so a caller that needs the keys can fall back to
// leaving its own operator symbolic.
//
// `never` decomposes to an empty exact set, a lone literal to that one key, and a union to its
// members with the union's own exactness. A union carrying a non-literal member is not a key set
// the reduction produced, so it reports false rather than silently dropping that member.
func literalKeys(reduced soltype.Type) (keys []soltype.Type, inexact bool, ok bool) {
	switch t := reduced.(type) {
	case *soltype.NeverType:
		return nil, false, true
	case *soltype.LitType:
		return []soltype.Type{t}, false, true
	case *soltype.UnionType:
		for _, m := range t.Types {
			if _, isLit := m.(*soltype.LitType); !isLit {
				return nil, false, false
			}
		}
		return t.Types, t.Inexact, true
	default:
		return nil, false, false
	}
}

// intersectTypes returns the members of a that equalType-match a member of b, preserving a's
// order. Both inputs are small key sets, so the quadratic scan costs less than building a set
// keyed on a canonical form.
func intersectTypes(a, b []soltype.Type) []soltype.Type {
	both := make([]soltype.Type, 0, len(a))
	for _, x := range a {
		for _, y := range b {
			if equalType(x, y) {
				both = append(both, x)
				break
			}
		}
	}
	return both
}

// keyofIntersection unions the keys of each member of an intersection operand: `keyof (A & B)`
// reduces to `keyof A | keyof B`, since an intersection carries the members of all its operands.
//
// Exactness needs no seed here. An inexact member reduces to an inexact key union, and newUnion
// carries a member union's marker out to the union it splices that member into. So
// `keyof ({a: number} & {b: string, ...})` reduces to `"a" | "b" | ...` on its own.
func (e *typeEvaluator) keyofIntersection(members []soltype.Type, inexact bool) soltype.Type {
	parts := make([]soltype.Type, len(members))
	for i, m := range members {
		parts[i] = e.reduceKeyof(m, inexact)
	}
	return newUnion(nil, parts, false)
}

// reduceIndex reduces `target[index]` to the type stored at that key, mirroring the old
// checker's IndexType case (internal/checker/expand_type.go):
//
//   - an object indexed by a string-literal key yields that member's read type, and an unknown
//     key records an UnknownObjectKeyError;
//   - a tuple indexed by a numeric-literal key yields that element, and an out-of-range or
//     non-integer index records a TupleIndexOutOfRangeError;
//   - a union index distributes, so `T["a" | "b"]` reduces to `T["a"] | T["b"]`; `T[keyof T]`
//     rides this once `keyof T` reduces to its key union;
//   - a union target distributes the other way, so `(A | B)[K]` reduces to `A[K] | B[K]`, where
//     every member must carry K;
//   - an intersection target reduces to the meet of the value types the members that carry K
//     contribute, so `(A & B)[K]` picks K from whichever members have it;
//   - an alias expands to its body and a class projects its instance body, and the access
//     reduces over that under the termination guard;
//   - a `typeof` query resolves to the value's type, and the access reduces over that.
//
// A type-variable target or index, or any operand the evaluator does not ground, keeps the
// access symbolic, rebuilt around the reduced operands.
func (e *typeEvaluator) reduceIndex(target, index soltype.Type, inexact bool) soltype.Type {
	idx := e.reduce(index)
	// A union index distributes member-wise. `T[keyof T]` rides this once `keyof T` reduces to
	// its `"a" | "b"` key union, so the access yields the union of the members' value types.
	//
	// An inexact key union names keys the access cannot enumerate. Each key it does not name holds
	// a value the result does not list, so the result union is open too (exact-types §7.3). This is
	// what carries an inexact object's openness into `T[keyof T]`. Over `type Obj = {a: number, ...}`,
	// `keyof Obj` reduces to `"a" | ...` and `Obj[keyof Obj]` to `number | ...`.
	if u, ok := idx.(*soltype.UnionType); ok {
		parts := make([]soltype.Type, len(u.Types))
		for i, m := range u.Types {
			parts[i] = e.reduceIndex(target, m, inexact)
		}
		return newUnion(nil, parts, u.Inexact)
	}
	switch tgt := target.(type) {
	case *soltype.AliasType:
		return e.reduceIndexAlias(tgt, idx, inexact)
	case *soltype.TypeofType:
		// `(typeof x)[K]` resolves the query to the value's type, then indexes that type.
		return e.reduceIndex(tgt.Ty, idx, inexact)
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType, *soltype.ExactnessType:
		// The target is itself an operator — a `keyof`, an indexed access, a conditional, or an
		// `Exact`/`Inexact`. Reduce it first, then index its value, so a ground conditional target
		// selects its branch and the access reduces over that. When the target stays symbolic
		// because its own operands are not ground, keep the access wrapped around the reduced
		// target rather than re-reducing the same shape forever. A mapped member arrives inside an
		// ObjectType, which grounds through the object path before the access reads one of its
		// emitted fields.
		inner := e.reduce(target)
		if isResidualOp(inner) {
			return &soltype.IndexType{Target: inner, Index: idx, Inexact: inexact}
		}
		return e.reduceIndex(inner, idx, inexact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground fields, so indexing it stays
		// symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(tgt); ok {
			return e.indexObject(obj, idx, inexact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Inexact: inexact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(tgt)
		if !ok {
			return &soltype.IndexType{Target: target, Index: idx, Inexact: inexact}
		}
		return e.indexObject(obj, idx, inexact)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground positions, so indexing it stays
		// symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(tgt); ok {
			return e.indexTuple(tup, idx, inexact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Inexact: inexact}
	case *soltype.UnionType:
		// A union target distributes member-wise: `(A | B)[K]` ⇒ `A[K] | B[K]`, the other-axis
		// twin of the union-index distribution above, matching how keyof distributes over a union
		// operand. A union value is one of its members, so every member must carry K — a member
		// lacking it records its own absence diagnostic through reduceIndex. Each member indexes
		// with the same reduced key.
		//
		// An inexact target union has unlisted members, and the value each holds at K is not among
		// the ones the access read, so the result union is open too (exact-types §7.6).
		parts := make([]soltype.Type, len(tgt.Types))
		for i, m := range tgt.Types {
			parts[i] = e.reduceIndex(m, idx, inexact)
		}
		return newUnion(nil, parts, tgt.Inexact)
	case *soltype.IntersectionType:
		return e.reduceIndexIntersection(tgt, idx, inexact)
	default:
		return &soltype.IndexType{Target: target, Index: idx, Inexact: inexact}
	}
}

// reduceIndexIntersection reduces `(A & B & …)[K]`. An intersection value satisfies every member,
// so it carries key K when ANY member does, and the access yields the meet of the value types the
// members that carry K contribute. This is the opposite of the union-target rule, where every
// member must carry K. A member lacking K contributes nothing rather than erroring, so its own
// absence diagnostic is rolled back and kept aside. The access stays symbolic when a member is not
// ground enough to decide whether it carries K, and reports absence only when no member carries it.
func (e *typeEvaluator) reduceIndexIntersection(tgt *soltype.IntersectionType, idx soltype.Type, inexact bool) soltype.Type {
	var resolved []soltype.Type
	var absentErrs []SolverError
	anySymbolic := false
	for _, m := range tgt.Types {
		before := len(e.errs)
		r := e.reduceIndex(m, idx, inexact)
		if produced := e.errs[before:]; len(produced) > 0 {
			// A ground member lacking K recorded its own absence diagnostic. A sibling may still
			// carry K, so roll the diagnostic back and keep it aside in case none does.
			absentErrs = append(absentErrs, produced...)
			e.errs = e.errs[:before]
			continue
		}
		if isResidualOp(r) {
			anySymbolic = true
			continue
		}
		resolved = append(resolved, r)
	}
	// A member the evaluator could not ground might carry K with an unknown type, so the meet is
	// undecided. Stay symbolic rather than committing to the members that did resolve.
	if anySymbolic {
		return &soltype.IndexType{Target: tgt, Index: idx, Inexact: inexact}
	}
	if len(resolved) > 0 {
		return newIntersection(nil, resolved)
	}
	// Every member is ground and none carries K, so K is genuinely absent. Surface one member's
	// absence diagnostic and reduce to the error sentinel.
	if len(absentErrs) > 0 {
		e.errs = append(e.errs, absentErrs[0])
	}
	return &soltype.ErrorType{}
}

// reduceIndexAlias reduces `Alias[K]` by expanding the alias and indexing its body under the
// termination guard, the indexed-access twin of reduceKeyofAlias. The alias stays on the active
// path for the whole reduction of its body, so a member that re-references it stops. A recurring
// instantiation state, an exhausted budget, or an unresolved body each leaves the access
// unexpanded and symbolic.
func (e *typeEvaluator) reduceIndexAlias(op *soltype.AliasType, index soltype.Type, inexact bool) soltype.Type {
	symbolic := func(a *soltype.AliasType) soltype.Type {
		return &soltype.IndexType{Target: a, Index: index, Inexact: inexact}
	}
	return e.expandAliasGuarded(op, symbolic, func(body soltype.Type) soltype.Type {
		return e.reduceIndex(body, index, inexact)
	})
}

// indexObject reduces `obj[key]` for a ground object. A string-literal key selects the named
// member's read type, which is a property's or getter's declared type or a method's callable value.
// A declared member always holds a value, so that read carries no `undefined`.
//
// A key that names no declared member falls to the object's index signature, which covers every key
// of its key set. That read is `Value | undefined`, since the signature says the key may be present
// rather than that it is. An object with no index signature instead records an UnknownObjectKeyError
// and reduces to the error sentinel.
//
// A key that is not a string literal, such as the bare `string` primitive, names no single member.
// It reads through the index signature the same way. Without one, a ground key of that shape can
// never name a member, so it records a NoIndexSignatureError. A key that has not grounded may still
// reduce to a literal, so the access stays symbolic instead.
func (e *typeEvaluator) indexObject(obj *soltype.ObjectType, index soltype.Type, inexact bool) soltype.Type {
	hasIdx := len(obj.IndexSignatures()) > 0
	name, ok := strLitName(index)
	if !ok {
		if hasIdx {
			return e.indexSignatureRead(obj, index, inexact)
		}
		if condOperandGround(index) {
			e.errs = append(e.errs, &NoIndexSignatureError{Object: obj, Index: index})
			return &soltype.ErrorType{}
		}
		return &soltype.IndexType{Target: obj, Index: index, Inexact: inexact}
	}
	if _, found := obj.Member(name); !found {
		if hasIdx {
			return e.indexSignatureRead(obj, index, inexact)
		}
		e.errs = append(e.errs, &UnknownObjectKeyError{Object: obj, Key: name})
		return &soltype.ErrorType{}
	}
	read, hasValue, _ := memberReadContribution(obj, name)
	if !hasValue {
		// The member is a write-only setter, which exposes no readable value. Leave the access
		// symbolic rather than resolving a write slot to a read type.
		return &soltype.IndexType{Target: obj, Index: index, Inexact: inexact}
	}
	return read
}

// indexSignatureRead reduces `obj[key]` through the signature indexSignatureFor matches to the key.
// A key none accepts records an IndexSignatureKeyError, with no coercion. The result unions
// `undefined` onto the value type, since the `?` every legal signature carries says the key may be
// absent, so `{[K: string]?: number}` reads as `number | undefined`. An ungrounded key stays symbolic.
func (e *typeEvaluator) indexSignatureRead(obj *soltype.ObjectType, index soltype.Type, inexact bool) soltype.Type {
	if !condOperandGround(index) {
		return &soltype.IndexType{Target: obj, Index: index, Inexact: inexact}
	}
	idx, ok := e.ctx.indexSignatureFor(obj, index, e.seen)
	if !ok {
		e.errs = append(e.errs, &IndexSignatureKeyError{Object: obj, Index: index})
		return &soltype.ErrorType{}
	}
	return newUnion(nil, []soltype.Type{idx.Value, &soltype.UndefinedType{}}, false)
}

// indexTuple reduces `tup[n]` for a ground tuple. A numeric-literal key selects the element at
// that position. An index outside `[0, len)`, or a non-integer or negative literal, records a
// TupleIndexOutOfRangeError and reduces to the error sentinel. A non-numeric-literal index has no
// positional slot to select, so the access stays symbolic.
func (e *typeEvaluator) indexTuple(tup *soltype.TupleType, index soltype.Type, inexact bool) soltype.Type {
	lit, ok := index.(*soltype.LitType)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Inexact: inexact}
	}
	num, ok := lit.Lit.(*soltype.NumLit)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Inexact: inexact}
	}
	i := int(num.Value)
	if float64(i) != num.Value || i < 0 || i >= len(tup.Elems) {
		e.errs = append(e.errs, &TupleIndexOutOfRangeError{Tuple: tup, Index: num.Value})
		return &soltype.ErrorType{}
	}
	return tup.Elems[i]
}

// reduceTemplateLit reduces a template literal to the union of string literals its interpolations
// produce, taking the cartesian product over each interpolation's choices. Each interpolation, and
// each member of an interpolation that grounds to a union, is grounded so a named alias expands to
// its body. A grounded interpolation that is a union contributes each member as a choice, so
// `on${"a" | "b"}` yields `"ona" | "onb"`, while any other grounded interpolation contributes
// itself. Each product combination folds its string-literal interpolations into the surrounding
// segments; a combination whose interpolation stays abstract — a type parameter, or a nested
// operator the evaluator could not ground — keeps that interpolation and stays a `TemplateLitType`,
// so the whole template reduces later once the interpolation grounds. A product that would exceed
// maxTemplateLitCombinations is rejected with a diagnostic rather than materialized.
//
// The result is exact only when every interpolation is, since an interpolation that names an open
// set of choices produces an open set of strings (exact-types §5.6). `on${"a" | "b"}` reduces to
// the exact `"ona" | "onb"`, while `on${"a" | "b" | ...}` reduces to `"ona" | "onb" | ...`.
func (e *typeEvaluator) reduceTemplateLit(t *soltype.TemplateLitType) soltype.Type {
	interpChoices := make([][]soltype.Type, len(t.Interps))
	combinations := 1
	openChoices := false
	for i, interp := range t.Interps {
		reduced := e.groundOperand(interp)
		if u, ok := reduced.(*soltype.UnionType); ok {
			openChoices = openChoices || u.Inexact
			// Ground each union member too, so a reducible member — an alias to a literal, or a
			// nested operator such as `keyof O` — collapses to its string literal before the product
			// rather than surviving as a residual interpolation.
			members := make([]soltype.Type, len(u.Types))
			for j, m := range u.Types {
				members[j] = e.groundOperand(m)
			}
			interpChoices[i] = members
		} else {
			interpChoices[i] = []soltype.Type{reduced}
		}
		combinations *= len(interpChoices[i])
		if combinations > maxTemplateLitCombinations {
			// The product would enumerate more string literals than the cap allows. Reject the
			// template with one diagnostic rather than materializing an unbounded union.
			e.errs = append(e.errs, &TemplateLitTooComplexError{Template: t})
			return &soltype.ErrorType{}
		}
	}
	combos := cartesianProduct(interpChoices)
	parts := make([]soltype.Type, 0, len(combos))
	for _, combo := range combos {
		parts = append(parts, foldTemplatePart(t.Quasis, combo))
	}
	return newUnion(nil, parts, openChoices)
}

// foldTemplatePart folds one cartesian-product combination into a single template result. It
// interleaves the fixed segments with that combination's interpolation values: a string-representable
// literal folds into the surrounding text, while any other value closes the accumulated segment and
// carries through as a residual interpolation. A combination whose values all fold collapses to a
// lone string-literal type; one carrying an abstract value stays a `TemplateLitType`. Quasis holds
// one more entry than interpValues, so the loop reads interpValues[i] between quasi i and quasi i+1.
func foldTemplatePart(quasis []string, interpValues []soltype.Type) soltype.Type {
	newQuasis := []string{}
	newInterps := []soltype.Type{}
	current := ""
	for i, quasi := range quasis {
		// current accumulates the literal text since the last abstract interpolation. The folding
		// branch below leaves current intact, so this appends quasi i onto the earlier quasi and
		// any literals folded into it — `a${"1"}b` builds "a" then "a1" then "a1b". After the
		// abstract branch resets current to "", or on the first iteration, this starts a fresh
		// segment instead.
		current += quasi
		if i >= len(interpValues) {
			continue
		}
		if s, ok := stringifyLit(interpValues[i]); ok {
			current += s
			continue
		}
		newQuasis = append(newQuasis, current)
		current = ""
		newInterps = append(newInterps, interpValues[i])
	}
	newQuasis = append(newQuasis, current)
	if len(newInterps) == 0 {
		return strLitType(newQuasis[0])
	}
	return &soltype.TemplateLitType{Quasis: newQuasis, Interps: newInterps}
}

// cartesianProduct returns every combination that picks one choice from each position, so the
// template reducer can enumerate `${A}${B}` over unions A and B. An empty choice list — a
// template with no interpolations — yields one empty combination, so a bare `abc` collapses
// to the single literal `"abc"`.
func cartesianProduct(interpChoices [][]soltype.Type) [][]soltype.Type {
	result := [][]soltype.Type{{}}
	for _, choices := range interpChoices {
		next := make([][]soltype.Type, 0, len(result)*len(choices))
		for _, combo := range result {
			for _, choice := range choices {
				extended := make([]soltype.Type, len(combo)+1)
				copy(extended, combo)
				extended[len(combo)] = choice
				next = append(next, extended)
			}
		}
		result = next
	}
	return result
}

// stringifyLit returns the surface string a literal type contributes inside a template, and false
// for any non-literal type. A string literal contributes its value, a number its decimal form, and
// a boolean `true`/`false`, matching how each renders in source.
func stringifyLit(t soltype.Type) (string, bool) {
	lit, ok := t.(*soltype.LitType)
	if !ok {
		return "", false
	}
	switch l := lit.Lit.(type) {
	case *soltype.StrLit:
		return l.Value, true
	case *soltype.NumLit:
		return strconv.FormatFloat(l.Value, 'f', -1, 64), true
	case *soltype.BoolLit:
		return strconv.FormatBool(l.Value), true
	}
	return "", false
}

// reduceStringIntrinsic reduces an intrinsic string operator such as `Uppercase<T>` over its operand.
// A string-literal operand maps to the transformed literal — `Uppercase<"abc">` ⇒ `"ABC"` — and a
// union operand distributes member-wise, so `Uppercase<"a" | "b">` ⇒ `"A" | "B"`. The operand is
// first grounded, so a named alias expands to its body. An operand that is not a string literal,
// such as a type parameter, keeps the operator symbolic as a `StringIntrinsicType` rebuilt around the
// grounded operand.
//
// An inexact operand union names strings the transform never sees, so the result union is open too:
// `Uppercase<"a" | "b" | ...>` reduces to `"A" | "B" | ...`.
func (e *typeEvaluator) reduceStringIntrinsic(kind soltype.StringIntrinsicKind, operand soltype.Type) soltype.Type {
	reduced := e.groundOperand(operand)
	switch op := reduced.(type) {
	case *soltype.UnionType:
		parts := make([]soltype.Type, len(op.Types))
		for i, m := range op.Types {
			parts[i] = e.reduceStringIntrinsic(kind, m)
		}
		return newUnion(nil, parts, op.Inexact)
	case *soltype.LitType:
		if s, ok := op.Lit.(*soltype.StrLit); ok {
			return strLitType(applyStringIntrinsic(kind, s.Value))
		}
	}
	return &soltype.StringIntrinsicType{Kind: kind, Operand: reduced}
}

// reduceExactness reduces `Exact<T>` and `Inexact<T>` by rewriting the trailing `...` marker on the
// grounded operand. `Exact` clears the marker and `Inexact` sets it, so `Inexact<{x: number}>`
// reduces to `{x: number, ...}` and `Exact<{x: number, ...}>` back to `{x: number}` (exact-types
// §6.1, §6.2). The operand is grounded first, so a named alias expands to its body.
//
// Four kinds of type carry the marker and take it from the operator: an object, a tuple, a function,
// and a union. Two more pass the operator inward rather than answering themselves. An intersection
// rewrites each of its members, since its exactness is its members' (§7.7). A borrow rewrites its
// pointee, since mutability is orthogonal to exactness and `mut T` carries T's (§7.11).
//
// A type with no exactness notion reduces to itself. A primitive, a literal, `never`, `unknown`,
// `void`, `null`, `undefined`, and a promise each name a single kind of value with no member list to
// close, so neither operator has anything to change (§7.13).
//
// `Exact<C>` over a class C that is not declared `final` is an error, since a subclass instance
// carries members C does not declare and Escalier has no use-site form that closes an open instance
// type (§2.6.2). Every other class case leaves the class unchanged: a final class is already exact,
// and `Inexact<C>` would need the same absent use-site form to open one.
//
// Any other operand keeps the operator symbolic as an `ExactnessType` rebuilt around the grounded
// operand. A type parameter, an unexpanded alias, and an operator whose own operands are not ground
// each land there.
func (e *typeEvaluator) reduceExactness(kind soltype.ExactnessKind, operand soltype.Type) soltype.Type {
	reduced := e.groundOperand(operand)
	inexact := kind == soltype.MakeInexact
	switch op := reduced.(type) {
	case *soltype.ObjectType:
		// An object still carrying a `...A` spread or a `[K]: V for K in Keys` member has no final
		// member list, so its own exactness is not settled yet and the operator stays symbolic.
		if soltype.HasResidualElem(op.Elems) {
			break
		}
		return &soltype.ObjectType{Elems: op.Elems, Inexact: inexact}
	case *soltype.TupleType:
		// A tuple still carrying a `...P` spread has no final element list, mirroring the object arm.
		if hasRestSpread(op.Elems) {
			break
		}
		return &soltype.TupleType{Elems: op.Elems, Inexact: inexact}
	case *soltype.FuncType:
		rewritten := *op
		rewritten.Inexact = inexact
		return &rewritten
	case *soltype.UnionType:
		// newUnion rather than a direct rebuild, so clearing the marker on a one-member union
		// collapses it to that member: `Exact<"only" | ...>` reduces to `"only"`.
		return newUnion(nil, op.Types, inexact)
	case *soltype.IntersectionType:
		parts := make([]soltype.Type, len(op.Types))
		for i, m := range op.Types {
			parts[i] = e.reduceExactness(kind, m)
		}
		return newIntersection(nil, parts)
	case *soltype.RefType:
		pointee, ok := e.reduceExactness(kind, op.Inner).(soltype.RefInner)
		if !ok {
			// The pointee stayed symbolic, so the operator over the borrow has no answer either.
			break
		}
		return &soltype.RefType{Mut: op.Mut, Lt: op.Lt, Inner: pointee}
	case *soltype.ClassType:
		if kind == soltype.MakeExact && !op.Final {
			e.errs = append(e.errs, &ExactNonFinalClassError{Class: op})
			return &soltype.ErrorType{}
		}
		return op
	case *soltype.PrimType, *soltype.LitType, *soltype.NeverType, *soltype.UnknownType,
		*soltype.Void, *soltype.NullType, *soltype.UndefinedType, *soltype.PromiseType,
		*soltype.ErrorType:
		return reduced
	}
	return &soltype.ExactnessType{Kind: kind, Operand: reduced}
}

// applyStringIntrinsic transforms one string by the named intrinsic operator. Uppercase and Lowercase
// map every character; Capitalize and Uncapitalize map only the first, leaving the rest unchanged.
func applyStringIntrinsic(kind soltype.StringIntrinsicKind, s string) string {
	switch kind {
	case soltype.Uppercase:
		return strings.ToUpper(s)
	case soltype.Lowercase:
		return strings.ToLower(s)
	case soltype.Capitalize:
		return mapFirstRune(s, unicode.ToUpper)
	case soltype.Uncapitalize:
		return mapFirstRune(s, unicode.ToLower)
	}
	return s
}

// mapFirstRune applies f to the first rune of s and leaves the remainder unchanged, the transform
// Capitalize and Uncapitalize share. An empty string maps to itself.
func mapFirstRune(s string, f func(rune) rune) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(f(r)) + s[size:]
}

// strLitName returns the property name a string-literal index selects, and false for any other
// type. Object keys are strings, so only a StrLit names a member.
func strLitName(t soltype.Type) (string, bool) {
	if lit, ok := t.(*soltype.LitType); ok {
		if s, ok := lit.Lit.(*soltype.StrLit); ok {
			return s.Value, true
		}
	}
	return "", false
}

// mappedKeyName returns the field name a mapped type's key emits. A string literal names one
// directly; a number literal names the digits it spells, as `{0: v}` stores under "0". Separate from
// strLitName because `T[0]` reads a tuple positionally, so that number must not be coerced.
func mappedKeyName(t soltype.Type) (string, bool) {
	if lit, ok := t.(*soltype.LitType); ok {
		if n, ok := lit.Lit.(*soltype.NumLit); ok {
			return strconv.FormatFloat(n.Value, 'f', -1, 64), true
		}
	}
	return strLitName(t)
}

// isResidualOp reports whether t is an unreduced type-level operator at its top level. That is a
// `keyof`, an indexed access, a conditional, a `...P` tuple-spread element, a template literal whose
// interpolation stayed abstract, an intrinsic string operator over an abstract operand, an
// `Exact`/`Inexact` operator over an abstract operand, or an object carrying a mapped member whose
// key set never ground. The evaluator consults it to stop re-reducing an operand whose reduction
// stayed symbolic.
func isResidualOp(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType,
		*soltype.RestSpreadType, *soltype.TemplateLitType, *soltype.StringIntrinsicType,
		*soltype.ExactnessType:
		return true
	}
	return objectHasMapped(t)
}

// objectHasMapped reports whether t is an object whose member list still carries an unreduced
// `[K]: V for K in Keys` member, the mapped twin of objectHasSpread. An index signature does not
// count: it is the final form of its member, so an object carrying one is ground.
func objectHasMapped(t soltype.Type) bool {
	obj, ok := t.(*soltype.ObjectType)
	return ok && soltype.HasUnsettledMapped(obj.Elems)
}

// objectIsResidual reports whether t is an object carrying either unreduced member kind, the type-
// level form of soltype.HasResidualElem. constrain consults it to decide whether to compare an
// object inertly rather than decomposing it.
func objectIsResidual(t soltype.Type) bool {
	obj, ok := t.(*soltype.ObjectType)
	return ok && soltype.HasResidualElem(obj.Elems)
}

// tupleHasSpread reports whether t is a tuple carrying at least one unreduced `...P` spread
// element. Such a tuple is inert — constrain passes it through untouched until the evaluator
// splices the spread — whereas a plain tuple is a structural type constrain decomposes. A non-tuple
// is never a spread tuple.
func tupleHasSpread(t soltype.Type) bool {
	tup, ok := t.(*soltype.TupleType)
	return ok && hasRestSpread(tup.Elems)
}

// objectHasSpread reports whether t is an object carrying an unreduced `...A` spread element, the
// object twin of tupleHasSpread.
func objectHasSpread(t soltype.Type) bool {
	obj, ok := t.(*soltype.ObjectType)
	return ok && soltype.HasObjectSpread(obj.Elems)
}

// hasRestSpread reports whether any element of elems is a `...P` spread.
func hasRestSpread(elems []soltype.Type) bool {
	for _, el := range elems {
		if _, ok := el.(*soltype.RestSpreadType); ok {
			return true
		}
	}
	return false
}

// strLitType builds the string-literal type for one key name, the form a projected object or
// tuple key takes in a `keyof` union.
func strLitType(name string) soltype.Type {
	return &soltype.LitType{Lit: &soltype.StrLit{Value: name}}
}

// containsResidualOp reports whether t holds any unreduced type-level operator node: a `keyof`, an
// indexed access, a conditional, a mapped type, or a tuple spread. constrain consults it to decide
// whether a reduced operator fully grounded. A result with no residual is safe to recurse on. One
// that still carries a `keyof`, a `T[K]`, a `{[K]: V for K in Keys}`, or a `[...T, x]` is not, since
// re-reducing it would loop. Such a residual survives reduction when its operand is an unexpanded
// type parameter, or an expanding alias the depth budget truncated.
func containsResidualOp(t soltype.Type) bool {
	f := &residualOpFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// residualOpFinder is the walking visitor behind containsResidualOp. It flags the first residual
// operator it reaches and skips that node's children, since one occurrence is enough.
type residualOpFinder struct{ found bool }

func (f *residualOpFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	if isResidualOp(t) {
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *residualOpFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}

// containsFreeVar reports whether t holds any type variable, skolem, or unsubstituted mapped-type
// key — an abstract leaf that makes t non-ground. A conditional consults it to decide whether its
// Check and Extends are concrete enough to probe `Check <: Extends`. A conditional whose Check is a
// bare type parameter stays symbolic, since that parameter is a free variable. A mapped type's key
// counts the same way: a position naming it stands for whichever key the reduction has yet to
// choose, so it is not ground until that key is substituted in.
func containsFreeVar(t soltype.Type) bool {
	f := &freeVarFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// freeVarFinder is the walking visitor behind containsFreeVar. It flags the first type variable,
// skolem, or mapped-type key it reaches and skips that node's children, since one occurrence is
// enough. A key inside the mapped type that binds it is reached only while that mapped type is
// still symbolic, which containsResidualOp already reports, so no binding-aware walk is needed.
type freeVarFinder struct{ found bool }

func (f *freeVarFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t.(type) {
	case *soltype.TypeVarType, *soltype.SkolemType, *soltype.MappedKeyType:
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *freeVarFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}
