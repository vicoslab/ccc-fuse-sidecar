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
	socketPath := flag.String("socket", protocol.DefaultSocketPath, "Unix-domain socket path for app containers")
	socketMode := flag.String("socket-mode", "0666", "octal permissions for the created Unix-domain socket")
	devFusePath := flag.String("dev-fuse", "/dev/fuse", "path to the FUSE device in the privileged sidecar")
	showVersion := flag.Bool("version", false, "print version")
	flag.Var(&prefixes, "allow-prefix", "absolute mountpoint prefix to allow; may be repeated")
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
	allowed, err := protocol.ParsePrefixes(prefixes, os.Getenv(protocol.EnvAllowedPrefix))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid allowed prefixes: %v\n", err)
		os.Exit(2)
	}

	logger := log.New(os.Stderr, "ccc-fuse-sidecar: ", log.LstdFlags|log.Lmicroseconds)
	daemon, err := sidecar.New(sidecar.Config{
		SocketPath:      *socketPath,
		SocketMode:      mode,
		AllowedPrefixes: allowed,
		DevFusePath:     *devFusePath,
		Logger:          logger,
	})
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
