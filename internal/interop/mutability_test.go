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

func TestClassifyTier2_Getter(t *testing.T) {
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

func TestClassifyTier2_Setter(t *testing.T) {
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

func TestClassifyTier2_ReadonlyProperty(t *testing.T) {
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

func TestClassifyTier8_WritableProperty(t *testing.T) {
	result := Classify(ClassifyContext{
		Member: &dts_parser.PropertyDecl{
			Modifiers: dts_parser.Modifiers{Readonly: false},
		},
	})
	if !result.Mut {
		t.Error("writable property should fall through to tier 8 (mutating)")
	}
	if result.Source != TierDefault {
		t.Errorf("writable property should use TierDefault, got %d", result.Source)
	}
}

func TestClassifyTier2_WellKnownMethods(t *testing.T) {
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

func TestClassifyTier2_ReadonlyThisParam(t *testing.T) {
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

func TestClassifyTier2_ReadonlyCollectionClass(t *testing.T) {
	tests := []struct {
		className string
		wantMut   bool
	}{
		{"ReadonlyArray", false},
		{"ReadonlySet", false},
		{"ReadonlyMap", false},
		{"Array", true},
		{"Set", true},
		{"Map", true},
		{"Foo", true},
	}

	for _, tt := range tests {
		t.Run(tt.className, func(t *testing.T) {
			method := makeMethodDecl("forEach", nil)
			result := Classify(ClassifyContext{Member: method, ClassName: tt.className})
			if result.Mut != tt.wantMut {
				t.Errorf("method on %s: got Mut=%v, want Mut=%v", tt.className, result.Mut, tt.wantMut)
			}
		})
	}
}

func TestClassifyTier8_DefaultMutating(t *testing.T) {
	// A plain method with no signals falls through to tier 8.
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

func TestClassifyTier2_ThisParamNotFirst(t *testing.T) {
	// `this` param that is NOT first should not trigger readonly-this classification.
	method := makeMethodDecl("doSomething", []*dts_parser.Param{
		makeParam("arg", makeTypeRef("string")),
		makeParam("this", makeTypeRef("Readonly")),
	})
	result := Classify(ClassifyContext{Member: method})
	if !result.Mut {
		t.Error("this param not in first position should not trigger non-mutating classification")
	}
}

func TestClassifyTier2_SymbolNonSymbol(t *testing.T) {
	// [Foo.iterator] where Foo != "Symbol" is not a well-known symbol.
	method := makeComputedMethodDecl("Foo", "iterator")
	result := Classify(ClassifyContext{Member: method})
	if !result.Mut {
		t.Error("[Foo.iterator] should not be classified as non-mutating")
	}
}
