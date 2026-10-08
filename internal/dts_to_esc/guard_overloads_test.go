package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/stretchr/testify/require"
)

// TestDropGuardOverloads covers which overloads dropGuardOverloads removes from a
// parsed declaration. Each case lists the members left, one entry per arm.
func TestDropGuardOverloads(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "AGuardArmBesideAPlainArmIsDropped",
			src: `interface Array<T> {
				filter<S extends T>(predicate: (value: T) => value is S): S[];
				filter(predicate: (value: T) => unknown): T[];
			}`,
			want: []string{"filter"},
		},
		{
			name: "AParenthesizedGuardIsDropped",
			src: `interface Array<T> {
				find<S extends T>(predicate: ((value: T) => value is S)): S | undefined;
				find(predicate: (value: T) => unknown): T | undefined;
			}`,
			want: []string{"find"},
		},
		{
			// A same-named overload taking something other than a callback is a different
			// call, so the guard overload is the only one that takes a predicate.
			name: "AGuardArmBesideAnUnrelatedOverloadIsKept",
			src: `interface Array<T> {
				choose<S extends T>(predicate: (value: T) => value is S): S;
				choose(index: number): T;
			}`,
			want: []string{"choose", "choose"},
		},
		{
			name: "AGuardArmBesideAPlainArmOfAnotherArityIsKept",
			src: `interface Array<T> {
				filter<S extends T>(predicate: (value: T) => value is S): S[];
				filter(predicate: (value: T) => unknown, thisArg: unknown): T[];
			}`,
			want: []string{"filter", "filter"},
		},
		{
			name: "ALoneGuardArmIsKept",
			src: `interface Array<T> {
				filter<S extends T>(predicate: (value: T) => value is S): S[];
			}`,
			want: []string{"filter"},
		},
		{
			// The guard names no type parameter of its own method, so no binder is left
			// unconstrained.
			name: "AGuardOnAnOuterParameterIsKept",
			src: `interface Box<T> {
				test(predicate: (value: unknown) => value is T): boolean;
				test(predicate: (value: unknown) => unknown): boolean;
			}`,
			want: []string{"test", "test"},
		},
		{
			name: "AnAssertionIsKept",
			src: `interface Box<T> {
				check<S extends T>(predicate: (value: T) => asserts value is S): void;
				check(predicate: (value: T) => unknown): void;
			}`,
			want: []string{"check", "check"},
		},
		{
			name: "AGuardArmInsideANamespaceIsDropped",
			src: `declare namespace NS {
				interface Array<T> {
					filter<S extends T>(predicate: (value: T) => value is S): S[];
					filter(predicate: (value: T) => unknown): T[];
				}
			}`,
			want: []string{"filter"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mod, errs := dts_parser.NewDtsParser(&ast.Source{Path: "lib.test.d.ts", Contents: tt.src}).ParseModule()
			require.Empty(t, errs)
			dropGuardOverloads([]LibInput{{SourceFile: "lib.test.d.ts", Module: mod}})
			require.Equal(t, tt.want, interfaceMemberNames(t, mod.Statements))
		})
	}
}

// interfaceMemberNames returns the method names of the one interface stmts declares,
// looking through a namespace.
func interfaceMemberNames(t *testing.T, stmts []dts_parser.Statement) []string {
	t.Helper()
	require.Len(t, stmts, 1)
	switch s := stmts[0].(type) {
	case *dts_parser.NamespaceDecl:
		return interfaceMemberNames(t, s.Statements)
	case *dts_parser.InterfaceDecl:
		var names []string
		for _, m := range s.Members {
			sig, ok := m.(*dts_parser.MethodSignature)
			require.True(t, ok)
			names = append(names, propertyKeyName(sig.Name))
		}
		return names
	}
	require.FailNow(t, "no interface declared")
	return nil
}
