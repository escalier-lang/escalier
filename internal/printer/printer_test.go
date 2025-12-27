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

func TestPrintPrecedenceAndParentheses(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Multiplication has higher precedence than addition
		{"addition in multiplication", "a * (b + c)", "a * (b + c)"},
		{"multiplication in addition", "a + b * c", "a + b * c"},

		// Division has higher precedence than subtraction
		{"subtraction in division", "a / (b - c)", "a / (b - c)"},
		{"division in subtraction", "a - b / c", "a - b / c"},

		// Same precedence - left associativity
		{"addition then subtraction", "a + b - c", "a + b - c"},
		{"subtraction then addition", "a - b + c", "a - b + c"},

		// Same precedence - right associativity requires parens
		{"right associative subtraction", "a - (b - c)", "a - (b - c)"},
		{"right associative division", "a / (b / c)", "a / (b / c)"},

		// Multiple levels of precedence
		// Parser creates: a + (b * c - d) structure
		{"complex expression 1", "a + b * c - d", "a + (b * c - d)"},
		{"complex expression 2", "(a + b) * (c - d)", "(a + b) * (c - d)"},
		{"complex expression 3", "a * b + c * d", "a * b + c * d"},

		// Comparison operators
		{"comparison with addition", "a + b < c + d", "a + b < c + d"},
		// a < (b + c) and a < b + c parse the same way - + has higher precedence
		// So printer outputs minimal form without unnecessary parens
		{"addition in comparison right", "a < (b + c)", "a < b + c"},
		{"comparison then addition", "a < b + c", "a < b + c"},

		// Logical operators have lower precedence
		{"logical and with comparison", "a < b && c > d", "a < b && c > d"},
		// a && (b < c) and a && b < c parse the same - < has higher precedence
		{"comparison in logical and right", "a && (b < c)", "a && b < c"},
		{"logical and then comparison", "a && b < c", "a && b < c"},

		// Mixed precedence levels
		{"multiplication, addition, comparison", "a * b + c < d", "a * b + c < d"},
		// (a * b + c) < d and a * b + c < d parse the same - * and + have higher precedence than <
		{"grouped multiplication and addition", "(a * b + c) < d", "a * b + c < d"},

		// Unary operators
		{"unary with binary", "-(a + b)", "-(a + b)"},
		{"unary with multiplication", "-a * b", "-a * b"},

		// Concatenation operator
		{"concatenation with addition", `"a" ++ "b" + "c"`, `"a" ++ "b" + "c"`},

		// Additional edge cases to ensure correctness
		// a + (b + c) and a + b + c parse the same for associative operators
		{"nested same precedence", "a + (b + c)", "a + b + c"},
		{"multiplication chains", "a * b * c", "a * b * c"},
		{"division chains", "a / b / c", "a / b / c"},
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

func TestPrintMoreLiterals(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"regex", `/test/g`, `/test/g`},
		// {"bigint", "123n", "123n"},
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

func TestPrintIfLet(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple if let",
			`if let Some(x) = opt { x }`,
			"if let Some(x) = opt {\n    x\n}",
		},
		{
			"if let with else",
			`if let Some(x) = opt { x } else { 0 }`,
			"if let Some(x) = opt {\n    x\n} else {\n    0\n}",
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

func TestPrintAssignExpr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple assignment", "x = 5", "x = 5"},
		{"member assignment", "obj.prop = 10", "obj.prop = 10"},
		{"index assignment", "arr[0] = 1", "arr[0] = 1"},
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

func TestPrintTryCatch(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"try without catch",
			`try { doSomething() }`,
			"try {\n    doSomething()\n}",
		},
		{
			"try with single catch pattern",
			`try {
				doSomething()
			} catch {
				Error(msg) => handleError(msg)
			}`,
			"try {\n    doSomething()\n} catch {\n    Error(msg) => handleError(msg)\n}",
		},
		{
			"try with multiple catch patterns",
			`try {
				riskyOperation()
			} catch {
				Error(msg) => logError(msg),
				_ => handleUnknown()
			}`,
			"try {\n    riskyOperation()\n} catch {\n    Error(msg) => logError(msg),\n    _ => handleUnknown()\n}",
		},
		{
			"try catch with guard",
			`try {
				doSomething()
			} catch {
				Error(msg) if msg != "" => handleError(msg)
			}`,
			"try {\n    doSomething()\n} catch {\n    Error(msg) if msg != \"\" => handleError(msg)\n}",
		},
		{
			"try catch with block body",
			`try {
				doSomething()
			} catch {
				Error(msg) => {
					logError(msg)
					return null
				}
			}`,
			"try {\n    doSomething()\n} catch {\n    Error(msg) => {\n        logError(msg)\n        return null\n    }\n}",
		},
		{
			"try with multiple statements",
			`try {
				val x = getValue()
				processValue(x)
			} catch {
				_ => null
			}`,
			"try {\n    val x = getValue()\n    processValue(x)\n} catch {\n    _ => null\n}",
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
				t.Errorf("Expected:\n%s\n\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

func TestPrintDoExpr(t *testing.T) {
	input := `do {
		val x = 5
		x + 1
	}`
	expected := "do {\n    val x = 5\n    x + 1\n}"

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

func TestPrintAwaitExpr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple await", "await promise", "await promise"},
		{"await call", "await fetchData()", "await fetchData()"},
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

func TestPrintThrowExpr(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"throw string", `throw "error"`, `throw "error"`},
		{"throw object", "throw Error(msg)", "throw Error(msg)"},
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

func TestPrintTemplateLiterals(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty template", "``", "``"},
		{"simple template", "`hello`", "`hello`"},
		{"template with spaces", "`hello world`", "`hello world`"},
		{"template with expr", "`Hello ${name}`", "`Hello ${name}`"},
		{"template with expr at start", "`${greeting} world`", "`${greeting} world`"},
		{"template with expr at end", "`Hello ${name}`", "`Hello ${name}`"},
		{"multiple exprs", "`${x} + ${y} = ${x + y}`", "`${x} + ${y} = ${x + y}`"},
		{"template with complex expr", "`Result: ${a + b * c}`", "`Result: ${a + b * c}`"},
		{"template with nested call", "`Value is ${getValue()}`", "`Value is ${getValue()}`"},
		{"template with member access", "`User: ${user.name}`", "`User: ${user.name}`"},
		{"template with special chars", "`Line 1\nLine 2`", "`Line 1\nLine 2`"},
		{"template with quotes", "`It's a \"test\"`", "`It's a \"test\"`"},
		{"template only expr", "`${value}`", "`${value}`"},
		{"template with multiple adjacent exprs", "`${first}${second}`", "`${first}${second}`"},
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

func TestPrintTaggedTemplateLiterals(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple tagged template", "html`<div>test</div>`", "html`<div>test</div>`"},
		{"tagged with expr", "sql`SELECT * FROM ${table}`", "sql`SELECT * FROM ${table}`"},
		{"tagged empty template", "tag``", "tag``"},
		{"tagged template with multiple exprs", "css`width: ${w}px; height: ${h}px;`", "css`width: ${w}px; height: ${h}px;`"},
		{"tagged template complex tag", "myObj.tag`value`", "myObj.tag`value`"},
		{"tagged template with nested expr", "fmt`${x + y}`", "fmt`${x + y}`"},
		{"tagged template with member access expr", "format`User ${user.name} logged in`", "format`User ${user.name} logged in`"},
		{"tagged template with call expr", "log`Result: ${calculate()}`", "log`Result: ${calculate()}`"},
		{"tagged template multiline", "html`<div>\n  ${content}\n</div>`", "html`<div>\n  ${content}\n</div>`"},
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

func TestPrintTypeCast(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple cast", "x:number", "x:number"},
		{"complex cast", "result:(string | number)", "result:(string | number)"},
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

func TestPrintPatterns(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"object pattern in val", "val {x, y} = point"},
		{"tuple pattern", "val [a, b] = tuple"},
		{"rest pattern", "val [first, ...rest] = arr"},
		// TODO: parse wildcard patterns
		// {"wildcard pattern", "val _ = ignored"},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := parseDecl(t, tt.input)
			result, err := Print(decl, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
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

func TestPrintInterfaceDecl(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"simple interface",
			`interface Shape {
				area: fn () -> number
			}`,
			"interface Shape {\n    area: fn () -> number\n}",
		},
		{
			"exported interface",
			`export interface Point {
				x: number,
				y: number
			}`,
			"export interface Point {\n    x: number,\n    y: number\n}",
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

func TestPrintAsyncFunction(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"async function decl",
			`async fn fetchData() -> Promise<string> {
				return await fetch()
			}`,
		},
		{
			"async function expr",
			`val f = async fn () {
				return await promise
			}`,
		},
	}

	opts := DefaultOptions()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var node ast.Node
			if strings.HasPrefix(tt.input, "async fn") {
				node = parseDecl(t, tt.input)
			} else {
				node = parseDecl(t, tt.input)
			}

			result, err := Print(node, opts)
			if err != nil {
				t.Fatalf("Print error: %v", err)
			}

			if !strings.Contains(result, "async") {
				t.Error("Expected output to contain 'async'")
			}
		})
	}
}

func TestPrintFunctionWithThrows(t *testing.T) {
	input := `fn divide(a: number, b: number) -> number throws string {
		if b == 0 {
			throw "Division by zero"
		}
		return a / b
	}`

	decl := parseDecl(t, input)
	opts := DefaultOptions()
	result, err := Print(decl, opts)
	if err != nil {
		t.Fatalf("Print error: %v", err)
	}

	if !strings.Contains(result, "throws") {
		t.Error("Expected output to contain 'throws'")
	}
}

func TestPrintObjectMethods(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"getter",
			`val obj = {
				get value() { return 42 }
			}`,
		},
		{
			"setter",
			`val obj = {
				set value(v: number) { this.v = v }
			}`,
		},
		{
			"method",
			`val obj = {
				greet(name: string) -> string { return "Hello" }
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
			if tt.name == "getter" && !strings.Contains(result, "get") {
				t.Error("Expected output to contain 'get'")
			}
			if tt.name == "setter" && !strings.Contains(result, "set") {
				t.Error("Expected output to contain 'set'")
			}
		})
	}
}

func TestPrintComplexTypeAnnotations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"function type", "fn (x: number) -> number", "fn (x: number) -> number"},
		{"keyof type", "keyof T", "keyof T"},
		{"typeof type", "typeof x", "typeof x"},
		{"indexed access", "T[K]", "T[K]"},
		{"intersection type", "A & B", "A & B"},
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
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintMatchWithGuard(t *testing.T) {
	input := `match x {
		n if n > 0 => "positive",
		0 => "zero",
		_ => "negative"
	}`

	expr := parseExpr(t, input)
	opts := DefaultOptions()
	result, err := Print(expr, opts)
	if err != nil {
		t.Fatalf("Print error: %v", err)
	}

	if !strings.Contains(result, "if") {
		t.Error("Expected output to contain guard 'if'")
	}
}

func TestPrintLogicalOperators(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"logical and", "a && b", "a && b"},
		{"logical or", "a || b", "a || b"},
		{"logical not", "!x", "!x"},
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

func TestPrintObjectSpread(t *testing.T) {
	input := `{x: 1, ...rest, y: 2}`
	expected := "{\n    x: 1,\n    ...rest,\n    y: 2\n}"

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

func TestPrintOptionalChaining(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"optional call", "obj?.method()", "obj?.method()"},
		{"optional index", "arr?[0]", "arr?[0]"},
		{"optional property", "obj?.prop", "obj?.prop"},
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

func TestPrintAllTypeAnnotations(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Primitive types
		{"number type", "number", "number"},
		{"string type", "string", "string"},
		{"boolean type", "boolean", "boolean"},
		{"symbol type", "symbol", "symbol"},
		{"unique symbol", "unique symbol", "unique symbol"},
		{"bigint type", "bigint", "bigint"},
		{"any type", "any", "any"},
		{"unknown type", "unknown", "unknown"},
		{"never type", "never", "never"},

		// Literal types
		{"string literal", `"hello"`, `"hello"`},
		{"number literal", "42", "42"},
		{"boolean literal true", "true", "true"},
		{"boolean literal false", "false", "false"},
		{"null literal", "null", "null"},

		// Composite types
		{"union type", "string | number", "string | number"},
		{"intersection type", "A & B & C", "A & B & C"},
		{"tuple type", "[string, number, boolean]", "[string, number, boolean]"},
		{"object type", "{x: number, y: string}", "{\n    x: number,\n    y: string\n}"},

		// Advanced types
		{"keyof type", "keyof T", "keyof T"},
		{"typeof type", "typeof x", "typeof x"},
		{"indexed access", "T[K]", "T[K]"},
		// TODO: parse wildcard types
		// {"wildcard type", "_", "_"},
		{"mutable type", "mut string", "mut string"},

		// Conditional types
		{"conditional type", "if T : string { number } else { boolean }", "if T : string { number } else { boolean }"},
		{"infer type", "infer R", "infer R"},

		// Template literal types
		{"template literal type", "`hello-${string}`", "`hello-${string}`"},

		// Function types
		{"function type", "fn (x: number) -> string", "fn (x: number) -> string"},
		{"function with throws", "fn (x: number) -> string throws Error", "fn (x: number) -> string throws Error"},

		// Type references
		{"simple type ref", "Array<number>", "Array<number>"},
		{"qualified type ref", "MyNamespace.MyType", "MyNamespace.MyType"},

		// Import types (TODO)
		// {"import type", `import("./module")`, `import("./module")`},
		// {"import with qualifier", `import("./module").MyType`, `import("./module").MyType`},
		// {"import with type args", `import("./module").MyType<string>`, `import("./module").MyType<string>`},

		// Rest/spread types (TODO)
		// {"rest spread type", "...string[]", "...string[]"},

		// Intrinsic type
		{"intrinsic type", "intrinsic", "intrinsic"},
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
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPrintObjectTypeElements(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"property",
			"{x: number}",
			"{\n    x: number\n}",
		},
		{
			"readonly property",
			"{readonly x: number}",
			"{\n    readonly x: number\n}",
		},
		{
			"optional property",
			"{x?: number}",
			"{\n    x?: number\n}",
		},
		{
			"method",
			"{greet(name: string) -> string}",
			"{\n    greet(name: string) -> string\n}",
		},
		{
			"getter",
			"{get value() -> number}",
			"{\n    get value(self) -> number\n}",
		},
		{
			"setter",
			"{set value(v: number) -> void}",
			"{\n    set value(mut self, v: number) -> void\n}",
		},
		// {
		// 	"callable",
		// 	"{(x: number) -> string}",
		// 	"{\n    fn (x: number) -> string\n}",
		// },
		// {
		// 	"constructor",
		// 	"{new(x: number) -> MyClass}",
		// 	"{\n    new (x: number) -> MyClass\n}",
		// },
		{
			"mapped type",
			"{[K]: T[K] for K in keyof T}",
			"{\n    [K]: T[K] for K in keyof T\n}",
		},
		{
			"mapped type with optional add",
			"{[K]+?: T[K] for K in keyof T}",
			"{\n    [K]+?: T[K] for K in keyof T\n}",
		},
		{
			"rest spread",
			"{...BaseType}",
			"{\n    ...BaseType\n}",
		},
		{
			"multiple elements",
			"{x: number, readonly y: string, greet(name: string) -> void}",
			"{\n    x: number,\n    readonly y: string,\n    greet(name: string) -> void\n}",
		},
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
				t.Errorf("Expected:\n%s\n\nGot:\n%s", tt.expected, result)
			}
		})
	}
}

// TODO: Add support MatchTypeAnn in the parser
// func TestPrintMatchTypeAnn(t *testing.T) {
// 	tests := []struct {
// 		name     string
// 		input    string
// 		expected string
// 	}{
// 		{
// 			"simple match type",
// 			"match T { string => number, _ => boolean }",
// 			"match T {\n    string => number,\n    _ => boolean\n}",
// 		},
// 		{
// 			"complex match type",
// 			"match T { Array<infer U> => U, _ => never }",
// 			"match T {\n    Array<infer U> => U,\n    _ => never\n}",
// 		},
// 	}

// 	opts := DefaultOptions()
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			typeAnn := parseTypeAnn(t, tt.input)
// 			result, err := Print(typeAnn, opts)
// 			if err != nil {
// 				t.Fatalf("Print error: %v", err)
// 			}
// 			if result != tt.expected {
// 				t.Errorf("Expected:\n%s\n\nGot:\n%s", tt.expected, result)
// 			}
// 		})
// 	}
// }
