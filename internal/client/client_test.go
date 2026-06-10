package client

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
		if req.Action != protocol.ActionMount || req.Mountpoint != mountpoint {
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
		protocol.EnvSocketPath: socketPath,
		protocol.EnvFuseCommFD: strconv.Itoa(fds[0]),
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

	err = requestUnmount(socketPath, Args{Mountpoint: "/mnt/demo", Lazy: true})
	if err != nil {
		t.Fatal(err)
	}
	req := <-reqc
	if req.Action != protocol.ActionUnmount || req.Mountpoint != "/mnt/demo" || !req.Lazy {
		t.Fatalf("unexpected request: %+v", req)
	}
}

var errUnexpectedRequest = &unexpectedRequestError{}

type unexpectedRequestError struct{}

func (*unexpectedRequestError) Error() string { return "unexpected request" }
