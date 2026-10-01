package tests

import (
	"testing"

	. "github.com/escalier-lang/escalier/internal/checker"
	"github.com/stretchr/testify/require"
)

// TestReceiverFormsPrint pins how the checker renders each receiver form on a class's
// instance type. A borrow keeps its `&`, and a consuming receiver is written without one.
func TestReceiverFormsPrint(t *testing.T) {
	ns := mustInferAsModule(t, `
		class C {
			v: number,
			look(&self) -> number { return self.v },
			bump(&mut self) -> number { return self.v },
			take(self) -> number { return self.v },
			drain(mut self) -> number { return self.v },
		}
	`)
	alias, ok := ns.Types["C"]
	require.True(t, ok)
	require.Equal(t,
		"{v: number, look(&self) -> number, bump(&mut self) -> number, take(self) -> number, drain(mut self) -> number}",
		alias.Type.String(),
	)
}

// TestSetterReceiver pins that an instance setter must take `&mut self`. A shared borrow
// cannot write, and a consuming receiver would move the instance on every write.
func TestSetterReceiver(t *testing.T) {
	const want = "Setters must declare a `&mut self` receiver, since writing through one mutates the instance."
	tests := map[string]struct {
		receiver string
		want     []string
	}{
		"mutable borrow":    {receiver: "&mut self"},
		"shared borrow":     {receiver: "&self", want: []string{want}},
		"consuming":         {receiver: "self", want: []string{want}},
		"mutable consuming": {receiver: "mut self", want: []string{want}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, `
				class C {
					v: number,
					set x(`+tc.receiver+`, value: number) { self.v = value },
				}
			`)
			var msgs []string
			for _, e := range errs {
				if _, ok := e.(SetterReceiverError); ok {
					msgs = append(msgs, e.Message())
				}
			}
			require.Equal(t, tc.want, msgs)
		})
	}
}

// TestGetterReceiver pins that an instance getter must borrow its receiver, since reading a
// property leaves the instance in place.
func TestGetterReceiver(t *testing.T) {
	const want = "Getters must borrow their receiver with `&self` or `&mut self`, since reading through one leaves the instance in place."
	tests := map[string]struct {
		receiver string
		want     []string
	}{
		"shared borrow":     {receiver: "&self"},
		"mutable borrow":    {receiver: "&mut self"},
		"consuming":         {receiver: "self", want: []string{want}},
		"mutable consuming": {receiver: "mut self", want: []string{want}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			errs := inferModuleErrors(t, `
				class C {
					v: number,
					get x(`+tc.receiver+`) -> number { return self.v },
				}
			`)
			var msgs []string
			for _, e := range errs {
				if _, ok := e.(GetterReceiverError); ok {
					msgs = append(msgs, e.Message())
				}
			}
			require.Equal(t, tc.want, msgs)
		})
	}
}
