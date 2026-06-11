package sidecar

import (
	"strings"
	"testing"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

func TestTranslateMountpointMapsClientPathToSidecarPath(t *testing.T) {
	translated, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/project/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{
		Type:        "bind",
		Source:      "/srv/users/bob",
		Destination: "/storage/user",
		Propagation: "rshared",
	}), TranslationConfig{
		Enabled:             true,
		HostRoot:            "/host",
		AllowedHostPrefixes: []string{"/srv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if translated.ClientPath != "/storage/user/project/mnt" ||
		translated.HostPath != "/srv/users/bob/project/mnt" ||
		translated.SidecarPath != "/host/srv/users/bob/project/mnt" ||
		translated.MatchedSource != "/srv/users/bob" ||
		translated.MatchedDest != "/storage/user" ||
		translated.ContainerName != "ccc-demo" ||
		translated.ContainerID != "abc123" ||
		translated.Propagation != "rshared" {
		t.Fatalf("translated = %+v", translated)
	}
}

func TestTranslateMountpointUsesLongestDestinationPrefix(t *testing.T) {
	translated, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/private/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(
		DockerMount{Type: "bind", Source: "/srv/users/bob", Destination: "/storage/user"},
		DockerMount{Type: "bind", Source: "/secure/bob", Destination: "/storage/user/private"},
	), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/secure", "/srv"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if translated.HostPath != "/secure/bob/mnt" {
		t.Fatalf("host path = %q, want nested source", translated.HostPath)
	}
}

func TestTranslateMountpointAllowsAnyClientPathWhenClientPrefixesOmitted(t *testing.T) {
	translated, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/mnt/custom/project/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{
		Type:        "bind",
		Source:      "/opt/shared_storage/user_data/bob",
		Destination: "/mnt/custom",
	}), TranslationConfig{
		HostRoot:            "/host",
		AllowedHostPrefixes: []string{"/opt/shared_storage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if translated.HostPath != "/opt/shared_storage/user_data/bob/project/mnt" {
		t.Fatalf("host path = %q", translated.HostPath)
	}
}

func TestTranslateMountpointRejectsMissingContainerName(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint: "/storage/user/mnt",
	}, inspectWithMounts(), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/srv"},
	})
	if err == nil || !strings.Contains(err.Error(), "container_name") {
		t.Fatalf("error = %v, want container_name", err)
	}
}

func TestTranslateMountpointRejectsClientPrefixBoundaryMismatch(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user2/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{Type: "bind", Source: "/srv/users/bob", Destination: "/storage/user2"}), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/srv"},
	})
	if err == nil || !strings.Contains(err.Error(), "outside allowed client prefixes") {
		t.Fatalf("error = %v, want client prefix rejection", err)
	}
}

func TestTranslateMountpointRejectsHostOutsideAllowedPrefixes(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{Type: "bind", Source: "/etc", Destination: "/storage/user"}), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/srv"},
	})
	if err == nil || !strings.Contains(err.Error(), "outside allowed host prefixes") {
		t.Fatalf("error = %v, want host prefix rejection", err)
	}
}

func TestTranslateMountpointRejectsNonBindMount(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{Type: "volume", Source: "/var/lib/docker/volumes/demo", Destination: "/storage/user"}), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/var/lib/docker"},
	})
	if err == nil || !strings.Contains(err.Error(), "only bind mounts are supported") {
		t.Fatalf("error = %v, want non-bind rejection", err)
	}
}

func TestTranslateMountpointRejectsNestedNonBindMount(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/cache/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(
		DockerMount{Type: "bind", Source: "/srv/users/bob", Destination: "/storage/user"},
		DockerMount{Type: "tmpfs", Destination: "/storage/user/cache"},
	), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/srv"},
	})
	if err == nil || !strings.Contains(err.Error(), "only bind mounts are supported") {
		t.Fatalf("error = %v, want nested non-bind rejection", err)
	}
}

func TestTranslateMountpointRequiresLabels(t *testing.T) {
	_, err := TranslateMountpoint(protocol.Request{
		Mountpoint:    "/storage/user/mnt",
		ContainerName: "ccc-demo",
	}, inspectWithMounts(DockerMount{Type: "bind", Source: "/srv/users/bob", Destination: "/storage/user"}), TranslationConfig{
		HostRoot:              "/host",
		AllowedClientPrefixes: []string{"/storage/user"},
		AllowedHostPrefixes:   []string{"/srv"},
		RequiredLabels:        map[string]string{"ccc.fuse": "enabled"},
	})
	if err == nil || !strings.Contains(err.Error(), "missing required label") {
		t.Fatalf("error = %v, want missing label", err)
	}
}

func TestSidecarPrefixesForHostPrefixes(t *testing.T) {
	prefixes, err := SidecarPrefixesForHostPrefixes("/host", []string{"/storage", "/storage/"})
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 1 || prefixes[0] != "/host/storage" {
		t.Fatalf("prefixes = %#v", prefixes)
	}
}

func TestSidecarPrefixesForHostPrefixesRejectsRoot(t *testing.T) {
	_, err := SidecarPrefixesForHostPrefixes("/host", []string{"/"})
	if err == nil || !strings.Contains(err.Error(), "refusing to allow filesystem root") {
		t.Fatalf("error = %v, want root rejection", err)
	}
}

func inspectWithMounts(mounts ...DockerMount) ContainerInspect {
	inspect := ContainerInspect{
		ID:     "abc123",
		Name:   "/ccc-demo",
		Mounts: mounts,
	}
	inspect.Config.Labels = map[string]string{}
	return inspect
}
