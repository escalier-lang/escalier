package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/stretchr/testify/require"
)

// TestEmitsTheImplicitConstructor covers the constructor a class that declares none
// gets. Emission derives it from the declared fields through ast.ImplicitConstructor,
// the same element internal/solver infers the class's constructor from.
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
		// JavaScript evaluates a computed member key once, where the class is defined. The
		// key is evaluated into a binding before the class, and the constructor indexes
		// with the binding.
		{
			name: "AWellKnownSymbolKey",
			src: `class Odd {
				[Symbol.iterator]: number,
			}`,
			want: `const temp1 = Symbol.iterator;
export class Odd {
  constructor(temp2) {
    const _field1 = temp2;
    this[temp1] = _field1;
  }
}`,
		},
		{
			name: "AVariableKey",
			src: `declare val k: unique symbol
			class Odd {
				[k]: number,
			}`,
			want: `const temp1 = k;
export class Odd {
  constructor(temp2) {
    const _field1 = temp2;
    this[temp1] = _field1;
  }
}`,
		},
		// A call answers freshly each time, so it is called once, as JavaScript calls it.
		{
			name: "AKeyFromACall",
			src: `declare fn makeKey() -> unique symbol
			class Odd {
				[makeKey()]: number,
			}`,
			want: `const temp1 = makeKey();
export class Odd {
  constructor(temp2) {
    const _field1 = temp2;
    this[temp1] = _field1;
  }
}`,
		},
		// A property read may run a getter, which runs once.
		{
			name: "AKeyReadOffAnObject",
			src: `declare val keys: {get k(&self) -> unique symbol}
			class Odd {
				[keys.k]: number,
			}`,
			want: `const temp1 = keys.k;
export class Odd {
  constructor(temp2) {
    const _field1 = temp2;
    this[temp1] = _field1;
  }
}`,
		},
		// The constructor never reads a key, so a field may bind a name a key reads. Here
		// `k` binds the field's own parameter while the key reads the module's `k` beside
		// the class.
		{
			name: "AFieldNamedForAKeysReadKeepsItsName",
			src: `declare val k: unique symbol
			class C {
				k: number,
				[k]: string,
			}`,
			want: `const temp1 = k;
export class C {
  constructor(temp2, temp3) {
    const k = temp2;
    const _field1 = temp3;
    this.k = k;
    this[temp1] = _field1;
  }
}`,
		},
		{
			name: "AFieldNamedSymbolKeepsItsName",
			src: `class C {
				Symbol: number,
				[Symbol.iterator]: string,
			}`,
			want: `const temp1 = Symbol.iterator;
export class C {
  constructor(temp2, temp3) {
    const Symbol = temp2;
    const _field1 = temp3;
    this.Symbol = Symbol;
    this[temp1] = _field1;
  }
}`,
		},
		// A key's binding cannot take a name the key reads, or the binding would read
		// itself. The binding skips to `temp2` and reads the module's `temp1`.
		{
			name: "AKeyBindingAvoidsANameTheKeyReads",
			src: `declare val temp1: unique symbol
			class C {
				[temp1]: number,
			}`,
			want: `const temp2 = temp1;
export class C {
  constructor(temp3) {
    const _field1 = temp3;
    this[temp2] = _field1;
  }
}`,
		},
		// A key's binding cannot take a name the constructor binds either, or the
		// constructor would index with the field's value. The field `temp1` keeps its
		// name, and the binding skips to `temp2`.
		{
			name: "AKeyBindingAvoidsAFieldsName",
			src: `declare val k: unique symbol
			class C {
				temp1: number,
				[k]: string,
			}`,
			want: `const temp2 = k;
export class C {
  constructor(temp3, temp4) {
    const temp1 = temp3;
    const _field1 = temp4;
    this.temp1 = temp1;
    this[temp2] = _field1;
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
			want: `const temp1 = Symbol.iterator;
export class Odd {
  constructor(temp2, temp3) {
    const plain = temp2;
    const _field1 = temp3;
    this.plain = plain;
    this[temp1] = _field1;
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

// subclassCtors answers ImplicitSubclassConstructor the way a checker run would, keyed by
// the subclass's name, and answers every other question the way an untyped tree does.
type subclassCtors struct {
	noJSTypes
	// superParams maps each subclass to its superclass constructor's parameter names.
	superParams map[string][]string
	// inherited maps each subclass to the names of the fields it redeclares.
	inherited map[string][]string
}

func (s subclassCtors) ImplicitSubclassConstructor(decl *ast.ClassDecl) ([]string, func(*ast.FieldElem) bool, bool) {
	params, ok := s.superParams[decl.Name.Name]
	if !ok {
		return nil, nil, false
	}
	redeclared := set.FromSlice(s.inherited[decl.Name.Name])
	return params, func(f *ast.FieldElem) bool {
		ident, ok := f.Name.(*ast.IdentExpr)
		return ok && redeclared.Contains(ident.Name)
	}, true
}

// TestEmitsTheImplicitSubclassConstructor covers the constructor a subclass that declares
// none gets, which ast.ImplicitSubclassConstructor derives from what the checker reports. It
// takes the superclass constructor's parameters and the subclass's added fields, passes the
// first to `super(…)`, and assigns the rest.
func TestEmitsTheImplicitSubclassConstructor(t *testing.T) {
	src := `class Base {
		foo: string,
	}
	class Derived1 extends Base {
		foo: string,
		bar: number,
	}
	class Derived2 extends Base {
		bar: number,
	}
	class Derived3 extends Base {}`
	got := buildSourceWith(t, src, subclassCtors{
		superParams: map[string][]string{
			"Derived1": {"foo"},
			"Derived2": {"foo"},
			"Derived3": {"foo"},
		},
		inherited: map[string][]string{"Derived1": {"foo"}},
	})
	require.Equal(t, `export class Base {
  constructor(temp1) {
    const foo = temp1;
    this.foo = foo;
  }
}
export class Derived1 extends Base {
  constructor(temp2, temp3) {
    const foo = temp2;
    const bar = temp3;
    super(foo);
    this.bar = bar;
  }
}
export class Derived2 extends Base {
  constructor(temp4, temp5) {
    const foo = temp4;
    const bar = temp5;
    super(foo);
    this.bar = bar;
  }
}
export class Derived3 extends Base {
  constructor(temp6) {
    const foo = temp6;
    super(foo);
  }
}`, got)
}

// TestEmitsASubclassComputedKeyOnce asserts that a synthesized subclass constructor
// indexes a computed-key field with a binding evaluated once beside the class.
func TestEmitsASubclassComputedKeyOnce(t *testing.T) {
	src := `declare fn makeKey() -> unique symbol
	class Base {
		foo: string,
	}
	class Derived extends Base {
		[makeKey()]: number,
	}`
	got := buildSourceWith(t, src, subclassCtors{
		superParams: map[string][]string{"Derived": {"foo"}},
	})
	require.Equal(t, `export class Base {
  constructor(temp1) {
    const foo = temp1;
    this.foo = foo;
  }
}
const temp2 = makeKey();
export class Derived extends Base {
  constructor(temp3, temp4) {
    const foo = temp3;
    const _field1 = temp4;
    super(foo);
    this[temp2] = _field1;
  }
}`, got)
}

// A subclass gets no constructor when the checker reports none for it. JavaScript's own
// default constructor then passes every argument to `super(…)`.
func TestEmitsNoImplicitSubclassConstructorUnreported(t *testing.T) {
	got := buildSource(t, `class Base {
		foo: string,
	}
	class Derived extends Base {
		bar: number,
	}`)
	require.Equal(t, `export class Base {
  constructor(temp1) {
    const foo = temp1;
    this.foo = foo;
  }
}
export class Derived extends Base {
}`, got)
}

// TestEmitsNoImplicitConstructor covers the classes that get none. A declared
// constructor is emitted as written rather than joined by a second one, and a `declare
// class` emits a body without one.
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
}
