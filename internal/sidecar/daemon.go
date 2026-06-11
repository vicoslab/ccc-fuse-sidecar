package sidecar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/fdpass"
	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

type MountFunc func(source, target, fstype string, flags uintptr, data string) error
type UnmountFunc func(target string, flags int) error
type OpenFuseFunc func(path string) (*os.File, error)

type Config struct {
	SocketPath      string
	SocketMode      os.FileMode
	AllowedPrefixes []string
	DevFusePath     string
	Mount           MountFunc
	Unmount         UnmountFunc
	OpenFuse        OpenFuseFunc
	Logger          *log.Logger
	Debug           bool
}

type Daemon struct {
	cfg Config
	log *log.Logger
}

func New(cfg Config) (*Daemon, error) {
	if cfg.SocketPath == "" {
		cfg.SocketPath = protocol.DefaultSocketPath
	}
	if err := protocol.ValidateSocketPath(cfg.SocketPath); err != nil {
		return nil, err
	}
	if len(cfg.AllowedPrefixes) == 0 {
		return nil, errors.New("at least one allowed mount prefix is required")
	}
	if cfg.SocketMode == 0 {
		cfg.SocketMode = 0o666
	}
	if cfg.DevFusePath == "" {
		cfg.DevFusePath = "/dev/fuse"
	}
	if cfg.Mount == nil {
		cfg.Mount = syscall.Mount
	}
	if cfg.Unmount == nil {
		cfg.Unmount = syscall.Unmount
	}
	if cfg.OpenFuse == nil {
		cfg.OpenFuse = func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_RDWR, 0)
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "ccc-fuse-sidecar: ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Daemon{cfg: cfg, log: logger}, nil
}

func (d *Daemon) ListenAndServe(ctx context.Context) error {
	if err := d.prepareSocket(); err != nil {
		return err
	}
	listener, err := net.Listen("unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", d.cfg.SocketPath, err)
	}
	defer listener.Close()
	defer func() {
		if st, err := os.Lstat(d.cfg.SocketPath); err == nil && st.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(d.cfg.SocketPath)
		}
	}()
	if err := os.Chmod(d.cfg.SocketPath, d.cfg.SocketMode); err != nil {
		return fmt.Errorf("chmod socket %q: %w", d.cfg.SocketPath, err)
	}

	d.log.Printf("listening on %s; allowed prefixes: %v", d.cfg.SocketPath, d.cfg.AllowedPrefixes)
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var opErr *net.OpError
			if errors.As(err, &opErr) && !opErr.Temporary() {
				return err
			}
			d.log.Printf("accept failed: %v", err)
			continue
		}
		go d.handleConn(conn)
	}
}

func (d *Daemon) prepareSocket() error {
	dir := filepath.Dir(d.cfg.SocketPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create socket directory %q: %w", dir, err)
	}
	st, err := os.Lstat(d.cfg.SocketPath)
	if err == nil {
		if st.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to remove non-socket at %q", d.cfg.SocketPath)
		}
		if err := os.Remove(d.cfg.SocketPath); err != nil {
			return fmt.Errorf("remove stale socket %q: %w", d.cfg.SocketPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat socket path %q: %w", d.cfg.SocketPath, err)
	}
	return nil
}

func (d *Daemon) handleConn(conn net.Conn) {
	defer conn.Close()
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		d.writeError(conn, "connection is not a Unix-domain socket")
		return
	}

	req, err := protocol.ReadRequest(conn)
	if err != nil {
		d.writeError(conn, fmt.Sprintf("read request: %v", err))
		return
	}
	d.debugf("request action=%q mountpoint=%q options=%v lazy=%v", req.Action, req.Mountpoint, req.Options, req.Lazy)

	switch req.Action {
	case protocol.ActionMount:
		d.handleMount(unixConn, req)
	case protocol.ActionUnmount:
		d.handleUnmount(conn, req)
	default:
		d.writeError(conn, fmt.Sprintf("unsupported action %q", req.Action))
	}
}

func (d *Daemon) handleMount(conn *net.UnixConn, req protocol.Request) {
	mountpointFile, mountTarget, err := protocol.OpenValidatedMountpoint(req.Mountpoint, d.cfg.AllowedPrefixes)
	if err != nil {
		d.writeError(conn, err.Error())
		return
	}
	defer mountpointFile.Close()
	d.debugf("validated mountpoint request=%q target=%q pinned=%q", req.Mountpoint, mountTarget, mountpointFile.Name())

	uid, gid := d.clientCreds(conn)
	fuseFile, err := d.cfg.OpenFuse(d.cfg.DevFusePath)
	if err != nil {
		d.writeError(conn, fmt.Sprintf("open %s: %v", d.cfg.DevFusePath, err))
		return
	}
	defer fuseFile.Close()

	fuseFD := int(fuseFile.Fd())
	plan, err := BuildMountPlan(fuseFD, req.Options, uid, gid)
	if err != nil {
		d.writeError(conn, err.Error())
		return
	}
	d.debugf("mount plan source=%q target=%q fstype=%q flags=0x%x data=%q uid=%d gid=%d fuseFD=%d", plan.Source, mountTarget, plan.FSType, plan.Flags, plan.Data, uid, gid, fuseFD)

	if err := d.cfg.Mount(plan.Source, mountTarget, plan.FSType, plan.Flags, plan.Data); err != nil {
		d.writeError(conn, fmt.Sprintf("mount %q: %v", req.Mountpoint, err))
		return
	}

	payload := protocol.MarshalResponse(protocol.Response{OK: true})
	if err := fdpass.SendFD(conn, fuseFD, payload); err != nil {
		d.log.Printf("failed to send FUSE fd for %s: %v", req.Mountpoint, err)
		if unmountErr := d.cfg.Unmount(mountTarget, syscall.MNT_DETACH); unmountErr != nil {
			d.log.Printf("failed to clean up %s after fd send failure: %v", req.Mountpoint, unmountErr)
		}
		return
	}
	d.log.Printf("mounted %s and sent FUSE fd to uid=%d gid=%d", req.Mountpoint, uid, gid)
}

func (d *Daemon) handleUnmount(conn net.Conn, req protocol.Request) {
	mountpoint, err := protocol.ValidateMountpoint(req.Mountpoint, d.cfg.AllowedPrefixes)
	if err != nil {
		d.writeError(conn, err.Error())
		return
	}

	flags := 0
	if req.Lazy {
		flags |= syscall.MNT_DETACH
	}
	if err := d.cfg.Unmount(mountpoint, flags); err != nil {
		d.writeError(conn, fmt.Sprintf("unmount %q: %v", mountpoint, err))
		return
	}
	if err := protocol.WriteJSON(conn, protocol.Response{OK: true}); err != nil {
		d.log.Printf("failed to write unmount response for %s: %v", mountpoint, err)
	}
	d.log.Printf("unmounted %s lazy=%v", mountpoint, req.Lazy)
}

func (d *Daemon) writeError(conn net.Conn, msg string) {
	d.log.Printf("request failed: %s", msg)
	if err := protocol.WriteJSON(conn, protocol.Response{OK: false, Error: msg}); err != nil {
		d.log.Printf("failed to write error response: %v", err)
	}
}

func (d *Daemon) debugf(format string, args ...any) {
	if d.cfg.Debug {
		d.log.Printf("debug: "+format, args...)
	}
}

func (d *Daemon) clientCreds(conn *net.UnixConn) (int, int) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return os.Getuid(), os.Getgid()
	}
	uid, gid := os.Getuid(), os.Getgid()
	var credErr error
	if err := raw.Control(func(socket uintptr) {
		var cred *syscall.Ucred
		cred, credErr = syscall.GetsockoptUcred(int(socket), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if credErr == nil {
			uid = int(cred.Uid)
			gid = int(cred.Gid)
		}
	}); err != nil || credErr != nil {
		return os.Getuid(), os.Getgid()
	}
	return uid, gid
}
