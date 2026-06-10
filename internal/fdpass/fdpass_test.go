package fdpass

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSendRecvFD(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "fd.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	payload := []byte(`{"ok":true}` + "\n")
	filePath := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(filePath, []byte("fd-payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			errc <- err
			return
		}
		defer conn.Close()
		f, err := os.Open(filePath)
		if err != nil {
			errc <- err
			return
		}
		defer f.Close()
		errc <- SendFD(conn.(*net.UnixConn), int(f.Fd()), payload)
	}()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fd, gotPayload, err := RecvFD(conn.(*net.UnixConn))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if string(gotPayload) != string(payload) {
		t.Fatalf("payload = %q, want %q", gotPayload, payload)
	}
	buf := make([]byte, len("fd-payload"))
	if _, err := syscall.Read(fd, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "fd-payload" {
		t.Fatalf("fd content = %q", buf)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}
