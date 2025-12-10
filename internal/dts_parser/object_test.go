package dts_parser

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
)

func TestObjectTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty object", "{}"},
		{"single property", "{ name: string }"},
		{"multiple properties", "{ name: string, age: number }"},
		{"optional property", "{ name?: string }"},
		{"readonly property", "{ readonly id: number }"},
		{"readonly and optional", "{ readonly name?: string }"},
		{"method signature", "{ getName(): string }"},
		{"method with params", "{ greet(name: string): void }"},
		// TODO: method with type params requires more sophisticated lookahead
		// {"method with type params", "{ map<T>(fn: (x: T) => T): T[] }"},
		// TODO: optional methods require checking for '?' before type params
		// {"optional method", "{ getName?(): string }"},
		{"call signature", "{ (x: number): string }"},
		{"construct signature", "{ new (x: string): Object }"},
		{"multiple call signatures", "{ (x: number): string, (x: string): number }"},
		{"index signature string", "{ [key: string]: any }"},
		{"index signature number", "{ [index: number]: string }"},
		{"readonly index signature", "{ readonly [key: string]: any }"},
		{"getter signature", "{ get value(): number }"},
		{"setter signature", "{ set value(v: number) }"},
		{"getter and setter", "{ get value(): number, set value(v: number) }"},
		{"mixed members", "{ name: string, getName(): string, [key: string]: any }"},
		{"string literal key", "{ \"prop-name\": string }"},
		{"number literal key", "{ 42: string }"},
		{"trailing comma", "{ name: string, age: number, }"},
		{"nested object", "{ user: { name: string, age: number } }"},
		{"union in property", "{ value: string | number }"},
		{"function type property", "{ callback: (x: number) => void }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestObjectTypesWithTypeParams(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		// TODO: These require more sophisticated lookahead to disambiguate '<' in different contexts
		// {"method with single type param", "{ get<T>(key: string): T }"},
		// {"method with multiple type params", "{ map<T, U>(fn: (x: T) => U): U[] }"},
		{"method with constraint", "{ sort<T extends number>(items: T[]): T[] }"},
		// {"call sig with type params", "{ <T>(x: T): T }"},
		{"construct sig with type params", "{ new <T>(x: T): Container<T> }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestPropertyKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"identifier key", "{ name: string }"},
		{"string literal key", "{ \"prop-name\": string }"},
		{"number literal key", "{ 0: string }"},
		{"number literal key 2", "{ 42: string }"},
		// TODO: computed key with bare identifier requires disambiguation
		// {"computed key simple", "{ [key]: string }"},
		{"multiple key types", "{ name: string, \"str-key\": number, 42: boolean }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestCallAndConstructSignatures(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple call sig", "{ (x: number): string }"},
		{"call sig no params", "{ (): void }"},
		{"call sig multiple params", "{ (x: number, y: string): boolean }"},
		// TODO: call sig with type params requires checking for '<' before '('
		// {"call sig with type params", "{ <T>(x: T): T }"},
		{"multiple call sigs", "{ (x: number): string, (x: string): number }"},
		{"construct sig", "{ new (x: string): Object }"},
		{"construct sig with type params", "{ new <T>(x: T): Container<T> }"},
		{"call and construct", "{ (): string, new (): Object }"},
		{"call sig with return type", "{ (x: number): string | number }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestIndexSignatures(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"string index", "{ [key: string]: any }"},
		{"number index", "{ [index: number]: string }"},
		{"readonly string index", "{ readonly [key: string]: any }"},
		{"readonly number index", "{ readonly [index: number]: string }"},
		{"index with property", "{ [key: string]: any, name: string }"},
		{"multiple properties with index", "{ name: string, age: number, [key: string]: any }"},
		{"complex value type", "{ [key: string]: string | number | boolean }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestGetterSetterSignatures(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple getter", "{ get value(): number }"},
		{"simple setter", "{ set value(v: number) }"},
		{"getter and setter", "{ get value(): number, set value(v: number) }"},
		{"getter with complex return", "{ get data(): { name: string, age: number } }"},
		{"setter with complex param", "{ set data(v: { name: string, age: number }) }"},
		{"multiple getters setters", "{ get x(): number, set x(v: number), get y(): string, set y(v: string) }"},
		{"mixed with properties", "{ name: string, get fullName(): string, set fullName(v: string) }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}

func TestComplexObjectTypes(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"all member types", "{ name: string, getName(): string, (x: number): string, new (): Object, [key: string]: any, get value(): number, set value(v: number) }"},
		{"deeply nested", "{ outer: { middle: { inner: string } } }"},
		{"array of objects", "{ items: { id: number, name: string }[] }"},
		{"union with object", "string | { name: string }"},
		{"intersection with object", "{ name: string } & { age: number }"},
		{"generic object property", "{ data: Map<string, { id: number }> }"},
		{"function returning object", "{ getUser(): { name: string, age: number } }"},
		// TODO: optional methods need special handling for '?' before '(' or '<'
		// {"optional everything", "{ name?: string, age?: number, getEmail?(): string }"},
		{"readonly everything", "{ readonly name: string, readonly age: number, readonly [key: string]: any }"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{
				Path:     "test.d.ts",
				Contents: tt.input,
				ID:       0,
			}
			parser := NewDtsParser(source)
			typeAnn := parser.ParseTypeAnn()

			if typeAnn == nil {
				t.Fatalf("Failed to parse type: %s", tt.input)
			}

			if len(parser.errors) > 0 {
				t.Fatalf("Unexpected errors: %v", parser.errors)
			}

			snaps.MatchSnapshot(t, typeAnn)
		})
	}
}
