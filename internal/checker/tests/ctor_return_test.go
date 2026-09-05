package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/require"
)

// A `.d.ts` construct signature can pin a type argument its class leaves free:
// `new (arrayLength?: number): any[]` builds `Array<any>` whatever `T` the caller
// wanted. A `declare class` constructor writes that return down, and a call
// through it produces the pinned type rather than the class's own parameter.
// The converter writes `mut Array<any>` for `Array`'s no-argument constructor,
// matching how the rest of that declaration spells a mutable array, so a `mut`
// wrapper has to read as naming the class too.
func TestDeclareClassConstructorPinsItsReturn(t *testing.T) {
	tests := map[string]struct {
		input        string
		expectedType string
	}{
		"PinnedTypeArgument": {
			input: `
				declare class Box<T> {
					constructor(mut self, n: number) -> Box<number>,
				}
				val b = Box(5)
			`,
			expectedType: "Box<number>",
		},
		"MutablePinnedTypeArgument": {
			input: `
				declare class Box<T> {
					constructor(mut self, n: number) -> mut Box<number>,
				}
				val b = Box(5)
			`,
			expectedType: "mut Box<number>",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ns := mustInferAsModule(t, test.input)
			actual := collectBindingTypes(ns)
			got, ok := actual["b"]
			require.True(t, ok, "binding b not found")
			require.Equal(t, test.expectedType, got)
		})
	}
}

// Errors a constructor's return type can draw. A class the compiler emits builds
// `Self` and has nowhere to put anything else, and even a `declare class`
// constructor builds the class it belongs to.
func TestConstructorReturnTypeErrors(t *testing.T) {
	tests := map[string]struct {
		input string
		want  []string
	}{
		"EmittedClass": {
			input: `
				class Box {
					constructor(mut self) -> Box,
				}
			`,
			want: []string{"Only a `declare class` constructor can declare a return type; every other constructor returns `Self`."},
		},
		"ReturnsAnotherClass": {
			input: `
				declare class Lid {}
				declare class Box {
					constructor(mut self) -> Lid,
				}
			`,
			want: []string{"A constructor of `Box` must return `Box`."},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var messages []string
			for _, e := range inferModuleErrors(t, test.input) {
				switch e.(type) {
				case ConstructorWithReturnTypeError, ConstructorReturnMustBeSelfError:
					messages = append(messages, e.Message())
				}
			}
			require.Equal(t, test.want, messages)
		})
	}
}
