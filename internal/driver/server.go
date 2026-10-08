package driver

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"google.golang.org/grpc"
)

// Serve listens on a unix domain socket and serves the registered CSI
// services. It blocks until the server stops.
//
// A stale socket file is removed first: a restarted pod reuses the same
// hostPath, and net.Listen refuses to bind an existing file.
func Serve(socketPath string, register func(*grpc.Server)) error {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	server := grpc.NewServer()
	register(server)
	return server.Serve(listener)
}
