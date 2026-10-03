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
		{
			name: "ANumericKeyIsReachedByIndex",
			src:  `class Odd { 1: number }`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this[1] = _field1;
  }
}`,
		},
		// A reserved word cannot be bound, so the field takes a generated parameter
		// even though its key is identifier-shaped.
		{
			name: "AReservedWordKeyIsReachedByIndex",
			src:  `class Odd { "class": number }`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this["class"] = _field1;
  }
}`,
		},
		// A generated name is chosen around the names the fields bind directly, so
		// `_field1` here goes to the second field.
		{
			name: "AGeneratedNameAvoidsADeclaredField",
			src: `class Odd {
				_field1: number,
				"foo-bar": string,
			}`,
			want: `export class Odd {
  constructor(temp1, temp2) {
    const _field1 = temp1;
    const _field2 = temp2;
    this._field1 = _field1;
    this["foo-bar"] = _field2;
  }
}`,
		},
		// An identifier key spelled like a reserved word is a valid property name and
		// not a valid binding name, so only the parameter is renamed.
		{
			name: "AnIdentifierKeySpelledLikeAKeywordIsReachedByIndex",
			src:  `class Odd { class: number }`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this["class"] = _field1;
  }
}`,
		},
		// A parameter named `self` would shadow the receiver, and codegen lowers every
		// `self` to `this`, so the field would be assigned the instance.
		{
			name: "AFieldNamedSelfIsReachedByIndex",
			src:  `class Odd { self: number }`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this["self"] = _field1;
  }
}`,
		},
		{
			name: "TwoGeneratedNamesDoNotCollide",
			src: `class Odd {
				"a-b": number,
				"c-d": string,
			}`,
			want: `export class Odd {
  constructor(temp1, temp2) {
    const _field1 = temp1;
    const _field2 = temp2;
    this["a-b"] = _field1;
    this["c-d"] = _field2;
  }
}`,
		},
		// A computed key reads the expression the source wrote. A property read off a
		// variable gives the same key every time, so reading it per construction is
		// what reading it once would have given.
		{
			name: "AWellKnownSymbolKey",
			src: `class Odd {
				[Symbol.iterator]: number,
			}`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this[Symbol.iterator] = _field1;
  }
}`,
		},
		// A bare variable read is the other stable shape, and the key's type is what
		// pins its value.
		{
			name: "AVariableKey",
			src: `declare val k: unique symbol
			class Odd {
				[k]: number,
			}`,
			want: `export class Odd {
  constructor(temp1) {
    const _field1 = temp1;
    this[k] = _field1;
  }
}`,
		},
		// A field cannot bind a name one of the keys reads, or the key would see the
		// parameter rather than what it meant where the class was defined. The field
		// goes by index instead and the key reads the module's `k`.
		{
			name: "AFieldNamedByAKeyIsReachedByIndex",
			src: `declare val k: unique symbol
			class C {
				k: number,
				[k]: string,
			}`,
			want: `export class C {
  constructor(temp1, temp2) {
    const _field1 = temp1;
    const _field2 = temp2;
    this["k"] = _field1;
    this[k] = _field2;
  }
}`,
		},
		// `Symbol` is read the same way, so a field spelling it is reached by index too.
		{
			name: "AFieldNamedSymbolIsReachedByIndex",
			src: `class C {
				Symbol: number,
				[Symbol.iterator]: string,
			}`,
			want: `export class C {
  constructor(temp1, temp2) {
    const _field1 = temp1;
    const _field2 = temp2;
    this["Symbol"] = _field1;
    this[Symbol.iterator] = _field2;
  }
}`,
		},
		// A field whose key binds a name directly keeps it, so the generated names
		// count only the fields that need one.
		{
			name: "AComputedKeyBesideADirectOne",
			src: `class Odd {
				plain: number,
				[Symbol.iterator]: string,
			}`,
			want: `export class Odd {
  constructor(temp1, temp2) {
    const plain = temp1;
    const _field1 = temp2;
    this.plain = plain;
    this[Symbol.iterator] = _field1;
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
// constructor is emitted as written rather than joined by a second one, and the four
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

	// A key a call produces answers differently per call, so no constructor is derived
	// and the checkers report that the class needs an explicit one. #1831 lifts this by
	// evaluating the key once beside the class.
	t.Run("AKeyFromACallGetsNone", func(t *testing.T) {
		got := buildSource(t, `declare fn makeKey() -> unique symbol
		class Odd {
			[makeKey()]: number,
		}`)
		require.NotContains(t, got, "constructor")
	})

	// A property read off anything but `Symbol` may be a getter, which is a call this
	// package cannot tell from a field, so it gets none for the same reason and is
	// lifted by the same ticket.
	t.Run("AKeyReadOffAnObjectGetsNone", func(t *testing.T) {
		got := buildSource(t, `declare val keys: {get k(&self) -> unique symbol}
		class Odd {
			[keys.k]: number,
		}`)
		require.NotContains(t, got, "constructor")
	})
}
