//go:build windows

package ipc

import (
	"context"
	"errors"
	"io/fs"
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
	//
	// It is still dialed, but only when nothing answers on this user's own name,
	// and the process serving it has to pass verifyPipeServer like any other.
	// That is enough for an upgraded CLI to reach a daemon started by the older
	// binary, so the version handshake can tell the user to restart it instead
	// of the CLI reporting that nothing is running. Drop it once daemons
	// predating the move are no longer a realistic thing to meet.
	legacyPipePath = pipePrefix
)

// pipeHandle is the part of a go-winio pipe connection the server check needs:
// the raw handle, to ask the kernel which process is on the other end.
type pipeHandle interface {
	Fd() uintptr
}

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
// Unix transport there is no search here: a named pipe cannot be inspected
// without opening it, so the fallback to the previous name lives in
// dialEndpoint, where the open has already happened.
func clientEndpointPath() string {
	return daemonEndpointPath()
}

// endpointHint returns extra guidance for a failed connection. The Windows
// client falls back to the previous name on its own, so there is nothing to add.
func endpointHint() string {
	return ""
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

// dialEndpoint opens path and hands back a connection only once the process
// serving it is known to run as this user.
//
// Opening is safe to do first: go-winio dials at SECURITY_ANONYMOUS, so
// whatever holds the name cannot impersonate the caller, and nothing is written
// until verifyPipeServer has passed. When this user's own name has no server,
// the previous machine-wide name is tried under the same check, so an old
// daemon is reached and answers with the version mismatch rather than being
// reported as absent.
func dialEndpoint(ctx context.Context, dialer net.Dialer, path string) (net.Conn, error) {
	sid, sidErr := pipeIdentity()
	if sidErr != nil {
		return nil, sidErr
	}

	if dialer.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, dialer.Timeout)
		defer cancel()
	}

	conn, dialErr := dialVerifiedPipe(ctx, path, sid)
	if errors.Is(dialErr, fs.ErrNotExist) && path == daemonEndpointPath() {
		legacyConn, legacyErr := dialVerifiedPipe(ctx, legacyPipePath, sid)
		if legacyErr == nil {
			return legacyConn, nil
		}

		// The failure reported is the one on the name the daemon should be
		// on; a missing legacy pipe is the ordinary case, not news.
	}

	return conn, dialErr
}

// dialVerifiedPipe opens path and closes it again unless owner is serving it.
func dialVerifiedPipe(ctx context.Context, path string, owner string) (net.Conn, error) {
	conn, dialErr := winio.DialPipeContext(ctx, path)
	if dialErr != nil {
		return nil, dialErr
	}

	verifyErr := verifyPipeServer(conn, path, owner)
	if verifyErr != nil {
		_ = conn.Close()

		return nil, verifyErr
	}

	return conn, nil
}

// verifyPipeServer reports whether the process on the server end of conn runs
// as owner. Anything that stops the question being answered is a refusal: the
// pipe name is the only thing the client chose, and a name is what another
// account can take first.
func verifyPipeServer(conn net.Conn, path string, owner string) error {
	pipe, ok := conn.(pipeHandle)
	if !ok {
		return derrors.Newf(derrors.CodeIPCFailed, "cannot read the owner of %s", path)
	}

	serverSID, sidErr := pipeServerSID(windows.Handle(pipe.Fd()))
	if sidErr != nil {
		return derrors.Wrapf(sidErr, derrors.CodeIPCFailed, "cannot read the owner of %s", path)
	}

	if serverSID != owner {
		return derrors.Newf(
			derrors.CodeIPCFailed,
			"%s belongs to %s rather than to this user",
			path,
			serverSID,
		)
	}

	return nil
}

// pipeServerSID returns the string SID of the user running the process that
// serves the pipe behind handle.
func pipeServerSID(handle windows.Handle) (string, error) {
	var pid uint32

	pidErr := windows.GetNamedPipeServerProcessId(handle, &pid)
	if pidErr != nil {
		return "", pidErr
	}

	process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if openErr != nil {
		return "", openErr
	}
	defer func() { _ = windows.CloseHandle(process) }()

	var token windows.Token

	tokenErr := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token)
	if tokenErr != nil {
		return "", tokenErr
	}
	defer func() { _ = token.Close() }()

	user, userErr := token.GetTokenUser()
	if userErr != nil {
		return "", userErr
	}

	return user.User.Sid.String(), nil
}

func cleanupEndpoint(_ string) error {
	return nil
}
