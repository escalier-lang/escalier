package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// errorsContaining filters `errs` to those whose message contains `want`.
func errorsContaining(errs []Error, want string) []Error {
	var out []Error
	for _, e := range errs {
		if strings.Contains(e.Message(), want) {
			out = append(out, e)
		}
	}
	return out
}

// TestConstructorDefiniteAssignmentOK exercises the happy paths from
// the requirements doc: every reachable exit has every required field
// assigned.
func TestConstructorDefiniteAssignmentOK(t *testing.T) {
	tests := map[string]string{
		"PointBothFields": `
			class Point {
				x: number,
				y: number,
				constructor(mut self, x: number, y: number) {
					self.x = x
					self.y = y
				}
			}
		`,
		"PreAssignmentLocalsAreFine": `
			class Email {
				local: string,
				domain: string,
				constructor(mut self, raw: string) {
					val parts = raw
					self.local = parts
					self.domain = parts
				}
			}
		`,
		"BothBranchesAssignBoth": `
			class Range {
				lo: number,
				hi: number,
				constructor(mut self, a: number, b: number) {
					if a < b {
						self.lo = a
						self.hi = b
					} else {
						self.lo = b
						self.hi = a
					}
				}
			}
		`,
		"ThrowingBranchExcusedFromAssignment": `
			class Pos {
				v: number,
				constructor(mut self, x: number) throws string {
					if x < 0 {
						throw "negative"
					}
					self.v = x
				}
			}
		`,
		"SynthesizedConstructorPasses": `
			class P {
				x: number,
				y: number,
			}
		`,
		"ComputedSelfAccessAfterAllInit": `
			val k = "x"
			class Foo {
				x: number,
				constructor(mut self, x: number) {
					self.x = x
					val v = self[k]
				}
			}
		`,
		"ComputedKeyFieldInitializedInBody": `
			val k = "tag"
			class Foo {
				[k]: number,
				name: string,
				constructor(mut self, name: string, tag: number = 42) {
					self.name = name
					self[k] = tag
				}
			}
		`,
		"NestedIfBranchesAllAssign": `
			class Triple {
				a: number,
				b: number,
				c: number,
				constructor(mut self, x: number, y: number, z: number) {
					if x < 0 {
						self.a = x
						self.b = y
						self.c = z
					} else {
						if x == 0 {
							self.a = y
							self.b = z
							self.c = x
						} else {
							self.a = z
							self.b = x
							self.c = y
						}
					}
				}
			}
		`,
		"MatchOneArmThrows": `
			class Foo {
				v: number,
				constructor(mut self, x: number) throws string {
					match x {
						0 => throw "zero",
						_ => self.v = x,
					}
				}
			}
		`,
		"MethodCallAfterAllInit": `
			class Foo {
				x: number,
				constructor(mut self, x: number) {
					self.x = x
					self.bump()
				},
				bump(self) -> number { return self.x }
			}
		`,
		"PassSelfToExternalFnAfterAllInit": `
			fn observe<T>(t: T) -> T { return t }
			class Foo {
				x: number,
				constructor(mut self, x: number) {
					self.x = x
					observe(self)
				}
			}
		`,
		"StaticFieldWithInitializer": `
			class Foo {
				static x: number = 0,
			}
		`,
		"StaticFieldWithLitTypeAnnAndInitializer": `
			class Foo {
				static kind: "tag" = "tag",
			}
		`,
		"StaticFieldInferredFromInitializer": `
			class Foo {
				static count = 0,
			}
		`,
		"StaticFieldWithUndefinedAllowedNeedsNoInitializer": `
			class Foo {
				static msg: string | undefined,
			}
		`,
		"StaticFieldUndefinedThroughTypeAlias": `
			type Maybe = string | undefined
			class Foo {
				static msg: Maybe,
			}
		`,
		"ReadonlyFieldCanBeInitialized": `
			class Foo {
				readonly x: number,
				constructor(mut self, x: number) {
					self.x = x
				}
			}
		`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, src)
			require.Empty(t, errs, "expected no errors; got: %v", formatErrs(errs))
		})
	}
}

// TestConstructorDefiniteAssignmentErrors covers each Phase 3 diagnostic.
func TestConstructorDefiniteAssignmentErrors(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected string
	}{
		"FieldNotInitialized": {
			input: `
				class User {
					name: string,
					age: number,
					constructor(mut self, name: string, age: number) {
						self.name = name
					}
				}
			`,
			expected: "not initialized",
		},
		"ReadBeforeInit": {
			input: `
				class User {
					name: string,
					constructor(mut self, name: string) {
						val n = self.name
						self.name = name
					}
				}
			`,
			expected: "read before",
		},
		"ConditionalAssignmentInOnlyOneBranch": {
			input: `
				class Range {
					lo: number,
					hi: number,
					constructor(mut self, a: number, b: number) {
						self.lo = a
						if a < b {
							self.hi = b
						}
					}
				}
			`,
			expected: "not initialized",
		},
		"SelfAliasBeforeInit": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						val r = self
						self.x = x
					}
				}
			`,
			expected: "alias",
		},
		"MethodCallBeforeInit": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						self.helper()
						self.x = x
					},
					helper(self) -> number { return 0 }
				}
			`,
			expected: "before all required fields",
		},
		"LoopInConstructor": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, xs: Array<number>) {
						for v in xs {
							self.x = v
						}
					}
				}
			`,
			expected: "Loops are not yet supported",
		},
		"ComputedSelfReadBeforeInit": {
			input: `
				val k = "name"
				class Foo {
					name: string,
					constructor(mut self, name: string) {
						val n = self[k]
						self.name = name
					}
				}
			`,
			expected: "Computed access on `self`",
		},
		"ComputedSelfWriteBeforeInit": {
			input: `
				val k = "name"
				class Foo {
					name: string,
					age: number,
					constructor(mut self, name: string, age: number) {
						self[k] = name
						self.age = age
					}
				}
			`,
			expected: "Computed access on `self`",
		},
		"PassSelfToExternalFnBeforeInit": {
			input: `
				fn observe<T>(t: T) -> T { return t }
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						observe(self)
						self.x = x
					}
				}
			`,
			expected: "alias",
		},
		"ReturnSelfBeforeInit": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						return self
					}
				}
			`,
			expected: "alias",
		},
		"TryInConstructor": {
			input: `
				class Foo {
					x: number,
					constructor(mut self, x: number) {
						val r = try {
							x
						} catch {
							_ => 0
						}
						self.x = r
					}
				}
			`,
			expected: "`try`/`catch` is not yet supported",
		},
		"StaticFieldMissingInitializer": {
			input: `
				class Foo {
					static x: number,
				}
			`,
			expected: "Static field 'x' must have an initializer",
		},
		"ClosureInsideCtorCannotBypassReadonly": {
			input: `
				class Foo {
					readonly x: number,
					constructor(mut self, x: number) {
						self.x = x
						val f = fn () {
							self.x = 0
						}
					}
				}
			`,
			expected: "Cannot mutate readonly property 'x'",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, test.input)
			matched := errorsContaining(errs, test.expected)
			require.NotEmptyf(t, matched,
				"expected an error containing %q; got: %v",
				test.expected, formatErrs(errs))
		})
	}
}

// A static field needs an initializer because the emitted `static x;` would
// otherwise read back `undefined`, contradicting the declared type. A
// `declare` class emits nothing, so there is no slot to read back and its
// static describes one the runtime already fills. Every fused class in the
// interop tree carries `static readonly prototype`, which is this shape, and
// the rule accounted for 1781 of `web:dom`'s diagnostics (#1725).
func TestStaticFieldInitializerRequiredOnlyWhenEmitted(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input       string
		wantMissing bool
		wantOther   string
	}{
		"DeclareClassNeedsNoInitializer": {
			input: `
				declare class Element {
					static readonly prototype: Element,
				}
			`,
		},
		"ClassWithABodyStillNeedsOne": {
			input: `
				class Foo {
					static x: number,
				}
			`,
			wantMissing: true,
		},
		// The pre-existing escape hatch: a type that admits `undefined`
		// matches what `static x;` actually holds.
		"AStaticPermittingUndefinedNeedsNone": {
			input: `
				class Foo {
					static x: number | undefined,
				}
			`,
		},
		// Waiving the initializer does not waive the annotation: the
		// static still has the type it declares.
		"ADeclareClassStillChecksTheAnnotation": {
			input: `
				declare class Foo {
					static x: number,
				}
				val n: string = Foo.x
			`,
			wantOther: "number cannot be assigned to string",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, test.input)
			matched := errorsContaining(errs, "must have an initializer")
			if test.wantMissing {
				require.NotEmptyf(t, matched,
					"expected a missing-initializer error; got: %v", formatErrs(errs))
			} else {
				require.Emptyf(t, matched,
					"expected no missing-initializer error; got: %v", formatErrs(errs))
			}
			if test.wantOther != "" {
				require.NotEmptyf(t, errorsContaining(errs, test.wantOther),
					"expected an error containing %q; got: %v",
					test.wantOther, formatErrs(errs))
			}
		})
	}
}

// The same rule on the statement-level class path, which is a separate
// implementation from the module one.
func TestStaticFieldInitializerInAScript(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		input       string
		wantMissing bool
	}{
		"DeclareClassNeedsNoInitializer": {
			input: "declare class Foo {\n\tstatic x: number,\n}\n",
		},
		"ClassWithABodyStillNeedsOne": {
			input:       "class Foo {\n\tstatic x: number,\n}\n",
			wantMissing: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			source := &ast.Source{ID: 0, Path: "input.esc", Contents: test.input}
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			script, parseErrors := parser.NewParser(ctx, source).ParseScript()
			require.Empty(t, parseErrors, "expected no parse errors")

			c := NewChecker(ctx)
			_, errs := c.InferScript(Context{Scope: Prelude(c)}, script)
			matched := errorsContaining(errs, "must have an initializer")
			if test.wantMissing {
				require.NotEmptyf(t, matched,
					"expected a missing-initializer error; got: %v", formatErrs(errs))
			} else {
				require.Emptyf(t, matched,
					"expected no missing-initializer error; got: %v", formatErrs(errs))
			}
		})
	}
}
