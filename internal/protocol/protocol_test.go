package protocol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePrefixesRejectsRootAndRelative(t *testing.T) {
	if _, err := ParsePrefixes([]string{"/"}, ""); err == nil {
		t.Fatal("expected root prefix to be rejected")
	}
	if _, err := ParsePrefixes([]string{"relative"}, ""); err == nil {
		t.Fatal("expected relative prefix to be rejected")
	}
}

func TestValidateMountpointAllowsResolvedSubpath(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mount")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ValidateMountpoint(mountpoint, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if got != mountpoint {
		t.Fatalf("mountpoint = %q, want %q", got, mountpoint)
	}
}

func TestValidateMountpointRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "real")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMountpoint(link, []string{dir}); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}

func TestOpenValidatedMountpointReturnsPinnedFDPath(t *testing.T) {
	dir := t.TempDir()
	mountpoint := filepath.Join(dir, "mount")
	if err := os.Mkdir(mountpoint, 0o755); err != nil {
		t.Fatal(err)
	}

	file, fdPath, err := OpenValidatedMountpoint(mountpoint, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	resolved, err := filepath.EvalSymlinks(fdPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != mountpoint {
		t.Fatalf("fd path resolved to %q, want %q", resolved, mountpoint)
	}
}

func TestValidateSocketPath(t *testing.T) {
	if err := ValidateSocketPath("/tmp/fuse.sock"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSocketPath("relative.sock"); err == nil {
		t.Fatal("expected relative socket path to be rejected")
	}
}
