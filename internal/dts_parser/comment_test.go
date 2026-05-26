package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/snapshot"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// Test the specific example from the user's request
func TestRealWorldSymbolInterface(t *testing.T) {
	input := `interface Symbol {
    /** Returns a string representation of an object. */
    toString(): string;

    /** Returns the primitive value of the specified object. */
    valueOf(): symbol;
}`

	source := &ast.Source{
		Path:     "test.d.ts",
		Contents: input,
		ID:       0,
	}
	parser := NewDtsParser(source)
	module, errors := parser.ParseModule()

	if len(errors) > 0 {
		t.Fatalf("Unexpected errors: %v", errors)
	}

	if module == nil {
		t.Fatal("Expected module to be parsed")
	}

	if len(module.Statements) != 1 {
		t.Fatalf("Expected 1 statement, got %d", len(module.Statements))
	}

	snaps.MatchSnapshot(t, snapshot.String(module))
}

// Test comments in various positions
func TestCommentsInObjectTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"single line comment before property",
			"type Foo = { /** doc */ prop: string }",
		},
		{
			"block comment before property",
			"type Foo = { /* comment */ prop: string }",
		},
		{
			"multiple comments before property",
			"type Foo = { /** doc */ /* inline */ prop: string }",
		},
		{
			"comments between properties",
			"type Foo = { a: string; /** doc for b */ b: number }",
		},
		{
			"comment after separator",
			"type Foo = { a: string, /** doc */ b: number }",
		},
		{
			"multi-line JSDoc comment",
			`type Foo = {
    /**
     * This is a property
     * with a multi-line comment
     */
    prop: string;
}`,
		},
		{
			"comments before method",
			"type Foo = { /** Gets value */ getValue(): number }",
		},
		{
			"comments before index signature",
			"type Foo = { /** Index sig */ [key: string]: any }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Unexpected errors: %v", errors)
			}

			snaps.MatchSnapshot(t, snapshot.String(module))
		})
	}
}

// TestTopLevelJSDocRetention verifies that leading JSDoc comments
// (`/** ... */`) on top-level declarations and class members are
// attached to the AST node's Doc field. Non-JSDoc block comments and
// line comments are not preserved. Intervening non-doc comments between
// a JSDoc block and the declaration reset the captured doc.
func TestTopLevelJSDocRetention(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		assertDoc func(t *testing.T, m *Module)
	}{
		{
			name:  "JSDoc on top-level declare class",
			input: "/** The Boolean class. */\ndeclare class Boolean {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "/** The Boolean class. */", c.Doc)
			},
		},
		{
			name:  "JSDoc on top-level declare fn",
			input: "/** Parse an integer. */\ndeclare function parseInt(s: string): number;",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				f, ok := m.Statements[0].(*FuncDecl)
				require.True(t, ok, "expected FuncDecl, got %T", m.Statements[0])
				require.Equal(t, "/** Parse an integer. */", f.Doc)
			},
		},
		{
			name:  "JSDoc on top-level interface",
			input: "/** The Boolean interface. */\ninterface Boolean { toString(): string }",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				i, ok := m.Statements[0].(*InterfaceDecl)
				require.True(t, ok, "expected InterfaceDecl, got %T", m.Statements[0])
				require.Equal(t, "/** The Boolean interface. */", i.Doc)
			},
		},
		{
			name:  "JSDoc on namespace member",
			input: "declare namespace JSON {\n    /** Parse JSON. */\n    function parse(s: string): any;\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				ns, ok := m.Statements[0].(*NamespaceDecl)
				require.True(t, ok, "expected NamespaceDecl, got %T", m.Statements[0])
				require.Len(t, ns.Statements, 1)
				f, ok := ns.Statements[0].(*FuncDecl)
				require.True(t, ok, "expected FuncDecl, got %T", ns.Statements[0])
				require.Equal(t, "/** Parse JSON. */", f.Doc)
			},
		},
		{
			name:  "line comment before decl is not retained",
			input: "// not JSDoc\ndeclare class Foo {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "", c.Doc)
			},
		},
		{
			name:  "plain block comment before decl is not retained",
			input: "/* not JSDoc */\ndeclare class Foo {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "", c.Doc)
			},
		},
		{
			name:  "intervening non-JSDoc comment resets the doc",
			input: "/** dropped */\n// noise\ndeclare class Foo {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "", c.Doc)
			},
		},
		{
			name:  "later JSDoc wins over earlier",
			input: "/** earlier */\n/** later */\ndeclare class Foo {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "/** later */", c.Doc)
			},
		},
		{
			name:  "JSDoc on class method",
			input: "declare class Foo {\n    /** Method doc. */\n    bar(): void;\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Len(t, c.Members, 1)
				md, ok := c.Members[0].(*MethodDecl)
				require.True(t, ok, "expected MethodDecl, got %T", c.Members[0])
				require.Equal(t, "/** Method doc. */", md.Doc)
			},
		},
		{
			name:  "empty /**/ is not JSDoc",
			input: "/**/\ndeclare class Foo {}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Equal(t, "", c.Doc)
			},
		},
		{
			name:  "JSDoc on class constructor",
			input: "declare class Foo {\n    /** Ctor doc. */\n    constructor(x: number);\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				c, ok := m.Statements[0].(*ClassDecl)
				require.True(t, ok, "expected ClassDecl, got %T", m.Statements[0])
				require.Len(t, c.Members, 1)
				ctor, ok := c.Members[0].(*ConstructorDecl)
				require.True(t, ok, "expected ConstructorDecl, got %T", c.Members[0])
				require.Equal(t, "/** Ctor doc. */", ctor.Doc)
			},
		},
		{
			name:  "JSDoc on ambient module",
			input: "/** Module doc. */\ndeclare module \"foo\" {\n    export const x: number;\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				md, ok := m.Statements[0].(*ModuleDecl)
				require.True(t, ok, "expected ModuleDecl, got %T", m.Statements[0])
				require.Equal(t, "/** Module doc. */", md.Doc)
			},
		},
		{
			name:  "JSDoc on property in object type",
			input: "type Foo = {\n    /** prop doc */\n    x: number;\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				td, ok := m.Statements[0].(*TypeDecl)
				require.True(t, ok, "expected TypeDecl, got %T", m.Statements[0])
				ot, ok := td.TypeAnn.(*ObjectType)
				require.True(t, ok, "expected ObjectType, got %T", td.TypeAnn)
				require.Len(t, ot.Members, 1)
				ps, ok := ot.Members[0].(*PropertySignature)
				require.True(t, ok, "expected PropertySignature, got %T", ot.Members[0])
				require.Equal(t, "/** prop doc */", ps.Doc)
			},
		},
		{
			name:  "JSDoc on declare global",
			input: "/** Global doc. */\ndeclare global {\n    interface Foo {}\n}",
			assertDoc: func(t *testing.T, m *Module) {
				require.Len(t, m.Statements, 1)
				gd, ok := m.Statements[0].(*GlobalDecl)
				require.True(t, ok, "expected GlobalDecl, got %T", m.Statements[0])
				require.Equal(t, "/** Global doc. */", gd.Doc)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{Path: "test.d.ts", Contents: tt.input, ID: 0}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()
			require.Empty(t, errors, "unexpected parse errors: %v", errors)
			require.NotNil(t, module)
			tt.assertDoc(t, module)
		})
	}
}

// Test inline comments in conditional types (regression test for Awaited type)
func TestInlineCommentsInConditionalTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			"inline comment after question mark",
			"type Foo<T> = T extends string ? /* comment */ number : boolean",
		},
		{
			"inline comment after colon",
			"type Foo<T> = T extends string ? number : /* comment */ boolean",
		},
		{
			"inline comments after both",
			"type Foo<T> = T extends string ? /* true */ number : /* false */ boolean",
		},
		{
			"line comment after question mark",
			"type Foo<T> = T extends string ? // comment\n    number : boolean",
		},
		{
			"line comment after colon",
			"type Foo<T> = T extends string ? number : // comment\n    boolean",
		},
		{
			"simplified Awaited type",
			"type Awaited<T> = T extends null | undefined ? T : // special case\n    T extends object ? never : // unwrap objects\n    T // fallback",
		},
		{
			"full Awaited-like type",
			`type Awaited<T> = T extends null | undefined ? T : // special case for null | undefined
    T extends object & { then(onfulfilled: infer F): any; } ? // await only unwraps object types
        F extends ((value: infer V) => any) ? // if the argument to then is callable
            Awaited<V> : // recursively unwrap the value
        never : // the argument to then was not callable
    T; // non-object or non-thenable`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			module, errors := parser.ParseModule()

			if len(errors) > 0 {
				t.Fatalf("Unexpected errors: %v", errors)
			}

			if module == nil {
				t.Fatal("Expected module to be parsed")
			}

			if len(module.Statements) != 1 {
				t.Fatalf("Expected 1 statement, got %d", len(module.Statements))
			}

			snaps.MatchSnapshot(t, snapshot.String(module))
		})
	}
}
