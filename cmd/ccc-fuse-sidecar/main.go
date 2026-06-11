package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/protocol"
	"github.com/vicoslab/ccc-fuse-sidecar/internal/sidecar"
)

var (
	version   = "unknown"
	buildDate = "unknown"
)

type prefixList []string

func (p *prefixList) String() string {
	return strings.Join(*p, ",")
}

func (p *prefixList) Set(v string) error {
	if v == "" {
		return fmt.Errorf("empty prefix")
	}
	*p = append(*p, v)
	return nil
}

func main() {
	var prefixes prefixList
	var clientPrefixes prefixList
	var hostPrefixes prefixList
	var requiredLabels labelList
	socketPath := flag.String("socket", protocol.DefaultSocketPath, "Unix-domain socket path for app containers")
	socketMode := flag.String("socket-mode", "0666", "octal permissions for the created Unix-domain socket")
	devFusePath := flag.String("dev-fuse", "/dev/fuse", "path to the FUSE device in the privileged sidecar")
	dockerSocket := flag.String("docker-socket", "", "Docker Engine Unix socket for Docker-inspect path translation; accepts /path or unix:///path")
	hostRoot := flag.String("host-root", "", "sidecar-visible root where host paths are mounted for Docker-inspect translation")
	debug := flag.Bool("debug", false, "enable verbose request and mount-plan logs; can also be enabled with CCC_FUSE_DEBUG=1")
	showVersion := flag.Bool("version", false, "print version")
	flag.Var(&prefixes, "allow-prefix", "absolute mountpoint prefix to allow; may be repeated")
	flag.Var(&clientPrefixes, "allow-client-prefix", "optional absolute client-visible mountpoint prefix to allow in Docker translation mode; may be repeated; when omitted, only translated host paths are allowlisted")
	flag.Var(&hostPrefixes, "allow-host-prefix", "absolute host path prefix to allow in Docker translation mode; may be repeated; can also be set with CCC_FUSE_ALLOWED_HOST_PREFIXES")
	flag.Var(&requiredLabels, "require-container-label", "required inspected Docker container label in key=value form; may be repeated")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ccc-fuse-sidecar %s (BuildDate %s)\n", version, buildDate)
		return
	}

	mode, err := protocol.ParseOctalMode(*socketMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --socket-mode: %v\n", err)
		os.Exit(2)
	}
	logger := log.New(os.Stderr, "ccc-fuse-sidecar: ", log.LstdFlags|log.Lmicroseconds)
	debugEnabled := *debug || envTruthy(os.Getenv(protocol.EnvDebug))
	cfg := sidecar.Config{
		SocketPath:  *socketPath,
		SocketMode:  mode,
		DevFusePath: *devFusePath,
		Logger:      logger,
		Debug:       debugEnabled,
	}
	if strings.TrimSpace(*dockerSocket) == "" {
		allowed, err := protocol.ParsePrefixes(prefixes, os.Getenv(protocol.EnvAllowedPrefix))
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid allowed prefixes: %v\n", err)
			os.Exit(2)
		}
		cfg.AllowedPrefixes = allowed
	} else {
		clients, err := parseOptionalPrefixes(clientPrefixes, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid allowed client prefixes: %v\n", err)
			os.Exit(2)
		}
		hosts, err := parseRequiredPrefixes(hostPrefixes, os.Getenv(protocol.EnvAllowedHostPrefix), "allowed host prefixes")
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid allowed host prefixes: %v\n", err)
			os.Exit(2)
		}
		if strings.TrimSpace(*hostRoot) == "" {
			fmt.Fprintln(os.Stderr, "--host-root is required when --docker-socket is set")
			os.Exit(2)
		}
		labels, err := requiredLabels.Map()
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid required container labels: %v\n", err)
			os.Exit(2)
		}
		cfg.DockerInspector = sidecar.NewDockerInspector(*dockerSocket)
		cfg.Translation = sidecar.TranslationConfig{
			Enabled:               true,
			HostRoot:              *hostRoot,
			AllowedClientPrefixes: clients,
			AllowedHostPrefixes:   hosts,
			RequiredLabels:        labels,
		}
	}
	daemon, err := sidecar.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure daemon: %v\n", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := daemon.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "daemon failed: %v\n", err)
		os.Exit(1)
	}
}

func envTruthy(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	return v != "" && v != "0" && v != "false" && v != "no" && v != "off"
}

type labelList []string

func (l *labelList) String() string {
	return strings.Join(*l, ",")
}

func (l *labelList) Set(v string) error {
	if strings.Count(v, "=") != 1 {
		return fmt.Errorf("label requirement must be key=value")
	}
	key, _, _ := strings.Cut(v, "=")
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("label requirement has empty key")
	}
	*l = append(*l, v)
	return nil
}

func (l labelList) Map() (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range l {
		if strings.Count(raw, "=") != 1 {
			return nil, fmt.Errorf("label requirement %q must be key=value", raw)
		}
		key, value, _ := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("label requirement %q has empty key", raw)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}

func parseOptionalPrefixes(values []string, env string) ([]string, error) {
	if len(values) == 0 && strings.TrimSpace(env) == "" {
		return nil, nil
	}
	return protocol.ParsePrefixes(values, env)
}

func parseRequiredPrefixes(values []string, env, name string) ([]string, error) {
	if len(values) == 0 && strings.TrimSpace(env) == "" {
		return nil, fmt.Errorf("at least one %s value is required", name)
	}
	return protocol.ParsePrefixes(values, env)
}
