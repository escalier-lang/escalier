package liveness

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// StmtUses holds the uses and definitions found in a single statement.
type StmtUses struct {
	Uses []VarID // Variables read in this statement
	Defs []VarID // Variables defined (written) in this statement
}

// CollectUses walks a linear block of statements and returns per-statement
// use/def information. Only local variables (VarID > 0) are tracked;
// non-local variables (VarID < 0) and unresolved references (VarID == 0)
// are ignored.
//
// VarIDs are read directly from AST nodes (set by the rename pass in Phase 2).
func CollectUses(stmts []ast.Stmt) []StmtUses {
	result := make([]StmtUses, len(stmts))
	for i, stmt := range stmts {
		c := &collector{}
		c.collectStmt(stmt)
		result[i] = StmtUses{Uses: c.uses, Defs: c.defs}
	}
	return result
}

// collector accumulates uses and defs for a single statement.
type collector struct {
	uses []VarID
	defs []VarID
}

func (c *collector) addUse(id int) {
	if id > 0 {
		c.uses = append(c.uses, VarID(id))
	}
}

func (c *collector) addDef(id int) {
	if id > 0 {
		c.defs = append(c.defs, VarID(id))
	}
}

func (c *collector) collectStmt(stmt ast.Stmt) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		c.collectExpr(s.Expr)
	case *ast.DeclStmt:
		c.collectDecl(s.Decl)
	case *ast.ReturnStmt:
		if s.Expr != nil {
			c.collectExpr(s.Expr)
		}
	case *ast.ForInStmt:
		// For Phase 3 (straight-line), we collect uses from the iterable
		// and treat the loop body as opaque. Phase 4 will handle this
		// properly with CFG.
		c.collectExpr(s.Iterable)
		c.collectBlock(s.Body)
		c.collectPatDefs(s.Pattern)
	case *ast.ImportStmt:
		// No variable uses.
	case *ast.ErrorStmt:
		// Nothing to do.
	}
}

func (c *collector) collectDecl(decl ast.Decl) {
	switch d := decl.(type) {
	case *ast.VarDecl:
		// Initializer is evaluated first (uses), then the pattern defines
		// new variables (defs).
		if d.Init != nil {
			c.collectExpr(d.Init)
		}
		c.collectPatDefs(d.Pattern)
	case *ast.FuncDecl:
		// The function name is a definition. Don't recurse into the body.
		// TODO: FuncDecl.Name is *ast.Ident which has no VarID field.
		// Add a VarID field to FuncDecl so the rename pass can store it
		// and liveness can track function name definitions.
	case *ast.TypeDecl, *ast.InterfaceDecl, *ast.EnumDecl, *ast.ExportAssignmentStmt:
		// No variable bindings relevant to liveness.
	}
}

// collectPatDefs records all binding sites in a pattern as definitions.
func (c *collector) collectPatDefs(pat ast.Pat) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		c.addDef(p.VarID)
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				c.collectPatDefs(e.Value)
			case *ast.ObjShorthandPat:
				c.addDef(e.VarID)
			case *ast.ObjRestPat:
				c.collectPatDefs(e.Pattern)
			}
		}
	case *ast.TuplePat:
		for _, elem := range p.Elems {
			c.collectPatDefs(elem)
		}
	case *ast.ExtractorPat:
		for _, arg := range p.Args {
			c.collectPatDefs(arg)
		}
	case *ast.InstancePat:
		c.collectPatDefs(p.Object)
	case *ast.RestPat:
		c.collectPatDefs(p.Pattern)
	case *ast.LitPat, *ast.WildcardPat:
		// No bindings.
	}
}

func (c *collector) collectExpr(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		c.addUse(e.VarID)
	case *ast.BinaryExpr:
		if e.Op == ast.Assign {
			// Plain assignment: RHS is a use, LHS is a definition.
			// The LHS IdentExpr is a definition (not a use of the old value).
			c.collectExpr(e.Right)
			c.collectAssignTarget(e.Left)
		} else {
			c.collectExpr(e.Left)
			c.collectExpr(e.Right)
		}
	case *ast.UnaryExpr:
		c.collectExpr(e.Arg)
	case *ast.LiteralExpr:
		// No variables.
	case *ast.FuncExpr:
		// Don't recurse into function bodies — they get their own analysis.
	case *ast.CallExpr:
		c.collectExpr(e.Callee)
		for _, arg := range e.Args {
			c.collectExpr(arg)
		}
	case *ast.IndexExpr:
		c.collectExpr(e.Object)
		c.collectExpr(e.Index)
	case *ast.MemberExpr:
		// Record a use of the base object.
		c.collectExpr(e.Object)
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			c.collectExpr(elem)
		}
	case *ast.ObjectExpr:
		c.collectObjExprElems(e.Elems)
	case *ast.IfElseExpr:
		c.collectExpr(e.Cond)
		c.collectBlock(e.Cons)
		if e.Alt != nil {
			c.collectBlockOrExpr(e.Alt)
		}
	case *ast.IfLetExpr:
		c.collectExpr(e.Target)
		c.collectPatDefs(e.Pattern)
		c.collectBlock(e.Cons)
		if e.Alt != nil {
			c.collectBlockOrExpr(e.Alt)
		}
	case *ast.MatchExpr:
		c.collectExpr(e.Target)
		for _, mc := range e.Cases {
			c.collectPatDefs(mc.Pattern)
			if mc.Guard != nil {
				c.collectExpr(mc.Guard)
			}
			c.collectBlockOrExpr(&mc.Body)
		}
	case *ast.TryCatchExpr:
		c.collectBlock(e.Try)
		for _, mc := range e.Catch {
			c.collectPatDefs(mc.Pattern)
			if mc.Guard != nil {
				c.collectExpr(mc.Guard)
			}
			c.collectBlockOrExpr(&mc.Body)
		}
	case *ast.DoExpr:
		c.collectBlock(e.Body)
	case *ast.ThrowExpr:
		c.collectExpr(e.Arg)
	case *ast.AwaitExpr:
		c.collectExpr(e.Arg)
	case *ast.YieldExpr:
		if e.Value != nil {
			c.collectExpr(e.Value)
		}
	case *ast.TemplateLitExpr:
		for _, expr := range e.Exprs {
			c.collectExpr(expr)
		}
	case *ast.TaggedTemplateLitExpr:
		c.collectExpr(e.Tag)
		for _, expr := range e.Exprs {
			c.collectExpr(expr)
		}
	case *ast.TypeCastExpr:
		c.collectExpr(e.Expr)
	case *ast.ArraySpreadExpr:
		c.collectExpr(e.Value)
	case *ast.JSXElementExpr:
		c.collectJSXOpening(e.Opening)
		c.collectJSXChildren(e.Children)
	case *ast.JSXFragmentExpr:
		c.collectJSXChildren(e.Children)
	case *ast.ErrorExpr:
		// Nothing to do.
	}
}

// collectAssignTarget handles the LHS of an assignment. For a plain
// identifier, this is a definition (no read). For member/index access,
// the base object is still a use.
func (c *collector) collectAssignTarget(expr ast.Expr) {
	switch e := expr.(type) {
	case *ast.IdentExpr:
		// Assignment to a variable: definition only, no use.
		c.addDef(e.VarID)
	case *ast.MemberExpr:
		// obj.field = val: obj is used (read), field is being set.
		c.collectExpr(e.Object)
	case *ast.IndexExpr:
		// obj[idx] = val: both obj and idx are used.
		c.collectExpr(e.Object)
		c.collectExpr(e.Index)
	default:
		// Fallback: treat as a regular expression.
		c.collectExpr(expr)
	}
}

func (c *collector) collectBlock(block ast.Block) {
	for _, stmt := range block.Stmts {
		c.collectStmt(stmt)
	}
}

func (c *collector) collectBlockOrExpr(boe *ast.BlockOrExpr) {
	if boe.Block != nil {
		c.collectBlock(*boe.Block)
	}
	if boe.Expr != nil {
		c.collectExpr(boe.Expr)
	}
}

func (c *collector) collectObjExprElems(elems []ast.ObjExprElem) {
	for _, elem := range elems {
		switch e := elem.(type) {
		case *ast.PropertyExpr:
			if e.Value != nil {
				c.collectExpr(e.Value)
				if ck, ok := e.Name.(*ast.ComputedKey); ok {
					c.collectExpr(ck.Expr)
				}
			} else {
				// Shorthand property {x} — the name is a variable reference.
				if ident, ok := e.Name.(*ast.IdentExpr); ok {
					c.addUse(ident.VarID)
				}
			}
		case *ast.MethodExpr:
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				c.collectExpr(ck.Expr)
			}
		case *ast.GetterExpr:
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				c.collectExpr(ck.Expr)
			}
		case *ast.SetterExpr:
			if ck, ok := e.Name.(*ast.ComputedKey); ok {
				c.collectExpr(ck.Expr)
			}
		case *ast.CallableExpr:
			// Don't recurse into callable body.
		case *ast.ConstructorExpr:
			// Don't recurse into constructor body.
		case *ast.ObjSpreadExpr:
			c.collectExpr(e.Value)
		}
	}
}

func (c *collector) collectJSXOpening(opening *ast.JSXOpening) {
	if opening == nil {
		return
	}
	for _, attr := range opening.Attrs {
		switch a := attr.(type) {
		case *ast.JSXAttr:
			if a.Value != nil {
				c.collectJSXAttrValue(a.Value)
			}
		case *ast.JSXSpreadAttr:
			c.collectExpr(a.Expr)
		}
	}
}

func (c *collector) collectJSXAttrValue(val *ast.JSXAttrValue) {
	switch v := (*val).(type) {
	case *ast.JSXExprContainer:
		c.collectExpr(v.Expr)
	case *ast.JSXElementExpr:
		c.collectJSXOpening(v.Opening)
		c.collectJSXChildren(v.Children)
	case *ast.JSXFragmentExpr:
		c.collectJSXChildren(v.Children)
	case *ast.JSXString:
		// No variables.
	}
}

func (c *collector) collectJSXChildren(children []ast.JSXChild) {
	for _, child := range children {
		switch ch := child.(type) {
		case *ast.JSXExprContainer:
			c.collectExpr(ch.Expr)
		case *ast.JSXElementExpr:
			c.collectJSXOpening(ch.Opening)
			c.collectJSXChildren(ch.Children)
		case *ast.JSXFragmentExpr:
			c.collectJSXChildren(ch.Children)
		case *ast.JSXText:
			// No variables.
		}
	}
}
