package sidecar

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/fdpass"
	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

func TestDaemonMountProtocol(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeFusePath := filepath.Join(dir, "fake-fuse")
	if err := os.WriteFile(fakeFusePath, []byte("fake-fuse"), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var gotSource, gotTarget, gotResolvedTarget, gotFSType, gotData string
	var gotFlags uintptr
	var gotResolveErr error
	daemon, err := New(Config{
		SocketPath:      filepath.Join(dir, "fuse.sock"),
		AllowedPrefixes: []string{dir},
		DevFusePath:     fakeFusePath,
		OpenFuse: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_RDONLY, 0)
		},
		Mount: func(source, target, fstype string, flags uintptr, data string) error {
			mu.Lock()
			defer mu.Unlock()
			gotSource, gotTarget, gotFSType, gotFlags, gotData = source, target, fstype, flags, data
			gotResolvedTarget, gotResolveErr = filepath.EvalSymlinks(target)
			return nil
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- daemon.ListenAndServe(ctx) }()
	waitForSocket(t, daemon.cfg.SocketPath)

	conn, err := net.Dial("unix", daemon.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	req := protocol.Request{
		Action:     protocol.ActionMount,
		Mountpoint: mountpoint,
		Options:    []string{"fsname=demo", "allow_other", "ro"},
	}
	if err := protocol.WriteJSON(conn, req); err != nil {
		t.Fatal(err)
	}
	fuseFD, payload, err := fdpass.RecvMaybeFD(conn.(*net.UnixConn))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fuseFD)
	var resp protocol.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response error: %s", resp.Error)
	}
	if fuseFD < 0 {
		t.Fatal("expected received fd")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotResolveErr != nil {
		t.Fatalf("mount target %q did not resolve: %v", gotTarget, gotResolveErr)
	}
	if gotSource != "demo" || gotResolvedTarget != mountpoint || gotFSType != "fuse" {
		t.Fatalf("mount call = source %q target %q resolvedTarget %q fstype %q", gotSource, gotTarget, gotResolvedTarget, gotFSType)
	}
	if gotFlags&syscall.MS_RDONLY == 0 {
		t.Fatal("expected readonly flag")
	}
	if !strings.Contains(gotData, "fd=") || !strings.Contains(gotData, "allow_other") {
		t.Fatalf("mount data missing expected fields: %q", gotData)
	}

	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonUnmountProtocol(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mnt")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotTarget string
	var gotFlags int
	daemon, err := New(Config{
		SocketPath:      filepath.Join(dir, "fuse.sock"),
		AllowedPrefixes: []string{dir},
		Unmount: func(target string, flags int) error {
			gotTarget = target
			gotFlags = flags
			return nil
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- daemon.ListenAndServe(ctx) }()
	waitForSocket(t, daemon.cfg.SocketPath)

	conn, err := net.Dial("unix", daemon.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := protocol.WriteJSON(conn, protocol.Request{
		Action:     protocol.ActionUnmount,
		Mountpoint: mountpoint,
		Lazy:       true,
	}); err != nil {
		t.Fatal(err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response error: %s", resp.Error)
	}
	if gotTarget != mountpoint {
		t.Fatalf("target = %q, want %q", gotTarget, mountpoint)
	}
	if gotFlags&syscall.MNT_DETACH == 0 {
		t.Fatal("expected lazy unmount flag")
	}

	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonMountProtocolTranslatesDockerPath(t *testing.T) {
	dir := t.TempDir()
	hostRoot := filepath.Join(dir, "host")
	sidecarMountpoint := filepath.Join(hostRoot, "storage", "user", "project", "mnt")
	if err := os.MkdirAll(sidecarMountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeFusePath := filepath.Join(dir, "fake-fuse")
	if err := os.WriteFile(fakeFusePath, []byte("fake-fuse"), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotResolvedTarget string
	daemon, err := New(Config{
		SocketPath: filepath.Join(dir, "fuse.sock"),
		DockerInspector: fakeInspector{inspect: inspectWithMounts(DockerMount{
			Type:        "bind",
			Source:      "/storage/user",
			Destination: "/storage/user",
		})},
		Translation: TranslationConfig{
			Enabled:             true,
			HostRoot:            hostRoot,
			AllowedHostPrefixes: []string{"/storage"},
		},
		DevFusePath: fakeFusePath,
		OpenFuse: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_RDONLY, 0)
		},
		Mount: func(source, target, fstype string, flags uintptr, data string) error {
			resolved, err := filepath.EvalSymlinks(target)
			if err != nil {
				return err
			}
			gotResolvedTarget = resolved
			return nil
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- daemon.ListenAndServe(ctx) }()
	waitForSocket(t, daemon.cfg.SocketPath)

	conn, err := net.Dial("unix", daemon.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := protocol.WriteJSON(conn, protocol.Request{
		Action:        protocol.ActionMount,
		Mountpoint:    "/storage/user/project/mnt",
		ContainerName: "ccc-demo",
	}); err != nil {
		t.Fatal(err)
	}
	fuseFD, payload, err := fdpass.RecvMaybeFD(conn.(*net.UnixConn))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fuseFD)
	var resp protocol.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response error: %s", resp.Error)
	}
	if gotResolvedTarget != sidecarMountpoint {
		t.Fatalf("mount target resolved to %q, want %q", gotResolvedTarget, sidecarMountpoint)
	}

	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonUnmountProtocolTranslatesDockerPath(t *testing.T) {
	dir := t.TempDir()
	hostRoot := filepath.Join(dir, "host")
	sidecarMountpoint := filepath.Join(hostRoot, "storage", "user", "project", "mnt")
	if err := os.MkdirAll(sidecarMountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	var gotTarget string
	daemon, err := New(Config{
		SocketPath: filepath.Join(dir, "fuse.sock"),
		DockerInspector: fakeInspector{inspect: inspectWithMounts(DockerMount{
			Type:        "bind",
			Source:      "/storage/user",
			Destination: "/storage/user",
		})},
		Translation: TranslationConfig{
			Enabled:             true,
			HostRoot:            hostRoot,
			AllowedHostPrefixes: []string{"/storage"},
		},
		Unmount: func(target string, flags int) error {
			gotTarget = target
			return nil
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errc := make(chan error, 1)
	go func() { errc <- daemon.ListenAndServe(ctx) }()
	waitForSocket(t, daemon.cfg.SocketPath)

	conn, err := net.Dial("unix", daemon.cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := protocol.WriteJSON(conn, protocol.Request{
		Action:        protocol.ActionUnmount,
		Mountpoint:    "/storage/user/project/mnt",
		ContainerName: "ccc-demo",
	}); err != nil {
		t.Fatal(err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("response error: %s", resp.Error)
	}
	if gotTarget != sidecarMountpoint {
		t.Fatalf("unmount target = %q, want %q", gotTarget, sidecarMountpoint)
	}

	cancel()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestResolveMountpointRejectsMissingContainerName(t *testing.T) {
	daemon, err := New(Config{
		DockerInspector: fakeInspector{inspect: inspectWithMounts()},
		Translation: TranslationConfig{
			Enabled:               true,
			HostRoot:              t.TempDir(),
			AllowedClientPrefixes: []string{"/storage/user"},
			AllowedHostPrefixes:   []string{"/storage"},
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = daemon.resolveMountpoint(context.Background(), protocol.Request{Mountpoint: "/storage/user/mnt"})
	if err == nil || !strings.Contains(err.Error(), "container_name") {
		t.Fatalf("error = %v, want container_name", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %q did not become ready", socketPath)
}

type fakeInspector struct {
	inspect ContainerInspect
	err     error
}

func (f fakeInspector) InspectContainer(ctx context.Context, name string) (ContainerInspect, error) {
	if f.err != nil {
		return ContainerInspect{}, f.err
	}
	return f.inspect, nil
}
