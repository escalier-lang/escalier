package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	buildCmd := flag.NewFlagSet("build", flag.ExitOnError)
	// `--stdlib-dir` is the highest-precedence channel for locating the
	// stdlib `.esc` files per planning/builtins §2.2a. When supplied,
	// it overrides ESCALIER_STDLIB_DIR and the executable-relative
	// discovery paths. We propagate it by setting the env var so the
	// checker's lazy `interop.StdlibDir("")` call picks it up without
	// further plumbing — the resolution order still ends up
	// flag > env > sibling > repo-relative.
	buildStdlibDir := buildCmd.String("stdlib-dir", "", "directory containing the stdlib `.esc` files (std/, dom/, node/)")
	formatCmd := flag.NewFlagSet("format", flag.ExitOnError)

	if len(os.Args) < 2 {
		fmt.Println("expected 'build' or 'format subcommands")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "build":
		err := buildCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println("failed to parse build command")
			os.Exit(1)
		}
		if *buildStdlibDir != "" {
			_ = os.Setenv("ESCALIER_STDLIB_DIR", *buildStdlibDir)
		}
		build(os.Stdout, os.Stderr, buildCmd.Args())
	case "format":
		err := formatCmd.Parse(os.Args[2:])
		if err != nil {
			fmt.Println("failed to parse format command")
			os.Exit(1)
		}
		fmt.Println("subcommand 'format'")
		fmt.Println("  tail:", formatCmd.Args())
		format(formatCmd.Args())
	default:
		fmt.Println("expected 'build' or 'format' subcommands")
	}
}
