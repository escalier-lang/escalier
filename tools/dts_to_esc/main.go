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
//	    declaration through interop.Route, and write the partitioned
//	    tree (std/*.esc, web/*.esc) under <out-dir>. <out-dir>/node/
//	    is scaffolded with a README explaining its reserved status per
//	    §6.1/§6.3; no `.esc` files are emitted there. The unmapped-
//	    symbol fail-safe aborts the run with the offending name +
//	    source file.
//
//	dts_to_esc check <lib-dir> <esc-dir>
//	    Read-only verification per §6.4: convert the pinned lib set and
//	    report every `.d.ts` declaration and member with no counterpart
//	    in the committed `.esc` tree under <esc-dir>. Exits non-zero
//	    when anything is missing. Signature and property-type drift are
//	    not checked yet — see internal/interop/rerun.go.
//
//	dts_to_esc regenerate <lib-dir> <esc-dir>
//	    Additive write per §6.4: add the declarations and members
//	    `check` reports to the committed tree, leaving every existing
//	    declaration byte-for-byte intact so hand-edits survive.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/interop"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dts_to_esc:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  dts_to_esc <path-to-d.ts>
  dts_to_esc partition <lib-dir> <out-dir>
  dts_to_esc check <lib-dir> <esc-dir>
  dts_to_esc regenerate <lib-dir> <esc-dir>`

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "partition":
		return runPartition(args[1:], stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "regenerate":
		return runRegenerate(args[1:], stdout, stderr)
	}
	return runSingleFile(args, stdout)
}

// errCheckFailed is returned when `check` finds a missing declaration
// or member. main turns any error into a non-zero exit, which is what
// CI keys off; the message stays short because the report itself has
// already been printed.
var errCheckFailed = errors.New("committed .esc tree is out of date with the .d.ts inputs")

func runCheck(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc check <lib-dir> <esc-dir>")
	}
	result, err := partitionLibDir(args[0], stderr)
	if err != nil {
		return err
	}
	report, err := interop.CheckPartition(result, args[1])
	if err != nil {
		return err
	}
	if err := report.Write(stdout); err != nil {
		return err
	}
	if report.Failed() {
		return errCheckFailed
	}
	return nil
}

func runRegenerate(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc regenerate <lib-dir> <esc-dir>")
	}
	result, err := partitionLibDir(args[0], stderr)
	if err != nil {
		return err
	}
	report, err := interop.RegeneratePartition(result, args[1])
	if err != nil {
		return err
	}
	return report.Write(stdout)
}

// partitionLibDir discovers, parses, and routes every lib.*.d.ts under
// libDir. Shared by the partition, check, and regenerate subcommands.
func partitionLibDir(libDir string, stderr io.Writer) (*interop.PartitionResult, error) {
	basenames, err := interop.DiscoverLibFiles(libDir)
	if err != nil {
		return nil, err
	}
	if len(basenames) == 0 {
		return nil, fmt.Errorf("no lib.*.d.ts files found under %s", libDir)
	}
	fmt.Fprintf(stderr, "discovered %d lib files\n", len(basenames))

	inputs, err := interop.ParseLibFiles(libDir, basenames)
	if err != nil {
		return nil, err
	}
	return interop.PartitionLib(inputs)
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
	standalone, err := interop.ConvertToStandaloneModule(dtsModule)
	if err != nil {
		return fmt.Errorf("converting %s: %w", path, err)
	}
	return interop.WriteStandaloneModule(standalone, out)
}

func runPartition(args []string, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc partition <lib-dir> <out-dir>")
	}
	libDir, outDir := args[0], args[1]

	result, err := partitionLibDir(libDir, stderr)
	if err != nil {
		return err
	}

	written, err := interop.WritePartitionedTree(result, outDir)
	if err != nil {
		return err
	}
	if err := interop.ScaffoldNodeDir(outDir); err != nil {
		return err
	}

	fmt.Fprintf(stderr, "wrote %d packages under %s\n", len(written), outDir)
	return interop.ReportPartition(result, stderr)
}
