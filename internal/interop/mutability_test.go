package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
)

// span is a zero-value span used throughout these tests.
var zeroSpan = ast.Span{}

func makeIdent(name string) *dts_parser.Ident {
	return dts_parser.NewIdent(name, zeroSpan)
}

func makeMethodDecl(name string, params []*dts_parser.Param) *dts_parser.MethodDecl {
	return &dts_parser.MethodDecl{
		Name:   makeIdent(name),
		Params: params,
	}
}

func makeParam(name string, typeAnn dts_parser.TypeAnn) *dts_parser.Param {
	return &dts_parser.Param{
		Name: makeIdent(name),
		Type: typeAnn,
	}
}

func makeTypeRef(name string) *dts_parser.TypeReference {
	return &dts_parser.TypeReference{
		Name:     makeIdent(name),
		TypeArgs: nil,
	}
}

func makeComputedMethodDecl(obj, prop string) *dts_parser.MethodDecl {
	return &dts_parser.MethodDecl{
		Name: &dts_parser.ComputedKey{
			Expr: &dts_parser.MemberExpr{
				Object: &dts_parser.IdentExpr{Name: obj},
				Prop:   makeIdent(prop),
			},
		},
	}
}

func TestClassifyTier3_Getter(t *testing.T) {
	result := Classify(ClassifyContext{
		Member: &dts_parser.GetterDecl{},
	})
	if result.Mut {
		t.Error("getter should be classified as non-mutating")
	}
	if result.Source != TierExplicitSignal {
		t.Errorf("getter should use TierExplicitSignal, got %d", result.Source)
	}
}

func TestClassifyTier3_Setter(t *testing.T) {
	result := Classify(ClassifyContext{
		Member: &dts_parser.SetterDecl{},
	})
	if !result.Mut {
		t.Error("setter should be classified as mutating")
	}
	if result.Source != TierExplicitSignal {
		t.Errorf("setter should use TierExplicitSignal, got %d", result.Source)
	}
}

func TestClassifyTier3_ReadonlyProperty(t *testing.T) {
	result := Classify(ClassifyContext{
		Member: &dts_parser.PropertyDecl{
			Modifiers: dts_parser.Modifiers{Readonly: true},
		},
	})
	if result.Mut {
		t.Error("readonly property should be classified as non-mutating")
	}
	if result.Source != TierExplicitSignal {
		t.Errorf("readonly property should use TierExplicitSignal, got %d", result.Source)
	}
}

func TestClassifyTier7_WritableProperty(t *testing.T) {
	result := Classify(ClassifyContext{
		Member: &dts_parser.PropertyDecl{
			Modifiers: dts_parser.Modifiers{Readonly: false},
		},
	})
	if !result.Mut {
		t.Error("writable property should fall through to tier 7 (mutating)")
	}
	if result.Source != TierDefault {
		t.Errorf("writable property should use TierDefault, got %d", result.Source)
	}
}

func TestClassifyTier3_WellKnownMethods(t *testing.T) {
	tests := []struct {
		name   string
		member dts_parser.ClassMember
	}{
		{"toString", makeMethodDecl("toString", nil)},
		{"toJSON", makeMethodDecl("toJSON", nil)},
		{"toLocaleString", makeMethodDecl("toLocaleString", nil)},
		{"valueOf", makeMethodDecl("valueOf", nil)},
		{"Symbol.iterator", makeComputedMethodDecl("Symbol", "iterator")},
		{"Symbol.asyncIterator", makeComputedMethodDecl("Symbol", "asyncIterator")},
		{"Symbol.toPrimitive", makeComputedMethodDecl("Symbol", "toPrimitive")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: tt.member})
			if result.Mut {
				t.Errorf("%s should be non-mutating", tt.name)
			}
			if result.Source != TierExplicitSignal {
				t.Errorf("%s should use TierExplicitSignal, got %d", tt.name, result.Source)
			}
		})
	}
}

func TestClassifyTier3_ReadonlyThisParam(t *testing.T) {
	tests := []struct {
		name     string
		thisType dts_parser.TypeAnn
		wantMut  bool
	}{
		{"Readonly<T>", makeTypeRef("Readonly"), false},
		{"ReadonlyArray<T>", makeTypeRef("ReadonlyArray"), false},
		{"ReadonlySet<T>", makeTypeRef("ReadonlySet"), false},
		{"ReadonlyMap<K,V>", makeTypeRef("ReadonlyMap"), false},
		{"readonly T[]", &dts_parser.ArrayType{Readonly: true}, false},
		{"plain type", makeTypeRef("Foo"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method := makeMethodDecl("doSomething", []*dts_parser.Param{
				makeParam("this", tt.thisType),
			})
			result := Classify(ClassifyContext{Member: method})
			if result.Mut != tt.wantMut {
				t.Errorf("method with `this: %s`: got Mut=%v, want Mut=%v", tt.name, result.Mut, tt.wantMut)
			}
			if !tt.wantMut && result.Source != TierExplicitSignal {
				t.Errorf("expected TierExplicitSignal, got %d", result.Source)
			}
		})
	}
}

func TestClassifyTier3_MethodOnReadonlyPrefixedClass(t *testing.T) {
	for _, className := range []string{"ReadonlyArray", "ReadonlySet", "ReadonlyMap"} {
		t.Run(className, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: makeMethodDecl("forEach", nil), ClassName: className})
			if result.Mut {
				t.Errorf("method on %s should be non-mutating", className)
			}
			if result.Source != TierExplicitSignal {
				t.Errorf("method on %s should use TierExplicitSignal, got %d", className, result.Source)
			}
		})
	}
}

func TestClassifyTier7_MethodOnMutableCollectionClass(t *testing.T) {
	for _, className := range []string{"Array", "Set", "Map", "Foo"} {
		t.Run(className, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: makeMethodDecl("forEach", nil), ClassName: className})
			if !result.Mut {
				t.Errorf("method on %s should fall through to mutating (tier 7)", className)
			}
		})
	}
}

func TestClassifyTier7_DefaultMutating(t *testing.T) {
	// A plain method with no signals falls through to tier 7.
	result := Classify(ClassifyContext{
		Member:    makeMethodDecl("doSomething", nil),
		ClassName: "Foo",
	})
	if !result.Mut {
		t.Error("unclassified method should default to mutating")
	}
	if result.Source != TierDefault {
		t.Errorf("expected TierDefault, got %d", result.Source)
	}
}

func TestClassifyTier3_ThisParamNotFirst(t *testing.T) {
	// `this` param that is NOT first should not trigger this: Readonly<T> classification.
	method := makeMethodDecl("doSomething", []*dts_parser.Param{
		makeParam("arg", makeTypeRef("string")),
		makeParam("this", makeTypeRef("Readonly")),
	})
	result := Classify(ClassifyContext{Member: method})
	if !result.Mut {
		t.Error("this param not in first position should not trigger non-mutating classification")
	}
}

func TestClassifyTier3_SymbolNonSymbol(t *testing.T) {
	// [Foo.iterator] where Foo != "Symbol" is not a well-known symbol.
	method := makeComputedMethodDecl("Foo", "iterator")
	result := Classify(ClassifyContext{Member: method})
	if !result.Mut {
		t.Error("[Foo.iterator] should not be classified as non-mutating")
	}
}

// TestClassifyTier3_EndToEnd parses real .d.ts source through dts_parser and
// confirms each strong-signal shape is plumbed end-to-end into the
// ClassMember that Classify consumes.
func TestClassifyTier3_EndToEnd(t *testing.T) {
	tests := []struct {
		name        string
		dts         string // declares a single class `C` with one member
		className   string
		memberIdx   int // which class member to classify
		wantMut     bool
		wantSource  ResolutionTier
		description string
	}{
		{
			name:        "getter is non-mutating",
			dts:         "declare class C { get foo(): number }",
			className:   "C",
			wantMut:     false,
			wantSource:  TierExplicitSignal,
			description: "get foo() ⇒ non-mutating",
		},
		{
			name:        "setter is mutating",
			dts:         "declare class C { set foo(v: number) }",
			className:   "C",
			wantMut:     true,
			wantSource:  TierExplicitSignal,
			description: "set foo() ⇒ mutating",
		},
		{
			name:        "readonly property",
			dts:         "declare class C { readonly x: number }",
			className:   "C",
			wantMut:     false,
			wantSource:  TierExplicitSignal,
			description: "readonly prop ⇒ non-mutating",
		},
		{
			name:        "writable property defaults to mutating",
			dts:         "declare class C { x: number }",
			className:   "C",
			wantMut:     true,
			wantSource:  TierDefault,
			description: "writable prop falls through to default",
		},
		{
			name:        "this: Readonly<T> param",
			dts:         "declare class C { m(this: Readonly<C>): void }",
			className:   "C",
			wantMut:     false,
			wantSource:  TierExplicitSignal,
			description: "explicit readonly this param",
		},
		{
			name:        "this: ReadonlyArray<T> param",
			dts:         "declare class C { m(this: ReadonlyArray<number>): void }",
			className:   "C",
			wantMut:     false,
			wantSource:  TierExplicitSignal,
		},
		{
			name:       "well-known toString",
			dts:        "declare class C { toString(): string }",
			className:  "C",
			wantMut:    false,
			wantSource: TierExplicitSignal,
		},
		// Computed-method-name syntax like `[Symbol.iterator]()` is not yet
		// supported by `dts_parser` (it treats `[` at member position as an
		// index signature). The classifier itself handles ComputedKey
		// correctly — see TestClassifyTier3_WellKnownMethods.
		{
			name:       "ReadonlyArray method",
			dts:        "declare class ReadonlyArray { forEach(): void }",
			className:  "ReadonlyArray",
			wantMut:    false,
			wantSource: TierExplicitSignal,
		},
		{
			name:       "plain method falls through to default",
			dts:        "declare class C { doIt(): void }",
			className:  "C",
			wantMut:    true,
			wantSource: TierDefault,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &ast.Source{Path: "test.d.ts", Contents: tt.dts, ID: 0}
			parser := dts_parser.NewDtsParser(source)
			module, errs := parser.ParseModule()
			if len(errs) > 0 {
				t.Fatalf("parse errors: %v", errs)
			}
			if len(module.Statements) == 0 {
				t.Fatalf("no statements parsed")
			}
			classDecl, ok := module.Statements[0].(*dts_parser.ClassDecl)
			if !ok {
				t.Fatalf("expected ClassDecl, got %T", module.Statements[0])
			}
			if len(classDecl.Members) <= tt.memberIdx {
				t.Fatalf("class has %d members, wanted index %d", len(classDecl.Members), tt.memberIdx)
			}
			result := Classify(ClassifyContext{
				Member:    classDecl.Members[tt.memberIdx],
				ClassName: tt.className,
			})
			if result.Mut != tt.wantMut {
				t.Errorf("Mut: got %v, want %v (%s)", result.Mut, tt.wantMut, tt.description)
			}
			if result.Source != tt.wantSource {
				t.Errorf("Source: got %d, want %d", result.Source, tt.wantSource)
			}
		})
	}
}
