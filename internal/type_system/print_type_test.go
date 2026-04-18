package type_system_test

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/test_util"
	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

// TestPrintTypeRoundTrip verifies that PrintType produces output that
// matches the original type annotation string. The only acceptable
// difference is when the original had unnecessary parentheses.
func TestPrintTypeRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string // empty means same as input
	}{
		// --- atoms ---
		{name: "number", input: "number"},
		{name: "string", input: "string"},
		{name: "boolean", input: "boolean"},
		{name: "any", input: "any"},
		{name: "unknown", input: "unknown"},
		{name: "never", input: "never"},
		{name: "void", input: "void"},

		// --- literal types ---
		{name: "string literal", input: `"hello"`},
		{name: "number literal", input: "42"},
		{name: "true literal", input: "true"},
		{name: "false literal", input: "false"},
		{name: "null literal", input: "null"},
		{name: "undefined literal", input: "undefined"},

		// --- type references ---
		{name: "simple ref", input: "Array<number>"},
		{name: "multi-arg ref", input: "Map<string, number>"},

		// --- union ---
		{name: "simple union", input: "number | string"},
		{name: "three-way union", input: "number | string | boolean"},

		// --- intersection ---
		{name: "simple intersection", input: "A & B"},
		{name: "three-way intersection", input: "A & B & C"},

		// --- mixed union and intersection (precedence) ---
		{name: "intersection in union", input: "A & B | C & D"},
		{name: "union in intersection needs parens", input: "(A | B) & C"},
		{name: "union in intersection both sides", input: "(A | B) & (C | D)"},
		{name: "intersection then union", input: "A & B | C"},
		{name: "union then intersection", input: "A | B & C"},
		{
			name:  "unnecessary parens on intersection in union",
			input: "(A & B) | C",
			want:  "A & B | C",
		},

		// --- keyof ---
		{name: "keyof atom", input: "keyof T"},
		{name: "keyof in union", input: "keyof T | string"},
		{
			name:  "unnecessary parens on keyof in union",
			input: "(keyof T) | string",
			want:  "keyof T | string",
		},
		{name: "keyof union needs parens", input: "keyof (A | B)"},
		{name: "keyof intersection needs parens", input: "keyof (A & B)"},

		// --- mut ---
		{name: "mut atom", input: "mut number"},
		{name: "mut in union", input: "mut number | string"},
		{
			name:  "unnecessary parens on mut in union",
			input: "(mut number) | string",
			want:  "mut number | string",
		},
		{name: "mut union needs parens", input: "mut (number | string)"},
		{name: "mut intersection needs parens", input: "mut (A & B)"},

		// --- tuple ---
		{name: "tuple", input: "[number, string]"},
		{name: "tuple with union elem", input: "[number | string, boolean]"},

		// --- function ---
		{name: "simple function", input: "fn (x: number) -> string"},
		{name: "function with union return", input: "fn (x: number) -> string | number"},
		{name: "function with union param", input: "fn (x: number | string) -> boolean"},
		{name: "union of functions", input: "(fn (x: number) -> string) | (fn (x: string) -> number)"},
		{name: "intersection of functions", input: "(fn (x: number) -> string) & (fn (x: string) -> number)"},

		// --- object ---
		{name: "simple object", input: "{x: number, y: string}"},
		{name: "object with union prop", input: "{x: number | string}"},

		// --- index type ---
		{name: "index type", input: "T[K]"},

		// --- conditional type ---
		{name: "conditional", input: "if A : B { C } else { D }"},

		// --- infer ---
		{name: "infer", input: "infer T"},

		// --- combinations ---
		{name: "keyof in intersection", input: "keyof T & U"},
		{
			name:  "unnecessary parens on keyof in intersection",
			input: "(keyof T) & U",
			want:  "keyof T & U",
		},
		{name: "nested union in intersection in union", input: "(A | B) & C | D"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ := test_util.ParseTypeAnn(tt.input)
			got := type_system.PrintType(typ, type_system.PrintConfig{})
			want := tt.want
			if want == "" {
				want = tt.input
			}
			assert.Equal(t, want, got)
		})
	}
}
