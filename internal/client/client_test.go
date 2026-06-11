package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/fdpass"
	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

func TestRunnerMountForwardsFDToFuseCommFD(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(dir, "sidecar.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	sidecarErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			sidecarErr <- err
			return
		}
		defer conn.Close()
		req, err := protocol.ReadRequest(conn)
		if err != nil {
			sidecarErr <- err
			return
		}
		if req.Action != protocol.ActionMount || req.Mountpoint != mountpoint || req.ContainerName != "ccc-demo" || req.ContainerIDHint != "abc123" || req.SessionID != "agent-20260611-test" {
			sidecarErr <- errUnexpectedRequest
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, "fake-fuse"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			sidecarErr <- err
			return
		}
		defer f.Close()
		sidecarErr <- fdpass.SendFD(conn.(*net.UnixConn), int(f.Fd()), protocol.MarshalResponse(protocol.Response{OK: true}))
	}()

	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fds[1])
	receiver, err := fdpass.UnixConnFromRawFD(fds[1])
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()

	env := map[string]string{
		protocol.EnvSocketPath:    socketPath,
		protocol.EnvFuseCommFD:    strconv.Itoa(fds[0]),
		protocol.EnvContainerName: " ccc-demo ",
		protocol.EnvHostname:      " abc123 ",
		protocol.EnvAgentSession:  " agent-20260611-test ",
	}
	code := Runner{
		Getenv: func(k string) string { return env[k] },
	}.Run([]string{"fusermount3", "-o", "fsname=test,allow_other", mountpoint})
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	gotFD, payload, err := fdpass.RecvFD(receiver)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(gotFD)
	if len(payload) != 1 || payload[0] != 0 {
		t.Fatalf("libfuse payload = %v, want [0]", payload)
	}
	if err := <-sidecarErr; err != nil {
		t.Fatal(err)
	}
}

func TestRequestUnmount(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "sidecar.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	reqc := make(chan protocol.Request, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			reqc <- protocol.Request{Action: err.Error()}
			return
		}
		defer conn.Close()
		req, err := protocol.ReadRequest(conn)
		if err != nil {
			reqc <- protocol.Request{Action: err.Error()}
			return
		}
		reqc <- req
		_ = json.NewEncoder(conn).Encode(protocol.Response{OK: true})
	}()

	err = requestUnmount(socketPath, Args{Mountpoint: "/mnt/demo", Lazy: true}, "ccc-demo", "abc123", "agent-20260611-test", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	req := <-reqc
	if req.Action != protocol.ActionUnmount || req.Mountpoint != "/mnt/demo" || !req.Lazy || req.ContainerName != "ccc-demo" || req.ContainerIDHint != "abc123" || req.SessionID != "agent-20260611-test" {
		t.Fatalf("unexpected request: %+v", req)
	}
}

func TestRunnerDebugLogsParsedMountRequest(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(dir, "missing.sock")
	env := map[string]string{
		protocol.EnvSocketPath:    socketPath,
		protocol.EnvFuseCommFD:    "7",
		protocol.EnvDebug:         "1",
		protocol.EnvContainerName: "ccc-demo",
		protocol.EnvHostname:      "abc123",
	}
	var stderr strings.Builder
	code := Runner{
		Getenv: func(k string) string { return env[k] },
		Stderr: &stderr,
	}.Run([]string{"fusermount3", "-o", "fsname=test", mountpoint})
	if code == 0 {
		t.Fatalf("exit code = 0, want failure because sidecar socket is absent")
	}
	log := stderr.String()
	if !strings.Contains(log, "ccc-fuse-debug:") || !strings.Contains(log, "mountpoint=\""+mountpoint+"\"") || !strings.Contains(log, "_FUSE_COMMFD=7") || !strings.Contains(log, "container=ccc-demo") || !strings.Contains(log, "idHint=abc123") {
		t.Fatalf("debug log missing expected details:\n%s", log)
	}
}

func TestContainerIdentityFromEnvTrimsWhitespace(t *testing.T) {
	env := map[string]string{
		protocol.EnvContainerName: " ccc-demo\t",
		protocol.EnvHostname:      "\nabc123 ",
		protocol.EnvAgentSession:  " agent-xyz \n",
	}
	name, idHint, session := containerIdentityFromEnv(func(k string) string { return env[k] })
	if name != "ccc-demo" || idHint != "abc123" || session != "agent-xyz" {
		t.Fatalf("identity = %q/%q/%q, want ccc-demo/abc123/agent-xyz", name, idHint, session)
	}
}

var errUnexpectedRequest = &unexpectedRequestError{}

type unexpectedRequestError struct{}

func (*unexpectedRequestError) Error() string { return "unexpected request" }
