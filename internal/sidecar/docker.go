package sidecar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DockerInspector interface {
	InspectContainer(ctx context.Context, name string) (ContainerInspect, error)
}

type ContainerInspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []DockerMount `json:"Mounts"`
}

type DockerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
	Propagation string `json:"Propagation"`
}

type dockerInspector struct {
	socket string
	client *http.Client
}

func NewDockerInspector(socket string) DockerInspector {
	socket = strings.TrimSpace(socket)
	socket = strings.TrimPrefix(socket, "unix://")
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socket)
		},
	}
	return &dockerInspector{
		socket: socket,
		client: &http.Client{
			Transport: transport,
			Timeout:   10 * time.Second,
		},
	}
}

func (d *dockerInspector) InspectContainer(ctx context.Context, name string) (ContainerInspect, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ContainerInspect{}, fmt.Errorf("container name is required")
	}
	escaped := url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+escaped+"/json", nil)
	if err != nil {
		return ContainerInspect{}, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return ContainerInspect{}, fmt.Errorf("inspect Docker container %q through %s: %w", name, d.socket, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return ContainerInspect{}, fmt.Errorf("inspect Docker container %q: %s", name, msg)
	}
	var inspect ContainerInspect
	if err := json.NewDecoder(resp.Body).Decode(&inspect); err != nil {
		return ContainerInspect{}, fmt.Errorf("decode Docker inspect for %q: %w", name, err)
	}
	return inspect, nil
}
