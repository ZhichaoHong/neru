//go:build integration && windows

package ipc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/y3owk1n/neru/internal/adapter/ipc"
)

// TestCommandLengthBoundaries sends commands whose encoded length is exactly one
// byte past a power of two.
//
// Those lengths used to deadlock: the named pipe was created without a buffer
// quota, the client's write never completed, the server's read never saw the
// command, and the exchange ended when the read deadline closed the connection.
// It surfaced as one CLI verb being unusable in a release build while its
// neighbors worked, because the payload for that verb happened to be 65 bytes.
//
// The lengths are asserted rather than the buffer setting, so the test still
// covers the failure if the transport is reworked.
func TestCommandLengthBoundaries(t *testing.T) {
	if ipc.IsServerRunning() {
		t.Skip("a neru daemon already owns the IPC endpoint; skipping")
	}

	server, err := ipc.NewServer(
		func(_ context.Context, _ ipc.Command) ipc.Response {
			return ipc.Response{Success: true, Code: ipc.CodeOK}
		},
		zap.NewNop(),
	)
	if err != nil {
		t.Fatalf("NewServer() error = %v, want nil", err)
	}

	server.Start()

	t.Cleanup(func() {
		_ = server.Stop()
	})

	deadline := time.Now().Add(3 * time.Second)
	for !ipc.IsServerRunning() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	client := ipc.NewClient()

	for _, size := range []int{64, 65, 66, 128, 129, 257, 513, 1025} {
		t.Run(fmt.Sprintf("%d bytes", size), func(t *testing.T) {
			cmd, ok := commandOfSize(size)
			if !ok {
				t.Skipf("cannot build a command of exactly %d bytes", size)
			}

			// Well under the deadline that used to end these exchanges, so a
			// regression fails fast instead of stalling the suite.
			response, sendErr := client.SendWithTimeout(cmd, 2*time.Second)
			if sendErr != nil {
				t.Fatalf("SendWithTimeout() with a %d byte command error = %v, want nil", size, sendErr)
			}

			if !response.Success {
				t.Errorf("response.Success = false (%s), want true", response.Message)
			}
		})
	}
}

// commandOfSize returns a command whose JSON encoding, newline included, is
// exactly size bytes, padding one argument to make up the difference.
func commandOfSize(size int) (ipc.Command, bool) {
	build := func(padding int) ipc.Command {
		return ipc.Command{
			Version: ipc.BuildVersion(),
			Action:  testCommandAction,
			Args:    []string{strings.Repeat("z", padding)},
		}
	}

	encodedLen := func(cmd ipc.Command) int {
		encoded, err := json.Marshal(cmd)
		if err != nil {
			return -1
		}

		// The client encodes with json.Encoder, which appends a newline.
		return len(encoded) + 1
	}

	base := encodedLen(build(0))
	if base < 0 || base > size {
		return ipc.Command{}, false
	}

	cmd := build(size - base)
	if encodedLen(cmd) != size {
		return ipc.Command{}, false
	}

	return cmd, true
}
