package lb

import (
	"testing"
)

func TestPickBackendAlternates(t *testing.T) {
	sockets := []string{"/tmp/api1.sock", "/tmp/api2.sock"}

	if got := PickBackend(0, sockets); got != sockets[0] {
		t.Fatalf("pickBackend(0) = %q, want %q", got, sockets[0])
	}
	if got := PickBackend(1, sockets); got != sockets[1] {
		t.Fatalf("pickBackend(1) = %q, want %q", got, sockets[1])
	}
	if got := PickBackend(2, sockets); got != sockets[0] {
		t.Fatalf("pickBackend(2) = %q, want %q", got, sockets[0])
	}
}
