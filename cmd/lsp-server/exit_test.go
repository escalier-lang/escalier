package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExitClosesTheExitedChannel pins that the `exit` notification wakes the
// serve loop in main. The loop parks on a select over this channel and the
// connection's disconnect, so a handler that did not close it would leave the
// process running until the client closed the pipe.
func TestExitClosesTheExitedChannel(t *testing.T) {
	s := NewServer()

	select {
	case <-s.exited:
		require.FailNow(t, "exited is closed before the exit notification")
	default:
	}

	require.NoError(t, s.exit(nil))

	select {
	case <-s.exited:
	default:
		require.FailNow(t, "exit did not close exited")
	}
}

// TestExitIsIdempotent pins that a second notification does not panic. Closing
// a closed channel is a runtime panic, and a client is free to send `exit`
// more than once.
func TestExitIsIdempotent(t *testing.T) {
	s := NewServer()

	require.NoError(t, s.exit(nil))
	require.NotPanics(t, func() {
		require.NoError(t, s.exit(nil))
	})
}
