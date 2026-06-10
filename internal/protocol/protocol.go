package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	DefaultSocketPath = "/run/ccc-fuse-sidecar/fuse.sock"
	EnvSocketPath     = "CCC_FUSE_SIDECAR_SOCKET"
	EnvAllowedPrefix  = "CCC_FUSE_ALLOWED_PREFIXES"
	EnvFuseCommFD     = "_FUSE_COMMFD"

	ActionMount   = "mount"
	ActionUnmount = "unmount"
)

type Request struct {
	Action     string   `json:"action"`
	Mountpoint string   `json:"mountpoint"`
	Options    []string `json:"options,omitempty"`
	Lazy       bool     `json:"lazy,omitempty"`
}

type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func WriteJSON(conn net.Conn, v any) error {
	enc := json.NewEncoder(conn)
	return enc.Encode(v)
}

func ReadRequest(conn net.Conn) (Request, error) {
	var req Request
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func MarshalResponse(resp Response) []byte {
	b, err := json.Marshal(resp)
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func SocketPathFromEnv(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv(EnvSocketPath)); v != "" {
		return v
	}
	return DefaultSocketPath
}

func ValidateSocketPath(path string) error {
	if strings.ContainsRune(path, 0) {
		return errors.New("socket path contains NUL")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("socket path %q is not absolute", path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return fmt.Errorf("socket path %q is not clean", path)
	}
	if clean == string(filepath.Separator) {
		return errors.New("socket path must include a filename")
	}
	if filepath.Base(clean) == "." || filepath.Base(clean) == string(filepath.Separator) {
		return fmt.Errorf("socket path %q must include a filename", path)
	}
	// Linux sockaddr_un paths are limited to 108 bytes including NUL.
	if len([]byte(clean)) >= 108 {
		return fmt.Errorf("socket path %q is too long for a Linux Unix-domain socket", path)
	}
	return nil
}

func ParsePrefixes(values []string, env string) ([]string, error) {
	var raw []string
	raw = append(raw, values...)
	if env != "" {
		raw = append(raw, strings.FieldsFunc(env, func(r rune) bool {
			return r == ':' || r == ','
		})...)
	}
	if len(raw) == 0 {
		raw = []string{"/mnt/ccc-fuse"}
	}

	seen := map[string]bool{}
	prefixes := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ContainsRune(p, 0) {
			return nil, errors.New("allowed prefix contains NUL")
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("allowed prefix %q is not absolute", p)
		}
		p = filepath.Clean(p)
		if p == string(filepath.Separator) {
			return nil, errors.New("refusing to allow filesystem root as a mount prefix")
		}
		if !seen[p] {
			seen[p] = true
			prefixes = append(prefixes, p)
		}
	}
	if len(prefixes) == 0 {
		return nil, errors.New("at least one allowed mount prefix is required")
	}
	return prefixes, nil
}

func ValidateMountpoint(mountpoint string, allowedPrefixes []string) (string, error) {
	if strings.ContainsRune(mountpoint, 0) {
		return "", errors.New("mountpoint contains NUL")
	}
	if !filepath.IsAbs(mountpoint) {
		return "", fmt.Errorf("mountpoint %q is not absolute", mountpoint)
	}
	clean := filepath.Clean(mountpoint)
	st, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("mountpoint %q is not accessible: %w", clean, err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("mountpoint %q is not a directory", clean)
	}
	realMountpoint, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("failed to resolve mountpoint %q: %w", clean, err)
	}
	realMountpoint = filepath.Clean(realMountpoint)

	for _, prefix := range allowedPrefixes {
		realPrefix := filepath.Clean(prefix)
		if resolved, err := filepath.EvalSymlinks(realPrefix); err == nil {
			realPrefix = filepath.Clean(resolved)
		}
		if PathWithin(realMountpoint, realPrefix) {
			return realMountpoint, nil
		}
	}

	return "", fmt.Errorf("mountpoint %q is outside allowed prefixes %s", realMountpoint, strings.Join(allowedPrefixes, ", "))
}

func OpenValidatedMountpoint(mountpoint string, allowedPrefixes []string) (*os.File, string, error) {
	clean, err := ValidateMountpoint(mountpoint, allowedPrefixes)
	if err != nil {
		return nil, "", err
	}

	fd, err := syscall.Open(clean, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open validated mountpoint %q: %w", clean, err)
	}
	file := os.NewFile(uintptr(fd), "ccc-fuse-mountpoint")
	if file == nil {
		_ = syscall.Close(fd)
		return nil, "", fmt.Errorf("wrap validated mountpoint fd for %q", clean)
	}

	procPath := fmt.Sprintf("/proc/self/fd/%d", fd)
	resolved, err := filepath.EvalSymlinks(procPath)
	if err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("resolve pinned mountpoint fd for %q: %w", clean, err)
	}
	resolved = filepath.Clean(resolved)

	for _, prefix := range allowedPrefixes {
		realPrefix := filepath.Clean(prefix)
		if p, err := filepath.EvalSymlinks(realPrefix); err == nil {
			realPrefix = filepath.Clean(p)
		}
		if PathWithin(resolved, realPrefix) {
			return file, procPath, nil
		}
	}

	_ = file.Close()
	return nil, "", fmt.Errorf("pinned mountpoint %q escaped allowed prefixes %s", resolved, strings.Join(allowedPrefixes, ", "))
}

func PathWithin(path, prefix string) bool {
	path = filepath.Clean(path)
	prefix = filepath.Clean(prefix)
	if path == prefix {
		return true
	}
	rel, err := filepath.Rel(prefix, path)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

func ParseOctalMode(s string) (os.FileMode, error) {
	if s == "" {
		return 0, errors.New("mode is empty")
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid octal mode %q: %w", s, err)
	}
	return os.FileMode(v), nil
}
