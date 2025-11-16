package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	buildCmd := flag.NewFlagSet("build", flag.ExitOnError)
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
