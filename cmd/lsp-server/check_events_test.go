package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tliron/glsp"
)

// recordedCheckEvents returns a context that records each check notification as
// "<method> <version>", in the order the server sends them.
func recordedCheckEvents() (*glsp.Context, func() []string) {
	var mu sync.Mutex
	var events []string
	ctx := &glsp.Context{
		Notify: func(method string, params any) {
			if method != checkStartedMethod && method != checkFinishedMethod {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			events = append(events, fmt.Sprintf("%s %d", method, params.(*checkEventParams).Version))
		},
	}
	return ctx, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
}

func TestValidateBracketsEachCheck(t *testing.T) {
	tests := []struct {
		name string
		// change is the text of version 2, which the test checks after version 1.
		change string
		// stale is true when the document moves on to version 3 before version 2 is
		// checked, so the check of version 2 finds it stale.
		stale bool
	}{
		{name: "Clean", change: "val y: number = 2"},
		{name: "TypeError", change: `val y: number = "a"`},
		{name: "Panic", change: "// " + panicMarker + "\nval y = 2"},
		{name: "Stale", change: "val y = 2", stale: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := integrationServer(t, map[string]string{"lib/index.esc": "val x = 1"})
			s.testHooks = true
			ctx, events := recordedCheckEvents()
			uri := openFileIn(t, s, ctx, "lib/index.esc", "val x = 1")

			if test.stale {
				s.mu.Lock()
				doc := s.documents[uri]
				doc.Version = 3
				s.documents[uri] = doc
				s.mu.Unlock()
				s.validate(ctx, uri, test.change, 2)
			} else {
				changeFile(s, ctx, uri, 2, test.change)
			}

			require.Equal(t, []string{
				checkStartedMethod + " 1",
				checkFinishedMethod + " 1",
				checkStartedMethod + " 2",
				checkFinishedMethod + " 2",
			}, events())
		})
	}
}
