package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/fdpass"
	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

type Runner struct {
	Version string
	Getenv  func(string) string
	Stdout  io.Writer
	Stderr  io.Writer
}

func (r Runner) Run(argv []string) int {
	if len(argv) == 0 {
		argv = []string{"fusermount3"}
	}
	getenv := r.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	stdout := r.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := r.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	args, err := ParseArgs(argv[1:])
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", argv[0], err)
		return 2
	}
	if args.Help {
		printHelp(stdout, argv[0])
		return 0
	}
	if args.Version {
		version := r.Version
		if version == "" {
			version = "unknown"
		}
		fmt.Fprintf(stdout, "%s (ccc-fuse-sidecar) %s\n", argv[0], version)
		return 0
	}
	if args.Mountpoint == "" {
		fmt.Fprintf(stderr, "%s: mountpoint is required\n", argv[0])
		return 2
	}
	debug := debugEnabled(getenv)
	containerName, containerIDHint := containerIdentityFromEnv(getenv)

	socketPath := protocol.SocketPathFromEnv(getenv)
	if err := protocol.ValidateSocketPath(socketPath); err != nil {
		printErr(stderr, args.Quiet, "%s: invalid %s: %v\n", argv[0], protocol.EnvSocketPath, err)
		return 1
	}
	debugf(stderr, debug, "%s: action=%s socket=%s mountpoint=%q options=%q lazy=%v quiet=%v%s\n", argv[0], actionName(args), socketPath, args.Mountpoint, args.Options, args.Lazy, args.Quiet, formatIdentityDebug(containerName, containerIDHint))

	if args.Unmount {
		if err := requestUnmount(socketPath, args, containerName, containerIDHint, stderr, debug); err != nil {
			printErr(stderr, args.Quiet, "%s: %v\n", argv[0], err)
			return 1
		}
		return 0
	}

	commFD, err := strconv.Atoi(getenv(protocol.EnvFuseCommFD))
	if err != nil || commFD < 0 {
		printErr(stderr, args.Quiet, "%s: %s is required and must be a Unix socket fd\n", argv[0], protocol.EnvFuseCommFD)
		return 1
	}
	debugf(stderr, debug, "%s: using %s=%d\n", argv[0], protocol.EnvFuseCommFD, commFD)
	if err := requestMountAndForwardFD(socketPath, commFD, args, containerName, containerIDHint, stderr, debug); err != nil {
		printErr(stderr, args.Quiet, "%s: %v\n", argv[0], err)
		return 1
	}
	return 0
}

func requestMountAndForwardFD(socketPath string, commFD int, args Args, containerName, containerIDHint string, debugOut io.Writer, debug bool) error {
	debugf(debugOut, debug, "fusermount3: requesting mount for %q via %s%s\n", args.Mountpoint, socketPath, formatIdentityDebug(containerName, containerIDHint))
	fuseFD, err := RequestMount(socketPath, args, containerName, containerIDHint)
	if err != nil {
		return err
	}
	defer syscallClose(fuseFD)
	debugf(debugOut, debug, "fusermount3: received FUSE fd %d from sidecar\n", fuseFD)

	commConn, err := fdpass.UnixConnFromRawFD(commFD)
	if err != nil {
		return fmt.Errorf("open %s socket fd %d: %w", protocol.EnvFuseCommFD, commFD, err)
	}
	defer commConn.Close()
	if err := fdpass.SendFD(commConn, fuseFD, []byte{0}); err != nil {
		return fmt.Errorf("forward FUSE fd to libfuse: %w", err)
	}
	debugf(debugOut, debug, "fusermount3: forwarded FUSE fd %d to libfuse comm fd %d\n", fuseFD, commFD)
	return nil
}

func RequestMount(socketPath string, args Args, containerName, containerIDHint string) (int, error) {
	sidecarConn, err := net.Dial("unix", socketPath)
	if err != nil {
		return -1, fmt.Errorf("connect to sidecar socket %q: %w", socketPath, err)
	}
	defer sidecarConn.Close()
	sidecarUnix, ok := sidecarConn.(*net.UnixConn)
	if !ok {
		return -1, fmt.Errorf("sidecar connection is not a Unix-domain socket")
	}

	req := protocol.Request{
		Action:          protocol.ActionMount,
		Mountpoint:      args.Mountpoint,
		Options:         SplitMountOptions(args.Options),
		ContainerName:   containerName,
		ContainerIDHint: containerIDHint,
	}
	if err := protocol.WriteJSON(sidecarConn, req); err != nil {
		return -1, fmt.Errorf("send mount request: %w", err)
	}

	fuseFD, payload, err := fdpass.RecvMaybeFD(sidecarUnix)
	if err != nil {
		return -1, fmt.Errorf("receive sidecar response: %w", err)
	}
	var resp protocol.Response
	if err := json.Unmarshal(payload, &resp); err != nil {
		if fuseFD >= 0 {
			_ = syscallClose(fuseFD)
		}
		return -1, fmt.Errorf("decode sidecar response: %w", err)
	}
	if !resp.OK {
		if fuseFD >= 0 {
			_ = syscallClose(fuseFD)
		}
		return -1, errors.New(resp.Error)
	}
	if fuseFD < 0 {
		return -1, fmt.Errorf("sidecar returned success without a FUSE fd")
	}
	return fuseFD, nil
}

func requestUnmount(socketPath string, args Args, containerName, containerIDHint string, debugOut io.Writer, debug bool) error {
	debugf(debugOut, debug, "fusermount3: requesting unmount for %q lazy=%v via %s%s\n", args.Mountpoint, args.Lazy, socketPath, formatIdentityDebug(containerName, containerIDHint))
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to sidecar socket %q: %w", socketPath, err)
	}
	defer conn.Close()

	req := protocol.Request{
		Action:          protocol.ActionUnmount,
		Mountpoint:      args.Mountpoint,
		Lazy:            args.Lazy,
		ContainerName:   containerName,
		ContainerIDHint: containerIDHint,
	}
	if err := protocol.WriteJSON(conn, req); err != nil {
		return fmt.Errorf("send unmount request: %w", err)
	}
	var resp protocol.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return fmt.Errorf("decode sidecar response: %w", err)
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	debugf(debugOut, debug, "fusermount3: sidecar unmounted %q lazy=%v\n", args.Mountpoint, args.Lazy)
	return nil
}

func containerIdentityFromEnv(getenv func(string) string) (name, idHint string) {
	return strings.TrimSpace(getenv(protocol.EnvContainerName)), strings.TrimSpace(getenv(protocol.EnvHostname))
}

func formatIdentityDebug(containerName, containerIDHint string) string {
	var parts []string
	if containerName != "" {
		parts = append(parts, "container="+containerName)
	}
	if containerIDHint != "" {
		parts = append(parts, "idHint="+containerIDHint)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func debugEnabled(getenv func(string) string) bool {
	v := strings.TrimSpace(strings.ToLower(getenv(protocol.EnvDebug)))
	return v != "" && v != "0" && v != "false" && v != "no" && v != "off"
}

func debugf(w io.Writer, enabled bool, format string, args ...any) {
	if !enabled || w == nil {
		return
	}
	fmt.Fprintf(w, "ccc-fuse-debug: "+format, args...)
}

func actionName(args Args) string {
	if args.Unmount {
		return protocol.ActionUnmount
	}
	return protocol.ActionMount
}

func printHelp(w io.Writer, name string) {
	fmt.Fprintf(w, `Usage:
  %[1]s [-o OPTIONS] MOUNTPOINT
  %[1]s -u [-z] [-q] MOUNTPOINT

Options:
  -o, --options OPTIONS   comma-separated FUSE mount options
  -u, --unmount           unmount MOUNTPOINT through the sidecar
  -z, --lazy              lazy unmount
  -q, --quiet             suppress error output
  -V, --version           print version
  -h, --help              print help

Environment:
  %[2]s   sidecar Unix socket (default %[3]s)
  %[4]s              libfuse communication fd for mount requests
  %[5]s           enable verbose helper debug logs when set to 1/true/yes
  %[6]s             optional Docker container name hint for sidecar translation
  %[7]s                    optional container id hint
`, name, protocol.EnvSocketPath, protocol.DefaultSocketPath, protocol.EnvFuseCommFD, protocol.EnvDebug, protocol.EnvContainerName, protocol.EnvHostname)
}

func printErr(w io.Writer, quiet bool, format string, args ...any) {
	if quiet {
		return
	}
	fmt.Fprintf(w, format, args...)
}
