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
			expected: `export { pub } from "./internal.js";`,
		},
		"NothingExported": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "val priv = 2"},
			},
			expected: "",
		},
		// A mutable root binding is forwarded rather than copied, so a consumer sees
		// whatever the internal bundle last assigned to it.
		"MutableRootDeclaration": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export var counter = 0"},
			},
			expected: `export { counter } from "./internal.js";`,
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
			expected: `export { pub } from "./internal.js";`,
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
			expected: `export { pub } from "./internal.js";`,
		},
		// The internal bundle emits no definition for an ambient declaration, so there
		// is nothing for the wrapper to forward.
		"AmbientDeclaration": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export declare val ambient: number\nexport val pub = 1"},
			},
			expected: `export { pub } from "./internal.js";`,
		},
		// A type alias has no runtime value, so it never reaches the wrapper.
		"TypeAlias": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export type Pub = number\nexport val pub = 1"},
			},
			expected: `export { pub } from "./internal.js";`,
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
		// A namespace named `internal` would shadow the binding the wrapper reads the
		// internal bundle through, so the alias moves out of its way.
		"NamespaceNamedInternal": {
			sources: []*ast.Source{
				{ID: 0, Path: "internal/thing.esc", Contents: "export val thing = 1"},
			},
			expected: `import * as internal_ from "./internal.js";
export const internal = {thing: internal_.internal.thing};`,
		},
		// A forwarded root declaration binds nothing locally, so it cannot collide with
		// the alias and the alias stays `internal`.
		"RootDeclarationNamedInternal": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export val internal = 1"},
				{ID: 1, Path: "geo/shapes.esc", Contents: "export val pub = 2"},
			},
			expected: `import * as internal from "./internal.js";
export { internal } from "./internal.js";
export const geo = {pub: internal.geo.pub};`,
		},
		// The internal bundle emits `throw "nope";` and binds nothing, so forwarding
		// `boom` would be a link error that keeps the whole entry point from loading.
		"ThrowInitializer": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "export val boom = throw \"nope\"\nexport val pub = 1"},
			},
			expected: `export { pub } from "./internal.js";`,
		},
		// `val pat = init else { … }` binds through buildPatternCondition, which marks
		// none of its bindings `export`, so there is nothing to forward.
		"ElseInitializer": {
			sources: []*ast.Source{
				{ID: 0, Path: "main.esc", Contents: "declare val obj: {a?: number}\nexport val a = obj.a else { 0 }\nexport val pub = 1"},
			},
			expected: `export { pub } from "./internal.js";`,
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
