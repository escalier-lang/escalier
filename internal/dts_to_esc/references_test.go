package dts_to_esc

import (
	"context"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

func parseSource(t *testing.T, src string) *ast.Module {
	t.Helper()
	module, errs := parser.ParseLibFiles(context.Background(), []*ast.Source{{
		ID: 1, Path: "pkg.esc", Contents: src,
	}})
	require.Empty(t, errs, "expected no parse errors")
	return module
}

// A reference is collected wherever it is written. The type-parameter and
// `throws` slots are the ones `Accept` does not reach, so each is named here.
func TestTypeRefNamesReachesEverySlot(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"AFieldAnnotation", `export declare class C { f: Target }`, "Target"},
		{"AGenericArgument", `export declare class C { f: Box<Target> }`, "Target"},
		{"AnExtendsClause", `export declare class C extends Target {}`, "Target"},
		{"AMethodReturn", `export declare class C { m(self) -> Target }`, "Target"},
		{"AnAliasBody", `export type A = Target`, "Target"},
		{"AFunctionParameter", `export declare fn f(x: Target) -> number`, "Target"},
		{"AQualifiedHead", `export type A = Target.Inner`, "Target"},

		// The four slots the walk skips.
		{"AClassTypeParamConstraint", `export declare class C<T: Target> {}`, "Target"},
		{"AClassTypeParamDefault", `export declare class C<T = Target> {}`, "Target"},
		{"AnAliasTypeParamConstraint", `export type A<T: Target> = T`, "Target"},
		{"AFunctionThrows", `export declare fn f() -> number throws Target`, "Target"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs := TypeRefNames(parseSource(t, tt.src))
			require.True(t, refs.Contains(tt.want),
				"%s did not record %s; recorded %v", tt.src, tt.want, refs.ToSlice())
		})
	}
}

// A qualified reference contributes its first segment alone. `Intl.Collator`
// needs an import of `Intl`, and `Collator` is read off it.
func TestTypeRefNamesRecordsTheHeadOfAQualifiedName(t *testing.T) {
	refs := TypeRefNames(parseSource(t, `export type A = Intl.Collator`))
	require.True(t, refs.Contains("Intl"))
	require.False(t, refs.Contains("Collator"))
}

func TestDeclaredNamesCoversEveryDeclarationKind(t *testing.T) {
	refs := DeclaredNames(parseSource(t, `
		export declare class Cls {}
		export declare fn func() -> number
		export type Alias = number
		export declare val value: number
		export declare var variable: number
		export interface Iface {}
		export namespace Ns {
			export declare val inner: number
		}
	`))
	for _, name := range []string{"Cls", "func", "Alias", "value", "variable", "Iface", "Ns"} {
		require.True(t, refs.Contains(name), "%s was not recorded; recorded %v", name, refs.ToSlice())
	}
	// A namespace member is read off the namespace and is not a top-level name.
	require.False(t, refs.Contains("inner"))
}
