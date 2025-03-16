package compiler

import (
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/parser"
)

type CompilerOutput struct {
	Errors    []*parser.Error
	JS        string
	SourceMap string
}

func Compile(source parser.Source) CompilerOutput {
	p1 := parser.NewParser(source)
	escMod := p1.ParseModule()
	jsMod := codegen.TransformModule(escMod)

	p2 := codegen.NewPrinter()
	p2.PrintModule(jsMod)

	output := p2.Output

	outfile := strings.TrimSuffix(source.Path, filepath.Ext(source.Path)) + ".js"
	sourceMap := codegen.GenerateSourceMap(source, jsMod, outfile)

	outmap := source.Path + ".map"
	output += "//# sourceMappingURL=" + outmap + "\n"

	return CompilerOutput{
		Errors:    p1.Errors,
		JS:        output,
		SourceMap: sourceMap,
	}
}
