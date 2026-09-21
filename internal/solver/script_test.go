package solver

import (
	"context"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// parseScript parses a single in-memory .esc source as a script and returns the AST.
// A script is top-level statements that run in source order with function-body
// semantics. parseScript is the script counterpart to parseModule, parsing through
// ParseScript rather than the library-module assembly ParseLibFiles drives.
func parseScript(t *testing.T, src string) *ast.Script {
	t.Helper()
	source := &ast.Source{ID: 0, Path: "input.esc", Contents: src}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()
	require.Empty(t, parseErrors, "expected no parse errors")
	registerTestSources(t, map[int]*ast.Source{source.ID: source})
	return script
}

// inferScriptSource parses src as a script, runs InferScript, and renders the
// top-level value and type bindings off the script scope's own maps rather than the
// prelude parent. It is the script counterpart to inferSource. A script's top-level
// bindings are linear locals, so this is how their generalized types are inspected.
func inferScriptSource(t *testing.T, src string) (values, types map[string]string, errs []SolverError) {
	t.Helper()
	scope, _, errs := InferScript(parseScript(t, src), testStdlibSource())
	values = make(map[string]string, len(scope.values))
	for name, b := range scope.values {
		values[name] = renderScheme(b.Schemes[0])
	}
	types = renderBindings(scope.types, func(b TypeBinding) soltype.Type { return b.Type })
	return values, types, errs
}

// TestInferScriptSourceOrder checks the core of the script entry point: top-level
// statements infer in source order, each binding seeing only the ones before it,
// exactly as inside a function body and unlike a module's dependency-ordered decls.
func TestInferScriptSourceOrder(t *testing.T) {
	values, _, errs := inferScriptSource(t, `
		val x = 5
		val y = x
	`)
	require.Empty(t, errs)
	require.Equal(t, "5", values["x"])
	require.Equal(t, "5", values["y"])
}

// TestInferScriptEnum checks that an enum can be declared and used in a script (bin/).
// A script's top-level statements run in source order, so an enum declared before its
// use binds its type and variant constructors just as in a module: a constructor call
// yields the enum's alias type, rendered under its own name.
func TestInferScriptEnum(t *testing.T) {
	values, types, errs := inferScriptSource(t, `
		enum Color {
			RGB(r: number, g: number, b: number),
			Hex(code: string),
		}
		val c = Color.Hex("#fff")
		val mk = Color.RGB
	`)
	require.Empty(t, errs)
	require.Equal(t, "Color", types["Color"])
	require.Equal(t, "Color", values["c"])
	require.Equal(t, "fn (r: number, g: number, b: number) -> Color", values["mk"])
}

// TestInferScriptEnumOutOfOrder checks that a script does NOT resolve an enum used before
// its declaration. Unlike a module, whose top-level declarations are dependency-ordered,
// a script's statements are linear, so a forward reference is an unknown identifier.
func TestInferScriptEnumOutOfOrder(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val c = Color.Hex("#fff")
		enum Color {
			RGB(r: number),
			Hex(code: string),
		}
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: Color", errs[0].Message())
}

// TestInferScriptClass checks that a class can be declared and used in a script (bin/):
// the constructor value and instance type bind, and field and method access resolve
// through the projected class body, just as in a module.
func TestInferScriptClass(t *testing.T) {
	values, types, errs := inferScriptSource(t, `
		class Point {
			x: number,
			y: number,
			getX(self) -> number { return self.x },
		}
		val p = Point(1, 2)
		val px = p.x
		val d = p.getX()
	`)
	require.Empty(t, errs)
	require.Equal(t, "Point", types["Point"])
	require.Equal(t, "{new (x: number, y: number) -> Point}", values["Point"])
	require.Equal(t, "Point", values["p"])
	require.Equal(t, "number", values["px"])
	require.Equal(t, "number", values["d"])
}

// TestInferScriptClassOutOfOrder checks that a script does NOT resolve a class used
// before its declaration — its statements are linear, so a forward reference is an
// unknown identifier, unlike a module's dependency-ordered declarations.
func TestInferScriptClassOutOfOrder(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val p = Point(1, 2)
		class Point { x: number, y: number }
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "Unknown identifier: Point", errs[0].Message())
}

// TestScriptTransitionParity is the milestone's central claim: a `mut`→immutable
// transition at script top level is checked identically to the same statements
// wrapped in a function body. Each case runs through InferScript directly, then again
// through InferModule after being indented into `fn test() { … }`. The two error lists
// must match each other and the expected verdict. That match proves a script's
// top-level body runs the same liveness, alias, and transition machinery a function
// body does.
func TestScriptTransitionParity(t *testing.T) {
	tests := map[string]struct {
		// stmts are valid both at script top level and inside a function body, so the
		// same text drives both entry points.
		stmts string
		want  []string
	}{
		// Rule 1, the mut→immutable case, errors when the mutable source is live after an
		// immutable borrow of it. A top-level `val items: mut {…}` borrowed immutably and
		// then used mutably reports the Rule 1 transition error.
		"Rule1_SourceLive_Error": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				val snapshot: &{x: number} = items
				items.x = 2
				snapshot
			`,
			want: []string{
				"cannot assign 'items' to immutable 'snapshot': 'items' is still used mutably after this point",
			},
		},
		// Rule 1: safe when the mutable source is dead after the borrow.
		"Rule1_SourceDead_OK": {
			stmts: `
				val items: mut {x: number} = {x: 1}
				items.x = 2
				val snapshot: &{x: number} = items
				snapshot
			`,
		},
		// Rule 3: two mutable borrows of the same value are always allowed.
		"Rule3_MultipleMutableAliases_OK": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: &mut {x: number} = a
				b.x = 2
				a.x
			`,
		},
		// Chain aliasing through a mutable borrow: the conflict names the live mutable
		// alias, not the source itself.
		"ChainAlias_TargetLive_Error": {
			stmts: `
				val a: mut {x: number} = {x: 1}
				val b: &mut {x: number} = a
				val c: &{x: number} = b
				a.x = 2
				c
			`,
			want: []string{
				"cannot assign 'b' to immutable 'c': 'a' still has mutable access to 'b' after this point",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, scriptErrs := inferScriptSource(t, tt.stmts)
			scriptMsgs := transitionMessages(t, scriptErrs)

			_, _, fnErrs := inferSource(t, "fn test() {"+tt.stmts+"\n}")
			fnMsgs := transitionMessages(t, fnErrs)

			if len(tt.want) == 0 {
				require.Empty(t, scriptMsgs)
				require.Empty(t, fnMsgs)
				return
			}
			require.ElementsMatch(t, tt.want, scriptMsgs)
			require.ElementsMatch(t, tt.want, fnMsgs, "script and function-wrapped forms must report the same transition errors")
		})
	}
}

// TestScriptLinearScoping pins the defining difference between a script and a
// module: a script's top-level statements are a linear body, so a binding sees only
// the ones before it. The same source that forward-references a later binding is an
// "Unknown identifier" error as a script but type-checks as a module, where
// BuildDepGraph orders declarations by dependency rather than source position.
func TestScriptLinearScoping(t *testing.T) {
	src := `
		val y = x
		val x = 5
	`
	_, _, scriptErrs := inferScriptSource(t, src)
	require.Len(t, scriptErrs, 1)
	require.Equal(t, "2:11-2:12: Unknown identifier: x", msgWithSpan(t, scriptErrs[0]))

	// The identical source is well-formed as a module. The dep graph types `val x`
	// before the `val y` that refers to it.
	_, _, moduleErrs := inferSource(t, src)
	require.Empty(t, moduleErrs)
}

// TestScriptEmpty checks that a script with no statements infers cleanly. The
// liveness pre-pass over an empty block and the empty statement walk must not panic
// or invent diagnostics.
func TestScriptEmpty(t *testing.T) {
	values, _, errs := inferScriptSource(t, "")
	require.Empty(t, errs)
	require.Empty(t, values)
}

// TestScriptRedeclaration checks that a script allows a name to be redeclared, the
// function-body rule rather than the module rule. inferStmt overwrites the name's binding
// on a body-level `val`/`var`, so a second declaration rebinds without constraining
// the old and new types together. A later reference sees the new type. The identical
// source as a module is rejected, because the dep-graph driver reports a duplicate
// top-level declaration.
func TestScriptRedeclaration(t *testing.T) {
	src := `
		val x = 5
		val x = "hi"
		val y = x
	`
	values, _, scriptErrs := inferScriptSource(t, src)
	require.Empty(t, scriptErrs)
	require.Equal(t, `"hi"`, values["x"])
	require.Equal(t, `"hi"`, values["y"])

	// The same source is a duplicate-declaration error as a module.
	_, _, moduleErrs := inferSource(t, src)
	require.Len(t, moduleErrs, 1)
	require.Equal(t, "3:3-3:15: Duplicate declaration: x", msgWithSpan(t, moduleErrs[0]))
}

// TestScriptReassignTransition exercises the reassignment transition path
// (inferAssign), distinct from the declaration-aliasing path the other parity cases
// drive. inferAssign reads the enclosing statement from c.fn.currentStmt to find its
// CFG StmtRef, which only exists because InferScript installs a funcCtx and runs the
// liveness pre-pass over the script body. Reassigning an owned value into an owned destination
// moves it, so the later use of the source is a use-after-move. Mutating it before the
// reassignment makes it dead, and the move stays silent.
func TestScriptReassignTransition(t *testing.T) {
	t.Run("source_live_error", func(t *testing.T) {
		_, _, errs := inferScriptSource(t, `
			var snap: {x: number} = {x: 0}
			val items: mut {x: number} = {x: 1}
			snap = items
			items.x = 2
			snap
		`)
		require.Equal(t, []string{
			"5:4-5:9: use of moved value 'items'",
		}, transitionMessagesWithSpan(t, errs))
	})

	t.Run("source_dead_ok", func(t *testing.T) {
		_, _, errs := inferScriptSource(t, `
			var snap: {x: number} = {x: 0}
			val items: mut {x: number} = {x: 1}
			items.x = 2
			snap = items
			snap
		`)
		require.Empty(t, transitionMessages(t, errs))
	})
}

// TestScriptBorrowLifetimeParity checks that the lifetime origination/escape
// machinery runs over a script body too. A function expression with a `&mut`
// parameter, bound to a top-level `val`, carries a fresh lifetime on its parameter
// and threads it through the return. The `'a` in the rendered type is the evidence
// the borrow lifetime was inferred, not skipped. The identical source as a module
// binds the same `id`, and the two rendered types must match. The script entry
// point and the module entry point thread the borrow lifetime the same way.
func TestScriptBorrowLifetimeParity(t *testing.T) {
	const src = `val id = fn (p: &mut {x: number}) { return p }`

	scriptValues, _, scriptErrs := inferScriptSource(t, src)
	require.Empty(t, scriptErrs)
	require.Equal(t, "fn <'a>(p: &'a mut {x: number}) -> &'a mut {x: number}", scriptValues["id"])

	moduleValues, _, moduleErrs := inferSource(t, src)
	require.Empty(t, moduleErrs)
	require.Equal(t, scriptValues["id"], moduleValues["id"])
}

// TestScriptAwaitOutsideAsync pins the top-level `await` diagnostic. A script has no
// enclosing function to mark `async`, so the error carries no related span. That
// matches a module top-level await, rather than pointing Related() at the whole
// script. This guards InferScript passing a nil funcCtx node.
func TestScriptAwaitOutsideAsync(t *testing.T) {
	_, _, errs := inferScriptSource(t, `
		val x = 5
		val y = await x
	`)
	require.Len(t, errs, 1)
	require.Equal(t, "3:11-3:18: await can only be used inside an async function", msgWithSpan(t, errs[0]))
	require.Empty(t, errs[0].Related())
}

// inferScriptInLib parses libSrc as a library module and scriptSrc as a script,
// infers the module, then infers the script against the module's scope through
// InferScriptInLib. It returns the script scope's own value bindings and the
// script's diagnostics. The module is required to infer cleanly, so an error the
// caller sees belongs to the script.
func inferScriptInLib(t *testing.T, libSrc, scriptSrc string) (values map[string]string, errs []SolverError) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: libSrc}
	module, libParseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, libParseErrors, "expected no parse errors in the library")

	scriptSource := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: scriptSrc}
	script, scriptParseErrors := parser.NewParser(ctx, scriptSource).ParseScript()
	require.Empty(t, scriptParseErrors, "expected no parse errors in the script")

	registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, scriptSource.ID: scriptSource})

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors, "expected no errors from the library module")

	scope, _, errs := InferScriptInLib(script, lib)
	values = make(map[string]string, len(scope.values))
	for name, b := range scope.values {
		values[name] = renderScheme(b.Schemes[0])
	}
	return values, errs
}

// TestInferScriptInLib checks the bin/ to lib/ seam: a script infers against the
// scope a library module's run produced, so a name the script does not declare
// resolves to the library's binding without an import.
//
// The class and alias cases are what a second inference run could not do. Both
// resolve to a handle carrying a name whose definition lives on the run's Context,
// so reading `p.x` off a library class or annotating against a library alias only
// works because the script carries the library's run on.
func TestInferScriptInLib(t *testing.T) {
	tests := []struct {
		name       string
		lib        string
		script     string
		wantValues map[string]string
	}{
		{
			name:       "Value",
			lib:        `export val greeting = "hello"`,
			script:     `val g = greeting`,
			wantValues: map[string]string{"g": `"hello"`},
		},
		{
			name: "ClassMember",
			lib: `
				export class Point {
					x: number,
					y: number,
					getX(self) -> number { return self.x },
				}
			`,
			script: `
				val p = Point(1, 2)
				val px = p.x
				val gx = p.getX()
			`,
			wantValues: map[string]string{"p": "Point", "px": "number", "gx": "number"},
		},
		{
			name:       "TypeAliasAnnotation",
			lib:        `export type Pair = {a: number, b: number}`,
			script:     `val pr: Pair = {a: 1, b: 2}`,
			wantValues: map[string]string{"pr": "Pair"},
		},
		{
			name:       "ScriptBindingShadowsLibrary",
			lib:        `export val greeting = "hello"`,
			script:     `val greeting = "goodbye"`,
			wantValues: map[string]string{"greeting": `"goodbye"`},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, errs := inferScriptInLib(t, test.lib, test.script)
			require.Empty(t, errs)
			for name, want := range test.wantValues {
				require.Equal(t, want, values[name], "value binding %q", name)
			}
		})
	}
}

// TestInferScriptInLibUnknownName checks that the library scope adds the library's
// bindings and nothing else: a name neither the script nor the library declares is
// still unknown.
func TestInferScriptInLibUnknownName(t *testing.T) {
	_, errs := inferScriptInLib(t, `export val greeting = "hello"`, `val f = farewell`)
	require.Len(t, errs, 1)
	require.Equal(t, "1:9-1:17: Unknown identifier: farewell", msgWithSpan(t, errs[0]))
}

// TestInferScriptInLibClassShadowsLibrary checks that a class a script declares is
// its own class rather than an addition to the library's class of the same name.
// Both scripts and the library register their nominal definitions in the run's one
// Context, so each needs a key prefix of its own for two same-named classes to hold
// two definitions.
func TestInferScriptInLibClassShadowsLibrary(t *testing.T) {
	values, errs := inferScriptInLib(t, `
		export class Point {
			x: number,
		}
		export fn origin() -> Point { return Point(0) }
	`, `
		class Point {
			label: string,
		}
		val p = Point("here")
		val l = p.label
		val o = origin()
		val ox = o.x
	`)
	require.Empty(t, errs)
	require.Equal(t, `{new (label: string) -> Point}`, values["Point"])
	require.Equal(t, "Point", values["p"])
	require.Equal(t, "string", values["l"])
	require.Equal(t, "Point", values["o"])
	require.Equal(t, "number", values["ox"])
}

// TestInferScriptInLibClassPerScript checks that two scripts checked against one
// library each keep their own definition for a class name they both declare. The
// registries live on the run all three share, so without a per-script key the second
// script's definition would replace the first's while the first's scope still holds a
// handle naming it.
func TestInferScriptInLibClassPerScript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	scripts := []*ast.Source{
		{ID: 1, Path: "bin/first.esc", Contents: "class Shape {\n\tsides: number,\n}\nval s = Shape(3)\nval n = s.sides"},
		{ID: 2, Path: "bin/second.esc", Contents: "class Shape {\n\tname: string,\n}\nval s = Shape(\"square\")\nval n = s.name"},
	}
	sources := map[int]*ast.Source{libSource.ID: libSource}
	for _, src := range scripts {
		sources[src.ID] = src
	}
	registerTestSources(t, sources)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)

	wantMember := []string{"number", "string"}
	qnames := make([]string, len(scripts))
	for i, src := range scripts {
		script, scriptParseErrors := parser.NewParser(ctx, src).ParseScript()
		require.Empty(t, scriptParseErrors)
		scope, _, errs := InferScriptInLib(script, lib)
		require.Empty(t, errs, "script %s", src.Path)

		b, found := scope.GetValue("n")
		require.True(t, found)
		require.Equal(t, wantMember[i], renderScheme(b.Schemes[0]), "script %s", src.Path)

		shape, found := scope.GetType("Shape")
		require.True(t, found)
		cls, isClass := shape.Type.(*soltype.ClassType)
		require.True(t, isClass)
		qnames[i] = cls.Name
	}

	// Each script's handle names a key of its own, and both definitions still hold
	// their own members once the second script has run.
	require.NotEqual(t, qnames[0], qnames[1])
	wantBody := []string{"{sides: number}", "{name: string}"}
	for i, qname := range qnames {
		def, registered := lib.checker.ctx.classDef(qname)
		require.True(t, registered, "script %s", scripts[i].Path)
		require.Equal(t, wantBody[i], soltype.Print(def.Body), "script %s", scripts[i].Path)
	}
}

// TestInferScriptInLibLeavesTheLibraryTableAlone checks that re-checking a script
// against one library does not grow the library run's Prov table. The table maps a
// type to the source it came from, and a script's own entries belong to the script:
// an editor re-checks one bin/ file on every keystroke against a cached library, so
// a table shared with the library would grow for as long as the session lasts.
func TestInferScriptInLibLeavesTheLibraryTableAlone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val greeting = "hello"`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	scriptSource := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: "val g = greeting\nval h = g"}
	registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, scriptSource.ID: scriptSource})

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	before := len(lib.checker.prov)

	for range 5 {
		script, scriptParseErrors := parser.NewParser(ctx, scriptSource).ParseScript()
		require.Empty(t, scriptParseErrors)
		_, _, errs := InferScriptInLib(script, lib)
		require.Empty(t, errs)
	}

	require.Equal(t, before, len(lib.checker.prov))
}

// TestInferScriptInLibRechecksAScript checks what a second check of one script
// against one library sees. An editor re-checks a bin/ file on every keystroke
// against a cached library, so the two checks share the run that holds the class
// and alias registries.
//
// The second check reads the declarations it was given rather than the ones the
// first check registered under the same names, and the registries hold what the
// current check declared rather than the union of every check so far. The second
// version below drops a class the first declared, which is what a registry that
// only ever overwrites would keep.
func TestInferScriptInLibRechecksAScript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	classesBefore := len(lib.checker.ctx.classes)

	// Every version is the same file, so they carry the same source id and key their
	// declarations under the same names.
	versions := []struct {
		name     string
		contents string
		member   string
		classes  int
	}{
		{
			name:     "TwoClasses",
			contents: "class Point {\n\tx: number,\n}\nclass Shape {\n\tsides: number,\n}\nval p = Point(1)\nval m = p.x",
			member:   "number",
			classes:  2,
		},
		{
			name:     "OneClassWithAChangedMember",
			contents: "class Point {\n\tlabel: string,\n}\nval p = Point(\"here\")\nval m = p.label",
			member:   "string",
			classes:  1,
		},
		{
			name:     "BackToTheFirstMember",
			contents: "class Point {\n\tx: number,\n}\nval p = Point(2)\nval m = p.x",
			member:   "number",
			classes:  1,
		},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			source := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: version.contents}
			registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, source.ID: source})
			script, scriptParseErrors := parser.NewParser(ctx, source).ParseScript()
			require.Empty(t, scriptParseErrors)

			scope, _, errs := InferScriptInLib(script, lib)
			require.Empty(t, errs)
			b, found := scope.GetValue("m")
			require.True(t, found)
			require.Equal(t, version.member, renderScheme(b.Schemes[0]))

			require.Equal(t, classesBefore+version.classes, len(lib.checker.ctx.classes))
		})
	}
}

// TestInferScriptInLibRechecksAnEnum is TestInferScriptInLibRechecksAScript for an
// enum, which registers a class per variant and one alias for the enum itself. A
// script cannot declare a type alias — the walk rejects a TypeDecl in a function
// body, which a script body is — so an enum is how a script reaches the alias
// registry, and this is what checks that the alias side is cleared too.
func TestInferScriptInLibRechecksAnEnum(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	libSource := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: `export val unused = 0`}
	module, parseErrors := parser.ParseLibFiles(ctx, []*ast.Source{libSource})
	require.Empty(t, parseErrors)

	lib := InferModuleWithSource(module, testStdlibSource())
	require.Empty(t, lib.Errors)
	classesBefore := len(lib.checker.ctx.classes)
	aliasesBefore := len(lib.checker.ctx.aliases)

	versions := []struct {
		name     string
		contents string
		payload  string
		variants int
	}{
		{
			name:     "TwoVariants",
			contents: "enum Color {\n\tHex(code: string),\n\tRgb(r: number),\n}\nval c = Color.Hex(\"#fff\")\nval p = c",
			payload:  "Color",
			variants: 2,
		},
		{
			name:     "OneVariantWithAChangedPayload",
			contents: "enum Color {\n\tHex(code: number),\n}\nval c = Color.Hex(1)\nval p = c",
			payload:  "Color",
			variants: 1,
		},
	}

	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			source := &ast.Source{ID: 1, Path: "bin/index.esc", Contents: version.contents}
			registerTestSources(t, map[int]*ast.Source{libSource.ID: libSource, source.ID: source})
			script, scriptParseErrors := parser.NewParser(ctx, source).ParseScript()
			require.Empty(t, scriptParseErrors)

			scope, _, errs := InferScriptInLib(script, lib)
			require.Empty(t, errs)
			b, found := scope.GetValue("p")
			require.True(t, found)
			require.Equal(t, version.payload, renderScheme(b.Schemes[0]))

			// One class per variant this version declares, and one alias for the
			// enum, whichever check this is.
			require.Equal(t, classesBefore+version.variants, len(lib.checker.ctx.classes))
			require.Equal(t, aliasesBefore+1, len(lib.checker.ctx.aliases))
		})
	}
}
