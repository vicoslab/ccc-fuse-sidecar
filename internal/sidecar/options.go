package sidecar

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
)

const (
	fuseFSType     = "fuse"
	defaultFSName  = "ccc-fuse"
	defaultRootMod = "40000"
)

type MountPlan struct {
	Source string
	FSType string
	Flags  uintptr
	Data   string
}

func BuildMountPlan(fuseFD int, options []string, uid, gid int) (MountPlan, error) {
	if fuseFD < 0 {
		return MountPlan{}, fmt.Errorf("invalid fuse fd %d", fuseFD)
	}
	if uid < 0 || gid < 0 {
		return MountPlan{}, fmt.Errorf("invalid uid/gid %d/%d", uid, gid)
	}

	source := defaultFSName
	flags := uintptr(syscall.MS_NODEV | syscall.MS_NOSUID)
	data := []string{
		fmt.Sprintf("fd=%d", fuseFD),
		"rootmode=" + defaultRootMod,
		fmt.Sprintf("user_id=%d", uid),
		fmt.Sprintf("group_id=%d", gid),
		"default_permissions",
	}
	seenData := map[string]bool{
		"fd":                  true,
		"rootmode":            true,
		"user_id":             true,
		"group_id":            true,
		"default_permissions": true,
	}

	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		key, value, hasValue := strings.Cut(opt, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return MountPlan{}, fmt.Errorf("invalid empty mount option in %q", opt)
		}
		if strings.ContainsAny(key, ",\x00 \t\r\n") {
			return MountPlan{}, fmt.Errorf("invalid mount option key %q", key)
		}

		switch key {
		case "fd", "rootmode", "user_id", "group_id":
			// These must match the privileged sidecar's opened descriptor and
			// the requesting process credentials, so client-provided values are
			// intentionally ignored.
			continue
		case "rw":
			flags &^= syscall.MS_RDONLY
			continue
		case "ro":
			flags |= syscall.MS_RDONLY
			continue
		case "nodev", "nosuid", "relatime", "norelatime", "strictatime", "defaults", "nonempty", "auto_unmount", "noauto", "user", "users", "owner", "group", "_netdev":
			continue
		case "dev", "suid":
			return MountPlan{}, fmt.Errorf("refusing unsafe mount option %q", key)
		case "exec":
			flags &^= syscall.MS_NOEXEC
			continue
		case "noexec":
			flags |= syscall.MS_NOEXEC
			continue
		case "atime":
			flags &^= syscall.MS_NOATIME
			continue
		case "noatime":
			flags |= syscall.MS_NOATIME
			continue
		case "diratime":
			flags &^= syscall.MS_NODIRATIME
			continue
		case "nodiratime":
			flags |= syscall.MS_NODIRATIME
			continue
		case "sync":
			flags |= syscall.MS_SYNCHRONOUS
			continue
		case "async":
			flags &^= syscall.MS_SYNCHRONOUS
			continue
		case "dirsync":
			flags |= syscall.MS_DIRSYNC
			continue
		case "fsname":
			if !hasValue {
				return MountPlan{}, fmt.Errorf("mount option %q requires a value", key)
			}
			if err := validateMountOptionValue(key, value); err != nil {
				return MountPlan{}, err
			}
			source = value
			continue
		}

		if !isAllowedFuseDataOption(key, hasValue, value) {
			return MountPlan{}, fmt.Errorf("unsupported or unsafe FUSE mount option %q", opt)
		}
		if hasValue {
			if err := validateMountOptionValue(key, value); err != nil {
				return MountPlan{}, err
			}
		}
		if !seenData[key] {
			seenData[key] = true
			data = append(data, opt)
		}
	}

	return MountPlan{
		Source: source,
		FSType: fuseFSType,
		Flags:  flags,
		Data:   strings.Join(data, ","),
	}, nil
}

func isAllowedFuseDataOption(key string, hasValue bool, value string) bool {
	boolOptions := map[string]bool{
		"allow_other":         true,
		"default_permissions": true,
		"kernel_cache":        true,
		"auto_cache":          true,
		"big_writes":          true,
		"posix_acl":           true,
	}
	valueOptions := map[string]bool{
		"subtype":              true,
		"max_read":             true,
		"max_write":            true,
		"blksize":              true,
		"attr_timeout":         true,
		"entry_timeout":        true,
		"negative_timeout":     true,
		"congestion_threshold": true,
	}
	if boolOptions[key] {
		return !hasValue
	}
	if valueOptions[key] {
		if !hasValue {
			return false
		}
		switch key {
		case "subtype":
			return value != ""
		default:
			return isNonNegativeNumber(value)
		}
	}
	return false
}

func validateMountOptionValue(key, value string) error {
	if value == "" {
		return fmt.Errorf("mount option %q requires a non-empty value", key)
	}
	if strings.ContainsAny(value, ",\x00\r\n") {
		return fmt.Errorf("mount option %q has an unsafe value", key)
	}
	return nil
}

func isNonNegativeNumber(s string) bool {
	if s == "" {
		return false
	}
	if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil && !strings.HasPrefix(s, "-") {
		return true
	}
	return false
}
