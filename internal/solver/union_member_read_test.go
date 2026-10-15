package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferUnionMemberRead covers the rule #1719 decided for reading a property off a
// union. A property every arm declares reads at its type. A property some arms declare
// reads as that type or `undefined`. A property no arm declares is an error.
func TestInferUnionMemberRead(t *testing.T) {
	const prelude = `
		type FooBar =
		  | {kind: "foo", id: number, foo: string}
		  | {kind: "bar", id: number, bar: boolean}
		declare val fb: FooBar
	`
	tests := []struct {
		name     string
		read     string
		want     string
		wantErrs []string
	}{
		{name: "EveryArmDeclaresIt", read: "fb.id", want: "number"},
		{name: "SomeArmsDeclareIt", read: "fb.foo", want: "string | undefined"},
		{name: "NoArmDeclaresIt", read: "fb.nope", wantErrs: []string{"object is missing property: nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, prelude+"val r = "+tt.read+"\n")
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Message()
			}
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, msgs)
				return
			}
			require.Empty(t, msgs)
			require.Equal(t, tt.want, values["r"])
		})
	}
}
