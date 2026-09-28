package solver

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
)

// InferScript infers a script. A script is a source file whose top-level statements
// run in source order with function-body semantics, the bin/ counterpart to a
// library module. It returns the populated script Scope, the Info side table, and any
// SolverErrors. The returned scope is a child of the run's prelude scope, so the
// prelude package's exports resolve through the parent and the operator table
// through its parent in turn.
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
	c := newChecker()
	c.source = source
	return c.inferScriptIn(c.preludeScope().Child(), script)
}

// InferScriptInLib infers script against the scope a library module's run produced, so
// the script reads that module's top-level declarations without importing them. It
// returns what InferScript returns, with the diagnostics covering the script alone.
//
// The script scope is a child of lib.Scope, so a name the script does not declare
// resolves to the library's binding, and then to the prelude. forScript builds the
// checker that carries the library's run on, which is what makes the library's named
// types usable here. Two scripts checked against one library share a Context, so a
// constraint one puts on a library binding is visible to the next.
//
// lib must be a result InferModuleWithSource or InferModuleAgainstStdlib returned. A
// script with no library goes through InferScript, which parents it to the prelude.
func InferScriptInLib(script *ast.Script, lib *ModuleResult) (*Scope, *Info, []SolverError) {
	c := lib.checker.forScript(scriptPkgURI(script))
	// Report shadowing before the walk, so a shadowed name is named once against its
	// declaration rather than once per use. The walk then proceeds as if the name were
	// the script's alone, which is how the rest of the script reads.
	c.reportShadowedLibDecls(script, lib.Scope)
	return c.inferScriptIn(lib.Scope.Child(), script)
}

// reportShadowedLibDecls reports every top-level declaration in script that reuses a
// name lib declares. lib is the library module's own scope, so a prelude name reached
// through its parent is not consulted and a script may still declare its own `Array`.
//
// A script's top-level declarations are val, var, class, and enum. A top-level fn or
// type is rejected before this matters, by the function-body rule a script body runs
// under. See the DeclStmt arm of inferStmt.
func (c *checker) reportShadowedLibDecls(script *ast.Script, lib *Scope) {
	for _, stmt := range script.Stmts {
		declStmt, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		names, ns := shadowableNames(declStmt.Decl)
		for _, name := range names {
			if prev, shadowed := ns.libDecl(lib, name); shadowed {
				c.report(&ShadowedLibDeclError{
					Decl:     declStmt.Decl,
					Previous: prev,
					Name:     name,
				})
			}
		}
	}
}

// declPositions is where a declaration's name can be written, which is what decides
// whether it hides a library name of the same spelling. resolveIdentPath reads the value
// map and then the namespace map, both walking the parent chain, so those two are the one
// term position: a script `val Color` hides a library `enum Color`'s variants. A type
// annotation reads only the type map, so typ is separate.
type declPositions struct {
	term bool
	typ  bool
}

// libDecl returns the library declaration of name in one of d's positions, and whether
// the library declares it at all. The node is nil for a library binding that carries no
// source, which leaves the diagnostic with nothing to relate.
func (d declPositions) libDecl(lib *Scope, name string) (ast.Node, bool) {
	if d.term {
		if b, ok := lib.ownValue(name); ok {
			return sourceDecl(b.Sources), true
		}
		if _, ok := lib.namespaces[name]; ok {
			return nil, true
		}
	}
	if d.typ {
		if b, ok := lib.ownType(name); ok {
			return sourceDecl(b.Sources), true
		}
	}
	return nil, false
}

// shadowableNames returns the names decl binds, sorted so two shadowed names in one
// destructuring pattern report in a stable order, along with the positions those names
// can be written in. A declaration that binds no name returns none.
func shadowableNames(decl ast.Decl) ([]string, declPositions) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		names := ast.FindBindings(d.Pattern).ToSlice()
		slices.Sort(names)
		return names, declPositions{term: true}
	case *ast.ClassDecl:
		// A class binds its constructor as a value and its instances as a type.
		return declaredName(d.Name), declPositions{term: true, typ: true}
	case *ast.EnumDecl:
		// An enum binds its union as a type and its variant constructors under a
		// namespace, which shares a position with the value map.
		return declaredName(d.Name), declPositions{term: true, typ: true}
	default:
		return nil, declPositions{}
	}
}

// declaredName wraps a declaration's identifier as the one-element list shadowableNames
// returns, or none when the parser left the declaration unnamed.
func declaredName(ident *ast.Ident) []string {
	if ident == nil || ident.Name == "" {
		return nil
	}
	return []string{ident.Name}
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
	c.pushFuncCtx(false, nil, 0)
	c.runLivenessPrePass(scope, nil, nil, scriptBody)

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
	c.checkQueuedInheritedMembers()
	// Every function expression the script wrote has been typed, so each one's return type is
	// final. Report each function whose return type no finite value inhabits.
	c.checkCanReturn()

	return scope, c.info, c.errs
}
