package liveness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// formatAliasSource formats an AliasSource as a readable string for test assertions.
// Examples: "fresh", "unknown", "variable [x]", "multiple [a, b]"
func formatAliasSource(source AliasSource, names map[VarID]string) string {
	kind := source.RootKind()
	switch kind {
	case AliasSourceFresh:
		return "fresh"
	case AliasSourceUnknown:
		return "unknown"
	case AliasSourceVariable, AliasSourceMultiple:
		ids := source.VarIDs()
		varNames := make([]string, len(ids))
		for i, id := range ids {
			if name, ok := names[id]; ok {
				varNames[i] = name
			} else {
				varNames[i] = fmt.Sprintf("%d", id)
			}
		}
		if kind == AliasSourceVariable {
			return "variable [" + strings.Join(varNames, ", ") + "]"
		}
		return "multiple [" + strings.Join(varNames, ", ") + "]"
	default:
		return fmt.Sprintf("unknown-kind(%d)", kind)
	}
}

// parseScript parses a source string into a Script AST.
// Fails the test if parsing produces errors.
func parseScript(t *testing.T, input string) *ast.Script {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "test.esc", Contents: input}
	p := parser.NewParser(context.Background(), source)
	script, errs := p.ParseScript()
	require.Empty(t, errs, "parse errors for: %s", input)
	return script
}

// parseExpr parses a source string containing a single expression statement
// and returns the expression.
func parseExpr(t *testing.T, input string) ast.Expr {
	t.Helper()
	script := parseScript(t, input)
	require.Len(t, script.Stmts, 1, "expected exactly one statement")
	exprStmt, ok := script.Stmts[0].(*ast.ExprStmt)
	require.True(t, ok, "expected ExprStmt, got %T", script.Stmts[0])
	return exprStmt.Expr
}

// setVarIDs walks an expression AST and sets VarID on every IdentExpr whose
// name appears in the provided map. This simulates what the rename pass does.
func setVarIDs(expr ast.Expr, ids map[string]int) {
	v := &varIDSetter{ids: ids}
	expr.Accept(v)
}

type varIDSetter struct {
	ast.DefaultVisitor
	ids map[string]int
}

func (v *varIDSetter) EnterExpr(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.IdentExpr); ok {
		if id, found := v.ids[ident.Name]; found {
			ident.VarID = id
		}
	}
	return true
}

func TestDetermineAliasSource_Identifier(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = 1
	names := map[VarID]string{1: "x"}

	// A local identifier aliases the variable it refers to
	require.Equal(t, "variable [x]", formatAliasSource(DetermineAliasSource(ident), names))
}

func TestDetermineAliasSource_NonLocalIdentifier(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = -1

	// Non-local variables (negative VarID) can't be tracked intraprocedurally
	require.Equal(t, "unknown", formatAliasSource(DetermineAliasSource(ident), nil))
}

func TestDetermineAliasSource_FreshExpressions(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "ObjectExpr", input: "{x: 1}"},
		{name: "TupleExpr", input: "[1, 2]"},
		{name: "CallExpr", input: "foo()"},
		{name: "BinaryExpr", input: "1 + 2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr := parseExpr(t, tc.input)
			// Constructors and operators produce new values, not aliases
			require.Equal(t, "fresh", formatAliasSource(DetermineAliasSource(expr), nil))
		})
	}
}

func TestDetermineAliasSource_MemberExpr(t *testing.T) {
	expr := parseExpr(t, "obj.field")
	setVarIDs(expr, map[string]int{"obj": 5})
	names := map[VarID]string{5: "obj"}

	// obj.field aliases obj — property access recurses into the object
	require.Equal(t, "variable [obj]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_MemberExpr_NonLocal(t *testing.T) {
	expr := parseExpr(t, "obj.field")
	setVarIDs(expr, map[string]int{"obj": -1})

	// Property access on a non-local object is still unknown
	require.Equal(t, "unknown", formatAliasSource(DetermineAliasSource(expr), nil))
}

func TestDetermineAliasSource_IndexExpr(t *testing.T) {
	expr := parseExpr(t, "obj[0]")
	setVarIDs(expr, map[string]int{"obj": 5})
	names := map[VarID]string{5: "obj"}

	// obj[0] aliases obj — index access recurses into the object
	require.Equal(t, "variable [obj]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_IndexExpr_NonLocal(t *testing.T) {
	expr := parseExpr(t, "obj[0]")
	setVarIDs(expr, map[string]int{"obj": -1})

	// Index access on a non-local object is still unknown
	require.Equal(t, "unknown", formatAliasSource(DetermineAliasSource(expr), nil))
}

func TestDetermineAliasSource_TypeCast(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = 3
	names := map[VarID]string{3: "x"}
	cast := ast.NewTypeCast(ident, nil, ast.Span{})

	// Type casts are transparent — the result aliases the inner expression
	require.Equal(t, "variable [x]", formatAliasSource(DetermineAliasSource(cast), names))
}

func TestDetermineAliasSource_TypeCast_NonLocal(t *testing.T) {
	ident := ast.NewIdent("x", ast.Span{})
	ident.VarID = -1
	cast := ast.NewTypeCast(ident, nil, ast.Span{})

	// Casting a non-local variable is still unknown
	require.Equal(t, "unknown", formatAliasSource(DetermineAliasSource(cast), nil))
}

func TestDetermineAliasSource_IfElseExpr_BothVariables(t *testing.T) {
	expr := parseExpr(t, "if true { a } else { b }")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})
	names := map[VarID]string{1: "a", 2: "b"}

	// Both branches return different variables, so the result may alias either
	require.Equal(t, "multiple [a, b]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_IfElseExpr_OneVariableOneFresh(t *testing.T) {
	expr := parseExpr(t, "if true { a } else { {x: 1} }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Only one branch aliases a variable; the fresh branch doesn't contribute
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_IfElseExpr_BothFresh(t *testing.T) {
	expr := parseExpr(t, "if true { {x: 1} } else { {x: 2} }")

	// Both branches produce fresh values, so the result is fresh
	require.Equal(t, "fresh", formatAliasSource(DetermineAliasSource(expr), nil))
}

func TestDetermineAliasSource_IfElseExpr_NoAlt_Variable(t *testing.T) {
	expr := parseExpr(t, "if true { a }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Missing else is implicitly undefined (fresh), so only the cons branch contributes
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_IfElseExpr_NoAlt_Fresh(t *testing.T) {
	expr := parseExpr(t, "if true { {x: 1} }")

	// Cons is fresh, missing else is implicitly fresh — both fresh
	require.Equal(t, "fresh", formatAliasSource(DetermineAliasSource(expr), nil))
}

func TestDetermineAliasSource_IfElseExpr_UnknownAndVariable(t *testing.T) {
	expr := parseExpr(t, "if true { ext } else { a }")
	setVarIDs(expr, map[string]int{"ext": -1, "a": 1})
	names := map[VarID]string{1: "a"}

	// Unknown branches are skipped — we still track aliases from known branches
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_IfElseExpr_SameVariable(t *testing.T) {
	expr := parseExpr(t, "if true { a } else { a }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Same variable in both branches is deduplicated to a single variable source
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_MatchExpr_MultipleVariables(t *testing.T) {
	expr := parseExpr(t, "match 1 { _ => a, _ => b, _ => c }")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2, "c": 3})
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	// Each arm returns a different variable, so the result may alias any of them
	require.Equal(t, "multiple [a, b, c]", formatAliasSource(DetermineAliasSource(expr), names))
}

func TestDetermineAliasSource_MatchExpr_SameVariable(t *testing.T) {
	expr := parseExpr(t, "match 1 { _ => a, _ => a }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Same variable in all arms is deduplicated to a single variable source
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

// formatLeaves returns a compact, deterministic string of an alias
// source's (root, path) pairs — used by Phase 8.9 path-tracking tests
// where the legacy Kind/VarIDs view loses the projection structure.
func formatLeaves(source AliasSource, names map[VarID]string) string {
	parts := make([]string, len(source.Leaves))
	for i, leaf := range source.Leaves {
		name, ok := names[leaf.RootVarID]
		if !ok {
			name = fmt.Sprintf("%d", leaf.RootVarID)
		}
		parts[i] = name + formatPath(leaf.Path)
	}
	return strings.Join(parts, ", ")
}

func formatPath(path []ProjectionStep) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	for _, step := range path {
		switch s := step.(type) {
		case ElementOf:
			b.WriteString(".[]")
		case PropertyOf:
			b.WriteString(".")
			b.WriteString(s.Key)
		case IndexOf:
			fmt.Fprintf(&b, "[%d]", s.Index)
		case AwaitOf:
			b.WriteString(".await")
		case CastOf:
			b.WriteString(".cast")
		}
	}
	return b.String()
}

func TestDetermineAliasSource_MemberExpr_AppendsProperty(t *testing.T) {
	expr := parseExpr(t, "obj.field.inner")
	setVarIDs(expr, map[string]int{"obj": 5})
	names := map[VarID]string{5: "obj"}

	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginAlias, src.Origin)
	require.Equal(t, "obj.field.inner", formatLeaves(src, names))
}

func TestDetermineAliasSource_IndexExpr_ConstAppendsIndex(t *testing.T) {
	expr := parseExpr(t, "obj[2]")
	setVarIDs(expr, map[string]int{"obj": 5})
	names := map[VarID]string{5: "obj"}

	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginAlias, src.Origin)
	require.Equal(t, "obj[2]", formatLeaves(src, names))
}

func TestDetermineAliasSource_IndexExpr_NonConstFallsBack(t *testing.T) {
	expr := parseExpr(t, "obj[i]")
	setVarIDs(expr, map[string]int{"obj": 5, "i": 7})
	names := map[VarID]string{5: "obj"}

	// Non-constant indexes can't be slotted statically — leaf has no
	// IndexOf step, just the bare root.
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginAlias, src.Origin)
	require.Equal(t, "obj", formatLeaves(src, names))
}

func TestDetermineAliasSource_TupleExpr_ProducesIndexLeaves(t *testing.T) {
	expr := parseExpr(t, "[a, b]")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})
	names := map[VarID]string{1: "a", 2: "b"}

	src := DetermineAliasSource(expr)
	// Root is freshly constructed — the alias tracker should still see
	// "fresh" via the legacy Kind() so it doesn't merge alias sets at
	// the root level.
	require.Equal(t, "fresh", formatAliasSource(src, names))
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a[0], b[1]", formatLeaves(src, names))
}

func TestDetermineAliasSource_TupleExpr_AllFreshHasNoLeaves(t *testing.T) {
	expr := parseExpr(t, "[1, 2]")
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Empty(t, src.Leaves)
}

func TestDetermineAliasSource_ObjectExpr_ProducesPropertyLeaves(t *testing.T) {
	expr := parseExpr(t, "{head: a, tail: b}")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})
	names := map[VarID]string{1: "a", 2: "b"}

	src := DetermineAliasSource(expr)
	require.Equal(t, "fresh", formatAliasSource(src, names))
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a.head, b.tail", formatLeaves(src, names))
}

func TestDetermineAliasSource_NestedTupleInObject(t *testing.T) {
	expr := parseExpr(t, "{items: [a, b]}")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})
	names := map[VarID]string{1: "a", 2: "b"}

	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a.items[0], b.items[1]", formatLeaves(src, names))
}

func TestDetermineAliasSource_TupleOfMember(t *testing.T) {
	expr := parseExpr(t, "[obj.x]")
	setVarIDs(expr, map[string]int{"obj": 9})
	names := map[VarID]string{9: "obj"}

	// `[obj.x]` — fresh root, one leaf rooted at obj with path
	// [IndexOf(0), PropertyOf("x")] describing the slot in the new
	// container followed by the projection into obj.
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "obj[0].x", formatLeaves(src, names))
}

func TestDetermineAliasSource_TupleExpr_RepeatedRootKeepsBothSlots(t *testing.T) {
	expr := parseExpr(t, "[a, a]")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// `[a, a]` — two distinct slots, both rooted at `a`. The dedupe key
	// must be (RootVarID, Path), not RootVarID alone, otherwise IndexOf(1)
	// is dropped and the second slot loses its lifetime attachment.
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a[0], a[1]", formatLeaves(src, names))
}

func TestDetermineAliasSource_ObjectExpr_RepeatedRootKeepsBothProps(t *testing.T) {
	expr := parseExpr(t, "{head: a, tail: a}")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a.head, a.tail", formatLeaves(src, names))
}

func TestDetermineAliasSource_IfElseExpr_BothFreshContainersStaysFresh(t *testing.T) {
	expr := parseExpr(t, "if true { [a] } else { [b] }")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})
	names := map[VarID]string{1: "a", 2: "b"}

	// Both branches produce fresh containers with element-level leaves.
	// The merged origin should remain Fresh so path-aware lifetime
	// attachment descends into the array's element type, rather than
	// attaching at the top.
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginFresh, src.Origin)
	require.Equal(t, "a[0], b[0]", formatLeaves(src, names))
}

func TestDetermineAliasSource_IfElseExpr_MixedFreshAndAliasIsAlias(t *testing.T) {
	expr := parseExpr(t, "if true { [a] } else { b }")
	setVarIDs(expr, map[string]int{"a": 1, "b": 2})

	// One branch is fresh-rooted with an element leaf; the other is an
	// alias-rooted bare variable. The merged origin must be Alias because
	// at least one branch's value root aliases an existing variable.
	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginAlias, src.Origin)
}

func TestDetermineAliasSource_AwaitExpr_AppendsAwait(t *testing.T) {
	expr := parseExpr(t, "await p")
	setVarIDs(expr, map[string]int{"p": 4})
	names := map[VarID]string{4: "p"}

	src := DetermineAliasSource(expr)
	require.Equal(t, AliasOriginAlias, src.Origin)
	require.Equal(t, "p.await", formatLeaves(src, names))
}

func TestLeafKey_NoCollisionWithDelimiterInPropertyName(t *testing.T) {
	// PropertyOf keys are user-supplied (string-keyed object literals can
	// contain '|' or 'p:'). The dedup key must not collide with a path
	// where the same characters are split across two property steps.
	a := leafKey(1, []ProjectionStep{PropertyOf{Key: "foo|p:bar"}})
	b := leafKey(1, []ProjectionStep{PropertyOf{Key: "foo"}, PropertyOf{Key: "bar"}})
	require.NotEqual(t, a, b, "different paths must produce different keys")
}

func TestDetermineAliasSource_MatchExpr_VariableAndFresh(t *testing.T) {
	expr := parseExpr(t, "match 1 { _ => a, _ => [1] }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Only one arm aliases a variable; the fresh arm doesn't contribute
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}
