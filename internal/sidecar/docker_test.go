package sidecar

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerInspectorInspectContainer(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	var gotPath string
	server := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.EscapedPath()
			if gotPath != "/containers/ccc%2Fdemo/json" {
				t.Errorf("path = %q", gotPath)
			}
			resp := ContainerInspect{
				ID:   "abc123",
				Name: "/ccc/demo",
				Mounts: []DockerMount{{
					Type:        "bind",
					Source:      "/srv/users/bob",
					Destination: "/storage/user",
					RW:          true,
					Propagation: "rshared",
				}},
			}
			resp.Config.Labels = map[string]string{"ccc.fuse": "enabled"}
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Error(err)
			}
		}),
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	}()

	inspect, err := NewDockerInspector("unix://"+socketPath).InspectContainer(context.Background(), "ccc/demo")
	if err != nil {
		t.Fatal(err)
	}
	if inspect.ID != "abc123" || inspect.Config.Labels["ccc.fuse"] != "enabled" || len(inspect.Mounts) != 1 {
		t.Fatalf("inspect = %+v", inspect)
	}
	if gotPath == "" {
		t.Fatal("server did not receive request")
	}
}

func TestDockerInspectorReturnsStatusError(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "docker.sock")
	server := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}),
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			t.Error(err)
		}
	}()

	_, err = NewDockerInspector(socketPath).InspectContainer(context.Background(), "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want not found", err)
	}
}

func TestDockerInspectorMissingSocketFailsClearly(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "missing.sock")
	_, err := NewDockerInspector(socketPath).InspectContainer(context.Background(), "ccc-demo")
	if err == nil || !strings.Contains(err.Error(), socketPath) {
		t.Fatalf("error = %v, want socket path", err)
	}
	if _, statErr := os.Stat(socketPath); !os.IsNotExist(statErr) {
		t.Fatalf("socket unexpectedly exists: %v", statErr)
	}
}
