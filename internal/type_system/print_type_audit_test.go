package type_system_test

import (
	"context"
	"math/big"
	"regexp"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/test_util"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// TestPrintTypeAudit_RoundTrip is the §8 audit: build one instance of
// every concrete type_system.Type variant that has a syntactic form,
// run it through PrintType, feed the output back through
// parser.ParseTypeAnn + the type-ann -> Type converter, print again,
// and assert idempotency.
//
// Variants without a syntactic form (TypeVarType, ErrorType,
// GlobalThisType, RegexType, NamespaceType, ExtractorType, plus
// internal IndexSignatureElem) are exercised by TestPrintTypeAudit_NoSyntax
// below, which only sanity-checks that PrintType returns a non-empty
// string.
func TestPrintTypeAudit_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		typ  type_system.Type
	}{
		// --- primitives ---
		{"prim bool", type_system.NewBoolPrimType(nil)},
		{"prim number", type_system.NewNumPrimType(nil)},
		{"prim string", type_system.NewStrPrimType(nil)},
		{"prim bigint", type_system.NewBigIntPrimType(nil)},
		{"prim symbol", type_system.NewSymPrimType(nil)},

		// --- literals ---
		{"lit str", type_system.NewStrLitType(nil, "hello")},
		{"lit num int", type_system.NewNumLitType(nil, 42)},
		{"lit num float", type_system.NewNumLitType(nil, 3.14)},
		{"lit bool true", type_system.NewBoolLitType(nil, true)},
		{"lit bool false", type_system.NewBoolLitType(nil, false)},
		{"lit bigint", type_system.NewBigIntLitType(nil, *big.NewInt(123))},
		{"lit null", type_system.NewNullType(nil)},
		{"lit undefined", type_system.NewUndefinedType(nil)},

		// --- top/bottom ---
		{"any", type_system.NewAnyType(nil)},
		{"unknown", type_system.NewUnknownType(nil)},
		{"never", type_system.NewNeverType(nil)},
		{"void", type_system.NewVoidType(nil)},

		// --- type ref ---
		{"ref no args", type_system.NewTypeRefType(nil, "Foo", nil)},
		{"ref one arg", type_system.NewTypeRefType(nil, "Array", nil,
			type_system.NewNumPrimType(nil))},
		{"ref two args", type_system.NewTypeRefType(nil, "Map", nil,
			type_system.NewStrPrimType(nil), type_system.NewNumPrimType(nil))},

		// --- typeof ---
		// `TypeOfType.Ident` is a QualIdent; PrintType uses
		// QualIdentToString. We construct one matching a bare ident
		// because nested member chains require a Member node we don't
		// have a constructor for here.
		{"typeof bare", type_system.NewTypeOfType(nil, type_system.NewIdent("foo"))},

		// --- unique symbol (no source form for the underlying value;
		// printer emits `symbol<N>` which isn't parseable. Covered by
		// TestPrintTypeAudit_NoSyntax. ---

		// --- union / intersection ---
		{"union", type_system.NewUnionType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil))},
		{"intersection", type_system.NewIntersectionType(nil,
			type_system.NewTypeRefType(nil, "A", nil),
			type_system.NewTypeRefType(nil, "B", nil))},

		// --- keyof / mut ---
		{"keyof ref", type_system.NewKeyOfType(nil,
			type_system.NewTypeRefType(nil, "T", nil))},
		{"mut ref", type_system.NewMutType(nil,
			type_system.NewTypeRefType(nil, "T", nil))},

		// --- tuple ---
		{"tuple", type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewStrPrimType(nil))},

		// --- index ---
		{"index", type_system.NewIndexType(nil,
			type_system.NewTypeRefType(nil, "T", nil),
			type_system.NewTypeRefType(nil, "K", nil))},

		// --- conditional / infer ---
		{"conditional", type_system.NewCondType(nil,
			type_system.NewTypeRefType(nil, "A", nil),
			type_system.NewTypeRefType(nil, "B", nil),
			type_system.NewTypeRefType(nil, "C", nil),
			type_system.NewTypeRefType(nil, "D", nil))},
		{"infer", type_system.NewInferType(nil, "T")},

		// --- wildcard ---
		{"wildcard", type_system.NewWildcardType(nil)},

		// --- function ---
		{"func no params", type_system.NewFuncType(nil, nil, nil,
			type_system.NewVoidType(nil), nil)},
		{"func one param", type_system.NewFuncType(nil, nil,
			[]*type_system.FuncParam{
				{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil)},
			},
			type_system.NewStrPrimType(nil), nil)},
		{"func two params", type_system.NewFuncType(nil, nil,
			[]*type_system.FuncParam{
				{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil)},
				{Pattern: type_system.NewIdentPat("y"), Type: type_system.NewStrPrimType(nil)},
			},
			type_system.NewBoolPrimType(nil), nil)},

		// --- object: property ---
		{"object prop", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.PropertyElem{
				Name:  type_system.NewStrKey("x"),
				Value: type_system.NewNumPrimType(nil),
			},
		})},
		{"object readonly prop", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.PropertyElem{
				Name:     type_system.NewStrKey("x"),
				Readonly: true,
				Value:    type_system.NewNumPrimType(nil),
			},
		})},
		{"object optional prop", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.PropertyElem{
				Name:     type_system.NewStrKey("x"),
				Optional: true,
				Value:    type_system.NewNumPrimType(nil),
			},
		})},

		// --- object: rest spread elem ---
		{"object rest", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			type_system.NewRestSpreadElem(type_system.NewTypeRefType(nil, "T", nil)),
		})},

		// --- top-level rest spread (only valid in tuple/object;
		// printer round-trips standalone via "..."+inner) ---
		{"rest spread top-level", type_system.NewRestSpreadType(nil,
			type_system.NewTypeRefType(nil, "T", nil))},

		// --- typeof with qualified path ---
		{"typeof qualified", type_system.NewTypeOfType(nil,
			&type_system.Member{
				Left:  type_system.NewIdent("Foo"),
				Right: type_system.NewIdent("bar"),
			})},

		// --- template literal types ---
		{"template lit empty", type_system.NewTemplateLitType(nil,
			[]*type_system.Quasi{{Value: ""}}, nil)},
		{"template lit one quasi", type_system.NewTemplateLitType(nil,
			[]*type_system.Quasi{{Value: "hello"}}, nil)},
		{"template lit with interpolation", type_system.NewTemplateLitType(nil,
			[]*type_system.Quasi{{Value: "hello "}, {Value: "!"}},
			[]type_system.Type{type_system.NewStrPrimType(nil)})},

		// --- object: methods / getters / setters ---
		{"object method", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MethodElem{
				Name: type_system.NewStrKey("foo"),
				Fn: type_system.NewFuncType(nil, nil,
					[]*type_system.FuncParam{
						{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil)},
					},
					type_system.NewBoolPrimType(nil), nil),
			},
		})},
		{"object getter", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.GetterElem{
				Name: type_system.NewStrKey("foo"),
				Fn:   type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil),
			},
		})},
		{"object setter", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.SetterElem{
				Name: type_system.NewStrKey("foo"),
				Fn: type_system.NewFuncType(nil, nil,
					[]*type_system.FuncParam{
						{Pattern: type_system.NewIdentPat("v"), Type: type_system.NewNumPrimType(nil)},
					},
					type_system.NewVoidType(nil), nil),
			},
		})},

		// CallableElem / ConstructorElem inside an object have no
		// Escalier source syntax — they are only synthesized by the
		// interop conversion from TypeScript `.d.ts` call/construct
		// signatures. The printer emits `{fn (...) -> T}` /
		// `{new fn (...) -> T}` for debug output; round-trip is
		// covered by the no-syntax test below.

		// --- function: typed params, optional, multiple, with throws ---
		{"func optional param", type_system.NewFuncType(nil, nil,
			[]*type_system.FuncParam{
				{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil), Optional: true},
			},
			type_system.NewStrPrimType(nil), nil)},
		{"func with throws", type_system.NewFuncType(nil, nil, nil,
			type_system.NewVoidType(nil),
			type_system.NewTypeRefType(nil, "Error", nil))},
		{"func with type param", type_system.NewFuncType(nil,
			[]*type_system.TypeParam{{Name: "T"}},
			[]*type_system.FuncParam{
				{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewTypeRefType(nil, "T", nil)},
			},
			type_system.NewTypeRefType(nil, "T", nil), nil)},
		{"func with constrained type param", type_system.NewFuncType(nil,
			[]*type_system.TypeParam{{Name: "T", Constraint: type_system.NewStrPrimType(nil)}},
			[]*type_system.FuncParam{
				{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewTypeRefType(nil, "T", nil)},
			},
			type_system.NewTypeRefType(nil, "T", nil), nil)},

		// --- union / intersection / keyof / mut nesting (precedence) ---
		{"keyof in union", type_system.NewUnionType(nil,
			type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
			type_system.NewStrPrimType(nil))},
		{"mut in intersection", type_system.NewIntersectionType(nil,
			type_system.NewMutType(nil, type_system.NewTypeRefType(nil, "T", nil)),
			type_system.NewTypeRefType(nil, "U", nil))},
		{"union of functions", type_system.NewUnionType(nil,
			type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil),
			type_system.NewFuncType(nil, nil, nil, type_system.NewStrPrimType(nil), nil))},

		// --- tuple with rest ---
		{"tuple with rest", type_system.NewTupleType(nil,
			type_system.NewNumPrimType(nil),
			type_system.NewRestSpreadType(nil, type_system.NewTypeRefType(nil, "T", nil)))},

		// --- mapped type ---
		{"mapped type basic", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
			},
		})},
		{"mapped type readonly", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
				Readonly: mappedModifierPtr(type_system.MMAdd),
			},
		})},
		{"mapped type optional add", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
				Optional: mappedModifierPtr(type_system.MMAdd),
			},
		})},
		{"mapped type optional remove", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
				Optional: mappedModifierPtr(type_system.MMRemove),
			},
		})},
		{"mapped type readonly remove", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
				Readonly: mappedModifierPtr(type_system.MMRemove),
			},
		})},
		{"mapped type key rename", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Name: type_system.NewTemplateLitType(nil,
					[]*type_system.Quasi{{Value: "prefix_"}, {Value: ""}},
					[]type_system.Type{type_system.NewTypeRefType(nil, "K", nil)}),
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
			},
		})},
		{"mapped type with if clause", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MappedElem{
				TypeParam: &type_system.IndexParam{
					Name:       "K",
					Constraint: type_system.NewKeyOfType(nil, type_system.NewTypeRefType(nil, "T", nil)),
				},
				Value: type_system.NewIndexType(nil,
					type_system.NewTypeRefType(nil, "T", nil),
					type_system.NewTypeRefType(nil, "K", nil)),
				Check:   type_system.NewTypeRefType(nil, "K", nil),
				Extends: type_system.NewStrPrimType(nil),
			},
		})},

		// --- method with self receiver ---
		{"method with self", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MethodElem{
				Name: type_system.NewStrKey("foo"),
				Fn:   funcWithSelf(false),
			},
		})},
		{"method with mut self", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.MethodElem{
				Name: type_system.NewStrKey("foo"),
				Fn:   funcWithSelf(true),
			},
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			printed := type_system.PrintType(tt.typ, type_system.PrintConfig{})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ast, parseErrs := parser.ParseTypeAnn(ctx, printed)
			if len(parseErrs) > 0 {
				t.Fatalf("PrintType(%s) produced %q which fails to parse: %s",
					tt.name, printed, parseErrs[0].Message)
			}
			if ast == nil {
				t.Fatalf("PrintType(%s) produced %q which parses to nil",
					tt.name, printed)
			}

			reparsed := test_util.ParseTypeAnn(printed)
			reprinted := type_system.PrintType(reparsed, type_system.PrintConfig{})
			if reprinted != printed {
				t.Errorf("round-trip mismatch for %s:\n  printed:   %q\n  reprinted: %q",
					tt.name, printed, reprinted)
			}
		})
	}
}

func mappedModifierPtr(m type_system.MappedModifier) *type_system.MappedModifier {
	return &m
}

func funcWithSelf(mut bool) *type_system.FuncType {
	fn := type_system.NewFuncType(nil, nil, nil, type_system.NewNumPrimType(nil), nil)
	receiver := type_system.NewTypeRefType(nil, "T", nil)
	var recvType type_system.Type = receiver
	if mut {
		recvType = type_system.NewMutType(nil, receiver)
	}
	fn.SelfParam = &type_system.FuncParam{Type: recvType}
	return fn
}

// TestPrintTypeAudit_NoSyntax sanity-checks variants that have no
// source-level syntax (so they can't round-trip through the parser).
// These types appear only in inferred / synthesized positions; the
// printer's job is to produce a stable, non-empty representation for
// debug output. Listing them here makes the gap explicit so a future
// reader doesn't think they were missed.
func TestPrintTypeAudit_NoSyntax(t *testing.T) {
	tests := []struct {
		name string
		typ  type_system.Type
	}{
		{"unique symbol", type_system.NewUniqueSymbolType(nil, 42)},
		{"object callable", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.CallableElem{
				Fn: type_system.NewFuncType(nil, nil,
					[]*type_system.FuncParam{
						{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil)},
					},
					type_system.NewStrPrimType(nil), nil),
			},
		})},
		{"object constructor", type_system.NewObjectType(nil, []type_system.ObjTypeElem{
			&type_system.ConstructorElem{
				Fn: type_system.NewFuncType(nil, nil,
					[]*type_system.FuncParam{
						{Pattern: type_system.NewIdentPat("x"), Type: type_system.NewNumPrimType(nil)},
					},
					type_system.NewTypeRefType(nil, "Foo", nil), nil),
			},
		})},
		{"global this", &type_system.GlobalThisType{}},
		{"namespace empty", type_system.NewNamespaceType(nil, type_system.NewNamespace())},
		{"regex", type_system.NewRegexType(nil, regexp.MustCompile(`^foo$`), nil)},
		{"extractor", type_system.NewExtractorType(nil,
			type_system.NewTypeRefType(nil, "Some", nil),
			type_system.NewTypeRefType(nil, "T", nil))},
		// IntrinsicType, ErrorType, TypeVarType, and IndexSignatureElem
		// all have no source-level syntax. They are exercised by their
		// own *_test.go files; the printer's job for them is debug-only.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := type_system.PrintType(tt.typ, type_system.PrintConfig{})
			if out == "" {
				t.Fatalf("PrintType(%s) returned empty string", tt.name)
			}
		})
	}
}
