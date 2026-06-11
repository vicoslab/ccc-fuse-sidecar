package sidecar

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
)

type TranslationConfig struct {
	Enabled               bool
	HostRoot              string
	AllowedClientPrefixes []string
	AllowedHostPrefixes   []string
	RequiredLabels        map[string]string
}

type TranslatedMountpoint struct {
	ClientPath    string
	HostPath      string
	SidecarPath   string
	MatchedSource string
	MatchedDest   string
	ContainerName string
	ContainerID   string
	Propagation   string
}

func TranslateMountpoint(req protocol.Request, inspect ContainerInspect, cfg TranslationConfig) (TranslatedMountpoint, error) {
	containerName := strings.TrimSpace(req.ContainerName)
	if containerName == "" {
		return TranslatedMountpoint{}, fmt.Errorf("container_name is required in Docker translation mode")
	}
	clientPath, err := cleanAbsolutePath("mountpoint", req.Mountpoint)
	if err != nil {
		return TranslatedMountpoint{}, err
	}
	if len(cfg.AllowedClientPrefixes) > 0 && !withinAnyPrefix(clientPath, cfg.AllowedClientPrefixes) {
		return TranslatedMountpoint{}, fmt.Errorf("mountpoint %q is outside allowed client prefixes %s", clientPath, strings.Join(cfg.AllowedClientPrefixes, ", "))
	}
	if len(cfg.AllowedHostPrefixes) == 0 {
		return TranslatedMountpoint{}, fmt.Errorf("at least one allowed host prefix is required")
	}
	hostRoot, err := cleanAbsolutePath("host root", cfg.HostRoot)
	if err != nil {
		return TranslatedMountpoint{}, err
	}
	for key, want := range cfg.RequiredLabels {
		if inspect.Config.Labels == nil || inspect.Config.Labels[key] != want {
			return TranslatedMountpoint{}, fmt.Errorf("container %q is missing required label %s=%s", containerName, key, want)
		}
	}

	var best *DockerMount
	var bestDest string
	var bestAnyType string
	var bestAnyDest string
	for i := range inspect.Mounts {
		mount := inspect.Mounts[i]
		dest, err := cleanAbsolutePath("Docker mount destination", mount.Destination)
		if err != nil {
			continue
		}
		if !protocol.PathWithin(clientPath, dest) {
			continue
		}
		if bestAnyDest == "" || len(dest) > len(bestAnyDest) {
			bestAnyDest = dest
			bestAnyType = mount.Type
		}
		if mount.Type != "bind" {
			continue
		}
		if best == nil || len(dest) > len(bestDest) {
			best = &inspect.Mounts[i]
			bestDest = dest
		}
	}
	if bestAnyDest != "" && bestAnyType != "bind" {
		return TranslatedMountpoint{}, fmt.Errorf("Docker mount %q is type %q; only bind mounts are supported", bestAnyDest, bestAnyType)
	}
	if best == nil {
		return TranslatedMountpoint{}, fmt.Errorf("no Docker bind mount contains client mountpoint %q", clientPath)
	}
	source, err := cleanAbsolutePath("Docker mount source", best.Source)
	if err != nil {
		return TranslatedMountpoint{}, err
	}
	rel, err := filepath.Rel(bestDest, clientPath)
	if err != nil {
		return TranslatedMountpoint{}, fmt.Errorf("compute relative path from %q to %q: %w", bestDest, clientPath, err)
	}
	hostPath := filepath.Clean(filepath.Join(source, rel))
	if !withinAnyPrefix(hostPath, cfg.AllowedHostPrefixes) {
		return TranslatedMountpoint{}, fmt.Errorf("translated host path %q is outside allowed host prefixes %s", hostPath, strings.Join(cfg.AllowedHostPrefixes, ", "))
	}
	sidecarPath := joinHostPathUnderRoot(hostRoot, hostPath)
	return TranslatedMountpoint{
		ClientPath:    clientPath,
		HostPath:      hostPath,
		SidecarPath:   sidecarPath,
		MatchedSource: source,
		MatchedDest:   bestDest,
		ContainerName: containerName,
		ContainerID:   inspect.ID,
		Propagation:   best.Propagation,
	}, nil
}

func SidecarPrefixesForHostPrefixes(hostRoot string, hostPrefixes []string) ([]string, error) {
	root, err := cleanAbsolutePath("host root", hostRoot)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hostPrefixes))
	seen := map[string]bool{}
	for _, prefix := range hostPrefixes {
		clean, err := cleanAllowedPrefix("allowed host prefix", prefix)
		if err != nil {
			return nil, err
		}
		sidecarPrefix := joinHostPathUnderRoot(root, clean)
		if !seen[sidecarPrefix] {
			seen[sidecarPrefix] = true
			out = append(out, sidecarPrefix)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one allowed host prefix is required")
	}
	return out, nil
}

func cleanAbsolutePath(name, p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%s contains NUL", name)
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%s %q is not absolute", name, p)
	}
	return filepath.Clean(p), nil
}

func cleanAllowedPrefix(name, p string) (string, error) {
	clean, err := cleanAbsolutePath(name, p)
	if err != nil {
		return "", err
	}
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("refusing to allow filesystem root as %s", name)
	}
	return clean, nil
}

func withinAnyPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		clean, err := cleanAllowedPrefix("allowed prefix", prefix)
		if err != nil {
			continue
		}
		if protocol.PathWithin(path, clean) {
			return true
		}
	}
	return false
}

func joinHostPathUnderRoot(hostRoot, hostPath string) string {
	return filepath.Clean(filepath.Join(hostRoot, strings.TrimPrefix(filepath.Clean(hostPath), string(filepath.Separator))))
}
