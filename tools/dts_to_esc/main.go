// Command dts_to_esc converts TypeScript `.d.ts` files into Escalier
// `.esc` source. Two modes:
//
//	dts_to_esc <path-to-d.ts>
//	    Single-file MVP path: convert one .d.ts to a standalone .esc
//	    written to stdout. Per planning/builtins/implementation_plan.md
//	    §5: AST-to-AST translation only (no checker), with trio
//	    recognition, namespace flattening, and `@js(...)` decorator
//	    emission.
//
//	dts_to_esc partition <lib-dir> <out-dir>
//	    Full pinned-lib partitioning path per §6 PR A: discover every
//	    lib.*.d.ts under <lib-dir>, parse each, route every top-level
//	    declaration through dts_to_esc.Route, and write the partitioned
//	    tree (std/*.esc, web/*.esc) under <out-dir>. <out-dir>/node/
//	    is scaffolded with a README explaining its reserved status per
//	    §6.1/§6.3; no `.esc` files are emitted there. The unmapped-
//	    symbol fail-safe aborts the run with the offending name +
//	    source file.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dts_to_esc:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n  dts_to_esc <path-to-d.ts>\n  dts_to_esc partition <lib-dir> <out-dir>")
	}
	if args[0] == "partition" {
		return runPartition(args[1:], stderr)
	}
	return runSingleFile(args, stdout)
}

func runSingleFile(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: dts_to_esc <path-to-d.ts>")
	}
	path := args[0]
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	source := &ast.Source{Path: path, Contents: string(contents)}
	parser := dts_parser.NewDtsParser(source)
	dtsModule, errs := parser.ParseModule()
	if len(errs) > 0 {
		return fmt.Errorf("parse errors in %s: %v", path, errs)
	}
	standalone, err := dts_to_esc.ConvertToStandaloneModule(dtsModule)
	if err != nil {
		return fmt.Errorf("converting %s: %w", path, err)
	}
	return dts_to_esc.WriteStandaloneModule(standalone, out)
}

func runPartition(args []string, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc partition <lib-dir> <out-dir>")
	}
	libDir, outDir := args[0], args[1]

	basenames, err := dts_to_esc.DiscoverLibFiles(libDir)
	if err != nil {
		return err
	}
	if len(basenames) == 0 {
		return fmt.Errorf("no lib.*.d.ts files found under %s", libDir)
	}
	fmt.Fprintf(stderr, "discovered %d lib files\n", len(basenames))

	inputs, err := dts_to_esc.ParseLibFiles(libDir, basenames)
	if err != nil {
		return err
	}

	result, err := dts_to_esc.PartitionLib(inputs)
	if err != nil {
		return err
	}

	written, err := dts_to_esc.WritePartitionedTree(result, outDir)
	if err != nil {
		return err
	}
	if err := dts_to_esc.ScaffoldNodeDir(outDir); err != nil {
		return err
	}

	fmt.Fprintf(stderr, "wrote %d packages under %s\n", len(written), outDir)
	return dts_to_esc.ReportPartition(result, stderr)
}
