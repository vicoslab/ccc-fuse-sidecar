/*
Copyright 2018 The Kubernetes Authors.
Copyright 2022 Google LLC
Copyright 2023 Preferred Networks, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This package is derived from fd-passing helpers in
github.com/pfnet-research/meta-fuse-csi-plugin/pkg/util and rewritten to use
net.UnixConn.SyscallConn directly for the Docker sidecar protocol.
*/

package fdpass

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

const MaxPayload = 64 * 1024

func SendFD(conn *net.UnixConn, fd int, payload []byte) error {
	if fd < 0 {
		return fmt.Errorf("invalid fd %d", fd)
	}
	if len(payload) == 0 {
		payload = []byte{0}
	}
	rights := syscall.UnixRights(fd)
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	var sendErr error
	if err := raw.Control(func(socket uintptr) {
		sendErr = syscall.Sendmsg(int(socket), payload, rights, nil, 0)
	}); err != nil {
		return err
	}
	return sendErr
}

func RecvFD(conn *net.UnixConn) (int, []byte, error) {
	fd, payload, err := RecvMaybeFD(conn)
	if err != nil {
		return -1, nil, err
	}
	if fd < 0 {
		return -1, payload, errors.New("message did not contain SCM_RIGHTS fd")
	}
	return fd, payload, nil
}

func RecvMaybeFD(conn *net.UnixConn) (int, []byte, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return -1, nil, err
	}

	payload := make([]byte, MaxPayload)
	oob := make([]byte, syscall.CmsgSpace(4*4))
	fd := -1
	var n int
	var recvErr error
	if err := raw.Read(func(socket uintptr) bool {
		var oobn int
		n, oobn, _, _, recvErr = syscall.Recvmsg(int(socket), payload, oob, 0)
		if recvErr != nil {
			if recvErr == syscall.EAGAIN || recvErr == syscall.EWOULDBLOCK {
				return false
			}
			return true
		}
		if oobn > 0 {
			msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
			if err != nil {
				recvErr = err
				return true
			}
			for _, msg := range msgs {
				fds, err := syscall.ParseUnixRights(&msg)
				if err == nil && len(fds) > 0 {
					fd = fds[0]
					for _, extra := range fds[1:] {
						_ = syscall.Close(extra)
					}
					break
				}
			}
		}
		return true
	}); err != nil {
		return -1, nil, err
	}
	if recvErr != nil {
		return -1, nil, recvErr
	}
	if n == 0 {
		return fd, nil, nil
	}
	return fd, payload[:n], nil
}

func UnixConnFromRawFD(fd int) (*net.UnixConn, error) {
	if fd < 0 {
		return nil, fmt.Errorf("invalid Unix socket fd %d", fd)
	}
	f := os.NewFile(uintptr(fd), "unix-socket")
	if f == nil {
		return nil, fmt.Errorf("failed to wrap fd %d", fd)
	}
	defer f.Close()

	conn, err := net.FileConn(f)
	if err != nil {
		return nil, err
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("fd %d is not a Unix-domain socket", fd)
	}
	return unixConn, nil
}
