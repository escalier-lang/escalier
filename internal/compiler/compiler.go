package compiler

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/parser"
)

type CompilerOutput struct {
	Errors    []*parser.Error
	JS        string
	SourceMap string
}

func Compile(source parser.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	escMod, escErrors := p.Parse()

	if len(escErrors) > 0 {
		return CompilerOutput{
			JS:        "",
			SourceMap: "",
			Errors:    escErrors,
		}
	}

	builder := &codegen.Builder{}
	jsMod := builder.BuildModule(escMod)

	p2 := codegen.NewPrinter()
	p2.PrintModule(jsMod)

	output := p2.Output

	srcFile := "./" + filepath.Base(source.Path)
	outFile := strings.TrimSuffix(srcFile, filepath.Ext(srcFile)) + ".js"
	sourceMap := codegen.GenerateSourceMap(srcFile, source.Contents, jsMod, outFile)

	outmap := "./" + filepath.Base(source.Path) + ".map"
	output += "//# sourceMappingURL=" + outmap + "\n"

	return CompilerOutput{
		Errors:    escErrors,
		JS:        output,
		SourceMap: sourceMap,
	}
}
