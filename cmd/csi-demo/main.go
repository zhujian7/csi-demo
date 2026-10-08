package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
)

const (
	driverName = "csi.demo.cubestack.io"
	version    = "0.1.0"
)

func main() {
	var (
		mode       = flag.String("mode", "", "run mode: controller or node")
		endpoint   = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		volumeRoot = flag.String("volume-root", "/var/lib/csi-demo", "host directory holding volume data")
	)
	flag.Parse()

	if *mode != "controller" && *mode != "node" {
		log.Fatalf("--mode must be controller or node, got %q", *mode)
	}

	// The CSI endpoint convention is unix://<path>; net.Listen wants the bare path.
	socketPath := strings.TrimPrefix(*endpoint, "unix://")

	// The gRPC server replaces this in Task 1, once the Identity service exists.
	fmt.Printf("%s %s would serve %s mode on %s with volumes under %s\n",
		driverName, version, *mode, socketPath, *volumeRoot)
}
