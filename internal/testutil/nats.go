// Package testutil provides shared test helpers.
package testutil

import (
	"fmt"
	"net"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// StartNATS starts an embedded NATS server on a random port and returns
// a connected client. The server and client are cleaned up when the test ends.
func StartNATS(t *testing.T) (*natsserver.Server, *nats.Conn) {
	t.Helper()

	opts := &natsserver.Options{
		Host:   "127.0.0.1",
		Port:   -1, // random port
		NoLog:  true,
		NoSigs: true,
	}

	ns, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("start nats server: %v", err)
	}
	ns.Start()

	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}

	url := NATSUrl(ns)
	nc, err := nats.Connect(url)
	if err != nil {
		ns.Shutdown()
		t.Fatalf("connect to nats: %v", err)
	}

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return ns, nc
}

// NATSUrl returns the client URL of the embedded NATS server.
func NATSUrl(ns *natsserver.Server) string {
	addr := ns.Addr().(*net.TCPAddr)
	return fmt.Sprintf("nats://127.0.0.1:%d", addr.Port)
}
