package liveness

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
)

// scope is internal to the rename pass. It tracks name-to-VarID mappings
// during the top-to-bottom walk. It is discarded after Rename() returns.
type scope struct {
	parent   *scope
	bindings map[string]VarID
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, bindings: make(map[string]VarID)}
}

func (s *scope) lookup(name string) (VarID, bool) {
	if id, ok := s.bindings[name]; ok {
		return id, true
	}
	if s.parent != nil {
		return s.parent.lookup(name)
	}
	return 0, false
}

// RenameResult holds the output of the rename pass for a function body.
// VarIDs are stored directly on AST nodes (IdentExpr, IdentPat, etc.).
type RenameResult struct {
	// UniqueVarCount is the number of distinct local variables found (for sizing
	// data structures in later phases).
	UniqueVarCount int

	// Errors contains any unresolved variable references found during
	// the rename pass.
	Errors []RenameError
}

// RenameError represents a variable use that could not be resolved to
// any in-scope binding.
type RenameError struct {
	Name string
	Span ast.Span
}

// renamer holds the mutable state of the rename pass.
type renamer struct {
	nextID VarID
	scope  *scope
	errors []RenameError
}

func newRenamer(outerBindings map[string]VarID) *renamer {
	s := newScope(nil)
	maps.Copy(s.bindings, outerBindings)
	return &renamer{
		nextID: 1, // local VarIDs start at 1
		scope:  s,
	}
}

func (r *renamer) freshID() VarID {
	id := r.nextID
	r.nextID++
	return id
}

func (r *renamer) pushScope() {
	r.scope = newScope(r.scope)
}

func (r *renamer) popScope() {
	r.scope = r.scope.parent
}

// define adds a binding to the current scope and returns the assigned VarID.
func (r *renamer) define(name string) VarID {
	id := r.freshID()
	r.scope.bindings[name] = id
	return id
}

// resolve looks up a name and returns its VarID, or reports an error.
func (r *renamer) resolve(name string, span ast.Span) VarID {
	if id, ok := r.scope.lookup(name); ok {
		return id
	}
	r.errors = append(r.errors, RenameError{Name: name, Span: span})
	return 0
}

// Rename walks a function body, assigns VarIDs to all local binding and
// use sites, and validates that all variable uses resolve to in-scope
// bindings. VarIDs are set directly on AST nodes (IdentExpr.VarID,
// IdentPat.VarID, etc.). Module-level and prelude bindings are supplied
// via outerBindings so that free variables can be distinguished from
// truly unresolved names.
//
// Only variable bindings and uses are processed. Type annotations are
// skipped — they contain type names (resolved via the checker's type
// namespace), not variable references, and are irrelevant to liveness
// and alias analysis.
//
// params are the function's parameters — they are assigned local VarIDs
// and are in scope for the entire body.
//
// After this function returns, the internal scope stack is discarded.
// All downstream phases read VarIDs directly from AST nodes.
func Rename(params []*ast.Param, body ast.Block, outerBindings map[string]VarID) *RenameResult {
	r := newRenamer(outerBindings)

	// Process function parameters — they are in scope for the entire body.
	// Parameters share the root scope with outerBindings. This is fine because
	// the VarID sign convention already distinguishes them: parameters get
	// positive IDs (assigned by define()), outer bindings have negative IDs.
	for _, param := range params {
		r.renamePat(param.Pattern)
	}

	// Process the function body.
	r.renameBlock(body)

	return &RenameResult{
		UniqueVarCount: int(r.nextID) - 1, // count of local variables assigned
		Errors:         r.errors,
	}
}

// renameBlock processes a block of statements sequentially.
func (r *renamer) renameBlock(block ast.Block) {
	for _, stmt := range block.Stmts {
		r.renameStmt(stmt)
	}
}

func (r *renamer) renameStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		r.renameExpr(s.Expr)
	case *ast.DeclStmt:
		r.renameDecl(s.Decl)
	case *ast.ReturnStmt:
		if s.Expr != nil {
			r.renameExpr(s.Expr)
		}
	case *ast.ForInStmt:
		r.renameExpr(s.Iterable)
		// The loop variable and body are in a new scope.
		r.pushScope()
		r.renamePat(s.Pattern)
		r.renameBlock(s.Body)
		r.popScope()
	case *ast.ImportStmt:
		// Imports are module-level, handled via outerBindings.
	case *ast.ErrorStmt:
		// Nothing to do.
	}
}

func (r *renamer) renameDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		// Process the initializer first (it's evaluated before the binding
		// becomes visible), then bind the pattern.
		if d.Init != nil {
			r.renameExpr(d.Init)
		}
		r.renamePat(d.Pattern)
	case *ast.FuncDecl:
		// The function name is a binding in the current scope.
		if d.Name != nil {
			r.define(d.Name.Name)
		}
		// Don't recurse into the function body — it gets its own rename
		// pass when inferFuncBody is called for it.
	case *ast.TypeDecl:
		// Type declarations don't introduce variable bindings.
	case *ast.InterfaceDecl:
		// Interface declarations don't introduce variable bindings.
	case *ast.EnumDecl:
		// Enum declarations don't introduce variable bindings.
	case *ast.ExportAssignmentStmt:
		// Nothing to do for variable renaming.
	}
}

// renamePat processes a pattern, assigning VarIDs to binding sites.
func (r *renamer) renamePat(pat ast.Pat) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p.Default != nil {
			r.renameExpr(p.Default)
		}
		p.VarID = int(r.define(p.Name))
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				r.renamePat(e.Value)
			case *ast.ObjShorthandPat:
				if e.Default != nil {
					r.renameExpr(e.Default)
				}
				e.VarID = int(r.define(e.Key.Name))
			case *ast.ObjRestPat:
				r.renamePat(e.Pattern)
			}
		}
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			r.renamePat(elem)
		}
	case *ast.ExtractorPat:
		for _, arg := range p.Args {
			r.renamePat(arg)
		}
	case *ast.InstancePat:
		r.renamePat(p.Object)
	case *ast.RestPat:
		r.renamePat(p.Pattern)
	case *ast.LitPat:
		// No binding.
	case *ast.WildcardPat:
		// No binding.
	}
}

func (r *renamer) renameExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		e.VarID = int(r.resolve(e.Name, e.Span()))
	case *ast.BinaryExpr:
		// For assignment, the RHS is evaluated first, then the LHS is
		// resolved (it's a use site for reassignment, not a new binding).
		if e.Op == ast.Assign {
			r.renameExpr(e.Right)
			r.renameExpr(e.Left)
		} else {
			r.renameExpr(e.Left)
			r.renameExpr(e.Right)
		}
	case *ast.UnaryExpr:
		r.renameExpr(e.Arg)
	case *ast.LiteralExpr:
		// No variables.
	case *ast.FuncExpr:
		// Don't recurse into the function body — it gets its own rename
		// pass. But we do NOT process parameters here either, since they
		// belong to the inner function's scope.
	case *ast.CallExpr:
		r.renameExpr(e.Callee)
		for _, arg := range e.Args {
			r.renameExpr(arg)
		}
	case *ast.IndexExpr:
		r.renameExpr(e.Object)
		r.renameExpr(e.Index)
	case *ast.MemberExpr:
		r.renameExpr(e.Object)
		// Prop is a field name, not a variable reference.
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			r.renameExpr(elem)
		}
	case *ast.ObjectExpr:
		r.renameObjExprElems(e.Elems)
	case *ast.IfElseExpr:
		r.renameExpr(e.Cond)
		r.pushScope()
		r.renameBlock(e.Cons)
		r.popScope()
		if e.Alt != nil {
			r.pushScope()
			r.renameBlockOrExpr(e.Alt)
			r.popScope()
		}
	case *ast.IfLetExpr:
		r.renameExpr(e.Target)
		// The pattern bindings are only visible in the consequent.
		r.pushScope()
		r.renamePat(e.Pattern)
		r.renameBlock(e.Cons)
		r.popScope()
		if e.Alt != nil {
			r.pushScope()
			r.renameBlockOrExpr(e.Alt)
			r.popScope()
		}
	case *ast.MatchExpr:
		r.renameExpr(e.Target)
		for _, mc := range e.Cases {
			r.pushScope()
			r.renamePat(mc.Pattern)
			if mc.Guard != nil {
				r.renameExpr(mc.Guard)
			}
			r.renameBlockOrExpr(&mc.Body)
			r.popScope()
		}
	case *ast.TryCatchExpr:
		r.pushScope()
		r.renameBlock(e.Try)
		r.popScope()
		for _, mc := range e.Catch {
			r.pushScope()
			r.renamePat(mc.Pattern)
			if mc.Guard != nil {
				r.renameExpr(mc.Guard)
			}
			r.renameBlockOrExpr(&mc.Body)
			r.popScope()
		}
	case *ast.DoExpr:
		r.pushScope()
		r.renameBlock(e.Body)
		r.popScope()
	case *ast.ThrowExpr:
		r.renameExpr(e.Arg)
	case *ast.AwaitExpr:
		r.renameExpr(e.Arg)
	case *ast.YieldExpr:
		if e.Value != nil {
			r.renameExpr(e.Value)
		}
	case *ast.TemplateLitExpr:
		for _, expr := range e.Exprs {
			r.renameExpr(expr)
		}
	case *ast.TaggedTemplateLitExpr:
		r.renameExpr(e.Tag)
		for _, expr := range e.Exprs {
			r.renameExpr(expr)
		}
	case *ast.TypeCastExpr:
		r.renameExpr(e.Expr)
	case *ast.ArraySpreadExpr:
		r.renameExpr(e.Value)
	case *ast.JSXElementExpr:
		r.renameJSXOpening(e.Opening)
		r.renameJSXChildren(e.Children)
	case *ast.JSXFragmentExpr:
		r.renameJSXChildren(e.Children)
	case *ast.ErrorExpr:
		// Nothing to do.
	}
}

func (r *renamer) renameBlockOrExpr(boe *ast.BlockOrExpr) {
	if boe.Block != nil {
		r.renameBlock(*boe.Block)
	}
	if boe.Expr != nil {
		r.renameExpr(boe.Expr)
	}
}

func (r *renamer) renameObjExprElems(elems []ast.ObjExprElem) {
	for _, elem := range elems {
		switch e := elem.(type) {
		case *ast.PropertyExpr:
			if e.Value != nil {
				r.renameExpr(e.Value)
				// For computed keys, resolve the key expression.
				if ck, ok := e.Name.(*ast.ComputedKey); ok {
					r.renameExpr(ck.Expr)
				}
			} else {
				// Shorthand property {x} — the name is also a variable reference.
				if ident, ok := e.Name.(*ast.IdentExpr); ok {
					ident.VarID = int(r.resolve(ident.Name, ident.Span()))
				}
			}
		case *ast.MethodExpr:
			// Method key: only resolve computed keys.
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				r.renameExpr(ck.Expr)
			}
			// Don't recurse into the method body.
		case *ast.GetterExpr:
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				r.renameExpr(ck.Expr)
			}
		case *ast.SetterExpr:
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				r.renameExpr(ck.Expr)
			}
		case *ast.CallableExpr:
			// Don't recurse into the callable body.
		case *ast.ConstructorExpr:
			// Don't recurse into the constructor body.
		case *ast.ObjSpreadExpr:
			r.renameExpr(e.Value)
		}
	}
}

func (r *renamer) renameJSXOpening(opening *ast.JSXOpening) {
	if opening == nil {
		return
	}
	for _, attr := range opening.Attrs {
		switch a := attr.(type) {
		case *ast.JSXAttr:
			if a.Value != nil {
				r.renameJSXAttrValue(a.Value)
			}
		case *ast.JSXSpreadAttr:
			r.renameExpr(a.Expr)
		}
	}
}

func (r *renamer) renameJSXAttrValue(val *ast.JSXAttrValue) {
	switch v := (*val).(type) {
	case *ast.JSXExprContainer:
		r.renameExpr(v.Expr)
	case *ast.JSXElementExpr:
		r.renameJSXOpening(v.Opening)
		r.renameJSXChildren(v.Children)
	case *ast.JSXFragmentExpr:
		r.renameJSXChildren(v.Children)
	case *ast.JSXString:
		// No variables.
	}
}

func (r *renamer) renameJSXChildren(children []ast.JSXChild) {
	for _, child := range children {
		switch c := child.(type) {
		case *ast.JSXExprContainer:
			r.renameExpr(c.Expr)
		case *ast.JSXElementExpr:
			r.renameJSXOpening(c.Opening)
			r.renameJSXChildren(c.Children)
		case *ast.JSXFragmentExpr:
			r.renameJSXChildren(c.Children)
		case *ast.JSXText:
			// No variables.
		}
	}
}
