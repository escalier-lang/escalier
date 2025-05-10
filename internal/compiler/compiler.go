package compiler

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/parser"
)

type CompilerOutput struct {
	ParseErrors []*parser.Error
	TypeErrors  []*checker.Error
	JS          string
	SourceMap   string
	DTS         string
}

func Compile(source parser.Source) CompilerOutput {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	inMod, parseErrors := p.ParseScript()

	c := checker.NewChecker()
	inferCtx := checker.Context{
		Filename:   "input.esc",
		Scope:      checker.Prelude(),
		IsAsync:    false,
		IsPatMatch: false,
	}
	bindings, typeErrors := c.InferScript(inferCtx, inMod)

	if len(parseErrors) > 0 {
		return CompilerOutput{
			JS:          "",
			DTS:         "",
			SourceMap:   "",
			ParseErrors: parseErrors,
			TypeErrors:  typeErrors,
		}
	}

	builder := &codegen.Builder{}
	jsMod := builder.BuildScript(inMod)
	dtsMod := builder.BuildDefinitions(inMod, bindings)

	printer := codegen.NewPrinter()
	jsOutput := printer.PrintModule(jsMod)

	srcFile := "./" + filepath.Base(source.Path)

	jsFile := strings.TrimSuffix(srcFile, filepath.Ext(srcFile)) + ".js"
	sourceMap := codegen.GenerateSourceMap(srcFile, source.Contents, jsMod, jsFile)

	outmap := "./" + filepath.Base(source.Path) + ".map"
	jsOutput += "//# sourceMappingURL=" + outmap + "\n"

	printer = codegen.NewPrinter()
	dtsOutput := printer.PrintModule(dtsMod)

	return CompilerOutput{
		ParseErrors: parseErrors,
		TypeErrors:  typeErrors,
		JS:          jsOutput,
		SourceMap:   sourceMap,
		DTS:         dtsOutput,
	}
}
