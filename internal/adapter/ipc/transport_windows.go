//go:build windows

package ipc

import (
	"context"
	"net"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/y3owk1n/neru/internal/derrors"
)

// Named-pipe transport for IPC on Windows via go-winio.
// Does not implement Unix-domain socket cleanup or permissions.
//
// The pipe namespace is machine-wide, so the name carries the owner's SID and
// the pipe carries a security descriptor naming that SID alone. Without both,
// one fixed name is shared by every account on the machine and a nil PipeConfig
// leaves the default descriptor on it.
const (
	// pipePrefix is the stem every neru endpoint name is built from.
	pipePrefix = `\\.\pipe\neru`

	// pipeBufferBytes is the buffer quota asked for on each side of the pipe.
	//
	// A zero PipeConfig buffer size asks the kernel for a zero quota, and a
	// command whose encoded length is exactly one byte past a power of two then
	// deadlocks: the client's write never completes, the server's read never sees
	// the command, and the exchange ends when the read deadline closes the
	// connection five seconds later. Measured at 65, 129, 257, 513 and 1025 bytes,
	// on every size between 57 and 1253 that is not one of those, and on all of
	// them again with a quota set. Which layer splits the transfer on those
	// boundaries is not established; that it stops once the pipe has a buffer is.
	//
	// The quota is a ceiling the kernel fills on demand, not an allocation, so
	// sizing it to the largest command that can arrive costs nothing per
	// connection and leaves no length that has to be split.
	//
	// This is why neru action scroll_right was unusable in a release build while
	// scroll_left worked: with an eight-character version stamp in the payload,
	// the one-character-longer action name lands the command on exactly 65 bytes.
	pipeBufferBytes = maxCommandBytes

	// legacyPipePath is the machine-wide name neru used before the endpoint was
	// scoped to one user.
	// It is never dialed — an unauthenticated name is exactly what the per-user
	// name replaces — but it is what an older daemon is still listening on, so
	// endpointHint mentions it when a connection fails.
	legacyPipePath = pipePrefix
)

// pipeIdentity resolves this process's user SID once. It reads the current
// process token, so it neither blocks nor depends on anything outside the
// process, but it is the one part of the endpoint name that can fail.
var pipeIdentity = sync.OnceValues(currentUserSID) //nolint:gochecknoglobals

// currentUserSID returns the string form of the SID owning this process.
func currentUserSID() (string, error) {
	user, tokenErr := windows.GetCurrentProcessToken().GetTokenUser()
	if tokenErr != nil {
		return "", derrors.Wrap(
			tokenErr,
			derrors.CodeIPCFailed,
			"cannot read this process's user identity",
		)
	}

	return user.User.Sid.String(), nil
}

// pipeSecurityDescriptor grants the endpoint to its owner and to nobody else:
// a protected DACL (no inherited entries) holding a single generic-all allow
// ACE for sid.
func pipeSecurityDescriptor(sid string) string {
	return "D:P(A;;GA;;;" + sid + ")"
}

// daemonEndpointPath is where the daemon listens. It is empty when the user's SID
// cannot be read: the alternative would be to fall back to the machine-wide
// name, which is the thing being scoped away. listenEndpoint and dialEndpoint
// report the reason instead.
func daemonEndpointPath() string {
	sid, sidErr := pipeIdentity()
	if sidErr != nil {
		return ""
	}

	return pipePrefix + "-" + sid
}

// clientEndpointPath returns the endpoint a CLI process should dial. Unlike the
// Unix transport there is no search: a named pipe carries no ownership a client
// can check before connecting, so the only name trusted is the one derived from
// this user's own SID.
func clientEndpointPath() string {
	return daemonEndpointPath()
}

// endpointHint returns extra guidance for a failed connection.
//
// A daemon from before the move is still serving the machine-wide name, and
// there is no safe way to detect that — probing it means handing a command to
// whatever holds a name anyone can create. So the possibility is stated rather
// than tested, and only on the path where connecting has already failed.
func endpointHint() string {
	return "if you just upgraded, a daemon from the previous version may still " +
		"be running on " + legacyPipePath + "; stop it and start neru again"
}

func listenEndpoint(ctx context.Context, path string) (net.Listener, error) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return nil, ctxErr
	}

	sid, sidErr := pipeIdentity()
	if sidErr != nil {
		return nil, sidErr
	}

	listener, listenErr := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: pipeSecurityDescriptor(sid),
		InputBufferSize:    pipeBufferBytes,
		OutputBufferSize:   pipeBufferBytes,
	})
	if listenErr != nil {
		return nil, derrors.Wrapf(
			listenErr,
			derrors.CodeIPCFailed,
			"cannot create the IPC endpoint %s; another process may already hold that name",
			path,
		)
	}

	return listener, nil
}

func dialEndpoint(ctx context.Context, dialer net.Dialer, path string) (net.Conn, error) {
	_, sidErr := pipeIdentity()
	if sidErr != nil {
		return nil, sidErr
	}

	if dialer.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, dialer.Timeout)
		defer cancel()
	}

	return winio.DialPipeContext(ctx, path)
}

func cleanupEndpoint(_ string) error {
	return nil
}
