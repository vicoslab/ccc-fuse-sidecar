package client

import (
	"fmt"
	"strings"
)

type Args struct {
	Options    string
	Unmount    bool
	Lazy       bool
	Quiet      bool
	Help       bool
	Version    bool
	Mountpoint string
}

func ParseArgs(argv []string) (Args, error) {
	var out Args
	var positional []string

	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			positional = append(positional, argv[i+1:]...)
			break
		}
		if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
			positional = append(positional, arg)
			continue
		}

		switch {
		case arg == "-h" || arg == "--help":
			out.Help = true
		case arg == "-V" || arg == "--version":
			out.Version = true
		case arg == "-u" || arg == "--unmount":
			out.Unmount = true
		case arg == "-z" || arg == "--lazy" || arg == "-l":
			out.Lazy = true
		case arg == "-q" || arg == "--quiet":
			out.Quiet = true
		case arg == "-o" || arg == "--options":
			if i+1 >= len(argv) {
				return Args{}, fmt.Errorf("%s requires an argument", arg)
			}
			i++
			out.Options = mergeOptions(out.Options, argv[i])
		case strings.HasPrefix(arg, "-o") && !strings.HasPrefix(arg, "--"):
			out.Options = mergeOptions(out.Options, arg[2:])
		case strings.HasPrefix(arg, "--options="):
			out.Options = mergeOptions(out.Options, strings.TrimPrefix(arg, "--options="))
		case strings.HasPrefix(arg, "--"):
			return Args{}, fmt.Errorf("unknown option %q", arg)
		case strings.HasPrefix(arg, "-") && len(arg) > 2:
			if err := parseCombinedShort(arg[1:], &out); err != nil {
				return Args{}, err
			}
		default:
			return Args{}, fmt.Errorf("unknown option %q", arg)
		}
	}

	if len(positional) > 1 {
		return Args{}, fmt.Errorf("expected one mountpoint, got %d", len(positional))
	}
	if len(positional) == 1 {
		out.Mountpoint = positional[0]
	}
	return out, nil
}

func parseCombinedShort(flags string, out *Args) error {
	for _, r := range flags {
		switch r {
		case 'u':
			out.Unmount = true
		case 'z':
			out.Lazy = true
		case 'l':
			out.Lazy = true
		case 'q':
			out.Quiet = true
		case 'V':
			out.Version = true
		case 'h':
			out.Help = true
		default:
			return fmt.Errorf("unknown option %q", "-"+string(r))
		}
	}
	return nil
}

func mergeOptions(a, b string) string {
	b = strings.TrimSpace(b)
	if b == "" {
		return a
	}
	if a == "" {
		return b
	}
	return a + "," + b
}

func SplitMountOptions(options string) []string {
	if strings.TrimSpace(options) == "" {
		return nil
	}
	parts := strings.Split(options, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
