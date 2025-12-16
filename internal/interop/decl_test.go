package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// Helper function to parse a .d.ts string and return the first statement
func parseStatement(t *testing.T, input string) dts_parser.Statement {
	t.Helper()
	source := &ast.Source{
		Path:     "test.d.ts",
		Contents: input,
		ID:       0,
	}
	parser := dts_parser.NewDtsParser(source)
	module, errors := parser.ParseModule()

	if len(errors) > 0 {
		t.Fatalf("Parse errors: %v", errors)
	}

	if len(module.Statements) == 0 {
		t.Fatalf("No statements parsed from input: %s", input)
	}

	return module.Statements[0]
}

func TestConvertStatement(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		wantNil   bool
	}{
		{
			name:      "DeclareVariable",
			input:     "declare var x: number",
			wantError: false,
			wantNil:   false,
		},
		{
			name:      "DeclareFunction",
			input:     "declare function foo(): void",
			wantError: false,
			wantNil:   false,
		},
		{
			name:      "DeclareTypeAlias",
			input:     "type MyType = string",
			wantError: false,
			wantNil:   false,
		},
		{
			name:      "DeclareEnum",
			input:     "enum Color { Red, Green, Blue }",
			wantError: true, // Enums not yet implemented
			wantNil:   false,
		},
		{
			name:      "DeclareClass",
			input:     "declare class MyClass {}",
			wantError: false,
			wantNil:   false,
		},
		{
			name:      "DeclareInterface",
			input:     "interface MyInterface {}",
			wantError: false,
			wantNil:   false,
		},
		// Note: Import and Export declarations are skipped as they're not
		// easily testable in isolation without a full module context
		{
			name:      "AmbientDecl - unwrap and convert",
			input:     "declare const ambient: string",
			wantError: false,
			wantNil:   false,
		},
		{
			name:      "DeclareNamespace - should error",
			input:     "declare namespace NS {}",
			wantError: true,
			wantNil:   false,
		},
		// Note: DeclareModule test skipped as the parser doesn't support
		// the declare module "string" syntax in this context
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			result, err := convertStatement(stmt)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil result but got %T", result)
				}
			} else if !tt.wantError {
				if result == nil {
					t.Errorf("expected non-nil result but got nil")
				}
			}
		})
	}
}

func TestConvertDeclareVariable(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		checkFunc func(*testing.T, *ast.VarDecl)
	}{
		{
			name:      "readonly variable (const)",
			input:     "declare const x: number",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.VarDecl) {
				if decl.Kind != ast.ValKind {
					t.Errorf("expected ValKind for readonly, got %v", decl.Kind)
				}
				if !decl.Declare() {
					t.Errorf("expected declare to be true")
				}
				identPat, ok := decl.Pattern.(*ast.IdentPat)
				if !ok {
					t.Fatalf("expected IdentPat, got %T", decl.Pattern)
				}
				if identPat.Name != "x" {
					t.Errorf("expected name 'x', got %q", identPat.Name)
				}
			},
		},
		{
			name:      "mutable variable (let/var)",
			input:     "declare let y: string",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.VarDecl) {
				if decl.Kind != ast.VarKind {
					t.Errorf("expected VarKind for non-readonly, got %v", decl.Kind)
				}
				identPat, ok := decl.Pattern.(*ast.IdentPat)
				if !ok {
					t.Fatalf("expected IdentPat, got %T", decl.Pattern)
				}
				if identPat.Name != "y" {
					t.Errorf("expected name 'y', got %q", identPat.Name)
				}
			},
		},
		{
			name:      "variable with union type",
			input:     "declare var z: string | number",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.VarDecl) {
				if decl.TypeAnn == nil {
					t.Errorf("expected type annotation")
				}
				_, ok := decl.TypeAnn.(*ast.UnionTypeAnn)
				if !ok {
					t.Errorf("expected UnionTypeAnn, got %T", decl.TypeAnn)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			dv, ok := stmt.(*dts_parser.VarDecl)
			if !ok {
				t.Fatalf("expected DeclareVariable, got %T", stmt)
			}

			result, err := convertVarDecl(dv)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatalf("expected non-nil result")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestConvertDeclareFunction(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		checkFunc func(*testing.T, *ast.FuncDecl)
	}{
		{
			name:      "simple function with no params",
			input:     "declare function foo(): void",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.FuncDecl) {
				if decl.Name.Name != "foo" {
					t.Errorf("expected name 'foo', got %q", decl.Name.Name)
				}
				if !decl.Declare() {
					t.Errorf("expected declare to be true")
				}
				if len(decl.Params) != 0 {
					t.Errorf("expected 0 params, got %d", len(decl.Params))
				}
			},
		},
		{
			name:      "function with parameters",
			input:     "declare function add(a: number, b: number): number",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.FuncDecl) {
				if decl.Name.Name != "add" {
					t.Errorf("expected name 'add', got %q", decl.Name.Name)
				}
				if len(decl.Params) != 2 {
					t.Errorf("expected 2 params, got %d", len(decl.Params))
				}
			},
		},
		{
			name:      "generic function",
			input:     "declare function identity<T>(x: T): T",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.FuncDecl) {
				if decl.Name.Name != "identity" {
					t.Errorf("expected name 'identity', got %q", decl.Name.Name)
				}
				if len(decl.TypeParams) != 1 {
					t.Errorf("expected 1 type param, got %d", len(decl.TypeParams))
				}
				if len(decl.TypeParams) > 0 && decl.TypeParams[0].Name != "T" {
					t.Errorf("expected type param 'T', got %q", decl.TypeParams[0].Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			df, ok := stmt.(*dts_parser.FuncDecl)
			if !ok {
				t.Fatalf("expected DeclareFunction, got %T", stmt)
			}

			result, err := convertFuncDecl(df)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatalf("expected non-nil result")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestConvertDeclareTypeAlias(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		checkFunc func(*testing.T, *ast.TypeDecl)
	}{
		{
			name:      "simple type alias",
			input:     "type MyString = string",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.TypeDecl) {
				if decl.Name.Name != "MyString" {
					t.Errorf("expected name 'MyString', got %q", decl.Name.Name)
				}
				if !decl.Declare() {
					t.Errorf("expected declare to be true")
				}
			},
		},
		{
			name:      "generic type alias",
			input:     "type Box<T> = { value: T }",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.TypeDecl) {
				if decl.Name.Name != "Box" {
					t.Errorf("expected name 'Box', got %q", decl.Name.Name)
				}
				if len(decl.TypeParams) != 1 {
					t.Errorf("expected 1 type param, got %d", len(decl.TypeParams))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			dt, ok := stmt.(*dts_parser.TypeDecl)
			if !ok {
				t.Fatalf("expected DeclareTypeAlias, got %T", stmt)
			}

			result, err := convertTypeDecl(dt)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatalf("expected non-nil result")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestConvertDeclareEnum(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
	}{
		{
			name:      "enum conversion not yet implemented",
			input:     "enum Color { Red, Green, Blue }",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			de, ok := stmt.(*dts_parser.EnumDecl)
			if !ok {
				t.Fatalf("expected DeclareEnum, got %T", stmt)
			}

			result, err := convertEnumDecl(de)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			if result != nil {
				t.Errorf("expected nil result for unimplemented enum, got %T", result)
			}
		})
	}
}

func TestConvertDeclareClass(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		checkFunc func(*testing.T, *ast.ClassDecl)
	}{
		{
			name:      "empty class",
			input:     "declare class MyClass {}",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.ClassDecl) {
				if decl.Name.Name != "MyClass" {
					t.Errorf("expected name 'MyClass', got %q", decl.Name.Name)
				}
				if !decl.Declare() {
					t.Errorf("expected declare to be true")
				}
				if len(decl.Body) != 0 {
					t.Errorf("expected 0 body elements, got %d", len(decl.Body))
				}
			},
		},
		{
			name:      "class with constructor",
			input:     "declare class Point { constructor(x: number, y: number) }",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.ClassDecl) {
				if decl.Name.Name != "Point" {
					t.Errorf("expected name 'Point', got %q", decl.Name.Name)
				}
				if len(decl.Params) != 2 {
					t.Errorf("expected 2 constructor params, got %d", len(decl.Params))
				}
			},
		},
		{
			name:      "class with readonly property",
			input:     "declare class Person { readonly name: string }",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.ClassDecl) {
				if decl.Name.Name != "Person" {
					t.Errorf("expected name 'Person', got %q", decl.Name.Name)
				}
				if len(decl.Body) != 1 {
					t.Fatalf("expected 1 body element, got %d", len(decl.Body))
				}
				field, ok := decl.Body[0].(*ast.FieldElem)
				if !ok {
					t.Errorf("expected FieldElem, got %T", decl.Body[0])
				}
				if field != nil && !field.Readonly {
					t.Errorf("expected readonly field")
				}
			},
		},
		{
			name:      "class with method",
			input:     "declare class Calculator { add(a: number): number }",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.ClassDecl) {
				if len(decl.Body) != 1 {
					t.Fatalf("expected 1 body element, got %d", len(decl.Body))
				}
				_, ok := decl.Body[0].(*ast.MethodElem)
				if !ok {
					t.Errorf("expected MethodElem, got %T", decl.Body[0])
				}
			},
		},
		// Note: Getter/setter test skipped - the class parser doesn't currently
		// support getter/setter syntax in class declarations
		{
			name:      "class with index signature (should be skipped)",
			input:     "declare class Dict { [key: string]: any }",
			wantError: false,
			checkFunc: func(t *testing.T, decl *ast.ClassDecl) {
				// Index signatures should be skipped
				if len(decl.Body) != 0 {
					t.Errorf("expected index signature to be skipped, got %d body elements", len(decl.Body))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			dc, ok := stmt.(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("expected DeclareClass, got %T", stmt)
			}

			result, err := convertClassDecl(dc)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatalf("expected non-nil result")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}

func TestConvertDeclareInterface(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantError bool
		checkFunc func(*testing.T, ast.Decl)
	}{
		{
			name:      "empty interface",
			input:     "interface Empty {}",
			wantError: false,
			checkFunc: func(t *testing.T, decl ast.Decl) {
				typeDecl, ok := decl.(*ast.TypeDecl)
				if !ok {
					t.Fatalf("expected TypeDecl, got %T", decl)
				}
				if typeDecl.Name.Name != "Empty" {
					t.Errorf("expected name 'Empty', got %q", typeDecl.Name.Name)
				}
				objType, ok := typeDecl.TypeAnn.(*ast.ObjectTypeAnn)
				if !ok {
					t.Errorf("expected ObjectTypeAnn, got %T", typeDecl.TypeAnn)
				}
				if objType != nil && len(objType.Elems) != 0 {
					t.Errorf("expected 0 elements, got %d", len(objType.Elems))
				}
			},
		},
		{
			name:      "interface with property",
			input:     "interface Named { readonly name: string }",
			wantError: false,
			checkFunc: func(t *testing.T, decl ast.Decl) {
				typeDecl, ok := decl.(*ast.TypeDecl)
				if !ok {
					t.Fatalf("expected TypeDecl, got %T", decl)
				}
				objType, ok := typeDecl.TypeAnn.(*ast.ObjectTypeAnn)
				if !ok {
					t.Fatalf("expected ObjectTypeAnn, got %T", typeDecl.TypeAnn)
				}
				if len(objType.Elems) != 1 {
					t.Errorf("expected 1 element, got %d", len(objType.Elems))
				}
			},
		},
		{
			name:      "interface with method",
			input:     "interface Callable { call(): void }",
			wantError: false,
			checkFunc: func(t *testing.T, decl ast.Decl) {
				typeDecl, ok := decl.(*ast.TypeDecl)
				if !ok {
					t.Fatalf("expected TypeDecl, got %T", decl)
				}
				objType, ok := typeDecl.TypeAnn.(*ast.ObjectTypeAnn)
				if !ok {
					t.Fatalf("expected ObjectTypeAnn, got %T", typeDecl.TypeAnn)
				}
				if len(objType.Elems) != 1 {
					t.Errorf("expected 1 element, got %d", len(objType.Elems))
				}
				_, ok = objType.Elems[0].(*ast.MethodTypeAnn)
				if !ok {
					t.Errorf("expected MethodTypeAnn, got %T", objType.Elems[0])
				}
			},
		},
		{
			name:      "interface with call signature",
			input:     "interface Function { (): any }",
			wantError: false,
			checkFunc: func(t *testing.T, decl ast.Decl) {
				typeDecl, ok := decl.(*ast.TypeDecl)
				if !ok {
					t.Fatalf("expected TypeDecl, got %T", decl)
				}
				objType, ok := typeDecl.TypeAnn.(*ast.ObjectTypeAnn)
				if !ok {
					t.Fatalf("expected ObjectTypeAnn, got %T", typeDecl.TypeAnn)
				}
				if len(objType.Elems) != 1 {
					t.Errorf("expected 1 element, got %d", len(objType.Elems))
				}
				_, ok = objType.Elems[0].(*ast.CallableTypeAnn)
				if !ok {
					t.Errorf("expected CallableTypeAnn, got %T", objType.Elems[0])
				}
			},
		},
		{
			name:      "interface with index signature (should be skipped)",
			input:     "interface Dictionary { [key: string]: any }",
			wantError: false,
			checkFunc: func(t *testing.T, decl ast.Decl) {
				typeDecl, ok := decl.(*ast.TypeDecl)
				if !ok {
					t.Fatalf("expected TypeDecl, got %T", decl)
				}
				objType, ok := typeDecl.TypeAnn.(*ast.ObjectTypeAnn)
				if !ok {
					t.Fatalf("expected ObjectTypeAnn, got %T", typeDecl.TypeAnn)
				}
				// Index signatures return nil from convertInterfaceMember, so should be skipped
				if len(objType.Elems) != 0 {
					t.Errorf("expected index signature to be skipped, got %d elements", len(objType.Elems))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmt := parseStatement(t, tt.input)
			di, ok := stmt.(*dts_parser.InterfaceDecl)
			if !ok {
				t.Fatalf("expected DeclareInterface, got %T", stmt)
			}

			result, err := convertInterfaceDecl(di)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatalf("expected non-nil result")
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, result)
			}
		})
	}
}
