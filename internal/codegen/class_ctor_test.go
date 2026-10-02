package codegen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmitsTheImplicitConstructor covers the constructor a class that declares none
// gets. Emission derives it from the declared fields through ast.ImplicitConstructor,
// which is also what internal/checker installs during inference, so the emitted class
// is the same whichever checker ran.
//
// buildSource runs no checker, so these assert what emission derives on its own.
func TestEmitsTheImplicitConstructor(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "OneParameterPerField",
			src: `class Point {
				x: number,
				y: number,
			}`,
			want: `export class Point {
  constructor(temp1, temp2) {
    const x = temp1;
    const y = temp2;
    this.x = x;
    this.y = y;
  }
}`,
		},
		{
			name: "AFieldlessClassTakesNoParameters",
			src:  `class Empty {}`,
			want: `export class Empty {
  constructor() {
  }
}`,
		},
		{
			name: "AnOptionalFieldTakesNoParameter",
			src: `class Partial {
				required: number,
				optional?: string,
			}`,
			want: `export class Partial {
  constructor(temp1) {
    const required = temp1;
    this.required = required;
  }
}`,
		},
		{
			name: "AStaticFieldTakesNoParameter",
			src: `class WithStatic {
				instance: number,
				static shared: number = 1,
			}`,
			want: `export class WithStatic {
  constructor(temp1) {
    const instance = temp1;
    this.instance = instance;
  }
  static shared = 1;
}`,
		},
		// A key that is not a valid JS identifier cannot name a parameter, so the
		// parameter is named `_field<N>` and the field is reached by index.
		{
			name: "AKeyThatIsNoIdentifierIsReachedByIndex",
			src: `class Odd {
				"foo-bar": number,
			}`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this["foo-bar"] = _field1;
  }
}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, buildSource(t, test.src))
		})
	}
}

// TestEmitsNoImplicitConstructor covers the classes that get none. A declared
// constructor is emitted as written rather than joined by a second one, and the two
// shapes with no derivable constructor emit a body without one.
func TestEmitsNoImplicitConstructor(t *testing.T) {
	// A declared constructor is the one emitted, so the class carries exactly one.
	t.Run("ADeclaredConstructorIsNotJoinedBySecond", func(t *testing.T) {
		got := buildSource(t, `class Point {
			x: number,
			constructor(&mut self, x: number) {
				self.x = x
			},
		}`)
		require.Equal(t, `export class Point {
  constructor(temp1) {
    const x = temp1;
    this.x = x;
  }
}`, got)
	})

	// A `declare class` describes a type and has no implementation to construct.
	t.Run("ADeclareClassGetsNone", func(t *testing.T) {
		got := buildSource(t, `declare class Point {
			x: number,
		}`)
		require.NotContains(t, got, "constructor")
	})

	// A subclass's constructor would have to forward to `super(…)`, so the checkers
	// require it to declare its own and emission derives none.
	t.Run("ASubclassGetsNone", func(t *testing.T) {
		got := buildSource(t, `class Base {
			x: number,
		}
		class Derived extends Base {
			y: number,
		}`)
		require.Contains(t, got, "export class Derived extends Base {")
		require.Equal(t, 1, strings.Count(got, "constructor"),
			"only Base gets a derived constructor")
	})

	// A computed key leaves no parameter name to bind, so no constructor is derived.
	// The checkers report that the class needs an explicit one.
	t.Run("AComputedKeyFieldGetsNone", func(t *testing.T) {
		got := buildSource(t, `class Odd {
			[Symbol.iterator]: number,
		}`)
		require.NotContains(t, got, "constructor")
	})
}
