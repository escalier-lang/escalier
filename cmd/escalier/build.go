package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/parser"
)

func build(stdout io.Writer, stderr io.Writer, files []string) {
	for _, file := range files {
		fmt.Fprintln(stdout, "building", file)

		// check that file has .esc extension
		if path.Ext(file) != ".esc" {
			fmt.Fprintln(stdout, "file does not have .esc extension")
			continue
		}

		// check if file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			fmt.Fprintln(stdout, "file does not exist")
			continue
		}

		// open the file
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintln(stdout, "failed to open file")
			continue
		}
		defer f.Close()

		// read file content
		bytes, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stdout, "failed to read file content")
			continue
		}

		source := parser.Source{
			Path:     file,
			Contents: string(bytes),
		}

		output := compiler.Compile(source)

		for _, err := range output.Errors {
			fmt.Fprintln(stderr, err)
		}

		// create js file
		outfile := strings.TrimSuffix(file, path.Ext(file)) + ".js"
		out, err := os.Create(outfile)
		if err != nil {
			fmt.Fprintln(out, "failed to create output file")
			continue
		}

		// write js output to file
		_, err = out.WriteString(output.JS)
		if err != nil {
			fmt.Fprintln(out, "failed to write output to file")
			continue
		}

		// create sourcemap file
		mapFile := file + ".map"
		mapOut, err := os.Create(mapFile)
		if err != nil {
			fmt.Fprintln(out, "failed to create map file")
			continue
		}

		// write sourcemap output to file
		_, err = mapOut.WriteString(output.SourceMap)
		if err != nil {
			fmt.Fprintln(out, "failed to write source map to file")
			continue
		}
	}
}
