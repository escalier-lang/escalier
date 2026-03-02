package checker

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
	"github.com/stretchr/testify/assert"
)

func TestGetBindingPriority(t *testing.T) {
	tests := []struct {
		name     string
		key      dep_graph.BindingKey
		expected int
	}{
		{
			name:     "Constructor type has priority 0",
			key:      dep_graph.TypeBindingKey("SymbolConstructor"),
			expected: 0,
		},
		{
			name:     "Namespaced constructor type has priority 0",
			key:      dep_graph.TypeBindingKey("Intl.NumberFormatConstructor"),
			expected: 0,
		},
		{
			name:     "Value binding has priority 1",
			key:      dep_graph.ValueBindingKey("Symbol"),
			expected: 1,
		},
		{
			name:     "Namespaced value binding has priority 1",
			key:      dep_graph.ValueBindingKey("Intl.NumberFormat"),
			expected: 1,
		},
		{
			name:     "Instance type has priority 2",
			key:      dep_graph.TypeBindingKey("Symbol"),
			expected: 2,
		},
		{
			name:     "Namespaced instance type has priority 2",
			key:      dep_graph.TypeBindingKey("Intl.NumberFormat"),
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := getBindingPriority(tt.key)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestSortComponentBindings(t *testing.T) {
	t.Run("sorts Symbol/SymbolConstructor correctly", func(t *testing.T) {
		component := []dep_graph.BindingKey{
			dep_graph.TypeBindingKey("Symbol"),            // Instance type - priority 2
			dep_graph.ValueBindingKey("Symbol"),           // Value - priority 1
			dep_graph.TypeBindingKey("SymbolConstructor"), // Constructor - priority 0
		}

		sorted := sortComponentBindings(component)

		assert.Equal(t, dep_graph.TypeBindingKey("SymbolConstructor"), sorted[0],
			"SymbolConstructor should be first")
		assert.Equal(t, dep_graph.ValueBindingKey("Symbol"), sorted[1],
			"Symbol value should be second")
		assert.Equal(t, dep_graph.TypeBindingKey("Symbol"), sorted[2],
			"Symbol type should be last")
	})

	t.Run("preserves order for non-constructor types", func(t *testing.T) {
		component := []dep_graph.BindingKey{
			dep_graph.TypeBindingKey("Foo"),
			dep_graph.TypeBindingKey("Bar"),
			dep_graph.ValueBindingKey("baz"),
		}

		sorted := sortComponentBindings(component)

		// Values come before non-constructor types
		assert.Equal(t, dep_graph.ValueBindingKey("baz"), sorted[0])
		// Types maintain their relative order (stable sort)
		assert.Equal(t, dep_graph.TypeBindingKey("Foo"), sorted[1])
		assert.Equal(t, dep_graph.TypeBindingKey("Bar"), sorted[2])
	})

	t.Run("handles empty component", func(t *testing.T) {
		component := []dep_graph.BindingKey{}
		sorted := sortComponentBindings(component)
		assert.Empty(t, sorted)
	})

	t.Run("handles single element", func(t *testing.T) {
		component := []dep_graph.BindingKey{
			dep_graph.TypeBindingKey("Foo"),
		}
		sorted := sortComponentBindings(component)
		assert.Equal(t, component, sorted)
	})

	t.Run("handles multiple constructors", func(t *testing.T) {
		component := []dep_graph.BindingKey{
			dep_graph.TypeBindingKey("Array"),
			dep_graph.TypeBindingKey("ArrayConstructor"),
			dep_graph.ValueBindingKey("Array"),
			dep_graph.TypeBindingKey("Map"),
			dep_graph.TypeBindingKey("MapConstructor"),
			dep_graph.ValueBindingKey("Map"),
		}

		sorted := sortComponentBindings(component)

		// All constructors first (priority 0)
		assert.True(t, sorted[0] == dep_graph.TypeBindingKey("ArrayConstructor") ||
			sorted[0] == dep_graph.TypeBindingKey("MapConstructor"))
		assert.True(t, sorted[1] == dep_graph.TypeBindingKey("ArrayConstructor") ||
			sorted[1] == dep_graph.TypeBindingKey("MapConstructor"))

		// Then values (priority 1)
		assert.True(t, sorted[2] == dep_graph.ValueBindingKey("Array") ||
			sorted[2] == dep_graph.ValueBindingKey("Map"))
		assert.True(t, sorted[3] == dep_graph.ValueBindingKey("Array") ||
			sorted[3] == dep_graph.ValueBindingKey("Map"))

		// Then instance types (priority 2)
		assert.True(t, sorted[4] == dep_graph.TypeBindingKey("Array") ||
			sorted[4] == dep_graph.TypeBindingKey("Map"))
		assert.True(t, sorted[5] == dep_graph.TypeBindingKey("Array") ||
			sorted[5] == dep_graph.TypeBindingKey("Map"))
	})
}
