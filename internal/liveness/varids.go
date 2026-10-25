package liveness

import (
	"maps"

	"github.com/escalier-lang/escalier/internal/ast"
)

// VarIDs maps each binding and use site the rename pass visited to the VarID it
// assigned. The sites are *ast.IdentExpr, *ast.IdentPat, *ast.ObjShorthandPat and
// *ast.FuncDecl nodes. A later rename of the same node replaces its entry.
//
// The table lives outside the AST because one parsed module can be shared by
// inference runs on different goroutines. Each run renames into a table of its own.
type VarIDs struct {
	ids map[ast.Node]VarID
}

// NewVarIDs returns an empty table.
func NewVarIDs() *VarIDs {
	return &VarIDs{ids: map[ast.Node]VarID{}}
}

// Clone returns a table holding the same entries as t, which a Set on either one
// leaves out of the other.
func (t *VarIDs) Clone() *VarIDs {
	return &VarIDs{ids: maps.Clone(t.ids)}
}

// Of returns the VarID the rename pass assigned to node, or 0 when no rename
// visited it.
func (t *VarIDs) Of(node ast.Node) VarID {
	return t.ids[node]
}

// Set records id as the VarID of node, replacing any earlier entry in t.
func (t *VarIDs) Set(node ast.Node, id VarID) {
	t.ids[node] = id
}

// RootObject walks a member or index chain such as `a.b.c` or `a[b][c]` to its
// root identifier and returns that identifier's VarID. It returns 0 when the
// root is not a local variable.
func (t *VarIDs) RootObject(expr ast.Expr) VarID {
	for {
		switch e := expr.(type) {
		case *ast.MemberExpr:
			expr = e.Object
		case *ast.IndexExpr:
			expr = e.Object
		case *ast.IdentExpr:
			if id := t.Of(e); id > 0 {
				return id
			}
			return 0
		default:
			return 0
		}
	}
}
