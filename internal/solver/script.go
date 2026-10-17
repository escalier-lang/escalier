package solver

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/provenance"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// InferScript infers a script. A script is a source file whose top-level statements
// run in source order with function-body semantics, the bin/ counterpart to a
// library module. It returns the populated script Scope, the Info side table, and any
// SolverErrors. The returned scope is a child of the run's ambient scope, which
// layers the ambient builtins over the prelude package's exports and the operator
// table.
//
// A module and a script differ in how their top-level declarations relate. InferModule
// dependency-orders mutually-visible top-level declarations through the dep graph. A
// script is instead one straight-line body. Its bindings are linear, and each
// statement sees only the ones before it, exactly as inside a function. So liveness,
// alias tracking, and the `mut` transition rules all apply to a script's top-level
// statements. A module skips those at top level, where c.fn is nil and every
// transition entry point is a no-op.
//
// It mirrors the old checker's InferScript in internal/checker/infer_script.go:
//
//  1. wrap the script's statements in an ast.Block;
//  2. push a fresh funcCtx so c.fn is non-nil and the transition checker is live;
//  3. run runLivenessPrePass over that block to rename the body's variable nodes and
//     seed the alias/liveness tables;
//  4. walk the block in source order through inferBlock.
//
// M4 shipped every building block this uses: the pre-pass, the per-body funcCtx, the
// alias tracker, and the transition checker. The only new code is this entry point
// that runs them over a script body. There is no new inference.
//
// The pushed funcCtx carries no async flag and a nil node. A top-level `await` is
// rejected the same way it is at module top level. There is no enclosing function to
// mark `async`, because a script has none. inferStmt routes a top-level `return` into
// the funcCtx's returns list. The script never joins that list, so the return is
// accepted and discarded. The old checker's inferStmt applies the same no-op to a
// script-level return.
//
// source supplies `std:prelude` the way it does for a module, since a script's rules
// name the same `Array` and `Promise` a module's do.
func InferScript(script *ast.Script, source ModuleSource) (*Scope, *Info, []SolverError) {
	return inferScriptWithGroups(script, source, nil, nil, Options{})
}

// inferScriptWithGroups is InferScript with the groups a stdlib directory adds, the
// script counterpart to inferModuleWithGroups. opts adjusts the run.
func inferScriptWithGroups(
	script *ast.Script,
	source ModuleSource,
	groupSource GroupSource,
	groups PackageGroups,
	opts Options,
) (*Scope, *Info, []SolverError) {
	c := newChecker()
	c.source = source
	c.groupSource = groupSource
	c.groups = groups
	c.noAmbientNames = opts.NoAmbientNames
	c.bindAmbientExports()
	return c.inferScriptIn(c.ambientScope().Child(), script)
}

// InferScriptInLib infers script against the scope a library module's run produced, so
// the script reads that module's top-level declarations without importing them. It
// returns what InferScript returns, with the diagnostics covering the script alone.
//
// The script scope is a child of lib.Scope, so a name the script does not declare
// resolves to the library's binding, and then to the prelude. forScript builds the
// checker the walk runs on, carrying the library's run forward. Two scripts checked
// against one library therefore share a Context, so a constraint one puts on a library
// binding is visible to the next.
//
// lib must be a result InferModuleWithSource or InferModuleAgainstStdlib returned. A
// script with no library goes through InferScript, which parents it to the prelude.
func InferScriptInLib(script *ast.Script, lib *ModuleResult) (*Scope, *Info, []SolverError) {
	c := lib.checker.forScript(scriptPkgURI(script))
	return c.inferScriptIn(lib.Scope.Child(), script)
}

// scriptPkgURI is the prefix the nominal registries key one script's declarations
// under. Scripts checked against one library share that run's registries, so each
// needs a prefix of its own for two of them to declare the same class name. The
// source id is what names the file.
func scriptPkgURI(script *ast.Script) string {
	return "script:" + strconv.Itoa(script.Span().SourceID)
}

// inferScriptIn walks script's statements into scope, the body both script entry
// points share. scope is the freshly created scope the script's own bindings land
// in, already parented to whatever the script resolves free names through.
func (c *checker) inferScriptIn(scope *Scope, script *ast.Script) (*Scope, *Info, []SolverError) {
	// A script's statements form one linear body, so give them the same per-body
	// context a function body gets. pushFuncCtx makes c.fn non-nil. The transition
	// checker keys off c.fn, and runLivenessPrePass writes its liveness and alias state
	// onto it. The node is nil because a script has no enclosing function. scope's
	// parent chain is what the pre-pass resolves outer names against. There is no
	// enclosing context to restore, so the returned previous one is discarded.
	scriptBody := &ast.Block{Stmts: script.Stmts, Span: script.Span()}
	c.scriptScope = scope
	c.pushFuncCtx(false, nil, 0)
	c.runLivenessPrePass(scope, nil, nil, nil, nil, scriptBody)

	// Walk the body through inferBlock, the same source-order statement walker a
	// function body uses, at level 0. inferVarDecl types each initializer one level
	// deeper and generalizes back to 0, so a top-level binding is a generalized local
	// exactly like one in a function body walked at its level. inferBlock's tail value
	// and divergence flag are discarded, just as inferFunc discards them. A script,
	// like a function body, produces no value from its last statement.
	c.inferBlock(scope, 0, scriptBody)
	// A script body runs with function-body semantics, so the move engine applies
	// here too. With the whole body walked, replay the recorded reads against the
	// consumed lattice to report use-after-move.
	c.checkUseAfterMoves()
	// Every class the script declares is inferred, so each superclass edge and body is
	// final. Check the members each subclass redeclares against the ones they override.
	c.settleQueuedVariance()
	c.checkQueuedInheritedMembers()
	c.checkQueuedImplements()
	// Every function expression the script wrote has been typed, so each one's return type is
	// final. Report each function whose return type no finite value inhabits.
	c.checkCanReturn()
	c.checkRigidLifetimes()
	// The script's top level is the outermost body, so every read off an inferred receiver
	// it or its functions made is decided here.
	c.pendingRecvReads = append(c.pendingRecvReads, c.fn.inferredRecvReads...)
	c.checkPendingRecvReads()

	return scope, c.info, c.errs
}

// bindScriptAlias infers a `type` alias declared at a script's top level and binds its name
// in scope. It runs the steps the module driver runs over a component, over a component of
// one alias:
//
//  1. preBindAlias registers the alias and binds its name, so the body can name itself.
//  2. inferAliasBody resolves the body.
//  3. checkProductive and markPhantomParams mark the AliasDef, which constrain reads.
//  4. runDeferredArgBounds checks the bounds on the type arguments the body wrote.
//
// A script is source-order, so a reference to an alias declared later is unbound. An alias
// whose name scope already binds as a type reports DuplicateDeclarationError and binds
// nothing.
func (c *checker) bindScriptAlias(scope *Scope, lvl int, decl *ast.TypeDecl) {
	// preBindAlias registers the alias under its name for the whole run, so a second
	// registration would replace the first's definition under every value already typed by
	// it. scope is the script's root, where every script-level type binds, so an earlier
	// declaration of the name is one of its own bindings.
	if prev, ok := scope.OwnType(decl.Name.Name); ok {
		if prevDecl, isDecl := typeBindingDecl(prev); isDecl {
			c.report(&DuplicateDeclarationError{Decl: decl, Previous: prevDecl, Name: decl.Name.Name})
			return
		}
	}
	sh := c.preBindAlias(scope, lvl+1, decl, "")
	if sh == nil {
		return
	}
	shells := []*aliasShell{sh}
	prevBuilding := c.ctx.buildingAliases
	c.ctx.buildingAliases = set.FromSlice([]string{sh.qname})
	c.deferArgBounds = true
	c.inferAliasBody(sh)
	c.deferArgBounds = false
	c.checkProductive(shells)
	markPhantomParams(shells)
	c.reportPhantomParams(shells)
	c.ctx.buildingAliases = prevBuilding
	c.runDeferredArgBounds()
}

// typeBindingDecl returns the declaration that introduced b, and false when b records none.
func typeBindingDecl(b TypeBinding) (ast.Decl, bool) {
	if len(b.Sources) == 0 {
		return nil, false
	}
	np, ok := b.Sources[0].(*ast.NodeProvenance)
	if !ok {
		return nil, false
	}
	decl, ok := np.Node.(ast.Decl)
	return decl, ok
}

// bindScriptFunc infers a `declare fn` written at a script's top level and binds its
// generalized type in scope. A name scope already binds to an earlier `declare fn` gains
// this one as another overload arm, and an arm whose parameters match an earlier arm's
// reports DuplicateOverloadError. A name scope already binds to an earlier `fn` with a
// body reports ScriptOverloadWithBodyError and is rebound to this declaration alone.
func (c *checker) bindScriptFunc(scope *Scope, lvl int, decl *ast.FuncDecl) {
	name := decl.Name.Name
	t, src := c.inferFuncDecl(scope, lvl+1, decl)
	sc := c.generalize(t, lvl)
	c.recordType(decl.Name, schemeType(sc))
	b := ValueBinding{Schemes: []TypeScheme{sc}, Sources: []provenance.Provenance{src}}
	if prev, prevDecl := ownFuncDecl(scope, name); prevDecl != nil && !c.checkScriptOverloadWithBody(decl, prevDecl) {
		c.checkDistinctArm(decl, t, prev)
		b.Schemes = append(slices.Clone(prev.Schemes), b.Schemes...)
		b.Sources = append(slices.Clone(prev.Sources), b.Sources...)
	}
	scope.defineValue(name, b)
}

// bindScriptFuncWithBody infers decl, a `fn` with a body written at a script's top level
// as statement s, and binds its generalized type in scope. The body's captures of the
// script's earlier bindings take the alias, borrow, and move checks a `val name = fn …`
// closure's captures take. The name is bound to a fresh variable while the body is walked,
// so the body can call itself. A name scope already binds to an earlier function
// declaration reports ScriptOverloadWithBodyError, and decl rebinds it.
func (c *checker) bindScriptFuncWithBody(scope *Scope, lvl int, s ast.Stmt, decl *ast.FuncDecl) {
	name := decl.Name.Name
	if _, prevDecl := ownFuncDecl(scope, name); prevDecl != nil {
		c.checkScriptOverloadWithBody(decl, prevDecl)
	}

	sources := []provenance.Provenance{&ast.NodeProvenance{Node: decl}}
	// The recursive binding carries no VarID, so the body's capture analysis does not count
	// the name as a capture of a script local. The body's pre-pass gives the name a VarID of
	// its own inside the body instead, which seedSelfCaptureEdges reads.
	self := c.freshAt(lvl + 1)
	scope.defineValue(name, ValueBinding{Schemes: []TypeScheme{monoScheme(self)}, Sources: sources})

	// The calls of the name take the capture loans, so writing the declaration takes none.
	c.markNamedClosure(decl)
	t, _ := c.inferFuncDecl(scope, lvl+1, decl)
	c.recordCaptureLoans(scope, decl)
	c.constrain(decl, t, self)
	scheme := c.generalize(t, lvl)
	c.recordType(decl.Name, schemeType(scheme))
	b := ValueBinding{Schemes: []TypeScheme{scheme}, Sources: sources, Kind: ast.ValKind, VarID: decl.VarID}
	scope.defineValue(name, b)
	c.trackFuncDeclCaptures(scope, decl, bindingType(b), s)
}

// trackFuncDeclCaptures records what binding decl's name in the current body sets up for the
// alias, move, and borrow checks. The name holds a fresh value, so it aliases nothing. It
// joins the alias set of each local the body captures and holds a capture edge to each one,
// so a call of the name takes the capture loans. bindingT is the name's type. s is the
// statement decl is written as.
func (c *checker) trackFuncDeclCaptures(scope *Scope, decl *ast.FuncDecl, bindingT soltype.Type, s ast.Stmt) {
	if c.fn == nil || decl.VarID <= 0 {
		return
	}
	id := liveness.VarID(decl.VarID)
	c.recordVarIDType(id, bindingT)
	if c.fn.aliases != nil {
		c.fn.aliases.NewValue(id, aliasMutability(isMutableType(bindingT)))
		c.trackCapturedAliases(scope, decl, id)
	}
	if c.fn.eagerBorrowGraph != nil {
		c.clearEagerSubtree(id, nil)
		c.addCaptureEdges(id, nil, decl)
	}
	if ref, ok := c.fn.stmtToRef[s]; ok {
		c.flushBorrowDirty(ref)
		c.dropLoansHeldBy(id)
	}
}

// ownFuncDecl returns scope's own binding of name and the function declaration that
// introduced it. The declaration is nil when scope does not bind name itself or a
// declaration of another kind introduced it.
func ownFuncDecl(scope *Scope, name string) (ValueBinding, *ast.FuncDecl) {
	prev, ok := scope.OwnValue(name)
	if !ok {
		return ValueBinding{}, nil
	}
	prevDecl, _ := bindingDecl(prev).(*ast.FuncDecl)
	return prev, prevDecl
}

// checkScriptOverloadWithBody reports ScriptOverloadWithBodyError when decl redeclares the
// function prevDecl declared and either of the two has a body. It returns whether it
// reported.
func (c *checker) checkScriptOverloadWithBody(decl, prevDecl *ast.FuncDecl) bool {
	if decl.Body == nil && prevDecl.Body == nil {
		return false
	}
	c.report(&ScriptOverloadWithBodyError{Decl: decl, Previous: prevDecl})
	return true
}

// checkDistinctArm reports DuplicateOverloadError when the overload arm t declared by decl
// has the same parameters as one of the arms prev already binds.
func (c *checker) checkDistinctArm(decl *ast.FuncDecl, t soltype.Type, prev ValueBinding) {
	ft, ok := t.(*soltype.FuncType)
	if !ok {
		return
	}
	for i, sc := range prev.Schemes {
		pt, ok := schemeType(sc).(*soltype.FuncType)
		if !ok || !equallySpecific(pt, ft) {
			continue
		}
		prevDecl, _ := bindingDecl(ValueBinding{Sources: prev.Sources[i:]}).(ast.Decl)
		if prevDecl == nil {
			continue
		}
		c.report(&DuplicateOverloadError{Decl: decl, Previous: prevDecl, Name: decl.Name.Name})
		return
	}
}
