package checker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	. "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/assert"
)

func TestSimpleThrowExpression(t *testing.T) {
	input := `val testFunc = fn () -> undefined {
		throw "error message"
	}`

	source := &ast.Source{
		ID:       0,
		Path:     "input.esc",
		Contents: input,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p := parser.NewParser(ctx, source)
	script, parseErrors := p.ParseScript()

	assert.Empty(t, parseErrors, "Expected no parse errors")
	assert.NotNil(t, script, "Expected script to be parsed successfully")

	checker := NewChecker()
	scope, errors := checker.InferScript(
		Context{Scope: NewScope(), IsAsync: false, IsPatMatch: false}, script)

	fmt.Printf("Errors: %v\n", errors)
	if len(errors) > 0 {
		for i, err := range errors {
			fmt.Printf("Error[%d]: %s\n", i, err)
		}
	}

	assert.NotNil(t, scope, "Expected scope to be created")

	// Get the function binding
	binding := scope.getValue("testFunc")
	assert.NotNil(t, binding, "Expected testFunc to be defined")

	fmt.Printf("Binding type: %T\n", binding.Type)
	fmt.Printf("Pruned type: %T\n", Prune(binding.Type))

	// Prune the type to resolve any type variables
	funcType, ok := Prune(binding.Type).(*FuncType)
	if !ok {
		t.Fatalf("Expected testFunc to be a function type, got %T", Prune(binding.Type))
	}

	fmt.Printf("Function throws type: %s\n", funcType.Throws.String())
}
