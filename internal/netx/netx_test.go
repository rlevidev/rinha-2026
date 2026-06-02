package netx

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSendRecvFD(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("Socketpair: %v", err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])

	f, _ := os.Open("/dev/null")
	defer f.Close()

	err = SendFD(fds[0], int(f.Fd()))
	if err != nil {
		t.Fatalf("SendFD: %v", err)
	}

	var oob [256]byte
	scratch := make([]int, 0, 64)
	got, ok, err := RecvFDs(fds[1], oob[:], scratch)
	if err != nil {
		t.Fatalf("RecvFDs: %v", err)
	}
	if !ok {
		t.Fatal("recv not ok")
	}
	if len(got) != 1 {
		t.Fatalf("got %d fds, want 1", len(got))
	}
	unix.Close(got[0])
}
