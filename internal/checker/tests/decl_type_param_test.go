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
		// A bound naming a sibling is resolved after that sibling, so resolution walks
		// the parameters in an order the declaration did not write. Both lists reaching
		// unifyTypeParams are in declaration order, which is what pairs each bound with
		// the parameter that carries it.
		"AClassBoundNamingASibling": {
			input: `class Holder<T: U, U: {value: number}> { a: T, b: U }`,
			name:  "Holder",
			want:  "T: U, U: {value: number}",
		},
		// A bound may name a later sibling, where a default may not. A default is filled
		// in from the arguments before it, so it can only reach a parameter that already
		// has one. A forward bound is therefore the only shape that makes resolution order
		// differ from declaration order.
		"AnEnumBoundNamingASibling": {
			input: `
				enum Box<T: U, U: {value: number}> {
					Full(v: T),
					Err(e: U),
				}
			`,
			name: "Box",
			want: "T: U, U: {value: number}",
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

// TestASignatureKeepsItsTypeParamOrder covers the order a signature's type parameters are
// stored in when one's bound names a sibling.
//
// Resolving a bound that names a sibling requires that sibling to be resolved first, so
// resolution walks the parameters in an order the declaration did not write. The stored
// list comes back in declaration order, for the reasons resolveTypeParams' doc gives.
func TestASignatureKeepsItsTypeParamOrder(t *testing.T) {
	tests := map[string]struct {
		input       string
		bindingName string
		want        string
	}{
		"AFunction": {
			input:       `declare fn f<T: U, U: {value: number}>(a: T, b: U) -> T`,
			bindingName: "f",
			want:        "fn <T: U, U: {value: number}>(a: T, b: U) -> T",
		},
		"AFunctionTypeAnnotation": {
			input:       `declare val g: fn <T: U, U: {value: number}>(a: T, b: U) -> T`,
			bindingName: "g",
			want:        "fn <T: U, U: {value: number}>(a: T, b: U) -> T",
		},
		// A constructor's own parameters follow the class's, so the class contributes `S`
		// and the constructor contributes `T` and `U`.
		"AConstructor": {
			input: `
				class Holder<S> {
					peer: S,
					constructor<T: U, U: {value: number}>(&mut self, s: S, a: T, b: U) {
						self.peer = s
					},
				}
			`,
			bindingName: "Holder",
			want:        "{new <S, T: U, U: {value: number}>(s: S, a: T, b: U) -> Holder<S>}",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ns := mustInferAsModule(t, test.input)
			binding := ns.Values[test.bindingName]
			require.NotNilf(t, binding, "no value named %q", test.bindingName)
			require.Equal(t, test.want, binding.Type.String())
		})
	}
}
