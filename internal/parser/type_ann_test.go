package parser

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/snapshot"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTypeAnnNoErrors(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		"StringTypeAnn": {
			input: "string",
		},
		"StringLiteralTypeAnn": {
			input: "\"hello\"",
		},
		"RegexLiteralTypeAnn": {
			input: "/hello/gi",
		},
		"NumberTypeAnn": {
			input: "number",
		},
		"UnknownTypeAnn": {
			input: "unknown",
		},
		"NumberLiteralTypeAnn": {
			input: "5",
		},
		"TrueLiteralTypeAnn": {
			input: "true",
		},
		"FalseLiteralTypeAnn": {
			input: "false",
		},
		"FuncWithoutParams": {
			input: "fn() -> number",
		},
		"FuncWithParams": {
			input: "fn(x: number, y: string) -> boolean",
		},
		"FuncWithTypeParams": {
			input: "fn<T: number, U: string>(x: T, y: U) -> boolean",
		},
		"FuncWithThrows": {
			input: "fn(x: number) -> boolean throws Error",
		},
		"ObjectMethodWithThrows": {
			input: "{parse(self) -> number throws SyntaxError}",
		},
		"UnionType": {
			input: "A | B | C",
		},
		"IntersectionType": {
			input: "A & B & C",
		},
		"UnionAndIntersectionType": {
			input: "A & B | X & Y",
		},
		"IndexedTypeWithBrackets": {
			input: "A[B]",
		},
		"IndexedTypeWithDot": {
			input: "A.B", // parses as a qualified type reference
		},
		"QualifiedTypeRef": {
			input: "Foo.Bar",
		},
		"DeepQualifiedTypeRef": {
			input: "Foo.Bar.Baz",
		},
		"QualifiedTypeRefWithTypeArgs": {
			input: "Foo.Bar<T, U>",
		},
		"MutableType": {
			input: "mut A",
		},
		"MutableUnionType": {
			input: "mut number | string",
		},
		"BorrowType": {
			input: "&{x: number}",
		},
		"BorrowMutType": {
			input: "&mut {x: number}",
		},
		"BorrowLifetimeType": {
			input: "&'a {x: number}",
		},
		"BorrowLifetimeMutType": {
			input: "&'a mut {x: number}",
		},
		"BorrowTypeRef": {
			input: "&Point",
		},
		"BorrowBindsTighterThanUnion": {
			input: "&A | B", // parses as (&A) | B
		},
		"BorrowOfUnion": {
			input: "&(A | B)", // borrow of the whole union
		},
		"BorrowAsIntersectionMember": {
			input: "&A & B", // (&A) & B, no parens needed
		},
		"InfixIntersectionStillParses": {
			input: "A & B", // infix '&' stays intersection
		},
		"ConditionalType": {
			input: "if A : B { C } else { D }",
		},
		"ConditionalTypeWithChaining": {
			input: "if A : B { C } else if E : F { G } else { H }",
		},
		"InferType": {
			input: "infer T",
		},
		"ConditionalTypeWithInfer": {
			input: "if T : fn(...args: infer P) -> any { P } else { never }",
		},
		"BasicObjectType": {
			input: "{a: A, b?: B, [c]: C, [d]?: D}",
		},
		"ObjectTypeWithReadonlyProperty": {
			input: "{id: number, readonly name: string}",
		},
		"MappedObjectType": {
			input: "{[K]: T[K] for K in Keys<T>}",
		},
		"MappedObjectTypeOptionalProperties": {
			input: "{[P]?: T[P] for P in keyof T}",
		},
		"MappedObjectTypeAddOptionalProperties": {
			input: "{[P]+?: T[P] for P in keyof T}",
		},
		"MappedObjectTypeRemoveOptionalProperties": {
			input: "{[P]-?: T[P] for P in keyof T}",
		},
		"MappedObjectTypeReadonlyProperties": {
			input: "{readonly [P]: T[P] for P in keyof T}",
		},
		"MappedObjectTypeAddReadonlyProperties": {
			input: "{+readonly [P]: T[P] for P in keyof T}",
		},
		"MappedObjectTypeRemoveReadonlyProperties": {
			input: "{-readonly [P]: T[P] for P in keyof T}",
		},
		"MappedObjectTypePropertyRenaming": {
			input: "{[`prefix_${K}`]: T[K] for K in keyof T}",
		},
		"MappedObjectTypeWithFiltering": {
			input: "{[K]: T[K] for K in keyof T if T[K] : string}",
		},
		"MappedObjectTypeShorthand": {
			input: "{[K: keyof T]: T[K]}",
		},
		"MappedObjectTypeShorthandOptional": {
			input: "{[K: string]?: number}",
		},
		"MappedObjectTypeShorthandReadonly": {
			input: "{readonly [K: keyof T]: T[K]}",
		},
		"MappedObjectTypeShorthandWithFiltering": {
			input: "{[K: keyof T]: T[K] if T[K] : string}",
		},
		"MappedObjectTypeShorthandAlongsideProperty": {
			input: "{id: number, [K: string]?: number}",
		},
		"MappedObjectTypeTwoShorthandsOverDifferentKeySets": {
			input: "{[K: string]?: number, [J: number]?: boolean}",
		},
		"ObjectTypeWithRestSpread": {
			input: "{x: string, ...T}",
		},
		"ObjectTypeWithOnlyRestSpread": {
			input: "{...T}",
		},
		"ObjectTypeWithMultipleRestSpread": {
			input: "{x: string, ...T, y: number, ...U}",
		},
		"Symbol": {
			input: "symbol",
		},
		"UniqueSymbol": {
			input: "unique symbol",
		},
		"TemplateLiteralType": {
			input: "`hello-${T}`",
		},
		"TemplateLiteralTypeMultipleParams": {
			input: "`${A}-${B}-${C}`",
		},
		"TemplateLiteralTypeNoParams": {
			input: "`hello-world`",
		},
		"KeyOfType": {
			input: "keyof T",
		},
		"UnionTypeWithKeyOf": {
			input: "keyof T | U",
		},
		"KeyOfObjectType": {
			input: "keyof {x: string, y: number}",
		},
		"IntersectionTypeWithKeyOf": {
			input: "keyof T & U",
		},
		"NegationType": {
			input: "~T",
		},
		"NegationOfLiteral": {
			input: "~\"a\"",
		},
		// `~` binds tighter than `&`, so this parses as `string & (~"a")`.
		"IntersectionTypeWithNegation": {
			input: "string & ~\"a\"",
		},
		// `~` binds tighter than `|`, so this parses as `(~T) | U`.
		"UnionTypeWithNegation": {
			input: "~T | U",
		},
		"TypeOfIdent": {
			input: "typeof x",
		},
		"UnionTypeWithTypeOf": {
			input: "typeof x | U",
		},
		"IntersectionTypeWithTypeOf": {
			input: "typeof x & U",
		},
		"TupleWithTrailingRestSpread": {
			input: "[number, ...T]",
		},
		"TupleWithLeadingRestSpread": {
			input: "[...T, string]",
		},
		"TupleWithRestSpreadArray": {
			input: "[number, ...Array<string>]",
		},
		"TupleInexact": {
			input: "[number, string, ...]",
		},
		"ObjectInexact": {
			input: "{x: number, y: number, ...}",
		},
		"ObjectInexactOnly": {
			input: "{...}",
		},
		"ObjectConstructorSignature": {
			input: "{new (x: number) -> Point}",
		},
		"ObjectConstructorSignatureWithRestParam": {
			input: "{new (...args: [number, string]) -> Point}",
		},
		"ObjectConstructorSignatureWithTypeParams": {
			input: "{new <T>(x: T) -> Box<T>}",
		},
		"ObjectConstructorSignatureBesideProperty": {
			input: "{new (x: number) -> Point, origin: Point}",
		},
		"ObjectMethod": {
			input: "{f(x: number) -> string}",
		},
		"ObjectMethodWithReceiver": {
			input: "{f(mut self, x: number) -> string}",
		},
		"ObjectMethodWithTypeParams": {
			input: "{f<T>(x: T) -> T}",
		},
		"ObjectGetter": {
			input: "{get a(self) -> number}",
		},
		// A setter yields nothing, so it writes no `-> R`, the way a class body declares one.
		"ObjectSetter": {
			input: "{set a(mut self, v: number)}",
		},
		// The arrow still parses on a setter, so a hand-converted `.d.ts` accessor keeps it.
		"ObjectSetterWithReturnType": {
			input: "{set a(mut self, v: number) -> undefined}",
		},
		"ObjectGetterWithThrows": {
			input: "{get a(self) -> number throws RangeError}",
		},
		// The clause follows the parameter list directly when a setter writes no arrow.
		"ObjectSetterWithThrows": {
			input: "{set a(mut self, v: number) throws RangeError}",
		},
		"UnionOfFunctions": {
			input: "(fn (x: number) -> string) | (fn (x: string) -> number)",
		},
		"IntersectionOfFunctions": {
			input: "(fn (x: number) -> string) & (fn (x: string) -> number)",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			typeAnn := parser.typeAnn()

			snaps.MatchSnapshot(t, snapshot.String(typeAnn))
			assert.Equal(t, []*Error{}, parser.errors)
		})
	}
}

func TestParseTypeAnnErrorHandling(t *testing.T) {
	tests := map[string]struct {
		input string
	}{
		// A key variable carries no lifetime, so a lifetime-decorated reference in the brackets is
		// not the index-signature shorthand. It falls through to the computed-key form, which
		// rejects the lifetime, rather than reading the name back and dropping the lifetime.
		"LifetimeQualifiedBracketIsNotTheShorthand": {
			input: "{['a K: string]?: number}",
		},
		"LifetimeArgBracketIsNotTheShorthand": {
			input: "{[K<'a>: string]?: number}",
		},
		"IncompleteUnion": {
			input: "number |",
		},
		"IncompleteIntersection": {
			input: "string &",
		},
		"KeyofMissingType": {
			input: "keyof",
		},
		"NegationMissingType": {
			input: "~",
		},
		"FuncTypeMissingReturnType": {
			input: "fn() ->",
		},
		"PropertyMissingType": {
			input: "{x: }",
		},
		"ConstructorSignatureMissingReturnType": {
			input: "{new (x: number)}",
		},
		"ConditionalTypeMissingElse": {
			input: "if A : B { C } else {",
		},
		"ConditionalTypeMissingThen": {
			input: "if A : B { } else { D }",
		},
		// Escalier has no `void` type. The keyword is still lexed so the parser can name
		// `undefined` as the replacement, and it recovers to that type, so the snapshot
		// shows an `undefined` annotation alongside the error.
		"VoidIsNotAType": {
			input: "void",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: test.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			parser := NewParser(ctx, source)
			typeAnn := parser.typeAnn()

			assert.NotNil(t, typeAnn, "Expected non-nil type annotation from recovery")
			snaps.MatchSnapshot(t, snapshot.String(typeAnn))
			assert.Greater(t, len(parser.errors), 0, "Expected parsing errors but got none")
			snaps.MatchSnapshot(t, snapshot.String(parser.errors))
		})
	}
}

// A negative number literal type. The sign belongs to the literal rather than to
// an operator, so `-1` is one annotation. The `.d.ts` surface reaches it two
// ways, and each fails through its own code path when the rule is missing. A
// bare annotation reports through typeAnnRequired. A tuple element reports the
// bracket mismatch instead.
func TestParseNegativeNumberLiteralTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare annotation", "-1", "-1"},
		{"fractional", "-1.5", "-1.5"},
		{"negative zero", "-0", "-0"},
		// `readonly TIMEOUT_IGNORED: -1` on WebGL2RenderingContext.
		{"object property", "{readonly TIMEOUT_IGNORED: -1}",
			"{\n    readonly TIMEOUT_IGNORED: -1\n}"},
		// The depth counter `FlatArray` indexes, `[-1, 0, 1, … 20][Depth]`.
		{"tuple element", "[-1, 0, 1]", "[-1, 0, 1]"},
		{"indexed tuple element", "[-1, 0, 1][Depth]", "[-1, 0, 1][Depth]"},
		{"union member", "-1 | 0 | 1", "-1 | 0 | 1"},
		{"positive is unchanged", "[1, 2]", "[1, 2]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.input)
			require.Empty(t, errors, "%s should parse", tt.input)
			require.NotNil(t, typeAnn)

			// The printer emits these, so the printed form has to parse back to
			// the same thing. A parse-only test would pass while leaving the
			// round-trip the converter depends on broken.
			printed, err := printer.Print(typeAnn, printer.DefaultOptions())
			require.NoError(t, err)
			require.Equal(t, tt.want, printed)

			reparsed, reparseErrors := parseTypeAnnSrc(t, printed)
			require.Empty(t, reparseErrors, "%q should parse back", printed)
			reprinted, err := printer.Print(reparsed, printer.DefaultOptions())
			require.NoError(t, err)
			require.Equal(t, printed, reprinted, "printing should be stable")
		})
	}
}

// A `-` that no number follows is not a type. Neither mapped-type modifier that
// opens with `-` claims it either, so the annotation is still missing.
func TestAMinusWithoutANumberIsNotAType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
	}{
		{"minus then a name", "-Foo"},
		{"minus alone", "-"},
		{"minus then a string", `-"a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, errors := parseTypeAnnSrc(t, "{x: "+tt.input+"}")
			require.NotEmpty(t, errors, "%s should report", tt.input)
			require.Equal(t, "expected a type annotation", errors[0].Message)
		})
	}

	// A lifetime read before the `-` cannot be given back, so declining the
	// token reports against the lifetime rather than returning nil, which the
	// caller would read as "nothing was consumed".
	t.Run("after a lifetime", func(t *testing.T) {
		t.Parallel()
		_, errors := parseTypeAnnSrc(t, "{x: 'a -Foo}")
		require.NotEmpty(t, errors)
		require.Equal(t, "expected a type annotation after lifetime", errors[0].Message)
	})
}

// The mapped-type modifiers that open with `-` keep working, since each requires
// its own following token rather than a number.
func TestMappedTypeMinusModifiers(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input string }{
		{"remove readonly", "{-readonly [K]: T[K] for K in keyof T}"},
		{"remove optional", "{[K]-?: T[K] for K in keyof T}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			typeAnn, errors := parseTypeAnnSrc(t, tt.input)
			require.Empty(t, errors, "%s should parse", tt.input)
			require.NotNil(t, typeAnn)
		})
	}
}
