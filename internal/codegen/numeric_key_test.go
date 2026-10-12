package codegen

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNumericKey covers which member names render as a number literal key.
func TestNumericKey(t *testing.T) {
	tests := []struct {
		name   string
		want   float64
		wantOk bool
	}{
		{name: "1", want: 1, wantOk: true},
		{name: "0", want: 0, wantOk: true},
		{name: "1.5", want: 1.5, wantOk: true},
		{name: "01"},
		{name: "-1"},
		{name: "1e3"},
		{name: "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := numericKey(tt.name)
			require.Equal(t, tt.wantOk, ok)
			require.Equal(t, tt.want, got)
		})
	}
}
