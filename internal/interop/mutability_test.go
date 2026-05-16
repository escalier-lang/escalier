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

func TestClassifyTier6_ForEachOnAnyClass(t *testing.T) {
	// `forEach` lands at tier 6 (name heuristic, iteration accessor)
	// regardless of containing class. Readonly-prefixed collection classes
	// (ReadonlyArray, etc.) are handled elsewhere in the compiler — Classify
	// does not special-case them.
	for _, className := range []string{"Array", "Set", "Map", "Foo", "ReadonlyArray", "ReadonlySet", "ReadonlyMap"} {
		t.Run(className, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: makeMethodDecl("forEach", nil), ClassName: className})
			if result.Mut {
				t.Errorf("forEach should be classified non-mutating (tier 6), got Mut=true on %s", className)
			}
			if result.Source != TierNameHeuristic {
				t.Errorf("forEach should use TierNameHeuristic, got %d", result.Source)
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
			name:        "this: Readonly<T> param",
			dts:         "declare class C { m(this: Readonly<C>): void }",
			className:   "C",
			wantMut:     false,
			wantSource:  TierExplicitSignal,
			description: "explicit readonly this param",
		},
		{
			name:       "this: ReadonlyArray<T> param",
			dts:        "declare class C { m(this: ReadonlyArray<number>): void }",
			className:  "C",
			wantMut:    false,
			wantSource: TierExplicitSignal,
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
			name:       "forEach method",
			dts:        "declare class Foo { forEach(): void }",
			className:  "Foo",
			wantMut:    false,
			wantSource: TierNameHeuristic,
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

func TestClassifyTier5_GetPrefix(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		wantMut    bool
		wantSource ResolutionTier
	}{
		{"getFoo is non-mutating", "getFoo", false, TierGetPrefix},
		{"getX is non-mutating", "getX", false, TierGetPrefix},
		{"bare get is non-mutating (Map.get, etc.)", "get", false, TierGetPrefix},
		{"getter falls through to tier 7 default", "getter", true, TierDefault},
		{"gets falls through to default", "gets", true, TierDefault},
		{"setFoo not a get prefix → tier 6 mutating", "setFoo", true, TierNameHeuristic},
		{"getOrInsert falls through → tier 6 mutating", "getOrInsertFoo", true, TierNameHeuristic},
		{"getOrUpdate falls through → tier 6 mutating", "getOrUpdateThing", true, TierNameHeuristic},
		{"getOrCreate falls through → tier 6 mutating", "getOrCreateX", true, TierNameHeuristic},
		// Exact bare names fall through to tier 6 — consistent with the
		// suffixed cases above.
		{"bare getOrInsert falls through", "getOrInsert", true, TierNameHeuristic},
		{"bare getOrUpdate falls through", "getOrUpdate", true, TierNameHeuristic},
		{"bare getOrCreate falls through", "getOrCreate", true, TierNameHeuristic},
		// getOrDefault is NOT a mutating exception (per requirements).
		{"getOrDefault stays non-mutating", "getOrDefault", false, TierGetPrefix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: makeMethodDecl(tt.method, nil)})
			if result.Mut != tt.wantMut {
				t.Errorf("%s: Mut=%v, want %v", tt.method, result.Mut, tt.wantMut)
			}
			if result.Source != tt.wantSource {
				t.Errorf("%s: Source=%d, want %d", tt.method, result.Source, tt.wantSource)
			}
		})
	}
}

func TestClassifyTier6_NameHeuristics(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		wantMut    bool
		wantSource ResolutionTier
	}{
		// Non-mutating prefixes.
		{"isReady", "isReady", false, TierNameHeuristic},
		{"hasValue", "hasValue", false, TierNameHeuristic},
		{"canRun", "canRun", false, TierNameHeuristic},
		{"shouldRetry", "shouldRetry", false, TierNameHeuristic},
		{"willFire", "willFire", false, TierNameHeuristic},
		{"wasDone", "wasDone", false, TierNameHeuristic},
		{"didMount", "didMount", false, TierNameHeuristic},
		{"toUpperCase", "toUpperCase", false, TierNameHeuristic},
		{"asReadonly", "asReadonly", false, TierNameHeuristic},
		{"withDefault", "withDefault", false, TierNameHeuristic},
		{"findItem", "findItem", false, TierNameHeuristic},
		{"filterX", "filterX", false, TierNameHeuristic},
		{"mapValues", "mapValues", false, TierNameHeuristic},
		{"reduceRight", "reduceRight", false, TierNameHeuristic},
		{"countItems", "countItems", false, TierNameHeuristic},
		{"cloneDeep", "cloneDeep", false, TierNameHeuristic},
		// copyWithin matches the `copy` non-mutating prefix at tier 6.
		// Array.prototype.copyWithin is actually mutating in JS, but
		// that's the job of tier 4 (builtin overrides) — tier 6 only
		// reflects the name-based heuristic.
		{"copyWithin", "copyWithin", false, TierNameHeuristic},
		// Non-mutating exact.
		{"contains", "contains", false, TierNameHeuristic},
		{"includes", "includes", false, TierNameHeuristic},
		{"equals", "equals", false, TierNameHeuristic},
		{"matches", "matches", false, TierNameHeuristic},
		{"every", "every", false, TierNameHeuristic},
		{"some", "some", false, TierNameHeuristic},
		{"indexOf", "indexOf", false, TierNameHeuristic},
		{"lastIndexOf", "lastIndexOf", false, TierNameHeuristic},
		{"at", "at", false, TierNameHeuristic},
		{"keys", "keys", false, TierNameHeuristic},
		{"values", "values", false, TierNameHeuristic},
		{"entries", "entries", false, TierNameHeuristic},
		{"forEach", "forEach", false, TierNameHeuristic},
		{"slice", "slice", false, TierNameHeuristic},
		{"concat", "concat", false, TierNameHeuristic},
		// Mutating prefixes.
		{"setX", "setX", true, TierNameHeuristic},
		// Bare `set` is the canonical JS mutator (Map.prototype.set, etc.).
		{"bare set is mutating (Map.set, etc.)", "set", true, TierNameHeuristic},
		{"addItem", "addItem", true, TierNameHeuristic},
		{"removeItem", "removeItem", true, TierNameHeuristic},
		{"deleteAll", "deleteAll", true, TierNameHeuristic},
		{"clearCache", "clearCache", true, TierNameHeuristic},
		{"resetState", "resetState", true, TierNameHeuristic},
		{"initFoo", "initFoo", true, TierNameHeuristic},
		{"pushVal", "pushVal", true, TierNameHeuristic},
		{"popLast", "popLast", true, TierNameHeuristic},
		{"shiftItem", "shiftItem", true, TierNameHeuristic},
		{"unshiftFoo", "unshiftFoo", true, TierNameHeuristic},
		{"insertAt", "insertAt", true, TierNameHeuristic},
		{"replaceWith", "replaceWith", true, TierNameHeuristic},
		{"updateValue", "updateValue", true, TierNameHeuristic},
		{"registerHandler", "registerHandler", true, TierNameHeuristic},
		{"unregisterHandler", "unregisterHandler", true, TierNameHeuristic},
		{"dispatchEvent", "dispatchEvent", true, TierNameHeuristic},
		{"emitChange", "emitChange", true, TierNameHeuristic},
		{"writeBytes", "writeBytes", true, TierNameHeuristic},
		{"flushBuffer", "flushBuffer", true, TierNameHeuristic},
		// Mutating exact.
		{"sort", "sort", true, TierNameHeuristic},
		{"reverse", "reverse", true, TierNameHeuristic},
		// Both prefixes → mutating wins.
		{"setToString (mut wins)", "setToString", true, TierNameHeuristic},
		// Counter-examples — must NOT match a prefix.
		{"today is not to-prefix", "today", true, TierDefault},
		{"render falls through", "render", true, TierDefault},
		{"asynchronous is not as-prefix", "asynchronous", true, TierDefault},
		{"setting is not set-prefix", "setting", true, TierDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Classify(ClassifyContext{Member: makeMethodDecl(tt.method, nil)})
			if result.Mut != tt.wantMut {
				t.Errorf("%s: Mut=%v, want %v", tt.method, result.Mut, tt.wantMut)
			}
			if result.Source != tt.wantSource {
				t.Errorf("%s: Source=%d, want %d", tt.method, result.Source, tt.wantSource)
			}
		})
	}
}

func TestClassifyTierOrdering(t *testing.T) {
	// `toString` matches both tier 3 (well-known) and tier 6 (to-prefix);
	// tier 3 must win.
	result := Classify(ClassifyContext{Member: makeMethodDecl("toString", nil)})
	if result.Source != TierExplicitSignal {
		t.Errorf("toString should resolve at tier 3 (well-known), got %d", result.Source)
	}

	// `setX` matches tier 6 (set-prefix) but not tier 5; verify it stops
	// at tier 6, not the default.
	result = Classify(ClassifyContext{Member: makeMethodDecl("setX", nil)})
	if result.Source != TierNameHeuristic {
		t.Errorf("setX should resolve at tier 6, got %d", result.Source)
	}

	// `getFoo` stops at tier 5 and never reaches tier 6.
	result = Classify(ClassifyContext{Member: makeMethodDecl("getFoo", nil)})
	if result.Source != TierGetPrefix {
		t.Errorf("getFoo should resolve at tier 5, got %d", result.Source)
	}
}

func TestClassifyInheritance(t *testing.T) {
	// Build a base context whose member is `render` (no signals → default
	// mutating on the base).
	baseRender := makeMethodDecl("render", nil)
	subRender := makeMethodDecl("render", nil)

	// Subclass `render` with no direct match: inherits from base, which
	// also falls through to TierDefault. The inherited result carries the
	// base's tier — which here is TierDefault.
	t.Run("no signals anywhere → TierDefault", func(t *testing.T) {
		baseCtx := &ClassifyContext{Member: baseRender, ClassName: "Base"}
		result := Classify(ClassifyContext{
			Member:    subRender,
			ClassName: "Sub",
			Base:      baseCtx,
		})
		if !result.Mut || result.Source != TierDefault {
			t.Errorf("got Mut=%v Source=%d, want Mut=true Source=TierDefault", result.Mut, result.Source)
		}
	})

	// Base member is a heuristic match (e.g. `findThing`); subclass
	// inherits and the heuristic tier carries.
	t.Run("heuristic-on-base stays heuristic", func(t *testing.T) {
		baseCtx := &ClassifyContext{
			Member:    makeMethodDecl("findThing", nil),
			ClassName: "Base",
		}
		result := Classify(ClassifyContext{
			Member:    makeMethodDecl("unrelated_no_match", nil),
			ClassName: "Sub",
			Base:      baseCtx,
		})
		if result.Mut || result.Source != TierNameHeuristic {
			t.Errorf("got Mut=%v Source=%d, want Mut=false Source=TierNameHeuristic", result.Mut, result.Source)
		}
	})

	// Subclass has a direct tier-3 hit (getter): inheritance is NOT
	// consulted — earlier-tier wins on the subclass.
	t.Run("subclass tier-3 wins over base inheritance", func(t *testing.T) {
		baseCtx := &ClassifyContext{
			Member:    makeMethodDecl("findThing", nil), // would be non-mut tier 6
			ClassName: "Base",
		}
		result := Classify(ClassifyContext{
			Member:    &dts_parser.GetterDecl{}, // subclass getter — tier 3
			ClassName: "Sub",
			Base:      baseCtx,
		})
		if result.Source != TierExplicitSignal {
			t.Errorf("subclass tier-3 should win, got Source=%d", result.Source)
		}
	})

	// Realistic same-name case: subclass overrides `toString` (no signal
	// on the subclass member itself — the override is a plain MethodDecl
	// with no readonly-this and no Readonly class). The base member with
	// the same name is the well-known `toString`, classified non-mutating
	// at tier 3. Inheritance fallthrough must carry that tier 3 result up.
	t.Run("same-name override inherits base tier-3", func(t *testing.T) {
		baseCtx := &ClassifyContext{
			Member:    makeMethodDecl("toString", nil),
			ClassName: "Base",
		}
		result := Classify(ClassifyContext{
			Member:    makeMethodDecl("toString", nil),
			ClassName: "Sub",
			Base:      baseCtx,
		})
		// Subclass `toString` itself hits tier 3 (well-known) directly,
		// before inheritance fallthrough is even consulted.
		if result.Mut || result.Source != TierExplicitSignal {
			t.Errorf("got Mut=%v Source=%d, want Mut=false Source=TierExplicitSignal", result.Mut, result.Source)
		}
	})
}
