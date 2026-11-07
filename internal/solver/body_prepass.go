package solver

import (
	"cmp"
	"maps"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// The per-body pre-pass. runLivenessPrePass renames a function body's bindings to VarIDs,
// builds its CFG and liveness, and opens its ownership event table before the body is
// walked. The helpers below collect the variables a body captures and the module-level
// bindings it reaches, which the rename defines as extra parameters of the body.

// isValueType reports whether the type is a primitive, literal, regex, `undefined` or `null`
// type, or a union of such types. Value types have copy semantics. Assigning one to another
// variable creates an independent copy, so no borrow or capture of it is tracked. A
// function type reports false, since a binding that holds a closure carries the closure's
// captured borrows, and its callers track loans through that binding.
//
// Every caller passes a binding's coalesced display type, which carries
// no inference-variable indirection to prune. A bare *soltype.TypeVarType falls through to
// false and is classified as a reference. Mis-seeing a value type as a reference only adds
// a spurious borrow edge or loan, so it over-reports rather than under-reports.
func isValueType(t soltype.Type) bool {
	switch p := t.(type) {
	case *soltype.PrimType, *soltype.LitType, *soltype.RegexType, *soltype.UndefinedType, *soltype.NullType:
		return true
	case *soltype.UnionType:
		for _, member := range p.Types {
			if !isValueType(member) {
				return false
			}
		}
		return len(p.Types) > 0
	}
	return false
}

// lifetimeEscapedToStatic reports whether a borrow's lifetime has been forced to
// outlive 'static — the escape constraint `v <: 'static`, which adds 'static as an
// UPPER bound. A literal *StaticLifetime escapes outright. A lower-bound 'static does
// not: it can arise from a join member and does not mean the borrow escaped, so the
// display-time forcedToStatic, which also matches a lower bound, would over-report.
func lifetimeEscapedToStatic(lt soltype.Lifetime) bool {
	switch lt := lt.(type) {
	case *soltype.StaticLifetime:
		return true
	case *soltype.LifetimeVar:
		return soltype.ContainsLifetime(lt.UpperBounds, soltype.Static)
	}
	return false
}

// bindingType returns a binding's coalesced display type, or nil for a binding with no
// definition.
func bindingType(b ValueBinding) soltype.Type {
	if len(b.Schemes) == 0 {
		return nil
	}
	return schemeType(b.Schemes[0])
}

// runLivenessPrePass runs name resolution, CFG construction, and liveness analysis
// on the function body about to be walked, populating the per-body state on c.fn. The
// rename pass records VarIDs in c.varIDs, which the walk's IdentPat and IdentExpr reads
// consult. paramTypes maps each parameter leaf's name to its soltype, which the pre-pass
// records into c.fn.varIDTypes.
//
// Must be called after parameters are bound in scope, so outer-scope names resolve, and
// before the body is walked.
//
// recv is the body's receiver, nil when the body has none. A receiver is not an entry in
// astParams, so the rename pass defines `self` as an extra parameter of the body.
//
// `closure` is the function whose body this is. It is nil for a script's top level, the one
// body that belongs to no function. enclosed reports whether the function is written inside
// an enclosing body.
//
//   - An enclosed function defines each variable it captures from the enclosing body as an
//     extra parameter of its body, so a place rooted at the capture is tracked inside the
//     closure. collectClosureCaptures says which variables those are, and records them for
//     closureCaptures to return.
//   - Any other function defines each module-level binding it reaches as an extra parameter,
//     so its moves and borrows are tracked inside the body. collectModuleCaptures says which
//     bindings those are.
//
// The returned map holds the VarID the rename assigned to each extra parameter, keyed by
// name: `self`, every captured variable, and every module-level name the body reaches.
func (c *checker) runLivenessPrePass(scope *Scope, astParams []*ast.Param, recv *ast.MethodReceiver, closure ast.Func, enclosed bool, paramTypes map[string]soltype.Type, body *ast.Block) map[string]liveness.VarID {
	// Build outer bindings from the scope chain. Every value binding accessible from
	// the current scope gets a negative VarID so the rename pass can distinguish
	// local from non-local variables.
	outerBindings := c.collectOuterBindings(scope)

	// A method's receiver is an ast.MethodReceiver on the function rather than an entry
	// in astParams, so `self` reaches the rename pass through its extraParamNames hook.
	// Without it every `self` in the body resolves as an outer name.
	var extraParams []string
	if recv != nil {
		extraParams = []string{"self"}
	}

	var captured []string
	// selfName is the name a function declaration's body calls itself by, empty for any other
	// body.
	selfName := ""
	// moduleFuncs holds the functions each module-level name among the extra parameters was
	// declared as, keyed by name.
	var moduleFuncs map[string][]ast.Func
	if closure != nil && !enclosed {
		captured, moduleFuncs = c.collectModuleCaptures(scope, closure, outerBindings)
		extraParams = append(extraParams, captured...)
	}
	if closure != nil && enclosed {
		captured, moduleFuncs = c.collectClosureCaptures(scope, closure, outerBindings)
		extraParams = append(extraParams, captured...)
		// A parameter of the same name shadows the function's own name for the whole body.
		if fd, ok := closure.(*ast.FuncDecl); ok && fd.Name != nil && !paramsBind(astParams, fd.Name.Name) {
			selfName = fd.Name.Name
			extraParams = append(extraParams, selfName)
		}
	}

	// Allocate this body's local VarIDs from the module-wide counter so they are
	// unique across every body in the run, then advance it past them. UniqueVarCount
	// is the number of locals this body defined, so the next body starts just after.
	firstID := liveness.VarID(c.varIDCounter)
	renameResult := liveness.RenameFrom(c.varIDs, astParams, *body, outerBindings, firstID, extraParams...)
	c.varIDCounter += renameResult.UniqueVarCount

	cfg := liveness.BuildCFG(c.varIDs, *body)
	livenessInfo := liveness.AnalyzeFunction(cfg)
	stmtToRef := liveness.BuildStmtToRef(cfg)

	varIDTypes := map[liveness.VarID]soltype.Type{}
	recordParamLeafTypes(c.varIDs, astParams, paramTypes, varIDTypes)
	selfVarID := renameResult.ExtraParamVarIDs["self"]
	if selfVarID > 0 {
		recordBindingType(scope, "self", selfVarID, varIDTypes)
	}
	captureVarIDs := set.NewSet[liveness.VarID]()
	for _, name := range captured {
		id := renameResult.ExtraParamVarIDs[name]
		recordBindingType(scope, name, id, varIDTypes)
		captureVarIDs.Add(id)
	}

	c.fn.stmtToRef = stmtToRef
	c.fn.varIDNames = renameResult.VarIDNames
	c.fn.varIDTypes = varIDTypes
	c.startOwnershipEvents(renameResult.VarIDNames, cfg, livenessInfo)
	c.fn.exprRefs = exprStmtRefs(cfg)

	c.fn.cfg = cfg
	c.fn.destructureInits = set.NewSet[ast.Node]()

	c.fn.paramVarIDs = collectParamVarIDs(c.varIDs, astParams)
	if selfVarID > 0 {
		c.fn.paramVarIDs.Add(selfVarID)
	}
	c.fn.paramVarIDs = c.fn.paramVarIDs.Union(captureVarIDs)
	c.fn.captureVarIDs = captureVarIDs
	c.fn.capturesModule = closure != nil && !enclosed

	// A call of a recursive closure's own name inside its body runs the closure, the way a call
	// of the closure's name outside it does. So the name holds a loan of each local the closure
	// captures. A capture with value semantics holds its own copy, as recordClosureCaptures
	// decides.
	if selfName != "" {
		var captures []liveness.CaptureInfo
		for _, capture := range c.closureCaptures(closure) {
			if b, found := scope.GetValue(capture.Name); found && !isValueType(bindingType(b)) {
				captures = append(captures, capture)
			}
		}
		c.seedCaptureLoans(closure, captures, renameResult.ExtraParamVarIDs[selfName], renameResult.ExtraParamVarIDs)
	}
	// A call of a module function in this body runs what that function reaches, so the
	// function's name holds a loan of each module-level binding it reaches. Each name is
	// seeded in sorted order, so the seeds are in the same order on every run.
	for _, name := range slices.Sorted(maps.Keys(moduleFuncs)) {
		for _, fn := range moduleFuncs[name] {
			c.seedCaptureLoans(fn, c.moduleCaptures[fn], renameResult.ExtraParamVarIDs[name], renameResult.ExtraParamVarIDs)
		}
	}

	return renameResult.ExtraParamVarIDs
}

// seedCaptureLoans adds to the body's event table a seed for each capture in `captures` of
// the function `closure`. The local `holder` holds each seed. ids maps each name the body
// defined as an extra parameter to its VarID. A capture with no VarID in ids, or with the
// VarID of `holder`, gets no seed. A seed is a borrow of the whole captured local, mutable when
// the function writes it.
func (c *checker) seedCaptureLoans(closure ast.Func, captures []liveness.CaptureInfo, holder liveness.VarID, ids map[string]liveness.VarID) {
	t := c.eventTable()
	if t == nil || holder <= 0 {
		return
	}
	for _, capture := range captures {
		id, ok := ids[capture.Name]
		if !ok || id == holder {
			continue
		}
		// The seed is in the state before the body's first statement, so it attaches to no
		// CFG point.
		t.seeds = append(t.seeds, &borrowEvent{
			flowBase: flowBase{
				eventBase: eventBase{place: movePlace{root: id}, node: closure, at: liveness.StmtRef{BlockID: -1, StmtIdx: -1}},
				holder:    eventHolder{target: placeTarget{root: holder}},
			},
			borrow: borrowKindOf(capture.IsMutable),
		})
	}
}

// paramsBind reports whether a leaf of one of params binds name.
func paramsBind(params []*ast.Param, name string) bool {
	found := false
	for _, param := range params {
		ast.ForEachLeafBinding(param.Pattern, func(leaf string, _ ast.Node) {
			found = found || leaf == name
		})
	}
	return found
}

// collectModuleCaptures returns the names of the module-level bindings fn reaches, sorted,
// and the functions each module-level name among them was declared as, keyed by name. fn
// reaches a binding its body reads or writes, and every binding a module function it names
// reaches. A function whose own walk has not recorded what it reaches contributes only
// itself. fn's own entry is recorded before its body is walked, so a call of fn inside its
// body reads it.
//
// Only a binding a `val` or `var` declares is reached. A class, an enum, or a `declare fn`
// names a declaration rather than a value fn can move. A binding with value semantics is
// left out too, since a copy of it cannot see a later change to the binding. So is a name one of fn's parameters binds, since the
// parameter shadows it for the whole body.
func (c *checker) collectModuleCaptures(scope *Scope, fn ast.Func, outerBindings map[string]liveness.VarID) ([]string, map[string][]ast.Func) {
	reached := map[string]bool{}
	funcs := map[string][]ast.Func{}
	for _, capture := range liveness.ClosureCaptures(c.varIDs, fn, outerBindings) {
		b, found := scope.GetValue(capture.Name)
		if !found || !b.ModuleLevel {
			continue
		}
		if callees := moduleFuncsOf(b); len(callees) > 0 {
			funcs[capture.Name] = callees
			continue
		}
		if !declaredByVarDecl(b) {
			continue
		}
		if isValueType(bindingType(b)) {
			continue
		}
		reached[capture.Name] = reached[capture.Name] || capture.IsMutable
	}
	for _, capture := range c.reachedThroughModuleFuncs(fn, funcs) {
		reached[capture.Name] = reached[capture.Name] || capture.IsMutable
	}

	own := make([]liveness.CaptureInfo, 0, len(reached))
	for name, mut := range reached {
		own = append(own, liveness.CaptureInfo{Name: name, IsMutable: mut})
	}
	slices.SortFunc(own, func(a, b liveness.CaptureInfo) int { return cmp.Compare(a.Name, b.Name) })
	if c.moduleCaptures == nil {
		c.moduleCaptures = map[ast.Func][]liveness.CaptureInfo{}
	}
	c.moduleCaptures[fn] = own
	names := make([]string, 0, len(reached)+len(funcs))
	for name := range reached {
		names = append(names, name)
	}
	for name := range funcs {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, funcs
}

// reachedThroughModuleFuncs returns the module-level bindings the functions in funcs reach,
// sorted by name. funcs holds the module functions fn names, keyed by name. A binding is
// written when any of those functions writes it. fn itself contributes nothing, and a name one
// of fn's parameters binds is left out.
func (c *checker) reachedThroughModuleFuncs(fn ast.Func, funcs map[string][]ast.Func) []liveness.CaptureInfo {
	reached := map[string]bool{}
	for _, callees := range funcs {
		for _, callee := range callees {
			if callee == fn {
				continue
			}
			for _, capture := range c.moduleCaptures[callee] {
				if !paramsBind(fn.Signature().Params, capture.Name) {
					reached[capture.Name] = reached[capture.Name] || capture.IsMutable
				}
			}
		}
	}

	out := make([]liveness.CaptureInfo, 0, len(reached))
	for name, mut := range reached {
		out = append(out, liveness.CaptureInfo{Name: name, IsMutable: mut})
	}
	slices.SortFunc(out, func(a, b liveness.CaptureInfo) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// declaredByVarDecl reports whether a `val` or `var` declaration introduced b.
func declaredByVarDecl(b ValueBinding) bool {
	for _, src := range b.Sources {
		if np, ok := src.(*ast.NodeProvenance); ok {
			if _, isVar := np.Node.(*ast.VarDecl); isVar {
				return true
			}
		}
	}
	return false
}

// moduleFuncsOf returns the functions a module-level binding was declared as, one per
// declaration in source order. Each is a `fn` declaration with a body or the function
// expression a `val` declaration is initialized with. A `var` can be reassigned to another
// function, so it yields none.
func moduleFuncsOf(b ValueBinding) []ast.Func {
	var fns []ast.Func
	for _, src := range b.Sources {
		np, ok := src.(*ast.NodeProvenance)
		if !ok {
			continue
		}
		switch decl := np.Node.(type) {
		case *ast.FuncDecl:
			if decl.Body != nil {
				fns = append(fns, decl)
			}
		case *ast.VarDecl:
			if fe, ok := decl.Init.(*ast.FuncExpr); ok && decl.Kind == ast.ValKind {
				fns = append(fns, fe)
			}
		}
	}
	return fns
}

// collectClosureCaptures returns the names of the variables `closure` captures from an
// enclosing body, sorted, and the functions each module-level name among them was declared
// as, keyed by name. A name only counts when the enclosing body tracks it, which a name
// bound in the prelude, or at module level without the body reaching it, is not.
//
// `closure` captures each name its body reads or writes. It also captures each module-level
// binding a module function it names reaches, since a call of that function inside
// `closure` runs it. Every capture is recorded for closureCaptures to return.
//
// liveness.ClosureCaptures renames the closure's body and the bodies nested in it. The
// pre-pass renames the closure's body again right after this, and each nested body is renamed
// by its own pre-pass before the walk reaches it.
func (c *checker) collectClosureCaptures(scope *Scope, closure ast.Func, outerBindings map[string]liveness.VarID) ([]string, map[string][]ast.Func) {
	captures := liveness.ClosureCaptures(c.varIDs, closure, outerBindings)

	funcs := map[string][]ast.Func{}
	for _, capture := range captures {
		b, found := scope.GetValue(capture.Name)
		if !found || !b.ModuleLevel || b.VarID <= 0 {
			continue
		}
		if callees := moduleFuncsOf(b); len(callees) > 0 {
			funcs[capture.Name] = callees
		}
	}
	for _, reached := range c.reachedThroughModuleFuncs(closure, funcs) {
		// A local of the enclosing body with the same name hides the module-level binding.
		if b, found := scope.GetValue(reached.Name); !found || !b.ModuleLevel {
			continue
		}
		captures = addCapture(captures, reached)
	}

	if c.closureCaptureInfo == nil {
		c.closureCaptureInfo = map[ast.Func][]liveness.CaptureInfo{}
	}
	c.closureCaptureInfo[closure] = captures

	var names []string
	for _, capture := range captures {
		if b, found := scope.GetValue(capture.Name); found && b.VarID > 0 {
			names = append(names, capture.Name)
		}
	}
	slices.Sort(names)
	return names, funcs
}

// addCapture returns captures with capture added. A name already present is kept once, and it
// is written when either entry writes it.
func addCapture(captures []liveness.CaptureInfo, capture liveness.CaptureInfo) []liveness.CaptureInfo {
	for i, existing := range captures {
		if existing.Name == capture.Name {
			captures[i].IsMutable = existing.IsMutable || capture.IsMutable
			return captures
		}
	}
	return append(captures, capture)
}

// closureCaptures returns the variables `closure` captures from enclosing scopes and whether it
// writes each one. A closure whose body has been walked returns what its pre-pass recorded.
// Any other closure is analyzed on the spot.
func (c *checker) closureCaptures(closure ast.Func) []liveness.CaptureInfo {
	if captures, ok := c.closureCaptureInfo[closure]; ok {
		return captures
	}
	return liveness.AnalyzeCaptures(c.varIDs, closure)
}

// recordExtraParamVarIDs copies the VarID of each extra parameter the pre-pass defined onto
// that name's binding, defining the copy in fnScope. The receiver's binding already lives in
// fnScope. A captured local's binding lives in an enclosing scope, and copying it leaves the
// enclosing body's VarID in place there.
func recordExtraParamVarIDs(fnScope *Scope, ids map[string]liveness.VarID) {
	for name, id := range ids {
		if b, found := fnScope.GetValue(name); found {
			b.VarID = int(id)
			fnScope.defineValue(name, b)
		}
	}
}

// collectParamVarIDs returns the VarID of every parameter leaf binding.
func collectParamVarIDs(varIDs *liveness.VarIDs, astParams []*ast.Param) set.Set[liveness.VarID] {
	ids := set.NewSet[liveness.VarID]()
	for _, param := range astParams {
		ast.ForEachLeafBinding(param.Pattern, func(_ string, leaf ast.Node) {
			if varID := varIDs.Of(leaf); varID > 0 {
				ids.Add(varID)
			}
		})
	}
	return ids
}

// recordParamLeafTypes records the type of each parameter leaf into varIDTypes, keyed by the
// VarID the rename gave the leaf. paramTypes is keyed by leaf name. An IdentPat parameter
// contributes the parameter itself, and a destructuring parameter contributes one entry per
// leaf. A destructured leaf records the type bindPattern resolved for it, including the
// `mut` or borrow wrapper its binding mode applied.
func recordParamLeafTypes(varIDs *liveness.VarIDs, astParams []*ast.Param, paramTypes map[string]soltype.Type, varIDTypes map[liveness.VarID]soltype.Type) {
	for _, param := range astParams {
		ast.ForEachLeafBinding(param.Pattern, func(name string, leaf ast.Node) {
			varID := varIDs.Of(leaf)
			if varID <= 0 {
				return
			}
			if t, ok := paramTypes[name]; ok {
				varIDTypes[varID] = t
			}
		})
	}
}

// recordBindingType records the type of name's binding in scope into varIDTypes under id,
// the VarID the rename assigned to name. A name with no binding or no type records nothing.
func recordBindingType(scope *Scope, name string, id liveness.VarID, varIDTypes map[liveness.VarID]soltype.Type) {
	if b, found := scope.GetValue(name); found {
		if t := bindingType(b); t != nil {
			varIDTypes[id] = t
		}
	}
}

// collectOuterBindings walks the scope chain and collects all value binding names,
// assigning each a unique negative VarID so the rename pass can tell a non-local
// reference from a local. Names within each scope are sorted before assignment so the
// ids are deterministic across runs. Go randomizes map iteration order.
//
// The root of every chain is the shared, immutable prelude, which this would re-walk
// and re-sort on every function body's pre-pass. preludeOuterNames memoizes the
// prelude's sorted names so only the mutable scopes above it are re-collected each
// time. The module scope above the prelude is NOT cached. It grows as later SCC
// components bind, so a cached snapshot would be stale for a body inferred after it.
func (c *checker) collectOuterBindings(scope *Scope) map[string]liveness.VarID {
	bindings := make(map[string]liveness.VarID)
	nextID := liveness.VarID(-1)
	add := func(names []string) {
		for _, name := range names {
			if _, exists := bindings[name]; !exists {
				bindings[name] = nextID
				nextID--
			}
		}
	}

	for s := scope; s != nil; s = s.parent {
		if s.parent == nil {
			add(c.preludeOuterNames(s)) // immutable prelude root: collected once, reused
		} else {
			add(sortedValueNames(s))
		}
	}

	return bindings
}

// preludeOuterNames returns the prelude root scope's sorted value names, computing
// them once per root and caching the result on the checker. The prelude is built once
// and never mutated during a run, so the cache is always fresh.
func (c *checker) preludeOuterNames(root *Scope) []string {
	if c.preludeNamesRoot != root {
		c.preludeNames = sortedValueNames(root)
		c.preludeNamesRoot = root
	}
	return c.preludeNames
}

// sortedValueNames returns a scope's own value binding names in sorted order.
func sortedValueNames(s *Scope) []string {
	names := make([]string, 0, len(s.values))
	for name := range s.values {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// recordParamVarIDs copies each parameter leaf's rename-assigned VarID onto its binding in
// fnScope, the way recordLeafVarIDs does for one pattern. It must run after the pre-pass,
// since the rename is what assigns the VarIDs.
func recordParamVarIDs(varIDs *liveness.VarIDs, fnScope *Scope, params []*ast.Param) {
	for _, p := range params {
		recordLeafVarIDs(varIDs, fnScope, p.Pattern)
	}
}

// recordLeafVarIDs copies the rename-assigned VarID of each leaf binding in pat onto that
// leaf's binding in scope. A leaf the rename gave no VarID, or one with no binding in scope,
// is skipped.
func recordLeafVarIDs(varIDs *liveness.VarIDs, scope *Scope, pat ast.Pat) {
	ast.ForEachLeafBinding(pat, func(name string, leaf ast.Node) {
		varID := varIDs.Of(leaf)
		if varID <= 0 {
			return
		}
		if b, found := scope.GetValue(name); found {
			b.VarID = int(varID)
			scope.defineValue(name, b)
		}
	})
}
