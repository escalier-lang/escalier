package codegen

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

func TestBuildPublicWrapper(t *testing.T) {
	tests := map[string]struct {
		sources  []*ast.Source
		expected string
	}{
		"RootDeclarations": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export val pub = 1\nval priv = 2"},
			},
			expected: `import * as internal from "./internal.js";
export const pub = internal.pub;`,
		},
		"NothingExported": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "val priv = 2"},
			},
			expected: "",
		},
		"NamespaceMembers": {
			sources: []*ast.Source{
				{ID: 0, Path: "geo/shapes.esc", Contents: "export val pub = 1\nval priv = 2"},
			},
			expected: `import * as internal from "./internal.js";
export const geo = {pub: internal.geo.pub};`,
		},
		"NamespaceWithNoExportedMembers": {
			sources: []*ast.Source{
				{ID: 0, Path: "geo/shapes.esc", Contents: "val priv = 2"},
				{ID: 1, Path: "main.esc", Contents: "export val pub = 1"},
			},
			expected: `import * as internal from "./internal.js";
export const pub = internal.pub;`,
		},
		"NestedNamespaces": {
			sources: []*ast.Source{
				{ID: 0, Path: "app/config.esc", Contents: "export val config = 1"},
				{ID: 1, Path: "app/utils/text.esc", Contents: "export val trim = 2\nval pad = 3"},
			},
			expected: `import * as internal from "./internal.js";
export const app = {config: internal.app.config, utils: {trim: internal.app.utils.trim}};`,
		},
		// The `app` namespace has no exported members of its own, so it reaches the
		// wrapper only because `app.utils.trim` does.
		"NestedNamespaceCarriesItsParent": {
			sources: []*ast.Source{
				{ID: 0, Path: "app/config.esc", Contents: "val config = 1"},
				{ID: 1, Path: "app/utils/text.esc", Contents: "export val trim = 2"},
			},
			expected: `import * as internal from "./internal.js";
export const app = {utils: {trim: internal.app.utils.trim}};`,
		},
		// Emptiness is transitive: `app` holds only `app.utils`, and every member of
		// `app.utils` is unexported.
		"EmptinessIsTransitive": {
			sources: []*ast.Source{
				{ID: 0, Path: "app/utils/text.esc", Contents: "val trim = 2"},
				{ID: 1, Path: "main.esc", Contents: "export val pub = 1"},
			},
			expected: `import * as internal from "./internal.js";
export const pub = internal.pub;`,
		},
		// The internal bundle emits no definition for an ambient declaration, so there
		// is nothing for the wrapper to read out of it.
		"AmbientDeclaration": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export declare val ambient: number\nexport val pub = 1"},
			},
			expected: `import * as internal from "./internal.js";
export const pub = internal.pub;`,
		},
		// A type alias has no runtime value, so it never reaches the wrapper.
		"TypeAlias": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export type Pub = number\nexport val pub = 1"},
			},
			expected: `import * as internal from "./internal.js";
export const pub = internal.pub;`,
		},
		// `app.utils` names both a member of `app` and a namespace nested in it. The
		// member wins, matching the internal bundle, where `app.utils = app__utils`
		// runs after `app.utils = {}` and overwrites it.
		"MemberAndNamespaceShareAName": {
			sources: []*ast.Source{
				{ID: 0, Path: "app/utils.esc", Contents: "export val utils = 1"},
				{ID: 1, Path: "app/utils/text.esc", Contents: "export val trim = 2"},
			},
			expected: `import * as internal from "./internal.js";
export const app = {utils: internal.app.utils};`,
		},
		"Class": {
			sources: []*ast.Source{
				{ID: 0, Path: "geo/shapes.esc", Contents: "export class Point {\n    x: number,\n    constructor(mut self, x: number) {\n        self.x = x\n    },\n}"},
			},
			expected: `import * as internal from "./internal.js";
export const geo = {Point: internal.geo.Point};`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()

			module, parseErrors := parser.ParseLibFiles(ctx, test.sources)
			require.Empty(t, parseErrors)

			depGraph := dep_graph.BuildDepGraph(module)
			outModule := BuildPublicWrapper(depGraph, "./internal.js")

			printer := NewPrinter()
			for i, stmt := range outModule.Stmts {
				if i > 0 {
					printer.NewLine()
				}
				printer.PrintStmt(stmt)
			}

			require.Equal(t, test.expected, printer.Output)
		})
	}
}
