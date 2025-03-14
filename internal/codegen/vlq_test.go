package codegen

import (
	"testing"
)

func TestVLQEncode(t *testing.T) {
	// Test cases with expected results
	tests := []struct {
		value    int
		expected string
	}{
		{0, "A"}, // edge case: encoding zero (should be empty string)
		{17, "iB"},
		{-17, "jB"},
	}

	for _, tt := range tests {
		t.Run("VLQEncodeTest", func(t *testing.T) {
			// Encode the value using VLQ encoding
			encoded := VLQEncode(tt.value)

			// Compare the expected value with the actual encoded result
			if encoded != tt.expected {
				t.Errorf("VLQEncode(%d) = %s; want %s", tt.value, encoded, tt.expected)
			}
		})
	}
}
