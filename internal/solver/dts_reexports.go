package solver

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/set"
)

// dts_reexports.go reads the export statements of a package's `.d.ts` files and
// sets the export flags of the declarations those files hold. The statements are:
//
//   - `export { a, b as c }`, which exports names the same file declares or binds
//     with a named import;
//   - `export { a, b as c } from "./other"`, which exports names another file
//     exports;
//   - `export * from "./other"`, which exports every name another file exports.
//
// A specifier that starts with `./` or `../` names another file of the package. Any
// other specifier names another package, and a file that exports names from one
// fails to load.

// exportSourcesCache maps the contents of a `.d.ts` file to the specifiers
// exportSources returns for it. collectTypesFiles runs on every load of a package,
// and the cache spares those loads a parse of each file.
var exportSourcesCache = struct {
	mu      sync.Mutex
	entries map[string][]string
}{entries: map[string][]string{}}

// exportSources returns the relative specifiers whose files the export statements
// in contents read from, each once. A file that does not parse has none, and the
// parse that reads its declarations reports the error. path names the file in the
// parser's source.
func exportSources(path, contents string) []string {
	exportSourcesCache.mu.Lock()
	specifiers, held := exportSourcesCache.entries[contents]
	exportSourcesCache.mu.Unlock()
	if held {
		return specifiers
	}
	specifiers = parseExportSources(path, contents)
	exportSourcesCache.mu.Lock()
	exportSourcesCache.entries[contents] = specifiers
	exportSourcesCache.mu.Unlock()
	return specifiers
}

// parseExportSources is exportSources without the cache.
func parseExportSources(path, contents string) []string {
	module, errs := dts_parser.NewDtsParser(&ast.Source{Path: path, Contents: contents}).ParseModule()
	if len(errs) > 0 {
		return nil
	}
	classification := dts_parser.ClassifyDTSFile(module)
	var specifiers []string
	seen := set.NewSet[string]()
	add := func(specifier string) {
		if isRelativeSpecifier(specifier) && !seen.Contains(specifier) {
			seen.Add(specifier)
			specifiers = append(specifiers, specifier)
		}
	}
	declared := dtsDeclaredNames(classification.PackageDecls)
	for _, stmt := range classification.NamedExports {
		if stmt.From != "" {
			add(stmt.From)
			continue
		}
		for _, spec := range stmt.Specifiers {
			if declared.Contains(spec.Local.Name) {
				continue
			}
			if imp, _ := importBinding(classification, spec.Local.Name); imp != nil {
				add(imp.From)
			}
		}
	}
	for _, stmt := range classification.ExportAllStmts {
		add(stmt.From)
	}
	return specifiers
}

// isRelativeSpecifier reports whether specifier names a file of the same package,
// as `./types` and `../shared` do.
func isRelativeSpecifier(specifier string) bool {
	return specifier == "." || specifier == ".." ||
		strings.HasPrefix(specifier, "./") || strings.HasPrefix(specifier, "../")
}

// resolveRelativeTypesFile returns the `.d.ts` file a relative specifier written in
// a file in dir names, or "" when there is none. It tries these paths in order,
// where base is specifier joined to dir:
//
//  1. base with a `.js`, `.mjs` or `.cjs` extension replaced by `.d.ts`, `.d.mts`
//     or `.d.cts`;
//  2. base itself, when it ends in `.ts`;
//  3. base with `.d.ts` added;
//  4. `index.d.ts` in the directory base names.
//
// A specifier that names a directory outright, such as `..` or `./sub/`, tries only
// the last.
func resolveRelativeTypesFile(dir, specifier string) string {
	base := filepath.Join(dir, filepath.FromSlash(specifier))
	var candidates []string
	last := filepath.Base(filepath.FromSlash(specifier))
	if last != "." && last != ".." && !strings.HasSuffix(specifier, "/") {
		for _, ext := range [][2]string{{".js", ".d.ts"}, {".mjs", ".d.mts"}, {".cjs", ".d.cts"}} {
			if trimmed, found := strings.CutSuffix(base, ext[0]); found {
				candidates = append(candidates, trimmed+ext[1])
			}
		}
		if strings.HasSuffix(base, ".ts") {
			candidates = append(candidates, base)
		}
		candidates = append(candidates, base+".d.ts")
	}
	candidates = append(candidates, filepath.Join(base, "index.d.ts"))
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// declName returns the name stmt declares, and false for a statement that declares
// no name.
func declName(stmt dts_parser.Statement) (string, bool) {
	var name *dts_parser.Ident
	switch decl := stmt.(type) {
	case *dts_parser.VarDecl:
		name = decl.Name
	case *dts_parser.FuncDecl:
		name = decl.Name
	case *dts_parser.ClassDecl:
		name = decl.Name
	case *dts_parser.InterfaceDecl:
		name = decl.Name
	case *dts_parser.TypeDecl:
		name = decl.Name
	case *dts_parser.EnumDecl:
		name = decl.Name
	case *dts_parser.NamespaceDecl:
		name = decl.Name
	}
	if name == nil {
		return "", false
	}
	return name.Name, true
}

// dtsDeclaredNames returns the names stmts declare.
func dtsDeclaredNames(stmts []dts_parser.Statement) set.Set[string] {
	names := set.NewSet[string]()
	for _, stmt := range stmts {
		if name, ok := declName(stmt); ok {
			names.Add(name)
		}
	}
	return names
}

// renamedDecl returns a copy of stmt that declares name. The copy shares every node
// below its name with stmt. stmt is one of the statements declName names.
func renamedDecl(stmt dts_parser.Statement, name string) dts_parser.Statement {
	switch decl := stmt.(type) {
	case *dts_parser.VarDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.FuncDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.ClassDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.InterfaceDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.TypeDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.EnumDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	case *dts_parser.NamespaceDecl:
		renamed := *decl
		renamed.Name = dts_parser.NewIdent(name, decl.Name.Span())
		return &renamed
	}
	panic(fmt.Sprintf("renamedDecl: %T declares no name", stmt))
}

// importBinding returns the import of classification that binds name, and the
// specifier that binds it when the import binds it by a named import. It returns
// nil and nil when no import binds name.
func importBinding(classification *dts_parser.FileClassification, name string) (*dts_parser.ImportDecl, *dts_parser.ImportSpecifier) {
	for _, imp := range classification.Imports {
		for _, spec := range imp.NamedImports {
			if spec.Local.Name == name {
				return imp, spec
			}
		}
		if imp.DefaultImport != nil && imp.DefaultImport.Name == name {
			return imp, nil
		}
		if imp.NamespaceAs != nil && imp.NamespaceAs.Name == name {
			return imp, nil
		}
	}
	return nil, nil
}

// exportOrigin is the declaration an exported name denotes: the declarations
// named local in files[file].
type exportOrigin struct {
	file  int
	local string
}

// exportResolver computes the names each file of a package exports.
type exportResolver struct {
	files           []typesFile
	classifications []*dts_parser.FileClassification
	// index maps a file's path to its position in files.
	index map[string]int
	// declared holds the names each file declares, by position in files.
	declared []set.Set[string]
	// exports holds each file's finished export map, by position in files.
	exports map[int]map[string]exportOrigin
	// building holds the unfinished export map of each file whose map is being
	// computed further up the call chain.
	building map[int]map[string]exportOrigin
	// reached holds every file an export map was computed for.
	reached set.Set[int]
}

// exportsOf returns the names files[i] exports and the declaration each denotes. A
// default export is keyed `default`. It also reports whether the map is partial. A
// map is partial when computing it reached a file further up the call chain, as a
// cycle of `export *` statements does. That file contributes only the names its
// unfinished map holds, and a partial map is not kept.
func (r *exportResolver) exportsOf(i int) (map[string]exportOrigin, bool, error) {
	if exports, held := r.exports[i]; held {
		return exports, false, nil
	}
	if unfinished, held := r.building[i]; held {
		return unfinished, true, nil
	}
	path := r.files[i].path
	classification := r.classifications[i]
	exports := map[string]exportOrigin{}
	r.building[i] = exports
	defer delete(r.building, i)
	r.reached.Add(i)
	partial := false
	// lookup returns the declaration that the file named by specifier exports as
	// name.
	lookup := func(specifier, name string) (exportOrigin, error) {
		target, err := r.target(i, specifier)
		if err != nil {
			return exportOrigin{}, err
		}
		targetExports, targetPartial, err := r.exportsOf(target)
		if err != nil {
			return exportOrigin{}, err
		}
		partial = partial || targetPartial
		origin, ok := targetExports[name]
		if !ok {
			return exportOrigin{}, fmt.Errorf("%s exports %q from %s, which does not export it",
				path, name, r.files[target].path)
		}
		return origin, nil
	}

	// The file's own declarations go in first, so a file further down a cycle
	// reads them from the unfinished map.
	for _, stmt := range classification.PackageDecls {
		if decl, ok := stmt.(dts_parser.Decl); ok && decl.Export() {
			if name, ok := declName(stmt); ok {
				key := name
				if decl.Default() {
					key = "default"
				}
				exports[key] = exportOrigin{file: i, local: name}
			}
		}
	}
	for _, stmt := range classification.NamedExports {
		for _, spec := range stmt.Specifiers {
			var origin exportOrigin
			var err error
			switch {
			case stmt.From != "":
				origin, err = lookup(stmt.From, spec.Local.Name)
			case r.declared[i].Contains(spec.Local.Name):
				origin = exportOrigin{file: i, local: spec.Local.Name}
			default:
				origin, err = r.importedOrigin(i, spec.Local.Name, lookup)
			}
			if err != nil {
				return nil, false, err
			}
			exports[spec.Exported.Name] = origin
		}
	}
	// A name the file exports by a declaration or an `export { … }` takes precedence
	// over the same name from an `export *`, as it does in TypeScript.
	explicit := set.NewSet[string]()
	for name := range exports {
		explicit.Add(name)
	}
	for _, stmt := range classification.ExportAllStmts {
		if stmt.AsName != nil {
			return nil, false, fmt.Errorf("%s exports %s as %q, and exporting a file as a namespace is not supported",
				path, stmt.From, stmt.AsName.Name)
		}
		target, err := r.target(i, stmt.From)
		if err != nil {
			return nil, false, err
		}
		targetExports, targetPartial, err := r.exportsOf(target)
		if err != nil {
			return nil, false, err
		}
		partial = partial || targetPartial
		for name, origin := range targetExports {
			// `export *` leaves out the default export.
			if name != "default" && !explicit.Contains(name) {
				exports[name] = origin
			}
		}
	}
	if !partial {
		r.exports[i] = exports
	}
	return exports, partial, nil
}

// importedOrigin returns the declaration that a named import of files[i] binds to
// name. lookup finds a name another file exports.
func (r *exportResolver) importedOrigin(
	i int,
	name string,
	lookup func(specifier, name string) (exportOrigin, error),
) (exportOrigin, error) {
	path := r.files[i].path
	imp, spec := importBinding(r.classifications[i], name)
	switch {
	case imp == nil:
		return exportOrigin{}, fmt.Errorf("%s exports %q, which it neither declares nor imports", path, name)
	case spec == nil:
		return exportOrigin{}, fmt.Errorf("%s exports %q, which a default or namespace import binds, and exporting one is not supported",
			path, name)
	}
	return lookup(imp.From, spec.Imported.Name)
}

// target returns the position in files of the file that specifier, written in
// files[i], names.
func (r *exportResolver) target(i int, specifier string) (int, error) {
	path := r.files[i].path
	if !isRelativeSpecifier(specifier) {
		return 0, fmt.Errorf("%s exports names from %q, and exporting names from another package is not supported",
			path, specifier)
	}
	target, ok := r.index[r.files[i].modules[specifier]]
	if !ok {
		return 0, fmt.Errorf("%s exports names from %q, which names no .d.ts file", path, specifier)
	}
	return target, nil
}

// moduleFileDecls returns the declarations of a package whose entry point,
// files[0], has top-level exports. They are the declarations of the entry point
// and of every file its export statements reach, in the order of files. A
// declaration is exported exactly when the entry point exports its name. When the
// entry point exports a declaration under a name other than its own, the result
// also holds a copy of each declaration of that name, declared under the exported
// name.
//
// classifications[i] is the classification of files[i]. It fails when two of the
// files declare the same name, since the declarations all land in one scope.
func moduleFileDecls(files []typesFile, classifications []*dts_parser.FileClassification) ([]dts_parser.Statement, error) {
	r := &exportResolver{
		files:           files,
		classifications: classifications,
		index:           make(map[string]int, len(files)),
		declared:        make([]set.Set[string], len(files)),
		exports:         map[int]map[string]exportOrigin{},
		building:        map[int]map[string]exportOrigin{},
		reached:         set.NewSet[int](),
	}
	for i, file := range files {
		r.index[file.path] = i
		r.declared[i] = dtsDeclaredNames(classifications[i].PackageDecls)
	}
	surface, _, err := r.exportsOf(0)
	if err != nil {
		return nil, err
	}

	reached := r.reached.ToSlice()
	slices.Sort(reached)
	declaredBy := map[string]int{}
	for _, i := range reached {
		names := r.declared[i].ToSlice()
		slices.Sort(names)
		for _, name := range names {
			if other, held := declaredBy[name]; held {
				return nil, fmt.Errorf("%s and %s both declare %q, and a package's files cannot share a top-level name",
					files[other].path, files[i].path, name)
			}
			declaredBy[name] = i
		}
	}
	// A renamed export adds its copies to the same scope, so its name must not
	// collide with a declared one either.
	exportedBy := map[exportOrigin][]string{}
	for _, name := range slices.Sorted(maps.Keys(surface)) {
		origin := surface[name]
		if name == origin.local || name == "default" {
			continue
		}
		if other, held := declaredBy[name]; held {
			return nil, fmt.Errorf("%s exports %q, which %s also declares, and a package's files cannot share a top-level name",
				files[0].path, name, files[other].path)
		}
		exportedBy[origin] = append(exportedBy[origin], name)
	}

	// An Escalier module has no default export, so the entry point's default export
	// is exported under the name it is declared with.
	exported := set.NewSet[exportOrigin]()
	for name, origin := range surface {
		if name == origin.local || name == "default" {
			exported.Add(origin)
		}
	}
	var decls []dts_parser.Statement
	var renamed []dts_parser.Statement
	for _, i := range reached {
		for _, stmt := range classifications[i].PackageDecls {
			name, named := declName(stmt)
			if !named {
				// A statement that declares no name, such as `export = Foo`, says what
				// its own file exports. Only the entry point's says what the package
				// exports, so the other files' are dropped.
				if i == 0 {
					decls = append(decls, stmt)
				}
				continue
			}
			origin := exportOrigin{file: i, local: name}
			if decl, ok := stmt.(dts_parser.Decl); ok {
				decl.SetExport(exported.Contains(origin))
			}
			decls = append(decls, stmt)
			for _, alias := range exportedBy[origin] {
				copied := renamedDecl(stmt, alias)
				copied.(dts_parser.Decl).SetExport(true)
				renamed = append(renamed, copied)
			}
		}
	}
	return append(decls, renamed...), nil
}
