// Command write_signatures writes the signature the checker infers for each function and
// method that leaves part of it out, in every .esc file of the packages it is given. It
// applies the edits the language server's "Write the inferred signature" code action makes.
//
// Usage:
//
//	go run ./tools/write_signatures [-stdlib-dir dir] <package-dir>...
//
// A package directory holds a package.json and lib/ or bin/ sources, as a fixture does. The
// standard library is read from -stdlib-dir, which defaults to internal/interop/data so the
// command runs from the repository root as the fixture tests do.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/sigedit"
)

func main() {
	stdlibDir := flag.String("stdlib-dir", filepath.Join("internal", "interop", "data"),
		"directory containing the stdlib `.esc` files (std/, web/, node/)")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: write_signatures [-stdlib-dir dir] <package-dir>...")
		os.Exit(2)
	}
	// The checker reads the standard library's location from the environment.
	if err := os.Setenv("ESCALIER_STDLIB_DIR", *stdlibDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := false
	for _, dir := range flag.Args() {
		if err := writePackage(dir); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// writePackage checks the package in dir and rewrites each of its source files that has a
// function whose inferred signature can be written.
func writePackage(dir string) error {
	sources, err := readSources(dir)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	config, err := compiler.ReadPackageConfig(dir)
	if err != nil {
		return err
	}
	co := compiler.CheckPackage(sources, config)
	for _, d := range co.TypeErrors {
		// A package the checker cannot load the standard library for would get degraded
		// signatures, so it is left as it is.
		if strings.HasPrefix(d.Message(), "cannot find the standard library") {
			return fmt.Errorf("%s", d.Message())
		}
	}
	for _, src := range sources {
		info := co.InfoFor(src.ID)
		if info == nil {
			continue
		}
		var edits []sigedit.Edit
		for _, target := range sigedit.Targets(info, declsOf(&co, src.ID), src.ID) {
			targetEdits, ok := target.Edits(src.Contents)
			if !ok {
				fmt.Fprintf(os.Stderr, "%s: skipped %s, whose signature cannot be written\n",
					ast.SpanString(src, target.SignatureSpan), nameOf(target))
				continue
			}
			edits = append(edits, targetEdits...)
		}
		if len(edits) == 0 {
			continue
		}
		path := filepath.Join(dir, filepath.FromSlash(src.Path))
		if err := os.WriteFile(path, []byte(sigedit.Apply(src.Contents, edits)), 0o644); err != nil {
			return err
		}
		fmt.Printf("%s: wrote %d edits\n", path, len(edits))
	}
	return nil
}

// readSources reads every .esc file under dir's lib/ and bin/ directories, each with a path
// relative to dir and its own source id.
func readSources(dir string) ([]*ast.Source, error) {
	var paths []string
	for _, sub := range []string{"lib", "bin"} {
		root := filepath.Join(dir, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".esc") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	sources := make([]*ast.Source, 0, len(paths))
	for i, path := range paths {
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, &ast.Source{ID: i, Path: filepath.ToSlash(rel), Contents: string(contents)})
	}
	return sources, nil
}

// declsOf returns the declarations of the file sourceID names: a script's top-level
// declarations, or the module declarations written in a lib/ file.
func declsOf(co *compiler.CheckOutput, sourceID int) []ast.Decl {
	var decls []ast.Decl
	if script, ok := co.Scripts[sourceID]; ok {
		for _, stmt := range script.Stmts {
			if ds, ok := stmt.(*ast.DeclStmt); ok {
				decls = append(decls, ds.Decl)
			}
		}
		return decls
	}
	if co.Module == nil {
		return nil
	}
	for _, ns := range co.Module.Namespaces.Values() {
		for _, d := range ns.Decls {
			if d.Span().SourceID == sourceID {
				decls = append(decls, d)
			}
		}
	}
	return decls
}

// nameOf returns the name target is declared under, for a message.
func nameOf(target sigedit.Target) string {
	if ident, ok := target.Name.(*ast.Ident); ok {
		return "'" + ident.Name + "'"
	}
	return "a method"
}
