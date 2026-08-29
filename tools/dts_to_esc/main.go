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
//
//	dts_to_esc check <lib-dir> <esc-dir>
//	    Read-only verification per §6.4: convert the pinned lib set and
//	    report every `.d.ts` declaration and member with no counterpart
//	    in the committed `.esc` tree under <esc-dir>. Exits non-zero
//	    when anything is missing. Signature and property-type drift are
//	    not checked yet — see internal/dts_to_esc/rerun.go.
//
//	dts_to_esc regenerate <lib-dir> <esc-dir>
//	    Additive write per §6.4: add the declarations and members
//	    `check` reports to the committed tree, leaving every existing
//	    declaration byte-for-byte intact so hand-edits survive.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

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

const usage = `usage:
  dts_to_esc <path-to-d.ts>
  dts_to_esc partition [--cfg <cfg.json>] <lib-dir> <out-dir>
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
	report, err := dts_to_esc.CheckPartition(result, args[1])
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
	report, err := dts_to_esc.RegeneratePartition(result, args[1])
	if err != nil {
		return err
	}
	return report.Write(stdout)
}

// partitionLibDir discovers, parses, and routes every lib.*.d.ts under
// libDir. Shared by the partition, check, and regenerate subcommands.
func partitionLibDir(libDir string, stderr io.Writer) (*dts_to_esc.PartitionResult, error) {
	basenames, err := dts_to_esc.DiscoverLibFiles(libDir)
	if err != nil {
		return nil, err
	}
	if len(basenames) == 0 {
		return nil, fmt.Errorf("no lib.*.d.ts files found under %s", libDir)
	}
	fmt.Fprintf(stderr, "discovered %d lib files\n", len(basenames))

	inputs, err := dts_to_esc.ParseLibFiles(libDir, basenames)
	if err != nil {
		return nil, err
	}
	return dts_to_esc.PartitionLib(inputs)
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
	cfgPath := flags.String("cfg", "", "path to the ECMA-262 cfg.json; adds the §4.4 curation and §5 effect-fact join reports")
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
	var facts *ecma262.Facts
	var join *ecma262.Join
	if *cfgPath != "" {
		cfg, err := ecma262.LoadCFG(*cfgPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", *cfgPath, err)
		}
		facts = ecma262.NewFacts(cfg)
		// A fact with a hole in it would leave the converter to guess a
		// determination nobody answered, and the receiver is auto-applied, so
		// the guess would be silent. Refuse the run instead.
		if holes := facts.Incomplete(); len(holes) > 0 {
			return fmt.Errorf("%s leaves determinations unanswered:\n  %s",
				*cfgPath, strings.Join(holes, "\n  "))
		}
		join = ecma262.NewJoin(facts)
	}

	result, err := partitionLibDir(libDir, stderr)
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
	// Both reports are informational. A curated entry the analysis caught up
	// with is an entry to delete, and the spec and the TypeScript lib drift
	// independently, so a name on one side only is a gap to close. Neither is a
	// failed run.
	if err := ecma262.WriteCurationReport(facts.Curation(), stderr); err != nil {
		return err
	}
	return ecma262.WriteJoinReport(join.Match(dts_to_esc.StdDeclarations(mods)), stderr)
}
