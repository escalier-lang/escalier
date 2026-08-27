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
//	dts_to_esc partition [--cfg <cfg.json>] <lib-dir> <out-dir>
//	    Full pinned-lib partitioning path per §6 PR A: discover every
//	    lib.*.d.ts under <lib-dir>, parse each, route every top-level
//	    declaration through dts_to_esc.Route, and write the partitioned
//	    tree (std/*.esc, web/*.esc) under <out-dir>. <out-dir>/node/
//	    is scaffolded with a README explaining its reserved status per
//	    §6.1/§6.3; no `.esc` files are emitted there. The unmapped-
//	    symbol fail-safe aborts the run with the offending name +
//	    source file.
//
//	    With --cfg, the run also joins every std:* member it emits
//	    against the ECMA-262 effect facts derived from that control-flow
//	    graph, and reports the names present on one side only. See
//	    planning/ecma-262/implementation_plan.md §5.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
	"github.com/escalier-lang/escalier/internal/ecma262"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dts_to_esc:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n  dts_to_esc <path-to-d.ts>\n  dts_to_esc partition [--cfg <cfg.json>] <lib-dir> <out-dir>")
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

// partitionUsage is the one-line synopsis every partition-mode argument error
// ends with.
const partitionUsage = "usage: dts_to_esc partition [--cfg <cfg.json>] <lib-dir> <out-dir>"

func runPartition(args []string, stderr io.Writer) error {
	// The flag package reports its own errors, which main would then print a
	// second time. Discarding its output leaves one report per error.
	flags := flag.NewFlagSet("partition", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cfgPath := flags.String("cfg", "", "path to the ECMA-262 cfg.json; adds the §5 effect-fact join report")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, partitionUsage)
			flags.SetOutput(stderr)
			flags.PrintDefaults()
			return nil
		}
		return fmt.Errorf("%w\n%s", err, partitionUsage)
	}
	if flags.NArg() != 2 {
		return errors.New(partitionUsage)
	}
	libDir, outDir := flags.Arg(0), flags.Arg(1)

	// Derive the facts before any output is written, so a bad --cfg path
	// fails the run before it leaves a half-joined tree on disk.
	var join *ecma262.Join
	if *cfgPath != "" {
		cfg, err := ecma262.LoadCFG(*cfgPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", *cfgPath, err)
		}
		join = ecma262.NewJoin(ecma262.NewFacts(cfg))
	}

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

	mods, err := dts_to_esc.ConvertBuckets(result)
	if err != nil {
		return err
	}

	written, err := dts_to_esc.WriteConvertedTree(mods, outDir)
	if err != nil {
		return err
	}
	if err := dts_to_esc.ScaffoldNodeDir(outDir); err != nil {
		return err
	}

	fmt.Fprintf(stderr, "wrote %d packages under %s\n", len(written), outDir)
	if err := dts_to_esc.ReportPartition(result, stderr); err != nil {
		return err
	}
	if join == nil {
		return nil
	}
	// The join is informational. The spec and the TypeScript lib drift
	// independently, so a name on one side only is a gap to close rather than
	// a failed run.
	return ecma262.WriteJoinReport(join.Match(dts_to_esc.StdDeclarations(mods)), stderr)
}
