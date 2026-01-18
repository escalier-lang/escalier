package tests

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeIntersectionType(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedType string
	}{
		"normalizes duplicates": {
			input: `
				type Result = string & string
			`,
			expectedType: "string",
		},
		"normalizes to never when primitives conflict": {
			input: `
				type Result = string & number
			`,
			expectedType: "never",
		},
		"flattens nested intersections": {
			input: `
				type A = string & number
				type Result = boolean & A
			`,
			expectedType: "never",
		},
		"merges object intersections": {
			input: `
				type Result = {a: string} & {b: number}
			`,
			expectedType: "{a: string, b: number}",
		},
		"handles any in intersection": {
			input: `
				type Result = any & string
			`,
			expectedType: "any",
		},
		"handles never in intersection": {
			input: `
				type Result = never & string
			`,
			expectedType: "never",
		},
		"handles readonly properties": {
			input: `
				type Result = {readonly a: string} & {b: number}
			`,
			expectedType: "{readonly a: string, b: number}",
		},
		"expands type aliases within intersection": {
			input: `
				type MyString = string
				type Result = MyString & string
			`,
			expectedType: "string",
		},
		"expands multiple type aliases to same underlying type": {
			input: `
				type MyString = string
				type YourString = string
				type Result = MyString & YourString
			`,
			expectedType: "string",
		},
		"not modify intersection of primitive and object": {
			input: `
				type Result = string & {__brand: "email"}
			`,
			expectedType: "string & {__brand: \"email\"}",
		},
		// NOTE: Phase 3 should fix this test case
		// "intersects properties with the same name": {
		// 	input: `
		// 		type Result = {a: string} & {a: number}
		// 	`,
		// 	expectedType: "{a: never}",
		// },
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: tc.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			binding, ok := scope.Types["Result"]
			assert.True(t, ok, "Expected Result type alias to be defined")

			// Prune to resolve type variables, then repeatedly call NormalizeIntersectionType
			// until the result stops changing
			resultType := type_system.Prune(binding.Type)

			// Keep normalizing until the result stabilizes
			maxIterations := 10
			for i := 0; i < maxIterations; i++ {
				if intersectionType, ok := resultType.(*type_system.IntersectionType); ok {
					previousStr := resultType.String()
					resultType = c.NormalizeIntersectionType(inferCtx, intersectionType)

					// Stop if no change
					if previousStr == resultType.String() {
						break
					}

					// Prune again in case we got a type variable
					resultType = type_system.Prune(resultType)
				} else {
					// Not an intersection type anymore, we're done
					break
				}
			}

			assert.Equal(t, tc.expectedType, resultType.String())
		})
	}
}

func TestDistributiveLawsUsingExpandType(t *testing.T) {
	tests := map[string]struct {
		input   string
		wantErr bool
	}{
		"A & (B | C) distributes to (A & B) | (A & C) - compatible": {
			input: `
                type T1 = {x: number} & ({y: string} | {z: boolean})
                type T2 = ({x: number, y: string} | {x: number, z: boolean})
            `,
			wantErr: false, // Should distribute and be equivalent
		},
		"A & (B | C) - checking against B": {
			input: `
                type T1 = {x: number} & ({y: string} | {z: boolean})
                type T2 = {x: number, y: string}
            `,
			wantErr: true, // T1 is a union after distribution, not assignable to one branch
		},
		"A & (B | C) - checking against union member": {
			input: `
                type T1 = {x: number} & ({y: string} | {z: boolean})
                type T2 = {x: number, y: string} | {x: number, z: boolean}
            `,
			wantErr: false, // One branch of distributed T1 matches one branch of T2
		},
		"(A | B) & C distributes to (A & C) | (B & C)": {
			input: `
                type T1 = ({x: number} | {y: string}) & {z: boolean}
                type T2 = {z: boolean, x: number} | {z: boolean, y: string}
            `,
			wantErr: false, // Should distribute and be equivalent
		},
		"(A | B) & (C | D) distributes to (A&C) | (A&D) | (B&C) | (B&D)": {
			input: `
                type T1 = ({a: number} | {b: string}) & ({c: boolean} | {d: symbol})
                type T2 = {a: number, c: boolean} | {a: number, d: symbol} | {b: string, c: boolean} | {b: string, d: symbol}
            `,
			wantErr: false, // Full cartesian product distribution
		},
		"nested distribution - A & (B & (C | D))": {
			input: `
                type T1 = {a: number} & ({b: string} & ({c: boolean} | {d: symbol}))
                type T2 = ({a: number, b: string, c: boolean} | {a: number, b: string, d: symbol})
            `,
			wantErr: false, // Nested intersections should distribute inner union
		},
		"distribution with never - A & (B | never) = A & B": {
			input: `
                type T1 = {x: number} & ({y: string} | never)
                type T2 = {x: number, y: string}
            `,
			wantErr: false, // never should be eliminated from union before distribution
		},
		"distribution preserves subtyping - specific to general": {
			input: `
                type T1 = {x: 5} & ({y: string} | {z: boolean})
                type T2 = {x: 5, y: string} | {x: 5, z: boolean}
            `,
			wantErr: false, // 5 is subtype of number, distribution preserves this
		},
		"distribution with overlapping properties": {
			input: `
                type T1 = {x: number} & ({x: 5} | {x: 10})
                type T2 = {x: number & 5} | {x: number & 10}
            `,
			wantErr: false, // {x: number} & {x: 5} = {x: number & 5} (could be simplified to {x: 5} with subtype checking)
		},
		"distribution with branded types": {
			input: `
                type T1 = string & ({__brand: "email"} | {__brand: "url"})
                type T2 = (string & {__brand: "email"}) | (string & {__brand: "url"})
            `,
			wantErr: false, // Branded type distribution
		},
		"triple union distribution - A & (B | C | D)": {
			input: `
                type T1 = {x: number} & ({y: string} | {z: boolean} | {w: symbol})
                type T2 = {x: number, y: string} | {x: number, z: boolean} | {x: number, w: symbol}
            `,
			wantErr: false, // Should distribute to all three union members
		},
		"distribution doesn't create invalid types": {
			input: `
                type T1 = string & (number | boolean)
                type T2 = never
            `,
			wantErr: false, // string & number = never, string & boolean = never, never | never = never
		},
		"non-distributive case - union on left": {
			input: `
                type T1 = {x: number} | {y: string}
                type T2 = ({x: number} | {y: string}) & {z: boolean}
            `,
			wantErr: true, // T1 doesn't have z property
		},
		"distribution preserves never in branches": {
			input: `
                type T1 = {x: number} & ({y: string} | {z: number & string})
                type T2 = {x: number, y: string} | {x: number, z: never}
            `,
			wantErr: false, // number & string = never, so second branch is never
		},
		"complex nested distribution": {
			input: `
                type T1 = ({a: number} & ({b: string} | {c: boolean})) & ({d: symbol} | {e: bigint})
                type T2 = {a: number, b: string, d: symbol} | {a: number, b: string, e: bigint} | {a: number, c: boolean, d: symbol} | {a: number, c: boolean, e: bigint}
            `,
			wantErr: false, // Deeply nested distribution
		},
		// TODO: we need to special case handling of nominal type references
		// "distribution with array types": {
		// 	input: `
		//         type T1 = Array<number> & (Array<number> | Array<string>)
		//         type T2 = Array<number>
		//     `,
		// 	wantErr: false, // Array<number> & Array<number> = Array<number>
		// },
		"ExpandType merges object intersections": {
			input: `
                type T1 = {x: number} & {y: string} & {z: boolean}
                type T2 = {x: number, y: string, z: boolean}
            `,
			wantErr: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: tc.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			t1Binding, ok := scope.Types["T1"]
			assert.True(t, ok, "Expected T1 type alias to be defined")
			t2Binding, ok := scope.Types["T2"]
			assert.True(t, ok, "Expected T2 type alias to be defined")

			t1Type := type_system.Prune(t1Binding.Type)
			t2Type := type_system.Prune(t2Binding.Type)

			// Expand T1 to see what it becomes after distribution
			expandedT1, expandErrors := c.ExpandType(inferCtx, t1Type, -1)
			assert.Len(t, expandErrors, 0)

			// Compare string representations
			expandedT1Str := expandedT1.String()
			t2TypeStr := t2Type.String()

			if expandedT1Str != t2TypeStr {
				if !tc.wantErr {
					t.Errorf("ExpandType() mismatch:\nGot:      %s\nExpected: %s", expandedT1Str, t2TypeStr)
				}
			} else {
				if tc.wantErr {
					t.Errorf("ExpandType() expected types to differ but they matched: %s", expandedT1Str)
				}
			}
		})
	}
}

func TestUnifyWithIntersections(t *testing.T) {
	tests := map[string]struct {
		input   string
		wantErr bool
	}{
		"intersection & object - intersection is subtype when all parts satisfy": {
			input: `
				type T1 = {x: number} & {y: string}
				type T2 = {x: number}
			`,
			wantErr: false,
		},
		"intersection & object - fails when no part satisfies": {
			input: `
				type T1 = {x: number} & {y: string}
				type T2 = {z: boolean}
			`,
			wantErr: true,
		},
		"intersection & primitive - intersection contains compatible primitive": {
			input: `
				type T1 = string & {__brand: "email"}
				type T2 = string
			`,
			wantErr: false,
		},
		"intersection & primitive - intersection does not contain compatible primitive": {
			input: `
				type T1 = number & {__brand: "id"}
				type T2 = string
			`,
			wantErr: true,
		},
		"object & intersection - object must satisfy all parts": {
			input: `
				type T1 = {x: number, y: string}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: false,
		},
		"object & intersection - object missing property from one part": {
			input: `
				type T1 = {x: number}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: true,
		},
		"primitive & intersection - branded type": {
			input: `
				type T1 = string
				type T2 = string & {__brand: "email"}
			`,
			wantErr: true, // Plain string is not an email-branded string
		},
		"same intersection types": {
			input: `
				type T1 = {x: number} & {y: string}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: false,
		},
		"t1 has more constraints than t2": {
			input: `
				type T1 = {x: number} & {y: string} & {z: boolean}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: false, // t1 (A & B & C) is a subtype of t2 (A & B)
		},
		"t1 missing constraint from t2": {
			input: `
				type T1 = {x: number}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: true, // t1 (A) is not a subtype of t2 (A & B)
		},
		"intersection with union - intersection distributes": {
			input: `
				type T1 = {x: number} & {y: string}
				type T2 = {x: number} | {y: string}
			`,
			wantErr: false, // Intersection is subtype of union (has all properties)
		},
		"union with intersection - must satisfy all parts": {
			input: `
				type T1 = {x: number} | {y: string}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: true, // Union doesn't have all properties required by intersection
		},
		"intersection with any - should succeed": {
			input: `
				type T1 = {x: number} & string
				type T2 = any
			`,
			wantErr: false, // Any accepts everything
		},
		"any with intersection - should succeed": {
			input: `
				type T1 = any
				type T2 = {x: number} & string
			`,
			wantErr: false, // Any is assignable to anything
		},
		"intersection with never - normalized to never": {
			input: `
				type T1 = {x: number} & never
				type T2 = never
			`,
			wantErr: false, // A & never normalizes to never
		},
		"never with intersection": {
			input: `
				type T1 = never
				type T2 = {x: number} & string
			`,
			wantErr: false, // never is bottom type, subtype of everything
		},
		"conflicting primitives in intersection": {
			input: `
				type T1 = string & number
				type T2 = never
			`,
			wantErr: false, // string & number normalizes to never
		},
		"intersection with literal type": {
			input: `
				type T1 = 5 & number
				type T2 = number
			`,
			wantErr: false, // 5 is a number, intersection reduces to 5
		},
		"literal with intersection containing base type": {
			input: `
				type T1 = 5
				type T2 = number & {__tag: "positive"}
			`,
			wantErr: true, // Literal 5 doesn't have __tag property
		},
		"intersection with array types": {
			input: `
				type T1 = Array<number> & {length: number}
				type T2 = {length: number}
			`,
			wantErr: false, // Array has length property
		},
		"object with overlapping compatible properties": {
			input: `
				type T1 = {x: 5} & {x: number}
				type T2 = {x: number}
			`,
			wantErr: false, // 5 is subtype of number
		},
		"object with overlapping incompatible properties": {
			input: `
				type T1 = {x: string} & {x: number}
				type T2 = {x: number}
			`,
			wantErr: false, // {x: number} part matches T2, so unification succeeds (property merging is Phase 5)
		},
		"intersection with empty object": {
			input: `
				type T1 = {x: number} & {}
				type T2 = {x: number}
			`,
			wantErr: false, // Empty object doesn't add constraints
		},
		"empty object with intersection": {
			input: `
				type T1 = {}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: true, // Empty object doesn't have required properties
		},
		"intersection creates merged type": {
			input: `
				type T1 = {x: number} & {y: string}
				type T2 = {x: number, y: string}
			`,
			wantErr: true, // Intersection type != merged object structurally (Phase 3 may merge them)
		},
		"merged object with intersection": {
			input: `
				type T1 = {x: number, y: string}
				type T2 = {x: number} & {y: string}
			`,
			wantErr: false, // Merged object has all properties
		},
		"triple intersection order independence": {
			input: `
				type T1 = {a: number} & {b: string} & {c: boolean}
				type T2 = {c: boolean} & {a: number} & {b: string}
			`,
			wantErr: false, // Order shouldn't matter in intersections
		},
		"intersection with readonly properties": {
			input: `
				type T1 = {readonly x: number} & {y: string}
				type T2 = {x: number}
			`,
			wantErr: false, // readonly property is compatible with non-readonly
		},
		"intersection with optional properties": {
			input: `
				type T1 = {x: number} & {y?: string}
				type T2 = {x: number}
			`,
			wantErr: false, // Optional property doesn't prevent subtyping
		},
		"unknown with intersection": {
			input: `
				type T1 = unknown
				type T2 = {x: number} & {y: string}
			`,
			wantErr: true, // unknown is top type, can't be assigned to more specific types
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			source := &ast.Source{
				ID:       0,
				Path:     "input.esc",
				Contents: tc.input,
			}

			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			assert.Len(t, errors, 0)

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			t1Binding, ok := scope.Types["T1"]
			assert.True(t, ok, "Expected T1 type alias to be defined")
			t2Binding, ok := scope.Types["T2"]
			assert.True(t, ok, "Expected T2 type alias to be defined")

			t1Type := type_system.Prune(t1Binding.Type)
			t2Type := type_system.Prune(t2Binding.Type)

			unifyErrors := c.Unify(inferCtx, t1Type, t2Type)

			if tc.wantErr && len(unifyErrors) == 0 {
				t.Errorf("Unify() expected error but got none")
			}
			if !tc.wantErr && len(unifyErrors) > 0 {
				t.Errorf("Unify() unexpected error: %v", unifyErrors)
			}
		})
	}
}

func TestIntersectionMemberAccess(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedVars map[string]string
		wantErr      bool
	}{
		"simple property access on object intersection": {
			input: `
				type A = {x: string}
				type B = {y: number}
				type C = A & B
				declare val obj: C
				val x = obj.x
				val y = obj.y
			`,
			expectedVars: map[string]string{
				"x": "string",
				"y": "number",
			},
			wantErr: false,
		},
		"property access returns intersection when both objects have the property": {
			input: `
				type A = {value: string}
				type B = {value: number}
				type C = A & B
				declare val obj: C
				val value = obj.value
			`,
			expectedVars: map[string]string{
				"value": "never",
			},
			wantErr: false,
		},
		"three-way intersection property access": {
			input: `
				type A = {a: string}
				type B = {b: number}
				type C = {c: boolean}
				type D = A & B & C
				declare val obj: D
				val a = obj.a
				val b = obj.b
				val c = obj.c
			`,
			expectedVars: map[string]string{
				"a": "string",
				"b": "number",
				"c": "boolean",
			},
			wantErr: false,
		},
		"branded primitive - access object property": {
			input: `
				type Email = string & {__brand: "email"}
				declare val email: Email
				val brand = email.__brand
			`,
			expectedVars: map[string]string{
				"brand": "\"email\"",
			},
			wantErr: false,
		},
		"branded primitive - access primitive method": {
			input: `
				type Email = string & {__brand: "email"}
				declare val email: Email
				val len = email.length
			`,
			expectedVars: map[string]string{
				"len": "number",
			},
			wantErr: false,
		},
		"branded number - access object property": {
			input: `
				type UserId = number & {__brand: "userId"}
				declare val id: UserId
				val brand = id.__brand
			`,
			expectedVars: map[string]string{
				"brand": "\"userId\"",
			},
			wantErr: false,
		},
		"branded number - access primitive method": {
			input: `
				type UserId = number & {__brand: "userId"}
				declare val id: UserId
				val fixed = id.toFixed
			`,
			expectedVars: map[string]string{
				"fixed": "fn (fractionDigits?: number) -> string throws never",
			},
			wantErr: false,
		},
		"multiple branded properties": {
			input: `
				type Tagged = string & {__brand: "tag", __version: 1}
				declare val tagged: Tagged
				val brand = tagged.__brand
				val version = tagged.__version
				val len = tagged.length
			`,
			expectedVars: map[string]string{
				"brand":   "\"tag\"",
				"version": "1",
				"len":     "number",
			},
			wantErr: false,
		},
		"function intersection - access object property": {
			input: `
				type Callable = (fn () -> string throws never) & {metadata: string}
				declare val func: Callable
				val meta = func.metadata
			`,
			expectedVars: map[string]string{
				"meta": "string",
			},
			wantErr: false,
		},
		"function intersection - access Function method": {
			input: `
				type Callable = (fn () -> string throws never) & {metadata: string}
				declare val func: Callable
				val apply = func.apply
			`,
			expectedVars: map[string]string{
				"apply": "fn (this: Function, thisArg: any, argArray?: any) -> any throws never",
			},
			wantErr: false,
		},
		"function intersection - multiple object properties": {
			input: `
				type EnhancedFunc = (fn (x: number) -> number throws never) & {name: string, version: number}
				declare val func: EnhancedFunc
				val name = func.name
				val version = func.version
			`,
			expectedVars: map[string]string{
				"name":    "string",
				"version": "number",
			},
			wantErr: false,
		},
		"function intersection - access both Function method and custom property": {
			input: `
				type Tagged = (fn () -> void throws never) & {tag: string}
				declare val func: Tagged
				val tag = func.tag
				val call = func.call
			`,
			expectedVars: map[string]string{
				"tag":  "string",
				"call": "fn (this: Function, thisArg: any, ...argArray: Array<any>) -> any throws never",
			},
			wantErr: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			source := &ast.Source{
				ID:       0,
				Path:     "test.es",
				Contents: tc.input,
			}

			module, errors := parser.ParseLibFiles(ctx, []*ast.Source{source})
			if len(errors) > 0 {
				t.Fatalf("Parse errors: %v", errors)
			}

			c := NewChecker()
			inferCtx := Context{
				Scope:      Prelude(c),
				IsAsync:    false,
				IsPatMatch: false,
			}
			inferErrors := c.InferModule(inferCtx, module)
			scope := inferCtx.Scope.Namespace

			if tc.wantErr {
				assert.True(t, len(inferErrors) > 0, "Expected inference errors but got none")
			} else {
				if len(inferErrors) > 0 {
					for _, err := range inferErrors {
						t.Logf("Inference error: %v", err.Message())
					}
				}
				assert.Equal(t, 0, len(inferErrors), "Unexpected inference errors")
			}

			// Verify that all expected variables have the correct types
			for expectedName, expectedType := range tc.expectedVars {
				binding, exists := scope.Values[expectedName]
				assert.True(t, exists, "Expected variable %s to be declared", expectedName)
				if exists {
					actualType := binding.Type.String()
					assert.Equal(t, expectedType, actualType,
						"Type mismatch for variable %s: expected %s but got %s", expectedName, expectedType, actualType)
				}
			}
		})
	}
}
