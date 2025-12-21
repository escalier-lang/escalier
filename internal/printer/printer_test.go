package printer

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
)

func parseScript(t *testing.T, input string) *ast.Script {
	t.Helper()
	source := &ast.Source{
		Path:     "test.esc",
		Contents: input,
		ID:       0,
	}
	p := parser.NewParser(context.Background(), source)
	script, errors := p.ParseScript()
	if len(errors) > 0 {
		t.Fatalf("Parse errors: %v", errors)
	}
	return script
}

func parseDecl(t *testing.T, input string) ast.Decl {
	t.Helper()
	script := parseScript(t, input)
	if len(script.Stmts) == 0 {
		t.Fatal("No statements parsed")
	}
	declStmt, ok := script.Stmts[0].(*ast.DeclStmt)
	if !ok {
		t.Fatalf("Expected DeclStmt, got %T", script.Stmts[0])
	}
	return declStmt.Decl
}

func parseExpr(t *testing.T, input string) ast.Expr {
	t.Helper()
	script := parseScript(t, input)
	if len(script.Stmts) == 0 {
		t.Fatal("No statements parsed")
	}
	exprStmt, ok := script.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("Expected ExprStmt, got %T", script.Stmts[0])
	}
	return exprStmt.Expr
}

func parseTypeAnn(t *testing.T, input string) ast.TypeAnn {
	t.Helper()
	typeAnn, errors := parser.ParseTypeAnn(context.Background(), input)
	if len(errors) > 0 {
		t.Fatalf("Parse errors: %v", errors)
	}
	return typeAnn
}

func TestPrintLiterals(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"number", "42", "42"},
		{"negative number", "-5", "-5"},
		{"float", "3.14", "3.14"},
		{"string", `"hello"`, `"hello"`},
		{"boolean true", "true", "true"},
		{"boolean false", "false", "false"},
		{"null", "null", "null"},
		{"undefined", "undefined", "undefined"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintBinaryExpressions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"addition", "1 + 2", "1 + 2"},
		{"subtraction", "5 - 3", "5 - 3"},
		{"multiplication", "4 * 2", "4 * 2"},
		{"division", "10 / 2", "10 / 2"},
		{"comparison", "x < y", "x < y"},
		{"equality", "a == b", "a == b"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintUnaryExpressions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"unary minus", "-x", "-x"},
		{"unary plus", "+x", "+x"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintArrays(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty array", "[]", "[]"},
		{"number array", "[1, 2, 3]", "[1, 2, 3]"},
		{"mixed array", `[1, "two", true]`, `[1, "two", true]`},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintObjects(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty object", "{}", "{}"},
		{
			"simple object",
			`{x: 5, y: 10}`,
			"{\n    x: 5,\n    y: 10\n}",
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintFunctionExpressions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple function",
			`val f = fn (x: number) {
				return x
			}`,
			"val f = fn (x: number) {\n    return x\n}",
		},
		{
			"multiple parameters",
			`val h = fn (a: number, b: number) {
				return a + b
			}`,
			"val h = fn (a: number, b: number) {\n    return a + b\n}",
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintCallExpressions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple call", "foo()", "foo()"},
		{"call with args", "add(1, 2)", "add(1, 2)"},
		{"method call", "obj.method()", "obj.method()"},
		{"optional chaining", "obj?.method()", "obj?.method()"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintMemberAccess(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple member", "obj.prop", "obj.prop"},
		{"chained member", "a.b.c", "a.b.c"},
		{"optional member", "obj?.prop", "obj?.prop"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintIndexAccess(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple index", "arr[0]", "arr[0]"},
		{"string index", `obj["key"]`, `obj["key"]`},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintIfElse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple if",
			`if x > 0 { x }`,
			"if x > 0 {\n    x\n}",
		},
		{
			"if-else",
			`if x > 0 { x } else { -x }`,
			"if x > 0 {\n    x\n} else {\n    -x\n}",
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr := parseExpr(t, tt.input)
			result, err := Print(expr, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintMatchExpression(t *testing.T) {
	input := `match x {
		1 => "one",
		2 => "two",
		_ => "other"
	}`
	expected := "match x {\n    1 => \"one\",\n    2 => \"two\",\n    _ => \"other\"\n}"

	expr := parseExpr(t, input)
	opts := DefaultOptions()
	result, err := Print(expr, opts)
	if err != nil {
		t.Fatalf("Print error: %v", err)
	}
	if result != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, result)
	}
}

func TestPrintVarDeclarations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"val declaration", "val x = 5", "val x = 5"},
		{"var declaration", "var y = 10", "var y = 10"},
		{"with type annotation", "val x: number = 5", "val x: number = 5"},
		{"exported", "export val x = 5", "export val x = 5"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintFunctionDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple function",
			`fn add(a: number, b: number) -> number {
				return a + b
			}`,
		},
		{
			"exported function",
			`export fn greet(name: string) -> string {
				return "Hello"
			}`,
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			// Verify key elements are present
			if !strings.Contains(result, "fn") {
				t.Error("Expected output to contain 'fn'")
			}
			if !strings.Contains(result, "return") {
				t.Error("Expected output to contain 'return'")
			}
			// Round-trip test
			decl2 := parseDecl(t, result)
			result2, err := Print(decl2, opts)
			if err != nil {
				t.Fatalf("Second print error: %v", err)
			}
			if result != result2 {
				t.Errorf("Round-trip failed:\nFirst:\n%s\nSecond:\n%s", result, result2)
			}
		})
	}
}

func TestPrintTypeDeclarations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"type alias", "type Point = {x: number, y: number}", "type Point = {\n    x: number,\n    y: number\n}"},
		{"union type", "type Result = string | number", "type Result = string | number"},
		{"exported type", "export type ID = number", "export type ID = number"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintEnumDeclarations(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"simple enum",
			`enum Option { Some(value: number), None }`,
		},
		{
			"exported enum",
			`export enum Result { Ok(value: string), Err(error: string) }`,
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			// Verify key elements are present
			if !strings.Contains(result, "enum") {
				t.Error("Expected output to contain 'enum'")
			}
			// Round-trip test
			decl2 := parseDecl(t, result)
			result2, err := Print(decl2, opts)
			if err != nil {
				t.Fatalf("Second print error: %v", err)
			}
			if result != result2 {
				t.Errorf("Round-trip failed:\nFirst:\n%s\nSecond:\n%s", result, result2)
			}
		})
	}
}

func TestPrintTypeAnnotations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"number type", "number", "number"},
		{"string type", "string", "string"},
		{"boolean type", "boolean", "boolean"},
		{"union type", "string | number", "string | number"},
		{"array type", "[number, string]", "[number, string]"},
		{"object type", "{x: number, y: number}", "{\n    x: number,\n    y: number\n}"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typeAnn := parseTypeAnn(t, tt.input)
			result, err := Print(typeAnn, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("Expected:\n%s\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintScript(t *testing.T) {
	input := `val x = 5
val y = 10
val sum = x + y`
	expected := "val x = 5\nval y = 10\nval sum = x + y"

	script := parseScript(t, input)
	opts := DefaultOptions()
	result, err := PrintScript(script, opts)
	if err != nil {
		t.Fatalf("Print error: %v", err)
	}
	if result != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, result)
	}
}

func TestPrintRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"literals", "42"},
		{"binary expr", "1 + 2"},
		{"function call", "foo(1, 2)"},
		{"member access", "obj.prop"},
		{"array", "[1, 2, 3]"},
		{"if expression", "if x > 0 { x } else { 0 }"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr1 := parseExpr(t, tt.input)

			printed, err := Print(expr1, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}

			expr2 := parseExpr(t, printed)

			printed2, err := Print(expr2, opts)
			if err != nil {
				t.Fatalf("Second print error: %v", err)
			}

			if printed != printed2 {
				t.Errorf("Round-trip failed:\nFirst:  %q\nSecond: %q", printed, printed2)
			}
		})
	}
}
