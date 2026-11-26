package compiler

import (
	"fmt"

	"github.com/escalier-lang/escalier/internal/ast"
)

// InternalError is the value CompilePackage and CompileScript panic with when a phase
// of the compile panics. It names the file and phase the panic was raised in.
type InternalError struct {
	// File is the source file the phase was compiling. A phase that compiles every
	// lib/ file at once names `lib/` unless the library has a single file.
	File string
	// Phase is the work that panicked, written to read before File, such as
	// `checking` or `generating JavaScript for`.
	Phase string
	// Value is the value the phase panicked with.
	Value any
}

func (e *InternalError) Error() string {
	return fmt.Sprintf("the Escalier compiler crashed while %s %s: %v", e.Phase, e.File, e.Value)
}

// compileStep is the file and phase a compile is running.
type compileStep struct {
	file  string
	phase string
}

// enter records that the compile is running phase on file.
func (s *compileStep) enter(file, phase string) {
	s.file = file
	s.phase = phase
}

// wrapPanic re-raises a panic as an *InternalError naming s's file and phase. It
// re-raises an *InternalError unchanged, so the innermost step names the phase. It
// does nothing when there is no panic. A caller must defer it directly, because recover
// stops a panic only when the deferred function itself calls it.
func (s *compileStep) wrapPanic() {
	value := recover()
	if value == nil {
		return
	}
	if _, wrapped := value.(*InternalError); wrapped {
		panic(value)
	}
	panic(&InternalError{File: s.file, Phase: s.phase, Value: value})
}

// emitError reports that producing a compile unit's output failed.
type emitError struct {
	span ast.Span
	err  error
}

func (e *emitError) Span() ast.Span { return e.span }

func (e *emitError) Message() string {
	return "cannot emit the output: " + e.err.Error()
}

// libUnitName returns the name an InternalError gives the lib/ compile unit made of
// sources.
func libUnitName(sources []*ast.Source) string {
	if len(sources) == 1 {
		return sources[0].Path
	}
	return "lib/"
}
