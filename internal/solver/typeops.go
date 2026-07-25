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
// This budget backstops an expanding recursive alias such as `type Grow<T> = Grow<Array<T>>`,
// whose argument grows every lap so its state never repeats and the active guard never matches.
// No finite analytical bound exists for that fragment, so the budget stops the walk and the
// operator over the unexpanded alias stays symbolic.
const maxExpandDepth = 200

// maxTemplateLitCombinations caps how many string literals a template literal may reduce to. Its
// cartesian product over interpolated unions grows multiplicatively, so `${A}${B}${C}` over three
// large unions could enumerate an unbounded union. The cap rejects such a template with a
// diagnostic rather than materializing the product.
const maxTemplateLitCombinations = 10_000

// typeEvaluator reduces a residual type-level operator to its value. It handles `keyof T`, indexed
// access `T[K]`, the conditional `if C : E { … } else { … }`, template literals, and the intrinsic
// string operators such as `Uppercase<T>`; later operators join as they land. Only constrain invokes
// it, to check a constraint against a residual. Annotation and display keep the residual symbolic, so
// a stored type prints `keyof {x: number}` or `Point["x"]` the way the source wrote it, never the
// reduced value.
//
// reduce projects the operand's keys: a ground `keyof {x: number}` yields `"x"`, and an alias or
// class operand expands to the referenced type's keys, the transparent-but-named treatment an
// alias itself gets under constrain. A `keyof T` over a type parameter has no ground key set, so
// it stays the symbolic KeyofType.
//
// A recursive alias reached through an operand is made safe by a two-part termination strategy:
//
//   - active holds the alias instantiations currently being expanded, each keyed by the alias
//     name together with its rendered arguments. When one recurs with the identical key, the
//     evaluator leaves that reference as the unexpanded alias node rather than expanding it again.
//     A recursive alias such as `type List<T> = {head: T, tail: List<T> | null}` therefore reduces
//     to a finite type whose recursive position points back to the alias instead of unfolding
//     forever.
//   - depth caps expansions along one path. It backstops an expanding recursion whose argument
//     grows every lap, so its key never repeats and the active guard never fires.
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
	// seen is the enclosing constraint's cycle-detection set, carried in so a conditional's
	// `Check <: Extends` probe shares the caller's alias-cycle guard. A conditional reduces by
	// re-entering constrain to decide its branch, and constrain expands an alias operand and
	// re-reduces the conditional in its body, so a self-referential alias such as
	// `type Bad = if Bad : number { number } else { string }` would recurse without bound if the
	// probe started a fresh set. Reusing the caller's set closes that cycle the same way two
	// structurally-equal instances of a recursive alias close through constrain's seen-set. The
	// value solve seeds it fresh at each constraint site, and the test expander passes an empty set.
	seen set.Set[constraintKey]
	// errs collects the diagnostics a reduction produces. `keyof` reduction is total and adds
	// none; indexed-access reduction records an UnknownObjectKeyError or a
	// TupleIndexOutOfRangeError when a ground access resolves to no member. constrain reads
	// these after reducing an operator it is checking a constraint against, so a malformed
	// `{x: number}["z"]` surfaces at the constraint site. It records diagnostics, not solver
	// state, so no bound or variable is mutated.
	errs []SolverError
}

func newTypeEvaluator(ctx *Context, seen set.Set[constraintKey]) *typeEvaluator {
	return &typeEvaluator{ctx: ctx, active: set.NewSet[string](), depth: maxExpandDepth, seen: seen}
}

// reduce reduces one type-level operator node to its value, returning any other type
// unchanged. A node whose operand is not yet ground reduces to the same operator rebuilt
// around the expanded operand, so it stays symbolic and reduces later once the operand
// grounds.
func (e *typeEvaluator) reduce(t soltype.Type) soltype.Type {
	switch t := t.(type) {
	case *soltype.KeyofType:
		return e.reduceKeyof(t.Operand, t.Exact)
	case *soltype.IndexType:
		return e.reduceIndex(t.Target, t.Index, t.Exact)
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
	case *soltype.StringMappingType:
		return e.reduceStringMapping(t.Kind, t.Operand)
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
	return newUnion(nil, parts, false)
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
func (c *Context) condExtends(check, extends soltype.Type, seen set.Set[constraintKey]) bool {
	return !hasHardError(c.trialUnderProbeSeen(check, extends, seen.Clone()))
}

// condOperandGround reports whether a conditional's Check or Extends operand is ground enough to
// decide the branch. An operand carrying a type variable, a skolem, or an unreduced residual
// operator is abstract, so the `Check <: Extends` probe cannot decide a branch over it and the
// conditional stays symbolic. containsFreeVar catches the variable and skolem cases;
// containsResidualOp catches a residual such as a `keyof T` the reduction left symbolic.
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
// interpolation `Dir` over `type Dir = "a" | "b"` grounds to the referenced union. A type parameter,
// a recurring alias state, an exhausted budget, or an unresolved alias body each leaves the operand
// unexpanded, which keeps the enclosing operator symbolic. The tuple-spread, template-literal, and
// string-mapping reductions share it.
func (e *typeEvaluator) groundOperand(operand soltype.Type) soltype.Type {
	reduced := e.reduce(operand)
	alias, ok := reduced.(*soltype.AliasType)
	if !ok {
		return reduced
	}
	return e.expandAliasGuarded(alias, reduced, func(body soltype.Type) soltype.Type {
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
	if !soltype.HasObjectSpread(t.Elems) {
		return t
	}
	reducedElems := make([]soltype.ObjTypeElem, len(t.Elems))
	operandElems := make([][]soltype.ObjTypeElem, 0, len(t.Elems))
	inexact := t.Inexact
	ground := true
	for i, el := range t.Elems {
		spread, ok := el.(*soltype.SpreadElem)
		if !ok {
			// A non-spread member has its value types reduced, then contributes as a one-field group.
			re := e.reduceElem(el)
			reducedElems[i] = re
			operandElems = append(operandElems, []soltype.ObjTypeElem{re})
			continue
		}
		operand := e.groundObjectOperand(spread.Type)
		reducedElems[i] = &soltype.SpreadElem{Type: operand}
		obj, ok := operand.(*soltype.ObjectType)
		if !ok || soltype.HasObjectSpread(obj.Elems) {
			ground = false
			continue
		}
		operandElems = append(operandElems, obj.Elems)
		inexact = inexact || obj.Inexact
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
// contributes, the object analogue of the tuple's groundSpreadOperand. It resolves a `typeof` query
// to the value's type, projects a class instance body, and expands an alias to its body under the
// active-state and depth guard reduceKeyofAlias uses, so a recursive alias reached through a spread
// terminates. Any other kind — an object, a type variable, a nested operator — is reduced. It
// returns the reduced operand rather than an ok flag, so a caller keeps that reduced form when the
// operand does not ground, matching how reduceTuple keeps the operand it could not splice.
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
		key := soltype.PrintQualified(op)
		if e.active.Contains(key) || e.depth <= 0 {
			return op
		}
		body := e.ctx.expandAlias(op)
		if _, unresolved := body.(*soltype.ErrorType); unresolved {
			return op
		}
		e.active.Add(key)
		e.depth--
		red := e.groundObjectOperand(body)
		e.active.Remove(key)
		e.depth++
		return red
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

// reduceKeyof reduces `keyof operand` to the union of the operand's keys, mirroring the old
// checker's KeyOfType case (internal/checker/expand_type.go):
//
//   - an object projects its property, getter, and setter names as string-literal types;
//   - a tuple yields only its own numeric indices, omitting the inherited "length"; see keyofTuple;
//   - `keyof` distributes over a union or intersection, unioning each member's keys;
//   - `keyof` of a primitive, literal, `never`, or `unknown` is `never`, since none has
//     enumerable keys;
//   - an alias expands to its body and a class projects its instance body, and `keyof` reduces
//     over that under the termination guard;
//   - a `typeof` query resolves to the value's type, and `keyof` reduces over that.
//
// A type variable, a skolem, or a named reference the evaluator does not expand keeps the
// operator symbolic, rebuilt around the operand.
func (e *typeEvaluator) reduceKeyof(operand soltype.Type, exact bool) soltype.Type {
	switch op := operand.(type) {
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType:
		// The operand is itself an operator — a `keyof`, an indexed access, or a conditional. Reduce
		// it first, then take keyof its value, so a ground conditional operand selects its branch and
		// `keyof` projects that branch's keys. If the inner operator stays symbolic because its own
		// operands are not ground, wrap it as `keyof <inner>` rather than re-reducing the same shape
		// forever.
		inner := e.reduce(op)
		if isResidualOp(inner) {
			return &soltype.KeyofType{Operand: inner, Exact: exact}
		}
		return e.reduceKeyof(inner, exact)
	case *soltype.AliasType:
		return e.reduceKeyofAlias(op, exact)
	case *soltype.TypeofType:
		// `keyof typeof x` resolves the query to the value's type, then projects that type's keys.
		return e.reduceKeyof(op.Ty, exact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground key set, so `keyof` over it
		// stays symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(op); ok {
			return e.keyofObject(obj)
		}
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(op)
		if !ok {
			return &soltype.KeyofType{Operand: operand, Exact: exact}
		}
		return e.keyofObject(obj)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground index set, so `keyof` over it
		// stays symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(op); ok {
			return e.keyofTuple(tup)
		}
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	case *soltype.UnionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.IntersectionType:
		return e.keyofDistribute(op.Types, exact)
	case *soltype.PrimType, *soltype.LitType, *soltype.NeverType, *soltype.UnknownType:
		return &soltype.NeverType{}
	default:
		return &soltype.KeyofType{Operand: operand, Exact: exact}
	}
}

// reduceKeyofAlias reduces `keyof Alias` by expanding the alias and reducing `keyof` over its
// body under the termination guard, leaving the alias symbolic when the guard blocks expansion.
func (e *typeEvaluator) reduceKeyofAlias(op *soltype.AliasType, exact bool) soltype.Type {
	symbolic := &soltype.KeyofType{Operand: op, Exact: exact}
	return e.expandAliasGuarded(op, symbolic, func(body soltype.Type) soltype.Type {
		return e.reduceKeyof(body, exact)
	})
}

// expandAliasGuarded expands a named alias to its body and applies cont to the result, under the
// two-part termination guard that makes reduction safe over a recursive alias. The alias stays on
// the active path for the whole reduction of its body, so a member that re-references it, directly
// or through a chain, sees it active and stops. A recurring instantiation state, an exhausted
// budget, or an unresolved body each returns fallback with the alias left unexpanded, so the
// operator over it stays symbolic.
func (e *typeEvaluator) expandAliasGuarded(op *soltype.AliasType, fallback soltype.Type, cont func(body soltype.Type) soltype.Type) soltype.Type {
	key := soltype.PrintQualified(op)
	if e.active.Contains(key) || e.depth <= 0 {
		return fallback
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		// expandAlias yields ErrorType for an unregistered alias, or one whose body a dep-graph
		// sibling has not filled yet. Keep the operator symbolic rather than reducing over `error`.
		return fallback
	}
	e.active.Add(key)
	e.depth--
	result := cont(body)
	e.active.Remove(key)
	e.depth++
	return result
}

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
func (e *typeEvaluator) keyofTuple(tup *soltype.TupleType) soltype.Type {
	keys := make([]soltype.Type, 0, len(tup.Elems))
	for i := range tup.Elems {
		keys = append(keys, &soltype.LitType{Lit: &soltype.NumLit{Value: float64(i)}})
	}
	return newUnion(nil, keys, false)
}

// keyofDistribute unions the keys of each member of a union or intersection operand, the
// shared body of both distribution arms: `keyof (A | B)` and `keyof (A & B)` both reduce to
// `keyof A | keyof B`, since an intersection carries the keys of all its members.
func (e *typeEvaluator) keyofDistribute(members []soltype.Type, exact bool) soltype.Type {
	parts := make([]soltype.Type, len(members))
	for i, m := range members {
		parts[i] = e.reduceKeyof(m, exact)
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
func (e *typeEvaluator) reduceIndex(target, index soltype.Type, exact bool) soltype.Type {
	idx := e.reduce(index)
	// A union index distributes member-wise. `T[keyof T]` rides this once `keyof T` reduces to
	// its `"a" | "b"` key union, so the access yields the union of the members' value types.
	if u, ok := idx.(*soltype.UnionType); ok {
		parts := make([]soltype.Type, len(u.Types))
		for i, m := range u.Types {
			parts[i] = e.reduceIndex(target, m, exact)
		}
		return newUnion(nil, parts, false)
	}
	switch tgt := target.(type) {
	case *soltype.AliasType:
		return e.reduceIndexAlias(tgt, idx, exact)
	case *soltype.TypeofType:
		// `(typeof x)[K]` resolves the query to the value's type, then indexes that type.
		return e.reduceIndex(tgt.Ty, idx, exact)
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType:
		// The target is itself an operator — a `keyof`, an indexed access, or a conditional. Reduce
		// it first, then index its value, so a ground conditional target selects its branch and the
		// access reduces over that. When the target stays symbolic because its own operands are not
		// ground, keep the access wrapped around the reduced target rather than re-reducing the same
		// shape forever.
		inner := e.reduce(target)
		if isResidualOp(inner) {
			return &soltype.IndexType{Target: inner, Index: idx, Exact: exact}
		}
		return e.reduceIndex(inner, idx, exact)
	case *soltype.ObjectType:
		// An object carrying an unreduced `...A` spread has no ground fields, so indexing it stays
		// symbolic until the spread grounds, mirroring the TupleType arm below.
		if obj, ok := e.groundToObject(tgt); ok {
			return e.indexObject(obj, idx, exact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	case *soltype.ClassType:
		obj, ok := e.ctx.projectClassBody(tgt)
		if !ok {
			return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
		}
		return e.indexObject(obj, idx, exact)
	case *soltype.TupleType:
		// A tuple carrying an unreduced `...P` spread has no ground positions, so indexing it stays
		// symbolic until the spread grounds to a concrete tuple.
		if tup, ok := e.groundTuple(tgt); ok {
			return e.indexTuple(tup, idx, exact)
		}
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	case *soltype.UnionType:
		// A union target distributes member-wise: `(A | B)[K]` ⇒ `A[K] | B[K]`, the other-axis
		// twin of the union-index distribution above, matching how keyof distributes over a union
		// operand. A union value is one of its members, so every member must carry K — a member
		// lacking it records its own absence diagnostic through reduceIndex. Each member indexes
		// with the same reduced key.
		parts := make([]soltype.Type, len(tgt.Types))
		for i, m := range tgt.Types {
			parts[i] = e.reduceIndex(m, idx, exact)
		}
		return newUnion(nil, parts, false)
	case *soltype.IntersectionType:
		return e.reduceIndexIntersection(tgt, idx, exact)
	default:
		return &soltype.IndexType{Target: target, Index: idx, Exact: exact}
	}
}

// reduceIndexIntersection reduces `(A & B & …)[K]`. An intersection value satisfies every member,
// so it carries key K when ANY member does, and the access yields the meet of the value types the
// members that carry K contribute. This is the opposite of the union-target rule, where every
// member must carry K. A member lacking K contributes nothing rather than erroring, so its own
// absence diagnostic is rolled back and kept aside. The access stays symbolic when a member is not
// ground enough to decide whether it carries K, and reports absence only when no member carries it.
func (e *typeEvaluator) reduceIndexIntersection(tgt *soltype.IntersectionType, idx soltype.Type, exact bool) soltype.Type {
	var resolved []soltype.Type
	var absentErrs []SolverError
	anySymbolic := false
	for _, m := range tgt.Types {
		before := len(e.errs)
		r := e.reduceIndex(m, idx, exact)
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
		return &soltype.IndexType{Target: tgt, Index: idx, Exact: exact}
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
func (e *typeEvaluator) reduceIndexAlias(op *soltype.AliasType, index soltype.Type, exact bool) soltype.Type {
	symbolic := &soltype.IndexType{Target: op, Index: index, Exact: exact}
	key := soltype.PrintQualified(op)
	if e.active.Contains(key) || e.depth <= 0 {
		return symbolic
	}
	body := e.ctx.expandAlias(op)
	if _, unresolved := body.(*soltype.ErrorType); unresolved {
		return symbolic
	}
	e.active.Add(key)
	e.depth--
	result := e.reduceIndex(body, index, exact)
	e.active.Remove(key)
	e.depth++
	return result
}

// indexObject reduces `obj[key]` for a ground object. A string-literal key selects the named
// member's read type — a property's or getter's declared type, a method's callable value — and a
// key the object carries no member for records an UnknownObjectKeyError and reduces to the error
// sentinel. A non-string-literal index, such as a bare `string` primitive, selects no single
// member yet. An index signature reads it once mapped types land (M9 PR4), so the access stays
// symbolic until then.
func (e *typeEvaluator) indexObject(obj *soltype.ObjectType, index soltype.Type, exact bool) soltype.Type {
	name, ok := strLitName(index)
	if !ok {
		return &soltype.IndexType{Target: obj, Index: index, Exact: exact}
	}
	if _, found := obj.Member(name); !found {
		e.errs = append(e.errs, &UnknownObjectKeyError{Object: obj, Key: name})
		return &soltype.ErrorType{}
	}
	read, hasValue, _ := memberReadContribution(obj, name)
	if !hasValue {
		// The member is a write-only setter, which exposes no readable value. Leave the access
		// symbolic rather than resolving a write slot to a read type.
		return &soltype.IndexType{Target: obj, Index: index, Exact: exact}
	}
	return read
}

// indexTuple reduces `tup[n]` for a ground tuple. A numeric-literal key selects the element at
// that position. An index outside `[0, len)`, or a non-integer or negative literal, records a
// TupleIndexOutOfRangeError and reduces to the error sentinel. A non-numeric-literal index has no
// positional slot to select, so the access stays symbolic.
func (e *typeEvaluator) indexTuple(tup *soltype.TupleType, index soltype.Type, exact bool) soltype.Type {
	lit, ok := index.(*soltype.LitType)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Exact: exact}
	}
	num, ok := lit.Lit.(*soltype.NumLit)
	if !ok {
		return &soltype.IndexType{Target: tup, Index: index, Exact: exact}
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
func (e *typeEvaluator) reduceTemplateLit(t *soltype.TemplateLitType) soltype.Type {
	interpChoices := make([][]soltype.Type, len(t.Interps))
	combinations := 1
	for i, interp := range t.Interps {
		reduced := e.groundOperand(interp)
		if u, ok := reduced.(*soltype.UnionType); ok {
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
		parts = append(parts, buildTemplatePart(t.Quasis, combo))
	}
	return newUnion(nil, parts, false)
}

// buildTemplatePart assembles one cartesian-product combination into a single template result.
// It interleaves the fixed segments with the combination's interpolations: a string-representable
// literal folds into the surrounding text, while any other interpolation closes the accumulated
// segment and carries through as a residual interpolation. A combination whose interpolations all
// fold collapses to a lone string-literal type; one carrying an abstract interpolation stays a
// `TemplateLitType`. Quasis holds one more entry than the combination, so the loop reads
// combo[i] between quasi i and quasi i+1.
func buildTemplatePart(quasis []string, combo []soltype.Type) soltype.Type {
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
		if i >= len(combo) {
			continue
		}
		if s, ok := stringifyLit(combo[i]); ok {
			current += s
			continue
		}
		newQuasis = append(newQuasis, current)
		current = ""
		newInterps = append(newInterps, combo[i])
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

// reduceStringMapping reduces an intrinsic string operator such as `Uppercase<T>` over its operand.
// A string-literal operand maps to the transformed literal — `Uppercase<"abc">` ⇒ `"ABC"` — and a
// union operand distributes member-wise, so `Uppercase<"a" | "b">` ⇒ `"A" | "B"`. The operand is
// first grounded, so a named alias expands to its body. An operand that is not a string literal,
// such as a type parameter, keeps the operator symbolic as a `StringMappingType` rebuilt around the
// grounded operand.
func (e *typeEvaluator) reduceStringMapping(kind soltype.StringMappingKind, operand soltype.Type) soltype.Type {
	reduced := e.groundOperand(operand)
	switch op := reduced.(type) {
	case *soltype.UnionType:
		parts := make([]soltype.Type, len(op.Types))
		for i, m := range op.Types {
			parts[i] = e.reduceStringMapping(kind, m)
		}
		return newUnion(nil, parts, false)
	case *soltype.LitType:
		if s, ok := op.Lit.(*soltype.StrLit); ok {
			return strLitType(applyStringMapping(kind, s.Value))
		}
	}
	return &soltype.StringMappingType{Kind: kind, Operand: reduced}
}

// applyStringMapping transforms one string by the named intrinsic operator. Uppercase and Lowercase
// map every character; Capitalize and Uncapitalize map only the first, leaving the rest unchanged.
func applyStringMapping(kind soltype.StringMappingKind, s string) string {
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

// isResidualOp reports whether t is an unreduced type-level operator node at its top level — a
// `keyof`, an indexed access, a conditional, a `...P` tuple-spread element, a template literal whose
// interpolation stayed abstract, or an intrinsic string operator over an abstract operand. The
// evaluator consults it to stop re-reducing an operand whose reduction stayed symbolic.
func isResidualOp(t soltype.Type) bool {
	switch t.(type) {
	case *soltype.KeyofType, *soltype.IndexType, *soltype.CondType, *soltype.RestSpreadType,
		*soltype.TemplateLitType, *soltype.StringMappingType:
		return true
	}
	return false
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

// containsResidualOp reports whether t holds any unreduced type-level operator node — a `keyof`,
// an indexed access, or a tuple spread. constrain consults it to decide whether a reduced operator
// fully grounded: a result with no residual is safe to recurse on, while one that still carries a
// `keyof`, a `T[K]`, or a `[...T, x]` — an unexpanded type parameter or a budget-truncated
// expanding alias — must not, since re-reducing it would loop.
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

// containsFreeVar reports whether t holds any type variable or skolem — an abstract leaf that makes
// t non-ground. A conditional consults it to decide whether its Check and Extends are concrete
// enough to probe `Check <: Extends`. A conditional whose Check is a bare type parameter stays
// symbolic, since that parameter is a free variable.
func containsFreeVar(t soltype.Type) bool {
	f := &freeVarFinder{}
	t.Accept(f, soltype.Positive)
	return f.found
}

// freeVarFinder is the walking visitor behind containsFreeVar. It flags the first type variable or
// skolem it reaches and skips that node's children, since one occurrence is enough.
type freeVarFinder struct{ found bool }

func (f *freeVarFinder) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	switch t.(type) {
	case *soltype.TypeVarType, *soltype.SkolemType:
		f.found = true
		return soltype.EnterResult{SkipChildren: true}
	}
	return soltype.EnterResult{}
}

func (f *freeVarFinder) ExitType(t soltype.Type, pol soltype.Polarity) soltype.Type {
	return t
}
