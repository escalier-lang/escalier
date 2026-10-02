package tests

import (
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// TestADeclarationKeepsItsTypeParamBoundAndDefault covers the constraint and default a
// declaration's stored type parameters carry.
//
// A declaration that may mention a sibling is pre-bound with a fresh variable per
// constraint and default, and the real annotations are resolved and unified into those
// placeholders afterwards. A placeholder left unsolved renders `unknown`, so a bound or a
// default that never reaches its parameter is silently replaced rather than reported.
func TestADeclarationKeepsItsTypeParamBoundAndDefault(t *testing.T) {
	tests := map[string]struct {
		input string
		name  string
		want  string
	}{
		"AClassBound": {
			input: `class Holder<T: {value: number}> { peer: T }`,
			name:  "Holder",
			want:  "T: {value: number}",
		},
		"AClassDefault": {
			input: `
				declare class Task<T, E = never> {
					run(&self) -> T,
					fail(&self, r: E) -> never,
				}
			`,
			name: "Task",
			want: "T, E = never",
		},
		// A default outside the bound is rejected at the declaration, so the two have to
		// agree here.
		"AClassBoundAndDefault": {
			input: `class Holder<T: {value: number} = {value: number}> { peer: T }`,
			name:  "Holder",
			want:  "T: {value: number} = {value: number}",
		},
		"AnEnumDefault": {
			input: `
				enum Box<T, E = string> {
					Full(v: T),
					Err(e: E),
				}
			`,
			name: "Box",
			want: "T, E = string",
		},
		// A bound naming a sibling is resolved after that sibling, so the resolved list
		// arrives in a different order from the declaration's. The two are paired by
		// position, so one of them is reordered first.
		"AClassBoundNamingASibling": {
			input: `class Holder<T: U, U: {value: number}> { a: T, b: U }`,
			name:  "Holder",
			want:  "T: U, U: {value: number}",
		},
		"AnEnumDefaultNamingASibling": {
			input: `
				enum Box<T = U, U = string> {
					Full(v: T),
					Err(e: U),
				}
			`,
			name: "Box",
			want: "T = U, U = string",
		},
		// An alias and a function resolve their parameters directly rather than through a
		// placeholder, so they are the control.
		"AnAliasDefault": {
			input: `type Alias<T, E = never> = {a: T, b: E}`,
			name:  "Alias",
			want:  "T, E = never",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ns := mustInferAsModule(t, test.input)
			alias := ns.Types[test.name]
			require.NotNilf(t, alias, "no type named %q", test.name)
			require.Equal(t, test.want, renderTypeParams(alias.TypeParams))
		})
	}
}

// renderTypeParams renders a parameter list the way the source writes one, without the
// surrounding `<>`: a name alone, `name: Bound`, `name = Default`, or both.
func renderTypeParams(tps []*type_system.TypeParam) string {
	rendered := make([]string, len(tps))
	for i, tp := range tps {
		s := tp.Name
		if tp.Constraint != nil {
			s += ": " + tp.Constraint.String()
		}
		if tp.Default != nil {
			s += " = " + tp.Default.String()
		}
		rendered[i] = s
	}
	return strings.Join(rendered, ", ")
}
