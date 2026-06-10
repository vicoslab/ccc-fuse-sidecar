package main

import (
	"os"

	"github.com/vicoslab/ccc-fuse-sidecar/internal/client"
)

var (
	version   = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(client.Runner{
		Version: version + " (BuildDate " + buildDate + ")",
	}.Run(os.Args))
}
