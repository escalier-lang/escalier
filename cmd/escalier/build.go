package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
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

		source := &ast.Source{
			Path:     file,
			Contents: string(bytes),
		}

		output := compiler.Compile(source)

		lines := strings.Split(source.Contents, "\n")

		for _, err := range output.ParseErrors {
			fmt.Fprintln(stderr, err)
		}

		// TODO: sort by err.Location()
		for _, err := range output.TypeErrors {
			if err.Span().Start.String() == "0:0" {
				message := fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message())
				fmt.Fprintln(stderr, message)
				continue
			}

			message := fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message())
			message += "\n"
			lineNum := strconv.Itoa(err.Span().Start.Line) + ":"
			message += fmt.Sprintf("%-4s", lineNum)
			message += lines[err.Span().Start.Line-1] + "\n"
			for range 4 + err.Span().Start.Column - 1 {
				message += " "
			}
			for range err.Span().End.Column - err.Span().Start.Column {
				message += "^"
			}
			message += "\n"

			fmt.Fprintln(stderr, message)
		}

		// create dist/ directory if it doesn't exist
		if _, err := os.Stat("dist"); os.IsNotExist(err) {
			err := os.Mkdir("dist", 0755)
			if err != nil {
				fmt.Fprintln(stderr, "failed to create dist directory")
				continue
			}
		}

		// create .js file
		jsFile := filepath.Join("dist", "index.js")
		jsOut, err := os.Create(jsFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create .js file")
			continue
		}

		// write .js output to file
		_, err = jsOut.WriteString(output.JS)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write .js to file")
			continue
		}

		// create .d.ts file
		defFile := filepath.Join("dist", "index.d.ts")
		defOut, err := os.Create(defFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create .d.ts file")
			continue
		}

		// write .d.ts output to file
		_, err = defOut.WriteString(output.DTS)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write .d.ts to file")
			continue
		}

		// create sourcemap file
		mapFile := filepath.Join("dist", "index.esc.map")
		mapOut, err := os.Create(mapFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create map file")
			continue
		}

		// write sourcemap output to file
		_, err = mapOut.WriteString(output.SourceMap)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write source map to file")
			continue
		}
	}
}
