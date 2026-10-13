package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	"github.com/zhujian7/csi-demo/internal/backend"
	"github.com/zhujian7/csi-demo/internal/driver"
)

const (
	driverName = "csi.demo.cubestack.io"
	version    = "0.1.0"
)

func main() {
	var (
		mode        = flag.String("mode", "", "run mode: controller or node")
		endpoint    = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint")
		volumeRoot  = flag.String("volume-root", "/var/lib/csi-demo", "host directory holding volume data")
		backendKind = flag.String("backend", "loop", "volume backend: dir or loop")
	)
	flag.Parse()

	if *mode != "controller" && *mode != "node" && *mode != "all" {
		log.Fatalf("--mode must be controller, node or all, got %q", *mode)
	}

	// The CSI endpoint convention is unix://<path>; net.Listen wants the bare path.
	socketPath := strings.TrimPrefix(*endpoint, "unix://")

	identity := driver.NewIdentityServer(driverName, version)

	// Only Identity is registered so far. Task 2 fills in the node branch and
	// Task 4 the controller branch.
	var register func(*grpc.Server)
	switch *mode {
	case "controller":
		be, err := newBackend(*backendKind, *volumeRoot)
		if err != nil {
			log.Fatalf("create backend: %v", err)
		}
		register = func(s *grpc.Server) {
			csi.RegisterIdentityServer(s, identity)
			csi.RegisterControllerServer(s, driver.NewControllerServer(be))
		}
	case "node":
		be, err := newBackend(*backendKind, *volumeRoot)
		if err != nil {
			log.Fatalf("create backend: %v", err)
		}
		node, err := driver.NewNodeServer(be)
		if err != nil {
			log.Fatalf("create node server: %v", err)
		}
		register = func(s *grpc.Server) {
			csi.RegisterIdentityServer(s, identity)
			csi.RegisterNodeServer(s, node)
		}
	case "all":
		// csi-sanity drives every service through a single endpoint.
		be, err := newBackend(*backendKind, *volumeRoot)
		if err != nil {
			log.Fatalf("create backend: %v", err)
		}
		node, err := driver.NewNodeServer(be)
		if err != nil {
			log.Fatalf("create node server: %v", err)
		}
		register = func(s *grpc.Server) {
			csi.RegisterIdentityServer(s, identity)
			csi.RegisterControllerServer(s, driver.NewControllerServer(be))
			csi.RegisterNodeServer(s, node)
		}
	}

	log.Printf("starting %s %s on %s (volume root %s)", driverName, *mode, socketPath, *volumeRoot)
	if err := driver.Serve(socketPath, register); err != nil {
		fmt.Fprintf(os.Stderr, "server exited: %v\n", err)
		os.Exit(1)
	}
}

func newBackend(kind, root string) (backend.Backend, error) {
	switch kind {
	case "dir":
		return backend.NewLocalDir(root), nil
	case "loop":
		return backend.NewLoopDev(root), nil
	default:
		return nil, fmt.Errorf("unknown backend %q, want dir or loop", kind)
	}
}
