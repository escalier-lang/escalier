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
	switch source.Kind {
	case AliasSourceFresh:
		return "fresh"
	case AliasSourceUnknown:
		return "unknown"
	case AliasSourceVariable, AliasSourceMultiple:
		varNames := make([]string, len(source.VarIDs))
		for i, id := range source.VarIDs {
			if name, ok := names[id]; ok {
				varNames[i] = name
			} else {
				varNames[i] = fmt.Sprintf("%d", id)
			}
		}
		if source.Kind == AliasSourceVariable {
			return "variable [" + strings.Join(varNames, ", ") + "]"
		}
		return "multiple [" + strings.Join(varNames, ", ") + "]"
	default:
		return fmt.Sprintf("unknown-kind(%d)", source.Kind)
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

	// Any unknown branch makes the whole result unknown (conservative)
	require.Equal(t, "unknown", formatAliasSource(DetermineAliasSource(expr), nil))
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

func TestDetermineAliasSource_MatchExpr_VariableAndFresh(t *testing.T) {
	expr := parseExpr(t, "match 1 { _ => a, _ => [1] }")
	setVarIDs(expr, map[string]int{"a": 1})
	names := map[VarID]string{1: "a"}

	// Only one arm aliases a variable; the fresh arm doesn't contribute
	require.Equal(t, "variable [a]", formatAliasSource(DetermineAliasSource(expr), names))
}

// Integration tests: alias tracking with DetermineAliasSource

func TestAliasTracking_ValBEqualsA(t *testing.T) {
	// val a = {x: 1}; val b = a
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	names := map[VarID]string{1: "a", 2: "b"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)

	// a and b should be in the same alias set
	require.Equal(t, "[{a(immut), b(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{a(immut), b(immut)}]", formatAliasSets(tracker.GetAliasSets(b), names))
}

func TestAliasTracking_ReassignToFresh(t *testing.T) {
	// var b = a; b = {x: 1}
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	names := map[VarID]string{1: "a", 2: "b"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.Reassign(b, nil, AliasImmutable)

	// b should have left a's set and gotten its own fresh set
	require.Equal(t, "[{a(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{b(immut)}]", formatAliasSets(tracker.GetAliasSets(b), names))
}

func TestAliasTracking_ReassignToOtherVar(t *testing.T) {
	// var b = a; b = c
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.NewValue(c, AliasImmutable)
	tracker.Reassign(b, &c, AliasImmutable)

	// b should have left a's set and joined c's set
	require.Equal(t, "[{a(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{b(immut), c(immut)}]", formatAliasSets(tracker.GetAliasSets(b), names))
}

func TestAliasTracking_MultipleAliases(t *testing.T) {
	// val b = a; val c = a
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, a, AliasImmutable)

	// All three should be in the same set
	require.Equal(t, "[{a(immut), b(immut), c(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
}

func TestAliasTracking_Chain(t *testing.T) {
	// val b = a; val c = b — transitive aliasing
	tracker := NewAliasTracker()
	var a VarID = 1
	var b VarID = 2
	var c VarID = 3
	names := map[VarID]string{1: "a", 2: "b", 3: "c"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(b, a, AliasImmutable)
	tracker.AddAlias(c, b, AliasImmutable)

	// c aliases b which aliases a, so all three end up in the same set
	require.Equal(t, "[{a(immut), b(immut), c(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
}

func TestAliasTracking_Shadowing(t *testing.T) {
	// val x = a; val x = {y: 1} — second x (x2) gets a distinct VarID
	tracker := NewAliasTracker()
	var a VarID = 1
	var x1 VarID = 2
	var x2 VarID = 3
	names := map[VarID]string{1: "a", 2: "x1", 3: "x2"}

	tracker.NewValue(a, AliasImmutable)
	tracker.AddAlias(x1, a, AliasImmutable)
	tracker.NewValue(x2, AliasImmutable)

	// x1 stays in a's set; x2 (the shadow) gets its own fresh set
	require.Equal(t, "[{a(immut), x1(immut)}]", formatAliasSets(tracker.GetAliasSets(a), names))
	require.Equal(t, "[{x2(immut)}]", formatAliasSets(tracker.GetAliasSets(x2), names))
}
