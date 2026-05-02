package tests

import (
	"strings"
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
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
